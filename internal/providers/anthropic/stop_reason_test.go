package anthropic

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func TestMapStopReasonRefusal(t *testing.T) {
	if got := mapStopReason("refusal"); got != kajicoderuntime.FinishReasonContentFilter {
		t.Errorf("refusal → %q, want content_filter (M4)", got)
	}
	if got := mapStopReason("max_tokens"); got != kajicoderuntime.FinishReasonLength {
		t.Errorf("max_tokens → %q, want length", got)
	}
	for _, normal := range []string{"end_turn", "tool_use", "stop_sequence", "pause_turn", ""} {
		if got := mapStopReason(normal); got != "" {
			t.Errorf("%q should be a normal stop (empty), got %q", normal, got)
		}
	}
}
