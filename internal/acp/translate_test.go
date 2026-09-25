package acp

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestAgentMessageAndThoughtChunks(t *testing.T) {
	m := agentMessageChunk("hello")
	if m.SessionUpdate != UpdateAgentMessageChunk || m.Content.Type != "text" || m.Content.Text != "hello" {
		t.Fatalf("unexpected message chunk: %+v", m)
	}
	th := agentThoughtChunk("thinking")
	if th.SessionUpdate != UpdateAgentThoughtChunk || th.Content.Text != "thinking" {
		t.Fatalf("unexpected thought chunk: %+v", th)
	}
}

func TestToolKindFor(t *testing.T) {
	cases := map[string]string{
		"read_file":      ToolKindRead,
		"list_directory": ToolKindRead,
		"grep":           ToolKindSearch,
		"glob":           ToolKindSearch,
		"edit_file":      ToolKindEdit,
		"apply_patch":    ToolKindEdit,
		"bash":           ToolKindExecute,
		"exec_command":   ToolKindExecute,
		"web_fetch":      ToolKindFetch,
		"todo_write":     ToolKindThink,
		"some_mcp_tool":  ToolKindOther,
	}
	for name, want := range cases {
		if got := toolKindFor(name); got != want {
			t.Errorf("toolKindFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestToolTitleAndHint(t *testing.T) {
	if got := toolTitle("read_file", `{"path":"src/main.go"}`); got != "read_file src/main.go" {
		t.Errorf("title = %q", got)
	}
	if got := toolTitle("bash", `{"command":"go test ./..."}`); got != "bash go test ./..." {
		t.Errorf("title = %q", got)
	}
	if got := toolTitle("mystery", `not json`); got != "mystery" {
		t.Errorf("malformed args should yield bare name, got %q", got)
	}
	if got := toolTitle("noargs", ``); got != "noargs" {
		t.Errorf("empty args should yield bare name, got %q", got)
	}
}

func TestToolCallStart(t *testing.T) {
	upd := toolCallStart(agent.ToolCall{ID: "tc1", Name: "read_file", Arguments: `{"path":"a.go"}`}, "/work")
	if upd.SessionUpdate != UpdateToolCall {
		t.Fatalf("sessionUpdate = %q", upd.SessionUpdate)
	}
	if upd.ToolCallID != "tc1" || upd.Status != ToolStatusInProgress || upd.Kind != ToolKindRead {
		t.Fatalf("unexpected start: %+v", upd)
	}
	if string(upd.RawInput) != `{"path":"a.go"}` {
		t.Fatalf("rawInput = %s", upd.RawInput)
	}
	// The initial file-tool call carries an absolute location so clients can
	// follow the agent before the tool finishes.
	if len(upd.Locations) != 1 || upd.Locations[0].Path != "/work/a.go" {
		t.Fatalf("expected absolute initial location, got %+v", upd.Locations)
	}
	// A non-file tool has no location to follow.
	shell := toolCallStart(agent.ToolCall{ID: "tc2", Name: "bash", Arguments: `{"command":"ls"}`}, "/work")
	if len(shell.Locations) != 0 {
		t.Fatalf("bash should not report a location, got %+v", shell.Locations)
	}
	// Malformed args must not produce invalid JSON on the wire.
	if got := toolCallStart(agent.ToolCall{ID: "x", Name: "bash", Arguments: "broken"}, "/work"); got.RawInput != nil {
		t.Fatalf("malformed args should drop rawInput, got %s", got.RawInput)
	}
}

func TestToolCallResult(t *testing.T) {
	oldContent := "package main\n"
	ok := toolCallResult(agent.ToolResult{
		ToolCallID:   "tc1",
		Name:         "edit_file",
		Status:       tools.StatusOK,
		Output:       "applied\n",
		ChangedFiles: []string{"a.go", ""},
		FileChanges:  []tools.FileChange{{Path: "a.go", OldContent: oldContent, NewContent: "package main\n\n"}},
	}, "/work")
	if ok.SessionUpdate != UpdateToolCallUpdate || ok.Status != ToolStatusCompleted {
		t.Fatalf("unexpected ok result: %+v", ok)
	}
	// A diff content item precedes the text summary.
	if len(ok.Content) != 2 || ok.Content[0].Type != "diff" {
		t.Fatalf("expected a diff then a text item, got %+v", ok.Content)
	}
	diff := ok.Content[0]
	if diff.Path != "/work/a.go" || diff.NewText != "package main\n\n" {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	if diff.OldText == nil || *diff.OldText != oldContent {
		t.Fatalf("expected old text preserved, got %v", diff.OldText)
	}
	if ok.Content[1].Type != "content" || ok.Content[1].Content.Text != "applied" {
		t.Fatalf("unexpected text content: %+v", ok.Content[1])
	}
	if len(ok.Locations) != 1 || ok.Locations[0].Path != "/work/a.go" {
		t.Fatalf("blank changed files should be dropped and paths made absolute, got %+v", ok.Locations)
	}

	failed := toolCallResult(agent.ToolResult{ToolCallID: "tc2", Status: tools.StatusError, Output: "boom"}, "/work")
	if failed.Status != ToolStatusFailed {
		t.Fatalf("error result should be failed, got %q", failed.Status)
	}
}

func TestToolCallResultCreateEmitsNullOldText(t *testing.T) {
	upd := toolCallResult(agent.ToolResult{
		ToolCallID:   "tc1",
		Name:         "write_file",
		Status:       tools.StatusOK,
		Output:       "Created a.go",
		ChangedFiles: []string{"a.go"},
		FileChanges:  []tools.FileChange{{Path: "a.go", OldContent: "", NewContent: "hello"}},
	}, "/work")
	if len(upd.Content) == 0 || upd.Content[0].Type != "diff" {
		t.Fatalf("expected a diff, got %+v", upd.Content)
	}
	// A new file must serialize oldText as null, not an omitted or empty string.
	raw, err := json.Marshal(upd.Content[0])
	if err != nil {
		t.Fatalf("marshal diff: %v", err)
	}
	if !strings.Contains(string(raw), `"oldText":null`) {
		t.Fatalf("create diff must carry oldText:null, got %s", raw)
	}
}

func TestAbsoluteWorkspacePath(t *testing.T) {
	if got := absoluteWorkspacePath("/work", "src/a.go"); got != "/work/src/a.go" {
		t.Fatalf("relative path = %q", got)
	}
	if got := absoluteWorkspacePath("/work", "/abs/a.go"); got != "/abs/a.go" {
		t.Fatalf("absolute path = %q", got)
	}
	if got := absoluteWorkspacePath("/work", "/abs/../a.go"); got != "/a.go" {
		t.Fatalf("absolute path should be cleaned, got %q", got)
	}
}

func TestPlanUpdateAndStatus(t *testing.T) {
	upd := planUpdate([]tools.PlanItem{
		{Content: "step a", Status: "completed"},
		{Content: "step b", Status: "in_progress"},
		{Content: "step c", Status: "failed"},
		{Content: "step d", Status: "weird"},
	})
	if upd.SessionUpdate != UpdatePlan || len(upd.Entries) != 4 {
		t.Fatalf("unexpected plan: %+v", upd)
	}
	want := []string{PlanStatusCompleted, PlanStatusInProgress, PlanStatusCompleted, PlanStatusPending}
	for i, w := range want {
		if upd.Entries[i].Status != w {
			t.Errorf("entry %d status = %q, want %q", i, upd.Entries[i].Status, w)
		}
		if upd.Entries[i].Priority != PlanPriorityMedium {
			t.Errorf("entry %d priority = %q", i, upd.Entries[i].Priority)
		}
	}
}

func TestPromptText(t *testing.T) {
	got := promptText([]ContentBlock{
		TextBlock("hello "),
		ContentBlock{Type: "image", Data: "base64", MimeType: "image/png"},
		TextBlock("world"),
	})
	if got != "hello world" {
		t.Fatalf("promptText = %q", got)
	}
}

func TestPromptTextIncludesResources(t *testing.T) {
	got := promptText([]ContentBlock{
		{Type: "resource_link", URI: "file:///a.go", Name: "a.go"},
		{Type: "resource", Resource: json.RawMessage(`{"uri":"file:///b.go","text":"package b"}`)},
	})
	if !strings.Contains(got, "file:///a.go") || !strings.Contains(got, "package b") {
		t.Fatalf("resource blocks dropped: %q", got)
	}
}

func TestToolTitleTruncateHintRuneSafe(t *testing.T) {
	// A 61-character string containing multi-byte UTF-8 runes (emojis / CJK characters).
	// We want to verify that it is truncated without cutting any runes or producing invalid UTF-8.
	longPath := "📁/项目/非常长的路径名称/测试/🚀/emoji-and-cjk-characters-which-are-very-long-and-exceed-sixty-characters"

	// Create JSON args for read_file
	rawArgs := `{"path":"` + longPath + `"}`
	got := toolTitle("read_file", rawArgs)

	expectedPrefix := "read_file "
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Fatalf("expected title to start with %q, got %q", expectedPrefix, got)
	}

	hint := strings.TrimPrefix(got, expectedPrefix)
	// Hint should end with the ellipsis character
	if !strings.HasSuffix(hint, "…") {
		t.Fatalf("expected truncated hint to end with ellipsis, got %q", hint)
	}

	// Check that we don't have invalid UTF-8 runes
	if !utf8.ValidString(hint) {
		t.Fatalf("truncated hint is not a valid UTF-8 string: %q", hint)
	}

	// The rune count of the hint (excluding ellipsis) should be exactly 60
	runes := []rune(strings.TrimSuffix(hint, "…"))
	if len(runes) != 60 {
		t.Fatalf("expected exactly 60 runes before ellipsis, got %d (hint: %q)", len(runes), hint)
	}
}
