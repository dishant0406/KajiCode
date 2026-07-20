package gemini

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// emitDone must mark the shared state done so callers observe it through the
// pointer (a by-value receiver would make state.done a dead store).
func TestEmitDoneMarksStateDoneThroughPointer(t *testing.T) {
	provider, err := New(Options{Model: "gemini-test"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	events := make(chan kajicoderuntime.StreamEvent, 4)
	state := &streamState{}
	provider.emitDone(context.Background(), state, events)
	close(events)

	if !state.done {
		t.Fatal("emitDone did not mark state.done = true through the pointer")
	}
	var sawDone bool
	for event := range events {
		if event.Type == kajicoderuntime.StreamEventDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("emitDone did not emit a done event")
	}
}
