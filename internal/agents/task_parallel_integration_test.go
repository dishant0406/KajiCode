package agents

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// taskDrivenProvider scripts one parent turn that issues tool calls, then a
// final-answer turn, so a test can drive the real agent.Run loop without a
// provider.
type taskDrivenProvider struct {
	turns    [][]kajicoderuntime.StreamEvent
	requests int
}

func (provider *taskDrivenProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	provider.requests++
	events := []kajicoderuntime.StreamEvent{{Type: kajicoderuntime.StreamEventDone}}
	if provider.requests <= len(provider.turns) {
		events = provider.turns[provider.requests-1]
	}
	ch := make(chan kajicoderuntime.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func taskCallEvents(callID, agentName, prompt string) []kajicoderuntime.StreamEvent {
	return []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: callID, ToolName: TaskToolName},
		{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: callID,
			ArgumentsFragment: fmt.Sprintf(`{"agent":%q,"prompt":%q}`, agentName, prompt)},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: callID},
	}
}

// TestTaskDelegationsRunConcurrentlyAcrossTheLoop is the end-to-end guard for
// the reported defect: a parent turn with two read-only delegations used to run
// them one after another (and stall). Driven through the real agent.Run loop,
// both child runs must be in flight at the same time.
func TestTaskDelegationsRunConcurrentlyAcrossTheLoop(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(effectStubTool{name: "read_file", effect: tools.EffectReadOnly})

	supervisor := Register(registry, fakeRunner(), ChildRunContext{})

	started := make(chan string, 2)
	release := make(chan struct{})
	active, maxActive := 0, 0
	supervisor.RunFunc = func(ctx context.Context, request ChildRequest) (ChildResult, error) {
		active++
		if active > maxActive {
			maxActive = active
		}
		started <- request.Agent.Name
		<-release
		active--
		return ChildResult{AgentName: request.Agent.Name, Text: "ok"}, nil
	}

	turn := append(taskCallEvents("call-1", "explorer", "a"), taskCallEvents("call-2", "code-review", "b")...)
	turn = append(turn, kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone})
	provider := &taskDrivenProvider{turns: [][]kajicoderuntime.StreamEvent{
		turn,
		{{Type: kajicoderuntime.StreamEventText, Content: "done"}, {Type: kajicoderuntime.StreamEventDone}},
	}}

	done := make(chan struct{})
	go func() {
		_, _ = agent.Run(context.Background(), "delegate", provider, agent.Options{
			Registry:       registry,
			PermissionMode: agent.PermissionModeAuto,
			MaxTurns:       3,
		})
		close(done)
	}()

	// Both children must reach their start signal before either can finish, so
	// receiving twice proves overlap; a sequential regression receives only one
	// and trips the timeout instead of hanging.
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 delegations started concurrently", i)
		}
	}
	close(release)
	<-done

	if maxActive < 2 {
		t.Fatalf("expected two Task delegations to overlap, max concurrency was %d", maxActive)
	}
}
