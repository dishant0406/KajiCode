package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/harness"
)

// recallMaxResults caps how many notes one recall call returns, keeping the
// output bounded however large the stores grow.
const recallMaxResults = 5

// recallTool searches the notes KajiCode saved from earlier sessions. The most
// relevant notes are already attached to each request automatically; recall is
// for follow-up questions in the middle of a task. It reads the project and
// global stores (a project note shadows a global one with the same id) and
// never changes anything.
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
			description: "Search notes saved from earlier sessions: fixes, project rules, gotchas, and saved steps. " +
				"Use a few keywords such as the error text, file, command, or feature name. " +
				"An empty query lists the most recent notes.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "A few keywords to search for. Empty lists the most recent notes."},
					"kind":  {Type: "string", Description: "Optional filter: prompt, memory, recipe, or subagent.", Enum: []string{"prompt", "memory", "recipe", "subagent"}},
				},
				Required:             []string{},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect: SideEffectRead,
				Permission: PermissionAllow,
				Reason:     "Reads KajiCode's saved notes only; never touches the workspace.",
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
		return okResult("No notes saved from earlier sessions yet.")
	}
	query := strings.TrimSpace(stringArgSafe(args, "query"))
	kindFilter := strings.ToLower(strings.TrimSpace(stringArgSafe(args, "kind")))
	if kindFilter != "" {
		entries = filterKind(entries, harness.Kind(kindFilter))
		if len(entries) == 0 {
			return okResult(fmt.Sprintf("No saved notes of kind %q.", kindFilter))
		}
	}

	matched := harness.Search(entries, query, recallMaxResults)
	if len(matched) == 0 {
		return okResult(fmt.Sprintf("No earlier notes about %q. Try fewer or different keywords.", query))
	}

	var b strings.Builder
	if query == "" {
		b.WriteString("Most recent notes from earlier sessions:\n")
	} else {
		fmt.Fprintf(&b, "Notes from earlier sessions about %q:\n", query)
	}
	for i, entry := range matched {
		fmt.Fprintf(&b, "\n%d. %s (%s, id: %s)\n", i+1, entry.Title, scopeLabel(entry.Scope), entry.ID)
		b.WriteString(strings.TrimSpace(entry.Content) + "\n")
		if hint := entry.RunHint(); hint != "" {
			b.WriteString(hint + "\n")
		}
	}
	return okResult(strings.TrimSpace(b.String()))
}

// scopeLabel names where a note applies in plain words. Recall reads only the
// project and global stores.
func scopeLabel(scope harness.Scope) string {
	if scope == harness.ScopeGlobal {
		return "all projects"
	}
	return "this project"
}

func filterKind(entries []harness.Entry, kind harness.Kind) []harness.Entry {
	var out []harness.Entry
	for _, entry := range entries {
		if entry.Kind == kind {
			out = append(out, entry)
		}
	}
	return out
}

// loadEntries reads the entries of one store, tolerating a missing store.
// Loading labels every entry with the store's scope.
func loadEntries(root string, scope harness.Scope) []harness.Entry {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	state, _ := harness.NewStore(harness.StoreOptions{Dir: root, Scope: scope}).Load()
	return state.Entries
}
