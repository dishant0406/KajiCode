package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// requestLearnMeta is the Meta key the learn tool sets to request a manual
// learning pass at the next safe boundary.
const requestLearnMeta = "request_learn"

// Learned-memory prompt-bounds. Durable state is stored unbounded on disk, but
// only a bounded, truncated slice is ever injected into the prompt so a growing
// memory can never blow the model's context window. These mirror prime-agent's
// per-kind overview caps.
const (
	// learnedPromptoMaxPerKind caps how many entries of a given kind are
	// surfaced in the <learned_memory> prompt block.
	learnedPromptoMaxPerKind = 6
	// learnedPromptoMaxContentLen truncates each entry's content shown in the
	// <learned_memory> prompt block.
	learnedPromptoMaxContentLen = 160
)

// LearningEngine is the opt-in self-learning controller for a run. It is the
// agent-side counterpart to the harness pipeline (review → plan → apply →
// record) and to the manual learn tool. Hooking a non-nil engine into
// agent.Options.Learning enables automatic learning; nil leaves the loop
// byte-identical, exactly like Profile, SelfCorrect, and Trace.
//
// The engine owns: the turn-interval / compact / cooldown gates, a manual
// request flag consumed from tool result Meta, and the provider + stores it
// uses for the pipeline. It is safe for concurrent use (a sync.Mutex guards
// counters and the pending flag).
//
// Provider and stores are injected at construction (by the CLI), so the loop
// hook needs no provider plumbing.
type LearningEngine struct {
	cfg         config.LearningConfig
	provider    kajicoderuntime.Provider
	globalStore *harness.Store
	localStore  *harness.Store

	mu          sync.Mutex
	turnCount   int
	lastReview  time.Time
	manualReady bool
}

// NewLearningEngine builds the engine. cfg is the effective (defaulted)
// learning config; provider must be non-nil or the engine is a no-op;
// globalStore and localStore are the harness stores for each scope.
func NewLearningEngine(cfg config.LearningConfig, provider kajicoderuntime.Provider, globalStore, localStore *harness.Store) *LearningEngine {
	return &LearningEngine{
		cfg:         cfg.Effective(),
		provider:    provider,
		globalStore: globalStore,
		localStore:  localStore,
	}
}

// Enabled reports whether the engine will act. An engine with a nil provider
// or a disabled config is inert.
func (e *LearningEngine) Enabled() bool {
	return e != nil && e.provider != nil && e.cfg.IsEnabled()
}

// NoteToolResult lets the loop hand each tool result to the engine; a result
// whose Meta sets request_learn arms a manual pass at the next boundary.
func (e *LearningEngine) NoteToolResult(result ToolResult) {
	if e == nil {
		return
	}
	if result.Meta[requestLearnMeta] == "true" {
		e.mu.Lock()
		e.manualReady = true
		e.mu.Unlock()
	}
}

// TurnElapsed is the loop hook, called once per assistant turn (after a
// compaction). compacted reports whether this turn followed a context
// compaction. It runs the gate, and when the gate opens, the pipeline:
//
//	review → (if should learn) plan → apply → record
//
// Failures are non-fatal: a transient provider/auth error just skips this
// pass and re-arms the cooldown so it cannot retry in a tight loop. It returns
// whether any learning was actually applied, so the loop can splice the fresh
// learned-memory block into the next request (same-session pickup) without an
// extra store read on every turn.
func (e *LearningEngine) TurnElapsed(ctx context.Context, messages []kajicoderuntime.Message, compacted bool) bool {
	applied := false
	if !e.Enabled() {
		return applied
	}
	e.mu.Lock()
	e.turnCount++
	manual := e.manualReady
	due := manual || (compacted && e.cfg.IsCompactEnabled()) || (e.turnCount > 0 && e.turnCount%e.cfg.TurnInterval == 0)
	if due {
		// Cooldown gate: respect the minimum gap unless a manual request armed
		// it (the user explicitly asked, bypassing cooldown).
		if !manual && !e.lastReview.IsZero() && time.Since(e.lastReview) < time.Duration(e.cfg.CooldownMs)*time.Millisecond {
			due = false
		}
	}
	if due {
		e.manualReady = false
		e.lastReview = time.Now()
	}
	e.mu.Unlock()

	if !due {
		return applied
	}

	conversation := renderTranscript(messages)
	decision, err := harness.RunReview(ctx, harness.ReviewOptions{
		Provider:     e.provider,
		Conversation: conversation,
	})
	if err != nil || decision == nil || !decision.ShouldLearn {
		return applied
	}

	plan, err := harness.PlanLearning(ctx, harness.PlanOptions{
		Provider:     e.provider,
		Conversation: conversation,
		State:        e.loadGlobalState(),
		Refinements:  e.loadRefinements(),
		Instructions: decision.Instructions,
		ScopePolicy:  "Prefer local (session-scoped) entries. Use global only for durable cross-session lessons.",
	})
	if err != nil {
		return applied
	}
	if len(plan.Proposals) == 0 {
		return applied
	}
	if e.globalStore != nil {
		outcome := harness.ApplyLearning(e.globalStore, harness.ApplyOptions{
			Plan:    plan,
			Trigger: "auto",
			Now:     time.Now,
		})
		applied = countApplied(outcome.Outcomes) > 0
	}
	if !applied && e.localStore != nil {
		outcome := harness.ApplyLearning(e.localStore, harness.ApplyOptions{
			Plan:    plan,
			Trigger: "auto",
			Now:     time.Now,
		})
		applied = countApplied(outcome.Outcomes) > 0
	}
	return applied
}

// countApplied reports how many proposals in a set actually landed. A proposal
// that was rejected (conflict, validation, duplicate) is not "applied" even if
// the pipeline ran.
func countApplied(outcomes []harness.EditOutcome) int {
	n := 0
	for _, outcome := range outcomes {
		if outcome.Applied {
			n++
		}
	}
	return n
}

func (e *LearningEngine) loadGlobalState() harness.State {
	if e.globalStore == nil {
		return harness.State{Scope: harness.ScopeGlobal}
	}
	state, _ := e.globalStore.Load()
	return state
}

func (e *LearningEngine) loadLocalState() harness.State {
	if e.localStore == nil {
		return harness.State{Scope: harness.ScopeLocal}
	}
	state, _ := e.localStore.Load()
	return state
}

func (e *LearningEngine) loadRefinements() []harness.RefinementEvent {
	return e.loadGlobalState().Refinements
}

// Context renders the bounded, merged learned memory as a system prompt
// section. It is called at run start by buildSystemPromptParts so the model
// sees durable lessons (memory/prompt/subagent entries) on the first turn, and
// later by the loop's same-session refresh to pick up newly applied lessons.
// The merged set is recall-ordered (freshest-first), the block is capped per
// kind, and the whole block obeys a token budget — so a growing harness store
// can never blow the context window and is biased toward memory the model has
// actually re-used (compaction's "keep the freshest within a budget").
func (e *LearningEngine) Context() string {
	if e == nil {
		return ""
	}
	merged := harness.MergeHarnessStates(e.loadGlobalState(), e.loadLocalState())
	if len(merged) == 0 {
		return ""
	}
	var b strings.Builder
	shows := map[harness.Kind]int{}
	budget := learnedMemoryTokenBudget()
	for _, entry := range merged {
		switch entry.Kind {
		case harness.KindMemory, harness.KindPrompt, harness.KindSubagent:
			if shows[entry.Kind] >= learnedPromptoMaxPerKind {
				continue
			}
			title := strings.TrimSpace(entry.Title)
			if title == "" {
				title = entry.ID
			}
			scope := string(entry.Scope)
			if scope == "" {
				scope = string(harness.ScopeLocal)
			}
			line := fmt.Sprintf("- [%s] %s: %s\n", scope, title, trimLearningContent(entry.Content))
			weight := ApproxTextTokens(line)
			if weight > budget {
				// The whole block is over budget; drop the rest (which are all
				// strictly older than what we've already shown).
				break
			}
			b.WriteString(line)
			budget -= weight
			shows[entry.Kind]++
		}
	}
	return strings.TrimSpace(b.String())
}

// learnedMemoryTokenBudget bounds the whole <learned_memory> block so a store
// with many lessons can never monopolize the context budget, mirroring the
// compaction tail budget (a fraction of the window). A static, self-contained
// ceiling keeps the block deterministic and cheap to reason about.
const learnedMemoryTokenBudgetValue = 1200

func learnedMemoryTokenBudget() int { return learnedMemoryTokenBudgetValue }

// trimLearningContent normalizes a learned entry's content for the one-line
// prompt summary: newlines collapse to spaces and the value is truncated with a
// marker so a verbose lesson cannot monopolize the context budget.
func trimLearningContent(content string) string {
	normalized := strings.Join(strings.Fields(content), " ")
	if len(normalized) <= learnedPromptoMaxContentLen {
		return normalized
	}
	const marker = "..."
	return normalized[:learnedPromptoMaxContentLen-len(marker)] + marker
}

// learnedMemoryOpen and learnedMemoryClose delimit the same-session injectable
// block. They match (part of) the static block built in system_prompt.go's
// learningContext, so splicing an updated block replaces the prior one without
// duplicating it.
const (
	learnedMemoryOpen  = "<learned_memory>"
	learnedMemoryClose = "</learned_memory>"
)

// EnsurePromptHasMemory splices a fresh, bounded <learned_memory> block into the
// leading system message so lessons applied mid-session take effect on the next
// provider call (same-session pickup). It is idempotent: if the message already
// carries a <learned_memory> block, only that block is replaced; otherwise the
// block is appended after any existing content. It returns true when the message
// changed so the loop can decide whether to re-seed the request. Gating mirrors
// the seed-time path (learningContext): a nil engine or no durable entries is a
// byte-identical no-op, and a nil provider never forces an arbitrary reflection.
func (e *LearningEngine) EnsurePromptHasMemory(messages []kajicoderuntime.Message) []kajicoderuntime.Message {
	if e == nil {
		return messages
	}
	memory := e.Context()
	if memory == "" {
		return messages
	}
	block := learningMemoryBlock(memory)
	changed := false
	out := make([]kajicoderuntime.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role != kajicoderuntime.MessageRoleSystem {
			continue
		}
		modified := spliceMemoryBlock(out[i].Content, block)
		if modified != out[i].Content {
			out[i].Content = modified
			changed = true
		}
		break // only the leading system message
	}
	if !changed {
		return messages
	}
	return out
}

func learningMemoryBlock(memory string) string {
	if memory == "" {
		return ""
	}
	return learnedMemoryOpen + "\nDurable lessons learned across prior sessions. Treat these as project/user conventions, not as immutable facts; if a current instruction contradicts one, follow the current instruction.\n" + memory + "\n" + learnedMemoryClose
}

// spliceMemoryBlock replaces an existing <learned_memory>...</learned_memory>
// block with replacement, or appends replacement at the end when no block is
// present. An empty replacement removes any existing block. It returns the
// modified content; identical to the input when no change was needed.
func spliceMemoryBlock(content, replacement string) string {
	start := strings.Index(content, learnedMemoryOpen)
	end := strings.Index(content, learnedMemoryClose)
	if start != -1 && end != -1 && end+len(learnedMemoryClose) >= start {
		before := content[:start]
		after := content[end+len(learnedMemoryClose):]
		if replacement == "" {
			return strings.TrimSpace(before + " " + after)
		}
		return strings.TrimSpace(before + replacement + after)
	}
	if replacement == "" {
		return content
	}
	if strings.TrimSpace(content) == "" {
		return replacement
	}
	return strings.TrimSpace(content) + "\n\n" + replacement
}
