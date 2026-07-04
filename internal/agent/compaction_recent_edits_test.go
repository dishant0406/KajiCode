package agent

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// recentEdits extracts each mutated file's path and a one-line note from the
// matching tool result, latest note per path in first-seen order.
func TestRecentEditsExtractsPathsAndNotes(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "e1", Name: "write_file", Arguments: `{"path":"internal/foo.go","content":"package foo"}`},
		}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "e1", Content: "Wrote internal/foo.go (12 lines)"},
		{Role: zeroruntime.MessageRoleAssistant, ToolCalls: []zeroruntime.ToolCall{
			{ID: "e2", Name: "edit_file", Arguments: `{"path":"internal/bar.go","old_string":"a","new_string":"b"}`},
		}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "e2", Content: "Applied edit to internal/bar.go"},
	}

	edits := recentEdits(messages)
	if len(edits) != 2 {
		t.Fatalf("expected 2 edited files, got %d: %#v", len(edits), edits)
	}
	if edits[0].name != "internal/foo.go" || !strings.Contains(edits[0].body, "12 lines") {
		t.Fatalf("first edit = %#v, want foo.go with its note", edits[0])
	}
	if edits[1].name != "internal/bar.go" || !strings.Contains(edits[1].body, "Applied edit") {
		t.Fatalf("second edit = %#v, want bar.go with its note", edits[1])
	}
}

// After compaction elides the editing turns, the preserved-state block still
// names the edited files and what changed, so the model needn't re-read them.
func TestCompactionPreservesRecentEdits(t *testing.T) {
	messages := []zeroruntime.Message{
		{Role: zeroruntime.MessageRoleSystem, Content: "system"},
		{Role: zeroruntime.MessageRoleUser, Content: "add a flag"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "editing", ToolCalls: []zeroruntime.ToolCall{
			{ID: "e1", Name: "write_file", Arguments: `{"path":"cmd/main.go","content":"..."}`},
		}},
		{Role: zeroruntime.MessageRoleTool, ToolCallID: "e1", Content: "Wrote cmd/main.go (adds --version flag)"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "done"},
		{Role: zeroruntime.MessageRoleUser, Content: "continue"},
		{Role: zeroruntime.MessageRoleAssistant, Content: "continuing"},
	}
	summary := compactStateConversation(t, messages)

	state := parsePreservedStateBlock(summary)
	if len(state.RecentEdits) != 1 {
		t.Fatalf("expected 1 preserved edit, got %#v", state.RecentEdits)
	}
	if state.RecentEdits[0].Path != "cmd/main.go" || !strings.Contains(state.RecentEdits[0].Note, "--version") {
		t.Fatalf("preserved edit = %#v, want cmd/main.go + its note", state.RecentEdits[0])
	}
}

// A fresh edit note for a path overrides the one carried from an earlier
// compaction (newer wins), rather than duplicating the path.
func TestRecentEditsMergeNewerWins(t *testing.T) {
	prior := preservedState{RecentEdits: []preservedEdit{{Path: "a.go", Note: "old note"}}}
	older := preservedEditsToEntries(prior.RecentEdits)
	newer := []skillEntry{{name: "a.go", body: "new note"}, {name: "b.go", body: "added"}}

	merged := mergeSkillEntries(older, newer)
	if len(merged) != 2 {
		t.Fatalf("expected a.go merged (not duplicated) + b.go, got %#v", merged)
	}
	if merged[0].name != "a.go" || merged[0].body != "new note" {
		t.Fatalf("a.go should take the newer note, got %#v", merged[0])
	}
}
