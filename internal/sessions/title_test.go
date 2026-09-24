package sessions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

type fakeTitleProvider struct{ text string }

func (f fakeTitleProvider) StreamCompletion(_ context.Context, _ kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	go func() {
		defer close(ch)
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: f.text}
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	}()
	return ch, nil
}

func TestCleanTitleNormalizesModelOutput(t *testing.T) {
	cases := map[string]string{
		`"Fix ACP Fork"`:              "Fix ACP Fork",
		"Title: Add Session Export":   "Add Session Export",
		"\n```\nCompact History\n```": "Compact History",
		"":                            "",
		"one two three four five six seven eight nine": "one two three four five six seven eight",
	}
	for raw, want := range cases {
		if got := CleanTitle(raw); got != want {
			t.Errorf("CleanTitle(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTitleDigestBoundsAndSkips(t *testing.T) {
	events := []Event{
		{Type: EventMessage, Payload: payloadJSON(t, map[string]any{"role": "user", "content": "Build the resume picker"})},
		{Type: EventMessage, Payload: payloadJSON(t, map[string]any{"role": "assistant", "content": "no progress stop"})},
		{Type: EventToolCall, Payload: payloadJSON(t, map[string]any{"name": "read_file"})},
	}
	// The skip predicate excludes the assistant message; TitleDigest must honor it.
	got := TitleDigest(events, func(c string) bool { return c == "no progress stop" })
	if strings.Contains(got, "no progress stop") {
		t.Fatalf("skip predicate ignored: %q", got)
	}
	if !strings.Contains(got, "Build the resume picker") || !strings.Contains(got, "read_file") {
		t.Fatalf("digest missing expected lines: %q", got)
	}
}

func TestGenerateTitleAndRetitlePersist(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	meta, err := store.Create(CreateInput{Title: "Untitled", Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.AppendEvent(meta.SessionID, AppendEventInput{
		Type:    EventMessage,
		Payload: map[string]any{"role": "user", "content": "Investigate the ACP fork handler"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	updated, err := store.RetitleSession(context.Background(), meta.SessionID, fakeTitleProvider{text: "ACP Fork Handler"}, nil)
	if err != nil {
		t.Fatalf("retitle: %v", err)
	}
	if updated.Title != "ACP Fork Handler" {
		t.Fatalf("title = %q", updated.Title)
	}
	// No content -> ErrTitleNoContent, no provider call wasted.
	empty, _ := store.Create(CreateInput{Title: "Empty", Cwd: "/tmp"})
	if _, err := store.RetitleSession(context.Background(), empty.SessionID, fakeTitleProvider{text: "x"}, nil); err == nil {
		t.Fatal("expected ErrTitleNoContent for an empty session")
	}
}

func TestTranscriptFromEvents(t *testing.T) {
	events := []Event{
		{Type: EventMessage, Payload: payloadJSON(t, map[string]any{"role": "user", "content": "hello"})},
		{Type: EventToolCall, Payload: payloadJSON(t, map[string]any{"name": "read_file"})},
		{Type: EventMessage, Payload: payloadJSON(t, map[string]any{"role": "assistant", "content": "world"})},
	}
	got := TranscriptFromEvents(events, nil)
	if !strings.Contains(got, "you: hello") || !strings.Contains(got, "kajicode: world") {
		t.Fatalf("transcript = %q", got)
	}
	if strings.Contains(got, "read_file") {
		t.Fatalf("tool calls must not appear in the transcript: %q", got)
	}
}

func payloadJSON(t *testing.T, m map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCompactSessionWritesModelMessages(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	meta, err := store.Create(CreateInput{Title: "Big", Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 30; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := store.AppendEvent(meta.SessionID, AppendEventInput{
			Type:    EventMessage,
			Payload: map[string]any{"role": role, "content": "message " + string(rune('a'+i%26))},
		}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// nil provider -> deterministic summary, still records a compaction with the
	// model-history snapshot (so CLI/ACP match the TUI event shape).
	event, err := store.CompactSession(context.Background(), meta.SessionID, nil, CompactionOptions{PreserveLast: 4})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if event.Type != EventCompaction {
		t.Fatalf("event type = %q", event.Type)
	}
	payload := eventPayload(event)
	if payloadString(payload, "summary") == "" {
		t.Fatal("compaction has no summary")
	}
	var full CompactionPayload
	if err := json.Unmarshal(event.Payload, &full); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(full.ModelMessages) == 0 {
		t.Fatal("compaction payload lost ModelMessages (diverges from the TUI event shape)")
	}

	empty, _ := store.Create(CreateInput{Title: "Small", Cwd: "/tmp"})
	if _, err := store.CompactSession(context.Background(), empty.SessionID, nil, CompactionOptions{}); err != ErrNothingToCompact {
		t.Fatalf("expected ErrNothingToCompact, got %v", err)
	}
}
