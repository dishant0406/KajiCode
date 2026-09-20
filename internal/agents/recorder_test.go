package agents

import (
	"encoding/json"
	"testing"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// newTestRecorder returns a childRecorder backed by a real store so persisted
// events can be read back and asserted.
func newTestRecorder(t *testing.T, sessionID string) (*childRecorder, *sessions.Store) {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	if _, err := store.Create(sessions.CreateInput{SessionID: sessionID, Title: sessionID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &childRecorder{sessionID: sessionID, store: store}, store
}

func assistantMessages(t *testing.T, store *sessions.Store, sessionID string) []string {
	t.Helper()
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	out := []string{}
	for _, event := range events {
		if event.Type != sessions.EventMessage {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if role, _ := payload["role"].(string); role != "assistant" {
			continue
		}
		content, _ := payload["content"].(string)
		out = append(out, content)
	}
	return out
}

// TestChildRecorderCoalescesStreamedDeltas is the regression guard: the provider
// calls onText once per token/fragment, but the child session must persist ONE
// assistant message per contiguous prose segment, not one per delta.
func TestChildRecorderCoalescesStreamedDeltas(t *testing.T) {
	recorder, store := newTestRecorder(t, "child_coalesce")

	for _, delta := range []string{"The ", "parser ", "lives ", "in ", "parse.go"} {
		recorder.onText(delta)
	}
	recorder.flushText()

	got := assistantMessages(t, store, "child_coalesce")
	if len(got) != 1 {
		t.Fatalf("assistant messages = %d (%q), want 1 coalesced message", len(got), got)
	}
	if got[0] != "The parser lives in parse.go" {
		t.Fatalf("coalesced content = %q", got[0])
	}
}

// TestChildRecorderFlushesProseBeforeToolSegments proves each prose segment
// around a tool call persists separately, in order.
func TestChildRecorderFlushesProseBeforeToolSegments(t *testing.T) {
	recorder, store := newTestRecorder(t, "child_segments")

	recorder.onText("First I will read the file.")
	recorder.onToolCall(agent.ToolCall{ID: "c1", Name: "read_file"})
	recorder.onText("Now ")
	recorder.onText("I will summarize.")
	recorder.flushText()

	got := assistantMessages(t, store, "child_segments")
	want := []string{"First I will read the file.", "Now I will summarize."}
	if len(got) != len(want) {
		t.Fatalf("assistant messages = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChildRecorderFinalFallbackRecordsNonStreamedAnswer covers a provider that
// never emits text deltas: the run's final answer must still be persisted.
func TestChildRecorderFinalFallbackRecordsNonStreamedAnswer(t *testing.T) {
	recorder, store := newTestRecorder(t, "child_nonstreamed")
	recorder.flushFinalText("Answered without streaming.")

	got := assistantMessages(t, store, "child_nonstreamed")
	if len(got) != 1 || got[0] != "Answered without streaming." {
		t.Fatalf("assistant messages = %q, want the final-answer fallback", got)
	}
}

// TestChildRecorderFinalFallbackDoesNotDuplicateStreamedText proves the fallback
// is skipped when deltas were already buffered, so a provider that streams AND
// reports a final answer does not double-write.
func TestChildRecorderFinalFallbackDoesNotDuplicateStreamedText(t *testing.T) {
	recorder, store := newTestRecorder(t, "child_streamed")
	recorder.onText("Streamed answer.")
	recorder.flushFinalText("Streamed answer.")

	got := assistantMessages(t, store, "child_streamed")
	if len(got) != 1 || got[0] != "Streamed answer." {
		t.Fatalf("assistant messages = %q, want exactly the streamed text once", got)
	}
}
