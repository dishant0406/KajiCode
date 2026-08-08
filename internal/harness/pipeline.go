package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Action is a single edit a learning pass proposes or applies.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

// EditProposal is one validated edit in a LearningPlan.
type EditProposal struct {
	Action  Action  `json:"action"`
	Kind    Kind    `json:"kind"`
	ID      string  `json:"id"`
	Title   string  `json:"title,omitempty"`
	Content string  `json:"content,omitempty"`
	Path    string  `json:"path,omitempty"`
	Scope   Scope   `json:"scope,omitempty"`
	Recipe  *Recipe `json:"recipe,omitempty"`
	Reason  string  `json:"reason,omitempty"`
}

// LearningPlan is the non-mutating output of the plan LLM pass. It carries a
// snapshot of the baseline state so apply can detect concurrent changes.
type LearningPlan struct {
	Summary      string
	Rationale    string
	Proposals    []EditProposal
	Baseline     State
	Instructions string
}

// EditOutcome is the result of attempting a single proposal.
type EditOutcome struct {
	Proposal EditProposal `json:"proposal"`
	Applied  bool         `json:"applied"`
	Error    string       `json:"error,omitempty"`
	Before   *Entry       `json:"before,omitempty"`
	After    *Entry       `json:"after,omitempty"`
}

// LearningResult is the outcome of an apply pass.
type LearningResult struct {
	PlanSummary  string
	Rationale    string
	Outcomes     []EditOutcome
	RefinementID string
	HarnessPath  string
	Errors       []string
}

// ReviewDecision is the output of the cheap auto-learn review gate.
type ReviewDecision struct {
	ShouldLearn  bool
	Rationale    string
	Instructions string
}

// PlanOptions configures the plan LLM pass.
type PlanOptions struct {
	Provider     kajicoderuntime.Provider
	Conversation string
	State        State
	Refinements  []RefinementEvent
	Instructions string
	ScopePolicy  string
}

// ReviewOptions configures the review gate call.
type ReviewOptions struct {
	Provider     kajicoderuntime.Provider
	Conversation string
	State        State
	Refinements  []RefinementEvent
}

// RunReview runs the cheap auto-learn gate. It returns ShouldLearn=true only
// when the conversation shows durable, reusable lessons; a provider failure
// degrades to a nil ReviewDecision (no learning) so a transient auth/network
// error never blocks the agent loop.
func RunReview(ctx context.Context, options ReviewOptions) (*ReviewDecision, error) {
	if options.Provider == nil {
		return nil, errors.New("learning review provider is required")
	}
	prompt := "You are the auto-learning review gate for KajiCode. Read the conversation and decide whether a learning pass should run.\n\n" +
		"<conversation>\n" + strings.TrimSpace(options.Conversation) + "\n</conversation>\n\n" +
		"Reply with ONLY a JSON object, no markdown fences:\n" +
		`{"shouldLearn": true|false, "rationale": "short", "instructions": "optional focus for the plan"}` +
		"\n\nReturn shouldLearn=false unless there is a clear, durable, reusable lesson (a repeated workflow, a project convention, a subtle gotcha). No lessons -> false."
	raw, err := completeText(ctx, options.Provider, "system", prompt)
	if err != nil {
		return nil, err
	}
	decision, err := parseReviewDecision(raw)
	if err != nil {
		return nil, err
	}
	if decision.ShouldLearn {
		if strings.TrimSpace(decision.Rationale) == "" {
			return nil, errors.New("review returned shouldLearn without rationale")
		}
		decision.Instructions = strings.TrimSpace(decision.Instructions)
	}
	return decision, nil
}

// PlanLearning runs the plan LLM pass. It never mutates state: the returned
// plan carries a baseline snapshot for apply-time conflict detection.
func PlanLearning(ctx context.Context, options PlanOptions) (LearningPlan, error) {
	if options.Provider == nil {
		return LearningPlan{}, errors.New("learning plan provider is required")
	}
	prompt := buildPlanPrompt(options)
	raw, err := completeText(ctx, options.Provider, planSystemPrompt, prompt)
	if err != nil {
		return LearningPlan{}, fmt.Errorf("learning plan request: %w", err)
	}
	plan, err := ParseLearningPlan(raw)
	if err != nil {
		return LearningPlan{}, fmt.Errorf("learning plan parse: %w (raw: %.300s)", err, raw)
	}
	plan.Baseline = options.State
	plan.Instructions = options.Instructions
	return plan, nil
}

// planSystemPrompt is the strict instruction for the plan pass.
const planSystemPrompt = `You are the optimizer for KajiCode's self-learning memory. You read a conversation, the current learning state, and refinement history, then propose minimal, evidence-backed edits to durable memory.

Rules:
- The base system prompt is immutable; never create/update/delete an entry with id "base_system_prompt".
- Prefer small edits over large rewrites. Cite evidence from the conversation.
- Entries are one of: prompt (supplemental notes), memory (durable facts), recipe (reusable procedure with a commands array), subagent (reusable delegation spec).
- Scope: local by default. Use global only for durable cross-session lessons.
- A recipe entry's commands array is [{ "id": string, "tool": string (a registered KajiCode tool name), "args": object }].
- Delete only entries that are stale or contradicted. Never delete an entry you did not first read.

Reply with ONLY a JSON object, no markdown fences:
{
  "summary": string,
  "rationale": string,
  "edits": [
    {
      "action": "create|update|delete",
      "kind": "prompt|memory|recipe|subagent",
      "id": string,
      "title": string,
      "content": string,
      "path": string,
      "scope": "local|global",
      "recipe": { "name": string, "description": string, "commands": [...] },
      "reason": string
    }
  ]
}`

func buildPlanPrompt(options PlanOptions) string {
	var b strings.Builder
	b.WriteString("<scope_policy>\n" + strings.TrimSpace(options.ScopePolicy) + "\n</scope_policy>\n\n")
	b.WriteString("<current_learning_state>\n")
	b.WriteString(FormatHarnessStateForPrompt(ScopeLocal, MergeHarnessStates(options.State, State{}), 20))
	b.WriteString("\n</current_learning_state>\n\n")
	b.WriteString("<refinement_history>\n")
	writeRefinementHistory(&b, options.Refinements)
	b.WriteString("\n</refinement_history>\n\n")
	if strings.TrimSpace(options.Instructions) != "" {
		b.WriteString("<user_learning_instructions>\n" + strings.TrimSpace(options.Instructions) + "\n</user_learning_instructions>\n\n")
	}
	b.WriteString("<conversation>\n" + strings.TrimSpace(options.Conversation) + "\n</conversation>\n")
	return b.String()
}

func writeRefinementHistory(b *strings.Builder, refinements []RefinementEvent) {
	if len(refinements) == 0 {
		b.WriteString("(none yet)")
		return
	}
	start := 0
	if len(refinements) > 20 {
		start = len(refinements) - 20
	}
	for _, event := range refinements[start:] {
		fmt.Fprintf(b, "- [%s] %s: %s\n", event.ID, event.Trigger, strings.Join(event.Changes, ", "))
	}
}

// completeText issues a single tool-less completion and returns the trimmed
// text. Mirrors summarizeMessagesOnce in agent/compaction.go.
func completeText(ctx context.Context, provider kajicoderuntime.Provider, system, user string) (string, error) {
	stream, err := provider.StreamCompletion(ctx, kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{
			{Role: kajicoderuntime.MessageRoleSystem, Content: system},
			{Role: kajicoderuntime.MessageRoleUser, Content: user},
		},
	})
	if err != nil {
		return "", err
	}
	collected := kajicoderuntime.CollectStreamWithOptions(ctx, stream, kajicoderuntime.CollectOptions{})
	if collected.Error != "" {
		return "", errors.New(collected.Error)
	}
	text := strings.TrimSpace(collected.Text)
	if text == "" {
		return "", errors.New("provider returned no text")
	}
	return text, nil
}

// ---- Parsing ----

type rawPlan struct {
	Summary   string            `json:"summary"`
	Rationale string            `json:"rationale"`
	Edits     []json.RawMessage `json:"edits"`
}

// ParseLearningPlan parses the model's strict JSON, recovering from markdown
// fences and truncation, and validates each edit.
func ParseLearningPlan(raw string) (LearningPlan, error) {
	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		return LearningPlan{}, errors.New("no JSON object found in model output")
	}
	var plan rawPlan
	if err := json.Unmarshal([]byte(jsonText), &plan); err != nil {
		return LearningPlan{}, fmt.Errorf("malformed plan JSON: %w", err)
	}
	if len(plan.Edits) == 0 {
		return LearningPlan{Summary: plan.Summary, Rationale: plan.Rationale}, nil
	}
	proposals := make([]EditProposal, 0, len(plan.Edits))
	for _, rawEdit := range plan.Edits {
		var proposal EditProposal
		if err := json.Unmarshal(rawEdit, &proposal); err != nil {
			return LearningPlan{}, fmt.Errorf("malformed edit JSON: %w", err)
		}
		if err := ValidateProposal(proposal); err != nil {
			return LearningPlan{}, fmt.Errorf("invalid edit: %w", err)
		}
		proposals = append(proposals, proposal)
	}
	return LearningPlan{Summary: plan.Summary, Rationale: plan.Rationale, Proposals: proposals}, nil
}

// ValidateProposal checks an edit's field contract.
func ValidateProposal(proposal EditProposal) error {
	switch proposal.Action {
	case ActionCreate, ActionUpdate, ActionDelete:
	default:
		return fmt.Errorf("action must be create, update, or delete, got %q", proposal.Action)
	}
	switch proposal.Kind {
	case KindPrompt, KindMemory, KindRecipe, KindSubagent:
	default:
		return fmt.Errorf("kind must be prompt, memory, recipe, or subagent, got %q", proposal.Kind)
	}
	if proposal.ID == BasePromptID {
		return fmt.Errorf("base system prompt is immutable")
	}
	if strings.TrimSpace(proposal.ID) == "" {
		return errors.New("edit requires an id")
	}
	if proposal.Action != ActionDelete && strings.TrimSpace(proposal.Title) == "" {
		return errors.New("create/update requires a title")
	}
	if proposal.Kind == KindRecipe && proposal.Recipe == nil && proposal.Action != ActionDelete {
		return errors.New("recipe entries require a commands contract")
	}
	if proposal.Recipe != nil && proposal.Action == ActionDelete {
		return errors.New("delete cannot carry a recipe")
	}
	if proposal.Scope != "" && proposal.Scope != ScopeLocal && proposal.Scope != ScopeGlobal {
		return fmt.Errorf("scope must be local or global, got %q", proposal.Scope)
	}
	return nil
}

// parseReviewDecision parses and validates the review gate's strict JSON.
func parseReviewDecision(raw string) (*ReviewDecision, error) {
	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		return nil, errors.New("no JSON object found in review output")
	}
	var decision struct {
		ShouldLearn  bool   `json:"shouldLearn"`
		Rationale    string `json:"rationale"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(jsonText), &decision); err != nil {
		return nil, fmt.Errorf("malformed review JSON: %w", err)
	}
	return &ReviewDecision{
		ShouldLearn:  decision.ShouldLearn,
		Rationale:    strings.TrimSpace(decision.Rationale),
		Instructions: strings.TrimSpace(decision.Instructions),
	}, nil
}

// extractJSONObject finds a balanced {...} object in text, trimming markdown
// fences and leading prose. It is lenient about trailing truncation: if the
// model output was cut mid-object, it returns the largest balanced prefix.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
	}
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	// Truncated object: return the largest balanced prefix seen.
	if depth > 0 {
		last := start
		found := false
		for j := start; j < len(text); j++ {
			if text[j] == '}' {
				last = j
				found = true
			}
		}
		if found {
			return text[start : last+1]
		}
	}
	return ""
}

// NewRefinementID derives a stable refinement identifier from time.
func NewRefinementID(now time.Time) string {
	return "refine_" + now.UTC().Format("20060102T150405.000000000Z")
}
