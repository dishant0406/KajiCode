package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/tools"
)

func newTestSupervisor(run ChildRunFunc) *Supervisor {
	runner := NewRunner(Paths{}, DefaultMaxDepth)
	runner.Load = func(Paths) (LoadResult, error) { return LoadResult{Agents: Builtins()}, nil }
	supervisor := NewSupervisor(runner, ChildRunContext{Registry: tools.NewRegistry()})
	supervisor.RunFunc = run
	return supervisor
}

func TestBackgroundTaskOutputAndStop(t *testing.T) {
	release := make(chan struct{})
	supervisor := newTestSupervisor(func(ctx context.Context, request ChildRequest) (ChildResult, error) {
		select {
		case <-release:
			return ChildResult{AgentName: request.Agent.Name, SessionID: "child_1", Text: "done"}, nil
		case <-ctx.Done():
			return ChildResult{AgentName: request.Agent.Name, SessionID: "child_1", Status: statusError}, ctx.Err()
		}
	})
	taskTool := NewTaskTool(supervisor)
	outputTool := NewOutputTool(supervisor)
	stopTool := NewStopTool(supervisor)

	result := taskTool.Run(context.Background(), map[string]any{
		"agent": "explorer", "prompt": "x", "run_in_background": true,
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("launch: %s", result.Output)
	}
	taskID := result.Meta["task_id"]
	if taskID == "" {
		t.Fatal("no task_id returned")
	}

	// Still running: a non-blocking read reports running.
	out := outputTool.Run(context.Background(), map[string]any{"task_id": taskID})
	if !strings.Contains(out.Output, "running") {
		t.Fatalf("expected running, got %s", out.Output)
	}

	// Stop cancels it.
	stop := stopTool.Run(context.Background(), map[string]any{"task_id": taskID})
	if stop.Status != tools.StatusOK {
		t.Fatalf("stop: %s", stop.Output)
	}
	// A second stop reports it is no longer running.
	again := stopTool.Run(context.Background(), map[string]any{"task_id": taskID})
	if !strings.Contains(again.Output, "canceled") && !strings.Contains(again.Output, "already") {
		t.Fatalf("second stop = %s", again.Output)
	}
}

func TestBackgroundCompletionPush(t *testing.T) {
	supervisor := newTestSupervisor(func(_ context.Context, request ChildRequest) (ChildResult, error) {
		return ChildResult{AgentName: request.Agent.Name, Text: "report body"}, nil
	})
	supervisor.NotifyCompletions(true)
	taskTool := NewTaskTool(supervisor)

	result := taskTool.Run(context.Background(), map[string]any{
		"agent": "worker", "prompt": "x", "description": "audit", "run_in_background": true,
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("launch: %s", result.Output)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if blocks := supervisor.DrainCompletedTasks(); len(blocks) > 0 {
			if !strings.Contains(blocks[0], "report body") {
				t.Fatalf("completion block missing child text: %s", blocks[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no completion delivered")
}

func TestBlockingOutputWaitsForResult(t *testing.T) {
	supervisor := newTestSupervisor(func(_ context.Context, request ChildRequest) (ChildResult, error) {
		time.Sleep(30 * time.Millisecond)
		return ChildResult{AgentName: request.Agent.Name, Text: "late result"}, nil
	})
	taskTool := NewTaskTool(supervisor)
	outputTool := NewOutputTool(supervisor)

	result := taskTool.Run(context.Background(), map[string]any{
		"agent": "explorer", "prompt": "x", "run_in_background": true,
	})
	blocked := outputTool.Run(context.Background(), map[string]any{
		"task_id": result.Meta["task_id"], "block": true, "timeout": 2000,
	})
	if !strings.Contains(blocked.Output, "late result") {
		t.Fatalf("blocking read = %s", blocked.Output)
	}
}
