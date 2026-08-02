package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestRunCanonicalizesToolAliasBeforeReplay(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "c1", ToolName: "cat"},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "c1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "c1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
		{
			{Type: kajicoderuntime.StreamEventText, Content: "done"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
	var observed []string

	result, err := Run(context.Background(), "read notes", provider, Options{
		MaxTurns:       3,
		Cwd:            root,
		Registry:       registry,
		PermissionMode: PermissionModeAuto,
		OnToolCall: func(call ToolCall) {
			observed = append(observed, call.Name)
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("FinalAnswer = %q, want done", result.FinalAnswer)
	}
	if len(observed) != 1 || observed[0] != "read_file" {
		t.Fatalf("OnToolCall names = %#v, want [read_file]", observed)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("expected second provider request, got %d", len(provider.requests))
	}
	if got := replayedToolCallName(provider.requests[1].Messages); got != "read_file" {
		t.Fatalf("replayed tool call name = %q, want read_file", got)
	}
}

func replayedToolCallName(messages []kajicoderuntime.Message) string {
	for _, message := range messages {
		if message.Role != kajicoderuntime.MessageRoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		return message.ToolCalls[0].Name
	}
	return ""
}
