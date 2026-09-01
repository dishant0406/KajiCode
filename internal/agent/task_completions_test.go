package agent

import (
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

type fakeCompletionSource struct {
	blocks [][]string
}

func (source *fakeCompletionSource) DrainCompletedTasks() []string {
	if len(source.blocks) == 0 {
		return nil
	}
	next := source.blocks[0]
	source.blocks = source.blocks[1:]
	return next
}

func TestDrainTaskCompletionsNilSafe(t *testing.T) {
	if got := drainTaskCompletions(nil); got != nil {
		t.Fatalf("nil source returned %v", got)
	}
	if got := drainTaskCompletions(&fakeCompletionSource{}); got != nil {
		t.Fatalf("empty source returned %v", got)
	}
}

func TestTaskCompletionMessageInjection(t *testing.T) {
	source := &fakeCompletionSource{blocks: [][]string{{"<task_result id=\"x\" state=\"completed\">done</task_result>"}}}
	messages := []kajicoderuntime.Message{}
	for _, block := range drainTaskCompletions(source) {
		messages = append(messages, kajicoderuntime.Message{
			Role:    kajicoderuntime.MessageRoleUser,
			Content: block,
		})
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].Role != kajicoderuntime.MessageRoleUser {
		t.Errorf("role = %q, want user", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "<task_result") {
		t.Errorf("content missing task_result envelope: %q", messages[0].Content)
	}
}
