package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// echoProvider replies with the last user message, so a child's final answer is
// deterministic without a live provider.
type echoProvider struct{}

func (echoProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
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

// TestRunChildAgentInProcess is the end-to-end contract for the rewrite: a Task
// child runs IN-PROCESS through the real agent loop, with its own parent-linked
// child session, and the parent sees the child's final answer.
func TestRunChildAgentInProcess(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, err := store.Create(sessions.CreateInput{SessionID: "parent_session", Title: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "read_file"})
	registry.Register(stubTool{name: "bash"})

	runner := NewRunner(Paths{}, DefaultMaxDepth)
	explorer, err := runner.Resolve("explorer")
	if err != nil {
		t.Fatalf("resolve explorer: %v", err)
	}

	result, err := RunChildAgent(context.Background(), ChildRequest{
		Agent:  explorer,
		Prompt: "inspect the parser",
		Context: ChildRunContext{
			Registry:        registry,
			Provider:        echoProvider{},
			Store:           store,
			ParentSessionID: parent.SessionID,
			Cwd:             t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("RunChildAgent: %v", err)
	}
	if !strings.Contains(result.Text, "inspect the parser") {
		t.Fatalf("child text = %q, want the echoed prompt", result.Text)
	}
	if result.Status != statusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}

	child, err := store.Get(result.SessionID)
	if err != nil || child == nil {
		t.Fatalf("child session not persisted: %v", err)
	}
	if child.SessionKind != sessions.SessionKindChild || child.ParentSessionID != parent.SessionID {
		t.Fatalf("child not linked to parent: %#v", child)
	}
	if child.AgentName != "explorer" {
		t.Fatalf("child agent name = %q, want explorer", child.AgentName)
	}
}

// TestRunChildAgentFiltersTools proves the child can never exceed the parent and
// only gets the tools its own ruleset allows — the core fix for "no tool access".
func TestRunChildAgentFiltersTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "read_file"})
	registry.Register(stubTool{name: "bash"})

	childRegistry := FilterRegistry(registry, Rules([]string{"read"}, nil))
	if _, ok := childRegistry.Get("bash"); ok {
		t.Fatalf("explorer must not be able to run bash")
	}
	if _, ok := childRegistry.Get("read_file"); !ok {
		t.Fatalf("explorer must keep read_file")
	}
}

// TestChildRunForwardsDepth proves nested delegation is actually bounded: the
// child's agent loop must see Depth+1 so a grandchild Task is rejected at the cap.
func TestChildRunForwardsDepth(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, _ := store.Create(sessions.CreateInput{SessionID: "parent_session", Title: "parent"})
	runner := NewRunner(Paths{}, 4)
	explorer, err := runner.Resolve("explorer")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "read_file"})

	result, err := RunChildAgent(context.Background(), ChildRequest{
		Agent:  explorer,
		Prompt: "x",
		Context: ChildRunContext{
			Registry:        registry,
			Provider:        echoProvider{},
			Store:           store,
			ParentSessionID: parent.SessionID,
			Cwd:             t.TempDir(),
		},
		Depth: 2,
	})
	if err != nil {
		t.Fatalf("RunChildAgent: %v", err)
	}
	child, err := store.Get(result.SessionID)
	if err != nil || child == nil {
		t.Fatalf("child session: %v", err)
	}
	if child.Depth != 3 {
		t.Fatalf("child session depth = %d, want 3 (parent depth 2 + 1)", child.Depth)
	}
}

// TestChildRunPersistsReplayableEvents proves the child session records the
// specialist_start/stop payload shape the TUI resume decoder reads.
func TestChildRunPersistsReplayableEvents(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	parent, _ := store.Create(sessions.CreateInput{SessionID: "parent_session", Title: "parent"})
	runner := NewRunner(Paths{}, 4)
	explorer, _ := runner.Resolve("explorer")
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "read_file"})

	result, err := RunChildAgent(context.Background(), ChildRequest{
		Agent:       explorer,
		Prompt:      "x",
		Description: "find stuff",
		Context: ChildRunContext{
			Registry:        registry,
			Provider:        echoProvider{},
			Store:           store,
			ParentSessionID: parent.SessionID,
			Cwd:             t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("RunChildAgent: %v", err)
	}
	events, err := store.ReadEvents(result.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	var sawStart, sawStop bool
	for _, event := range events {
		payload := map[string]any{}
		_ = json.Unmarshal(event.Payload, &payload)
		switch event.Type {
		case sessions.EventSpecialistStart:
			sawStart = payload["childSessionId"] == result.SessionID && payload["specialist"] == "explorer"
		case sessions.EventSpecialistStop:
			sawStop = payload["childSessionId"] == result.SessionID && payload["specialist"] == "explorer" && payload["status"] == statusCompleted
		}
	}
	if !sawStart || !sawStop {
		t.Fatalf("missing replayable events: start=%v stop=%v", sawStart, sawStop)
	}
}
