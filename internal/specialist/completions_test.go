package specialist

import (
	"strings"
	"sync"
	"testing"

	"github.com/dishant0406/KajiCode/internal/background"
)

func TestCompletionCollectorPushDrain(t *testing.T) {
	collector := newCompletionCollector()
	collector.setEnabled(true)
	collector.push(backgroundCompletion{taskID: "t1", name: "explorer", status: background.StatusCompleted})
	collector.push(backgroundCompletion{taskID: "t2", name: "verifier", status: background.StatusError})

	first := collector.drain()
	if len(first) != 2 {
		t.Fatalf("drain = %d completions, want 2", len(first))
	}
	if first[0].taskID != "t1" || first[1].taskID != "t2" {
		t.Fatalf("drain order changed: %v then %v", first[0].taskID, first[1].taskID)
	}
	if again := collector.drain(); again != nil {
		t.Fatalf("second drain returned %v, want empty", again)
	}
}

func TestCompletionCollectorDropsWhenDisabled(t *testing.T) {
	collector := newCompletionCollector()
	collector.push(backgroundCompletion{taskID: "t1"})
	if got := collector.drain(); got != nil {
		t.Fatalf("disabled collector delivered %v", got)
	}
	collector.setEnabled(true)
	collector.push(backgroundCompletion{taskID: "t2"})
	if got := collector.drain(); len(got) != 1 {
		t.Fatalf("enabled collector delivered %v, want 1", got)
	}
}

func TestCompletionCollectorDeduplicates(t *testing.T) {
	collector := newCompletionCollector()
	collector.setEnabled(true)
	collector.push(backgroundCompletion{taskID: "t1", status: background.StatusCompleted})
	collector.push(backgroundCompletion{taskID: "t1", status: background.StatusError})
	if got := collector.drain(); len(got) != 1 || got[0].status != background.StatusCompleted {
		t.Fatalf("duplicate delivery: %v", got)
	}
}

func TestCompletionCollectorConcurrentPushDrain(t *testing.T) {
	collector := newCompletionCollector()
	collector.setEnabled(true)
	var waitGroup sync.WaitGroup
	for i := 0; i < 50; i++ {
		waitGroup.Add(1)
		go func(i int) {
			defer waitGroup.Done()
			collector.push(backgroundCompletion{taskID: "same-task", name: "explorer"})
		}(i)
	}
	waitGroup.Wait()
	if got := collector.drain(); len(got) != 1 {
		t.Fatalf("concurrent duplicate pushes delivered %d results, want exactly 1", len(got))
	}
}

func TestRenderCompletion(t *testing.T) {
	completed := renderCompletion(backgroundCompletion{
		taskID:      "specialist_abc",
		name:        "explorer",
		description: "map the auth flow",
		status:      background.StatusCompleted,
		summary:     "Found three call sites.",
	})
	for _, want := range []string{
		`<task_result id="specialist_abc" state="completed">`,
		"<summary>specialist explorer: map the auth flow</summary>",
		"Found three call sites.",
	} {
		if !strings.Contains(completed, want) {
			t.Errorf("completed render missing %q in:\n%s", want, completed)
		}
	}

	failed := renderCompletion(backgroundCompletion{
		taskID:  "specialist_err",
		name:    "verifier",
		status:  background.StatusError,
		summary: "",
	})
	if !strings.Contains(failed, `state="error"`) {
		t.Errorf("error render missing error state:\n%s", failed)
	}
	if !strings.Contains(failed, "(the sub-agent produced no text output)") {
		t.Errorf("error render missing empty-output note:\n%s", failed)
	}
}

func TestRenderCompletionTruncatesHugeSummary(t *testing.T) {
	huge := strings.Repeat("x", maxCompletionSummaryBytes+5000)
	rendered := renderCompletion(backgroundCompletion{
		taskID:  "t",
		name:    "n",
		status:  background.StatusCompleted,
		summary: huge,
	})
	if len(rendered) > maxCompletionSummaryBytes+1000 {
		t.Fatalf("rendered summary not truncated: %d bytes", len(rendered))
	}
	if !strings.Contains(rendered, "(truncated)") {
		t.Error("truncation marker missing")
	}
}

func TestRuntimeDrainCompletedTasksNilSafe(t *testing.T) {
	var runtime *Runtime
	if blocks := runtime.DrainCompletedTasks(); blocks != nil {
		t.Fatalf("nil runtime returned %v", blocks)
	}
}

func TestRuntimeRecordBackgroundExitAndDrain(t *testing.T) {
	runtime := NewRuntime(RuntimeOptions{})
	runtime.NotifyCompletions(true)
	runtime.recordBackgroundExit(
		"specialist_42", "explorer", "explore auth",
		background.StatusCompleted, 0,
		nil,
	)
	blocks := runtime.DrainCompletedTasks()
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if !strings.Contains(blocks[0], "specialist_42") || !strings.Contains(blocks[0], "completed") {
		t.Errorf("block missing task identity/state:\n%s", blocks[0])
	}
	if runtime.DrainCompletedTasks() != nil {
		t.Error("second drain should be empty")
	}
}
