package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agents"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// echoAgentProvider replies with the last user message.
type echoAgentProvider struct{}

func (echoAgentProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	prompt := ""
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == kajicoderuntime.MessageRoleUser {
			prompt = request.Messages[i].Content
			break
		}
	}
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	select {
	case <-ctx.Done():
		close(ch)
		return ch, ctx.Err()
	case ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: prompt}:
	}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// TestRegisterAgentsTaskRunsAfterSetBase proves the headless wiring: once the
// runtime is hydrated, a Task tool call runs a child in-process instead of
// failing with "no provider available".
func TestRegisterAgentsTaskRunsAfterSetBase(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(t.TempDir(), nil) {
		registry.Register(tool)
	}
	runtime, err := registerAgents(registry, t.TempDir(), 0)
	if err != nil {
		t.Fatalf("registerAgents: %v", err)
	}
	taskTool, ok := registry.Get("Task")
	if !ok {
		t.Fatal("Task not registered")
	}

	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(sessions.CreateInput{SessionID: "parent_session", Title: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	runtime.setBase(agents.ChildRunContext{
		Registry:        registry,
		Provider:        echoAgentProvider{},
		Store:           store,
		ParentSessionID: parent.SessionID,
		Cwd:             t.TempDir(),
	})

	result := registry.RunWithOptions(context.Background(), taskTool.Name(), map[string]any{
		"agent": "explorer", "prompt": "find the loader",
	}, tools.RunOptions{SessionID: parent.SessionID})
	if result.Status != tools.StatusOK {
		t.Fatalf("Task failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, "find the loader") {
		t.Fatalf("child output missing echo: %s", result.Output)
	}
	if result.Meta["session_id"] == "" {
		t.Fatalf("Task did not return a child session id")
	}
}
