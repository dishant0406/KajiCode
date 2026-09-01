package specialist

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dishant0406/KajiCode/internal/background"
	"github.com/dishant0406/KajiCode/internal/streamjson"
)

// backgroundCompletion is one finished background specialist waiting to be
// delivered to its parent run.
type backgroundCompletion struct {
	taskID      string
	name        string
	description string
	status      background.Status
	exitCode    int
	summary     string
}

// completionCollector is the bounded queue of finished-but-undelivered
// background specialist results. The background onExit callback pushes; the
// agent loop drains once per turn (same shape as async post-edit diagnostics).
// Delivery replaces polling: without it the model must burn turns calling
// TaskOutput to discover a finished task.
type completionCollector struct {
	mu      sync.Mutex
	enabled bool
	pending []backgroundCompletion
	// delivered deduplicates pushes so an exit callback racing a manual
	// TaskOutput read cannot deliver the same task twice.
	delivered map[string]bool
}

func newCompletionCollector() *completionCollector {
	return &completionCollector{delivered: map[string]bool{}}
}

func (collector *completionCollector) setEnabled(enabled bool) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.enabled = enabled
}

func (collector *completionCollector) push(completion backgroundCompletion) {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if !collector.enabled || collector.delivered[completion.taskID] {
		return
	}
	collector.delivered[completion.taskID] = true
	collector.pending = append(collector.pending, completion)
}

func (collector *completionCollector) drain() []backgroundCompletion {
	if collector == nil {
		return nil
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.pending) == 0 {
		return nil
	}
	out := collector.pending
	collector.pending = nil
	return out
}

// maxCompletionSummaryBytes bounds how much of a finished child's transcript
// summary rides back into the parent context. A full research report is useful;
// a runaway transcript would bloat every subsequent parent turn.
const maxCompletionSummaryBytes = 8 * 1024

// renderCompletion renders one finished task as a self-describing block the
// orchestrator model can act on: what ran, how it ended, and its final output.
func renderCompletion(completion backgroundCompletion) string {
	state := "error"
	switch completion.status {
	case background.StatusCompleted:
		state = "completed"
	case background.StatusKilled:
		state = "killed"
	}
	var b strings.Builder
	b.WriteString("Background sub-agent task finished (result below was produced by the sub-agent, not verified by you):\n")
	fmt.Fprintf(&b, "<task_result id=%q state=%q>\n", completion.taskID, state)
	fmt.Fprintf(&b, "<summary>specialist %s", completion.name)
	if trimmed := strings.TrimSpace(completion.description); trimmed != "" {
		fmt.Fprintf(&b, ": %s", trimmed)
	}
	fmt.Fprintf(&b, "</summary>\n")
	summary := strings.TrimSpace(completion.summary)
	if summary == "" {
		summary = "(the sub-agent produced no text output)"
	}
	if len(summary) > maxCompletionSummaryBytes {
		summary = summary[:maxCompletionSummaryBytes] + "\n…(truncated)"
	}
	b.WriteString(summary)
	b.WriteString("\n</task_result>")
	return b.String()
}

// DrainCompletedTasks implements the agent.TaskCompletionSource contract: it
// returns fully-rendered nudge blocks for background tasks that exited since
// the last drain, oldest first, and clears the queue. Always non-nil safe.
func (runtime *Runtime) DrainCompletedTasks() []string {
	if runtime == nil || runtime.completions == nil {
		return nil
	}
	completions := runtime.completions.drain()
	if len(completions) == 0 {
		return nil
	}
	blocks := make([]string, 0, len(completions))
	for _, completion := range completions {
		blocks = append(blocks, renderCompletion(completion))
	}
	return blocks
} // NotifyCompletions turns on delivery of finished background-task summaries to
// the parent run. Interactive surfaces call this at startup; headless one-shot
// runs leave it off (their run usually ends before any background task does,
// and TaskOutput remains available there).
func (runtime *Runtime) NotifyCompletions(enabled bool) {
	if runtime == nil {
		return
	}
	runtime.completions.setEnabled(enabled)
}

// recordBackgroundExit summarizes a finished background child and queues its
// result for delivery. Called from the launch onExit callback; failures here
// must never panic the waiter goroutine.
func (runtime *Runtime) recordBackgroundExit(taskID string, name string, description string, status background.Status, exitCode int, events []streamjson.Event) {
	if runtime == nil {
		return
	}
	summary := SummarizeStream(events, exitCode).Text
	runtime.completions.push(backgroundCompletion{
		taskID:      taskID,
		name:        name,
		description: description,
		status:      status,
		exitCode:    exitCode,
		summary:     summary,
	})
}
