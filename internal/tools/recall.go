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
// It reads both the project and global stores (project entries shadow global on
// the same kind:id) and reports each entry's scope, never mutating anything.
type recallTool struct {
	baseTool
	projectRoot string
	globalRoot  string
}

// NewRecallTool builds the recall tool over the project and global learning
// roots. An empty globalRoot disables the global source (used by tests).
func NewRecallTool(projectRoot, globalRoot string) Tool {
	return &recallTool{
		projectRoot: projectRoot,
		globalRoot:  globalRoot,
		baseTool: baseTool{
			name: "recall",
			description: "Search KajiCode's stored lessons (memory, prompt notes, recipes, subagents) across the " +
				"project and global memory for a topic. Use it when a lesson you remember exists but is not in the " +
				"current context. Empty query lists the most recent lessons.",
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
	project := loadEntries(tool.projectRoot, harness.ScopeProject)
	global := loadEntries(tool.globalRoot, harness.ScopeGlobal)
	entries := harness.MergeHarnessStates(harness.State{Entries: global}, harness.State{Entries: project})
	if len(entries) == 0 {
		return okResult("No stored lessons yet.")
	}

	query := strings.ToLower(strings.TrimSpace(stringArgSafe(args, "query")))
	kindFilter := strings.ToLower(strings.TrimSpace(stringArgSafe(args, "kind")))

	matched := make([]harness.Entry, 0, len(entries))
	for _, entry := range entries {
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
		scope := entry.Scope
		if scope == "" {
			scope = harness.ScopeProject
		}
		fmt.Fprintf(&b, "\n[%s:%s:%s] %s (v%d, used %d×)\n", scope, entry.Kind, entry.ID, entry.Title, entry.Version, entry.Reinforcements)
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

// loadEntries reads the entries of one store, tolerating a missing store. The
// scope is applied to entries that lack one so recall can label them reliably.
func loadEntries(root string, scope harness.Scope) []harness.Entry {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	store := harness.NewStore(harness.StoreOptions{Dir: root, Scope: scope})
	state, _ := store.Load()
	for i := range state.Entries {
		if state.Entries[i].Scope == "" {
			state.Entries[i].Scope = scope
		}
	}
	return state.Entries
}

// entryMatches reports whether an entry contains the (already-lowercased) query
// in its title, content, or path.
func entryMatches(entry harness.Entry, query string) bool {
	haystack := strings.ToLower(entry.Title + "\n" + entry.Content + "\n" + entry.Path)
	return strings.Contains(haystack, query)
}
