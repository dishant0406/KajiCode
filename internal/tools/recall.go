package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/harness"
)

// recallMaxResults caps how many matching entries a recall call returns, keeping
// the output bounded regardless of how large the store has grown.
const recallMaxResults = 12

// recallTool searches KajiCode's durable learning memory. Prompt injection
// surfaces only the freshest few lessons; recall is the deep-retrieval path for
// when the model needs a specific past lesson that did not fit the prompt block.
// It reads the project store (and reports entry scope), never mutating anything.
type recallTool struct {
	baseTool
	learningRoot string
}

// NewRecallTool builds the recall tool over the given (project) learning root.
func NewRecallTool(learningRoot string) Tool {
	return &recallTool{
		learningRoot: learningRoot,
		baseTool: baseTool{
			name: "recall",
			description: "Search KajiCode's stored lessons (memory, prompt notes, recipes, subagents) for a topic. " +
				"Use it when a lesson you remember exists but is not in the current context. Empty query lists the " +
				"most recent lessons.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "Keywords to match against entry titles, content, and paths. Empty lists recent entries."},
					"kind":  {Type: "string", Description: "Optional filter: prompt, memory, recipe, or subagent.", Enum: []string{"prompt", "memory", "recipe", "subagent"}},
				},
				Required:             []string{},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect: SideEffectRead,
				Permission: PermissionAllow,
				Reason:     "Reads KajiCode's learning memory store only; never touches the workspace.",
			},
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false},
		},
	}
}

func (tool *recallTool) Run(_ context.Context, args map[string]any) Result {
	store := harness.NewStore(harness.StoreOptions{Dir: tool.learningRoot, Scope: harness.ScopeProject})
	state, _ := store.Load()
	if len(state.Entries) == 0 {
		return okResult("No stored lessons yet.")
	}

	query := strings.ToLower(strings.TrimSpace(stringArgSafe(args, "query")))
	kindFilter := strings.ToLower(strings.TrimSpace(stringArgSafe(args, "kind")))

	matched := make([]harness.Entry, 0, len(state.Entries))
	for _, entry := range state.Entries {
		if kindFilter != "" && string(entry.Kind) != kindFilter {
			continue
		}
		if query == "" || entryMatches(entry, query) {
			matched = append(matched, entry)
		}
	}
	if len(matched) == 0 {
		return okResult(fmt.Sprintf("No stored lessons matched %q.", query))
	}
	harness.OrderByRecency(matched)
	if len(matched) > recallMaxResults {
		matched = matched[:recallMaxResults]
	}

	header := "Stored lessons"
	if query != "" {
		header += fmt.Sprintf(" matching %q", query)
	}
	var b strings.Builder
	b.WriteString(header + ":\n")
	for _, entry := range matched {
		fmt.Fprintf(&b, "\n[%s:%s] %s (v%d, used %d×)\n", entry.Kind, entry.ID, entry.Title, entry.Version, entry.Reinforcements)
		if entry.Path != "" && entry.Path != "general" {
			fmt.Fprintf(&b, "  path: %s\n", entry.Path)
		}
		b.WriteString("  " + strings.TrimSpace(entry.Content) + "\n")
		if entry.Recipe != nil {
			fmt.Fprintf(&b, "  recipe %q commands: %d (run via recipe_run)\n", entry.Recipe.Name, len(entry.Recipe.Commands))
		}
	}
	return okResult(strings.TrimSpace(b.String()))
}

// entryMatches reports whether an entry contains the (already-lowercased) query
// in its title, content, or path.
func entryMatches(entry harness.Entry, query string) bool {
	haystack := strings.ToLower(entry.Title + "\n" + entry.Content + "\n" + entry.Path)
	return strings.Contains(haystack, query)
}
