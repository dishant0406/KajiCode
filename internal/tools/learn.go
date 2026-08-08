package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/harness"
)

// LearnRequestMeta is the Meta key a learn run call sets to ask the agent loop
// to run a learning pass at the next safe turn boundary. It mirrors the
// escalate_to_model convention: the tool only flags intent; the loop performs
// the scheduled work when Options.Harness wires a learning controller.
const LearnRequestMeta = "request_learn"

// learnTool is the model-facing manual learning surface. It mirrors
// prime-agent's rlm.harness.* / refine.run split:
//
//   - status: report whether a learning pass is pending/in flight and list the
//     current memory/prompt/recipe/subagent entries.
//   - run: signal the loop to run a full review+plan+apply at the next turn
//     boundary (the loop checks Meta[request_learn]).
//   - create/update/delete: direct editorial CRUD on the durable store so the
//     model can persist a lesson immediately without waiting for review.
//
// The tool only schedules or writes local learning state; it never touches the
// workspace.
type learnTool struct {
	baseTool
	learningRoot string
	now          func() time.Time
}

// NewLearnTool builds the manual learn tool over the given learning root.
func NewLearnTool(learningRoot string) Tool {
	return &learnTool{
		learningRoot: learningRoot,
		now:          time.Now,
		baseTool: baseTool{
			name: "learn",
			description: "Manage KajiCode's self-learning memory. status reports pending learning and current entries; " +
				"run schedules a review pass at the next turn boundary; create/update/delete persist prompt notes, " +
				"durable facts (memory), reusable procedures (recipe), or delegation specs (subagent).",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"action":  {Type: "string", Description: "status, run, create, update, or delete.", Enum: []string{"status", "run", "create", "update", "delete"}},
					"kind":    {Type: "string", Description: "prompt, memory, recipe, or subagent (create/update/delete).", Enum: []string{"prompt", "memory", "recipe", "subagent"}},
					"id":      {Type: "string", Description: "Entry id (delete/update, and explicit create). Omit for create to derive from title."},
					"title":   {Type: "string", Description: "Entry title (create/update)."},
					"content": {Type: "string", Description: "Entry content (create/update)."},
					"path":    {Type: "string", Description: "Optional path/topic grouping (create/update; defaults to general)."},
					"recipe":  {Type: "string", Description: "Optional JSON recipe manifest for kind=recipe create/update. Prefer building recipes via the review pass."},
				},
				Required:             []string{"action"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect:      SideEffectRead,
				Permission:      PermissionAllow,
				Reason:          "Reads and writes KajiCode's learning memory store only; never touches the workspace.",
				AdvertiseInAuto: true,
			},
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false},
		},
	}
}

func (tool *learnTool) Run(_ context.Context, args map[string]any) Result {
	action, err := stringArg(args, "action", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for learn: " + err.Error())
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "status":
		return tool.status(args)
	case "run":
		return tool.schedule()
	case "create", "update", "delete":
		return tool.edit(strings.ToLower(strings.TrimSpace(action)), args)
	default:
		return errorResult(fmt.Sprintf("Error: learn action must be status, run, create, update, or delete, got %q", action))
	}
}

func (tool *learnTool) status(_ map[string]any) Result {
	store := harness.NewStore(harness.StoreOptions{Dir: tool.learningRoot, Scope: harness.ScopeGlobal, Now: tool.now})
	state, _ := store.Load()
	var b strings.Builder
	b.WriteString("Learning status:\n")
	b.WriteString("  auto: enabled (default; tune via config or /settings)\n")
	if len(state.Entries) == 0 {
		b.WriteString("  entries: none yet\n")
	} else {
		b.WriteString("  entries:\n")
		// Sort within the store; render grouped by kind for readability.
		byKind := map[harness.Kind]bool{}
		for _, e := range state.Entries {
			if !byKind[e.Kind] {
				byKind[e.Kind] = true
			}
		}
		b.WriteString("    groups: " + strings.Join(kindNames(byKind), ", ") + "\n")
		// Count per kind.
		counts := map[harness.Kind]int{}
		for _, e := range state.Entries {
			counts[e.Kind]++
		}
		for _, kind := range []harness.Kind{harness.KindPrompt, harness.KindMemory, harness.KindRecipe, harness.KindSubagent} {
			fmt.Fprintf(&b, "      %s: %d\n", kind, counts[kind])
		}
		// Recent entries (last 10 by updatedAt best-effort).
		recent := append([]harness.Entry(nil), state.Entries...)
		sort.Slice(recent, func(i, j int) bool { return recent[i].UpdatedAt > recent[j].UpdatedAt })
		limit := len(recent)
		if limit > 10 {
			limit = 10
		}
		for _, e := range recent[:limit] {
			summary := strings.TrimSpace(strings.ReplaceAll(e.Content, "\n", " "))
			if len(summary) > 80 {
				summary = summary[:77] + "..."
			}
			fmt.Fprintf(&b, "    - [%s:%s] %s (v%d): %s\n", e.Kind, e.ID, e.Title, e.Version, summary)
		}
		if len(state.Refinements) > 0 {
			fmt.Fprintf(&b, "  refinements: %d\n", len(state.Refinements))
		}
	}
	return okResult(strings.TrimSpace(b.String()))
}

func (tool *learnTool) schedule() Result {
	result := okResult("Learning review scheduled: KajiCode will run a review+plan+apply pass at the next safe turn boundary.")
	result.Meta = map[string]string{LearnRequestMeta: "true"}
	return result
}

func (tool *learnTool) edit(action string, args map[string]any) Result {
	kind := harness.Kind(strings.ToLower(strings.TrimSpace(stringArgSafe(args, "kind"))))
	switch kind {
	case harness.KindPrompt, harness.KindMemory, harness.KindRecipe, harness.KindSubagent:
	default:
		return errorResult("Error: learn kind must be prompt, memory, recipe, or subagent")
	}

	id := strings.TrimSpace(stringArgSafe(args, "id"))
	title := strings.TrimSpace(stringArgSafe(args, "title"))
	content := strings.TrimSpace(stringArgSafe(args, "content"))
	path := strings.TrimSpace(stringArgSafe(args, "path"))

	store := harness.NewStore(harness.StoreOptions{Dir: tool.learningRoot, Scope: harness.ScopeGlobal, Now: tool.now})
	state, err := store.Load()
	if err != nil {
		state = harness.State{Scope: harness.ScopeGlobal}
	}

	switch action {
	case "create":
		if id == "" {
			id = harness.Slug(title, string(kind))
		}
		if id == "" {
			return errorResult("Error: learn id could not be derived from title; supply an explicit id")
		}
		if harness.BasePromptID == id {
			return errorResult(fmt.Sprintf("Error: %s is immutable and cannot be created", harness.BasePromptID))
		}
		if entryExists(state.Entries, kind, id) {
			return errorResult(fmt.Sprintf("Error: %s:%s already exists; use update", kind, id))
		}
		entry := harness.NewEntry(kind, title, content, id, path, harness.ScopeGlobal, "manual", tool.now())
		if kind == harness.KindRecipe {
			recipe, perr := parseRecipeArg(args)
			if perr != nil {
				return errorResult(perr.Error())
			}
			entry.Recipe = recipe
		}
		state.Entries = append(state.Entries, entry)
	case "update":
		if id == "" {
			return errorResult("Error: learn id is required for update")
		}
		idx := findEntry(state.Entries, kind, id)
		if idx < 0 {
			return errorResult(fmt.Sprintf("Error: %s:%s does not exist; use create", kind, id))
		}
		if content != "" {
			state.Entries[idx].Content = content
		}
		if title != "" {
			state.Entries[idx].Title = title
		}
		if path != "" {
			state.Entries[idx].Path = path
		}
		if kind == harness.KindRecipe {
			recipe, perr := parseRecipeArg(args)
			if perr != nil {
				return errorResult(perr.Error())
			}
			state.Entries[idx].Recipe = recipe
		}
		state.Entries[idx].UpdatedAt = tool.now().UTC().Format(time.RFC3339)
		state.Entries[idx].Version++
	case "delete":
		if id == "" {
			return errorResult("Error: learn id is required for delete")
		}
		if harness.BasePromptID == id {
			return errorResult(fmt.Sprintf("Error: %s is immutable and cannot be deleted", harness.BasePromptID))
		}
		idx := findEntry(state.Entries, kind, id)
		if idx < 0 {
			return errorResult(fmt.Sprintf("Error: %s:%s does not exist", kind, id))
		}
		state.Entries = append(state.Entries[:idx], state.Entries[idx+1:]...)
	}

	if err := store.Save(state); err != nil {
		return errorResult("Error: failed to save learning state: " + err.Error())
	}
	return okResult(fmt.Sprintf("ok: %s %s:%s", action, kind, id))
}

// parseRecipeArg decodes an optional inline recipe JSON argument. It defaults to
// an empty (commandless) recipe for the "notes" style, but a recipe entry with
// no commands is still valid as a documented procedure (validated at run time).
// A malformed JSON string is a hard error.
func parseRecipeArg(args map[string]any) (*harness.Recipe, error) {
	raw := strings.TrimSpace(stringArgSafe(args, "recipe"))
	if raw == "" {
		return &harness.Recipe{Name: strings.TrimSpace(stringArgSafe(args, "title"))}, nil
	}
	var recipe harness.Recipe
	if err := json.Unmarshal([]byte(raw), &recipe); err != nil {
		return nil, fmt.Errorf("Error: invalid recipe JSON: %v", err)
	}
	return &recipe, nil
}

func kindNames(set map[harness.Kind]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func stringArgSafe(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func entryExists(entries []harness.Entry, kind harness.Kind, id string) bool {
	return findEntry(entries, kind, id) >= 0
}

func findEntry(entries []harness.Entry, kind harness.Kind, id string) int {
	for i, e := range entries {
		if e.Kind == kind && e.ID == id {
			return i
		}
	}
	return -1
}
