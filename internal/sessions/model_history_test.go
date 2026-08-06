package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func TestModelMessagesFromEventsKeepsFullAssistantTail(t *testing.T) {
	longBody := strings.Repeat("context ", 90)
	tail := "Want me to apply the threshold bumps to the config file?"
	events := []Event{
		modelHistoryEvent(1, EventMessage, map[string]any{"role": "user", "content": "research the SLO"}),
		modelHistoryEvent(2, EventMessage, map[string]any{"role": "assistant", "content": longBody + tail}),
	}

	messages := ModelMessagesFromEvents(events)

	if len(messages) != 2 {
		t.Fatalf("expected 2 model messages, got %d", len(messages))
	}
	if messages[1].Role != kajicoderuntime.MessageRoleAssistant {
		t.Fatalf("expected assistant role, got %q", messages[1].Role)
	}
	if !strings.Contains(messages[1].Content, tail) {
		t.Fatalf("assistant tail was lost:\n%s", messages[1].Content)
	}
}

func TestModelMessagesFromEventsResetsAtCompactionSnapshot(t *testing.T) {
	snapshot := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "[Summary of earlier conversation]\nold context summary"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "recent answer"},
	}
	events := []Event{
		modelHistoryEvent(1, EventMessage, map[string]any{"role": "user", "content": "dropped old request"}),
		modelHistoryEvent(2, EventCompaction, map[string]any{
			"summary":       "old context summary",
			"modelMessages": snapshot,
		}),
		modelHistoryEvent(3, EventMessage, map[string]any{"role": "user", "content": "continue"}),
	}

	messages := ModelMessagesFromEvents(events)

	if len(messages) != 3 {
		t.Fatalf("expected compacted snapshot plus later event, got %#v", messages)
	}
	if strings.Contains(messages[0].Content, "dropped old request") {
		t.Fatalf("old pre-compaction event leaked into model history: %#v", messages)
	}
	if messages[2].Content != "continue" {
		t.Fatalf("expected later event to remain after compaction, got %#v", messages[2])
	}
}

func TestModelMessagesFromEventsAvoidsSnapshotDuplicatesAfterRehydrate(t *testing.T) {
	events := []Event{
		modelHistoryEvent(1, EventCompaction, map[string]any{
			"summary": "old context summary",
			"modelMessages": []kajicoderuntime.Message{
				{Role: kajicoderuntime.MessageRoleUser, Content: "[Summary of earlier conversation]\nold context summary"},
				{Role: kajicoderuntime.MessageRoleUser, Content: "current request"},
			},
		}),
		modelHistoryEvent(2, EventMessage, map[string]any{"role": "user", "content": "current request"}),
	}

	messages := ModelMessagesFromEvents(events)

	if len(messages) != 2 {
		t.Fatalf("expected summary plus retained current event, got %#v", messages)
	}
	if messages[1].Content != "current request" {
		t.Fatalf("expected retained current request after summary, got %#v", messages)
	}
}

func TestModelCompactionPayloadRecordsCompactedBoundary(t *testing.T) {
	events := []Event{
		{ID: "event_1", Sequence: 1, Type: EventMessage},
		{ID: "event_2", Sequence: 2, Type: EventToolResult},
	}
	payload := ModelCompactionPayload("summary", []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "summary"},
		{Role: kajicoderuntime.MessageRoleSystem, Content: "stale system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: ""},
	}, events, "proactive")

	if payload.CompactedThroughEventID != "event_2" || payload.CompactedThroughSequence != 2 {
		t.Fatalf("expected compaction boundary at event_2/2, got %#v", payload)
	}
	if len(payload.CompactableEvents) != 2 {
		t.Fatalf("expected compactable event refs, got %#v", payload.CompactableEvents)
	}
	if payload.Trigger != "proactive" {
		t.Fatalf("expected proactive trigger, got %q", payload.Trigger)
	}
	if payload.PreservedCount != 1 {
		t.Fatalf("preserved count must reflect cleaned model messages only, got %d", payload.PreservedCount)
	}
	if len(payload.ModelMessages) != 1 || payload.ModelMessages[0].Content != "summary" {
		t.Fatalf("expected compacted model messages, got %#v", payload.ModelMessages)
	}
}

func modelHistoryEvent(sequence int, eventType EventType, payload any) Event {
	data, _ := json.Marshal(payload)
	return Event{
		ID:       fmt.Sprintf("event_%d", sequence),
		Sequence: sequence,
		Type:     eventType,
		Payload:  data,
	}
}

func TestCompactedModelMessagesBuildsSummaryPlusPreservedTail(t *testing.T) {
	preserved := []Event{
		modelHistoryEvent(2, EventMessage, map[string]any{"role": "user", "content": "current request"}),
		modelHistoryEvent(3, EventMessage, map[string]any{"role": "assistant", "content": "current answer"}),
	}
	snapshot := CompactedModelMessages("  summary text  ", preserved)

	if len(snapshot) != 3 {
		t.Fatalf("expected summary + 2 preserved messages, got %#v", snapshot)
	}
	if !strings.Contains(snapshot[0].Content, "[Summary of earlier conversation]") ||
		!strings.Contains(snapshot[0].Content, "summary text") {
		t.Fatalf("expected summary header + text first, got %#v", snapshot[0])
	}
	if snapshot[1].Content != "current request" || snapshot[2].Content != "current answer" {
		t.Fatalf("expected preserved tail after summary, got %#v", snapshot)
	}
}

func TestCompactedModelMessagesHandlesEmptyTail(t *testing.T) {
	snapshot := CompactedModelMessages("only a summary", nil)
	if len(snapshot) != 1 || !strings.Contains(snapshot[0].Content, "only a summary") {
		t.Fatalf("expected summary-only snapshot, got %#v", snapshot)
	}
}
