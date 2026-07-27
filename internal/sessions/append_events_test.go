package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreAppendEventsBatchesSequencesAndMetadata(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: sequenceClock([]time.Time{
		time.Date(2026, 6, 4, 15, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 4, 15, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 4, 15, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 4, 15, 0, 3, 0, time.UTC),
	})})
	session, err := store.Create(CreateInput{SessionID: "batch"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	appended, err := store.AppendEvents(session.SessionID, []AppendEventInput{
		{Type: EventMessage, Payload: map[string]any{"role": "user", "content": "one"}},
		{Type: EventToolCall, Payload: map[string]any{"id": "call_1", "name": "read_file"}},
		{Type: EventToolResult, Payload: map[string]any{"toolCallId": "call_1", "status": "ok"}},
	})
	if err != nil {
		t.Fatalf("AppendEvents returned error: %v", err)
	}
	if len(appended) != 3 {
		t.Fatalf("expected 3 appended events, got %#v", appended)
	}
	for index, event := range appended {
		wantSequence := index + 1
		if event.Sequence != wantSequence || event.ID != fmt.Sprintf("batch:%d", wantSequence) {
			t.Fatalf("event identity mismatch at %d: %#v", index, event)
		}
	}
	if appended[0].CreatedAt != "2026-06-04T15:00:01Z" || appended[2].CreatedAt != "2026-06-04T15:00:03Z" {
		t.Fatalf("unexpected event timestamps: %#v", appended)
	}

	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != 3 || loaded.LastEventType != EventToolResult || loaded.UpdatedAt != appended[2].CreatedAt {
		t.Fatalf("metadata not updated from final batch event: %#v", loaded)
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if !reflect.DeepEqual(eventTypesForTest(events), []EventType{EventMessage, EventToolCall, EventToolResult}) {
		t.Fatalf("unexpected event types: %#v", events)
	}
}

func TestStoreHasMatchingEventStopsAtFirstMatch(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "scan"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	for i := 0; i < 20; i++ {
		eventType := EventMessage
		if i == 2 {
			eventType = EventToolCall
		}
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{
			Type:    eventType,
			Payload: map[string]any{"index": i},
		}); err != nil {
			t.Fatalf("AppendEvent %d returned error: %v", i, err)
		}
	}

	visited := 0
	ok, err := store.HasMatchingEvent(session.SessionID, func(event Event) bool {
		visited++
		return event.Type == EventToolCall
	})

	if err != nil {
		t.Fatalf("HasMatchingEvent returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected to find matching tool-call event")
	}
	if visited != 3 {
		t.Fatalf("visited %d events, want stop after first match at third event", visited)
	}
}

func TestStoreHasMatchingEventToleratesTornTail(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "torn"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{
		Type:    EventMessage,
		Payload: map[string]any{"content": "complete"},
	}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	file, err := os.OpenFile(store.eventsPath(session.SessionID), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile returned error: %v", err)
	}
	if _, err := file.WriteString(`{"type":"message"`); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	ok, err := store.HasMatchingEvent(session.SessionID, func(event Event) bool {
		return event.Type == EventToolResult
	})

	if err != nil {
		t.Fatalf("HasMatchingEvent should tolerate torn tail, got %v", err)
	}
	if ok {
		t.Fatal("unexpected match in session with only a complete message and torn tail")
	}
}

func TestStoreAppendEventsEmptyBatchDoesNotRewriteMetadata(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T15:10:00Z")})
	session, err := store.Create(CreateInput{SessionID: "empty_batch", Title: "unchanged"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	appended, err := store.AppendEvents(session.SessionID, nil)
	if err != nil {
		t.Fatalf("AppendEvents empty returned error: %v", err)
	}
	if len(appended) != 0 {
		t.Fatalf("empty AppendEvents returned %#v", appended)
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || !reflect.DeepEqual(*loaded, session) {
		t.Fatalf("empty AppendEvents rewrote metadata: before=%#v after=%#v", session, loaded)
	}
}

func TestStoreAppendEventsInvalidPayloadDoesNotPartiallyAppend(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T15:20:00Z")})
	session, err := store.Create(CreateInput{SessionID: "invalid_batch"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	_, err = store.AppendEvents(session.SessionID, []AppendEventInput{
		{Type: EventMessage, Payload: map[string]any{"content": "valid first"}},
		{Type: EventMessage, Payload: json.RawMessage(`{"broken"`)},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid raw JSON payload") {
		t.Fatalf("expected invalid raw payload error, got %v", err)
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("invalid batch should not append partial events: %#v", events)
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != 0 || loaded.LastEventType != "" {
		t.Fatalf("invalid batch should not update metadata: %#v", loaded)
	}
}

func TestStoreAppendEventsSequencesAfterMetadataLag(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T15:30:00Z")})
	session, err := store.Create(CreateInput{SessionID: "lagging"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]any{"content": "already durable"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	lagging, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	lagging.EventCount = 0
	lagging.LastEventType = ""
	if err := store.writeMetadata(*lagging); err != nil {
		t.Fatalf("writeMetadata returned error: %v", err)
	}

	appended, err := store.AppendEvents(session.SessionID, []AppendEventInput{
		{Type: EventToolCall, Payload: map[string]any{"id": "call"}},
		{Type: EventToolResult, Payload: map[string]any{"toolCallId": "call"}},
	})
	if err != nil {
		t.Fatalf("AppendEvents returned error: %v", err)
	}
	if len(appended) != 2 || appended[0].Sequence != 2 || appended[1].Sequence != 3 {
		t.Fatalf("batch did not sequence after durable log tail: %#v", appended)
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != 3 || loaded.LastEventType != EventToolResult {
		t.Fatalf("metadata not repaired by batch append: %#v", loaded)
	}
}

func TestStoreAppendEventsSerializesConcurrentBatches(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: fixedClock("2026-06-04T15:40:00Z")})
	session, err := store.Create(CreateInput{SessionID: "concurrent_batches"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const writers = 8
	const perBatch = 3
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			appended, err := store.AppendEvents(session.SessionID, []AppendEventInput{
				{Type: EventMessage, Payload: map[string]int{"writer": writer, "index": 0}},
				{Type: EventMessage, Payload: map[string]int{"writer": writer, "index": 1}},
				{Type: EventMessage, Payload: map[string]int{"writer": writer, "index": 2}},
			})
			if err != nil {
				errs <- err
				return
			}
			if len(appended) != perBatch {
				errs <- fmt.Errorf("writer %d appended %d events", writer, len(appended))
				return
			}
			for index := 1; index < len(appended); index++ {
				if appended[index].Sequence != appended[index-1].Sequence+1 {
					errs <- fmt.Errorf("writer %d batch was interleaved: %#v", writer, appended)
					return
				}
			}
			errs <- nil
		}(writer)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendEvents returned error: %v", err)
		}
	}

	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	total := writers * perBatch
	if len(events) != total {
		t.Fatalf("expected %d events, got %d", total, len(events))
	}
	seen := map[int]bool{}
	for _, event := range events {
		if seen[event.Sequence] {
			t.Fatalf("duplicate sequence %d in %#v", event.Sequence, events)
		}
		seen[event.Sequence] = true
	}
	for sequence := 1; sequence <= total; sequence++ {
		if !seen[sequence] {
			t.Fatalf("missing sequence %d in %#v", sequence, events)
		}
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loaded == nil || loaded.EventCount != total || loaded.LastEventType != EventMessage {
		t.Fatalf("metadata not updated after concurrent batches: %#v", loaded)
	}
}

func eventTypesForTest(events []Event) []EventType {
	types := make([]EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
