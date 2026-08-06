package sessions

import (
	"reflect"
	"testing"
)

// TestCompactableEventsFromMessagesElidesExactMessagePrefix walks real events
// through the shared event-to-message fold and asserts the compactable boundary
// is exactly the events whose messages were elided — including tool call/result
// pairs and events that produce no model message at all.
func TestCompactableEventsFromMessagesElidesExactMessagePrefix(t *testing.T) {
	events := []Event{
		{ID: "e1", Sequence: 1, Type: EventComposerInput, Payload: jsonRawString(`{"text":"composer"}`)},
		{ID: "e2", Sequence: 2, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request one"}`)},
		{ID: "e3", Sequence: 3, Type: EventToolCall, Payload: jsonRawString(`{"id":"call_1","name":"read_file","arguments":"{}"}`)},
		{ID: "e4", Sequence: 4, Type: EventToolResult, Payload: jsonRawString(`{"toolCallId":"call_1","output":"file contents"}`)},
		{ID: "e5", Sequence: 5, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"answer one"}`)},
		{ID: "e6", Sequence: 6, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request two"}`)},
		{ID: "e7", Sequence: 7, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"answer two"}`)},
	}
	// The fold yields request one, tool call, tool result, answer one (4 model
	// messages across e2..e5). Eliding the first 4 messages maps to e2..e5;
	// e1 (composer_input) and e6..e7 (preserved tail) are untouched.
	got := CompactableEventsFromMessages(4, events)

	want := []string{"e2", "e3", "e4", "e5"}
	if !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}
}

// TestCompactableEventsFromMessagesSkipsEventWithoutModelMessage proves events
// that produce no model message (empty content, usage, error) never consume an
// elided slot, so the boundary stays an exact message boundary.
func TestCompactableEventsFromMessagesSkipsEventWithoutModelMessage(t *testing.T) {
	events := []Event{
		{ID: "e1", Sequence: 1, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"  "}`)},
		{ID: "e2", Sequence: 2, Type: EventUsage, Payload: jsonRawString(`{"promptTokens":10}`)},
		{ID: "e3", Sequence: 3, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request one"}`)},
		{ID: "e4", Sequence: 4, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"answer one"}`)},
	}
	// An elided count of 1 must land on e3 (the first event with content), not
	// the empty e1 or the usage event e2.
	got := CompactableEventsFromMessages(1, events)

	want := []string{"e3"}
	if !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}
}

// TestCompactableEventsFromMessagesResetsAtPriorCompaction proves the mapping
// operates on the REHYDRATED launch view (as launchPrompt/OnCompaction do): a
// prior compaction is collapsed to a single summary slot, so previously
// compacted events are gone from the fold and the elided count lands on the
// post-compaction tail only.
func TestCompactableEventsFromMessagesResetsAtPriorCompaction(t *testing.T) {
	raw := []Event{
		{ID: "e1", Sequence: 1, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"dropped long ago"}`)},
		{ID: "e2", Sequence: 2, Type: EventCompaction, Payload: mustRawJSON(t, CompactionPayload{
			Summary: "earlier summary",
			CompactableEvents: []EventRef{
				{ID: "e1", Sequence: 1, Type: EventMessage},
			},
		})},
		{ID: "e3", Sequence: 3, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"live request"}`)},
		{ID: "e4", Sequence: 4, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"live answer"}`)},
	}
	rehydrated, err := RehydrateEvents(raw)
	if err != nil {
		t.Fatalf("RehydrateEvents: %v", err)
	}
	// Rehydrated view: [compaction summary, e3, e4] — e1 is long gone. Eliding
	// one message maps to the summary slot (e2); a later compaction of the live
	// tail therefore folds e2's summary into the new snapshot, and e1 can never
	// be re-marked because it is no longer in the launch view.
	got := CompactableEventsFromMessages(1, rehydrated)

	want := []string{"e2"}
	if !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}
	// The preserved live tail stays intact for elided count 2 (live request
	// would become compactable, but the summary keeps its slot).
	got = CompactableEventsFromMessages(2, rehydrated)
	if want = []string{"e2", "e3"}; !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}
}

// TestCompactableEventsFromMessagesStopsAtElidedCount proves the mapping never
// walks into the preserved tail: once the fold consumes the elided count every
// later event is left alone even if it contributes model messages.
func TestCompactableEventsFromMessagesStopsAtElidedCount(t *testing.T) {
	events := []Event{
		{ID: "e1", Sequence: 1, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request one"}`)},
		{ID: "e2", Sequence: 2, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"answer one"}`)},
		{ID: "e3", Sequence: 3, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request two"}`)},
	}
	got := CompactableEventsFromMessages(1, events)

	want := []string{"e1"}
	if !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}
}

// TestCompactableEventsFromMessagesStopsAtZeroElided covers the degenerate
// inputs: nothing to elide means nothing is marked compactable.
func TestCompactableEventsFromMessagesStopsAtZeroElided(t *testing.T) {
	events := []Event{
		{ID: "e1", Sequence: 1, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request"}`)},
	}
	if got := CompactableEventsFromMessages(0, events); len(got) != 0 {
		t.Fatalf("expected no compactable events for zero elided, got %v", eventIDs(got))
	}
	if got := CompactableEventsFromMessages(-1, events); len(got) != 0 {
		t.Fatalf("expected no compactable events for negative elided, got %v", eventIDs(got))
	}
}

// TestCompactableEventsFromMessagesInterleavesToolTraffic proves the fold
// drives the boundary exactly across tool call/result pairs.
func TestCompactableEventsFromMessagesInterleavesToolTraffic(t *testing.T) {
	events := []Event{
		{ID: "e1", Sequence: 1, Type: EventMessage, Payload: jsonRawString(`{"role":"user","content":"request"}`)},
		{ID: "e2", Sequence: 2, Type: EventToolCall, Payload: jsonRawString(`{"id":"call_1","name":"read_file","arguments":"{}"}`)},
		{ID: "e3", Sequence: 3, Type: EventToolResult, Payload: jsonRawString(`{"toolCallId":"call_1","output":"out"}`)},
		{ID: "e4", Sequence: 4, Type: EventMessage, Payload: jsonRawString(`{"role":"assistant","content":"answer"}`)},
	}
	// request + tool call + tool result = 3 messages; e1..e3 are compactable.
	got := CompactableEventsFromMessages(3, events)

	want := []string{"e1", "e2", "e3"}
	if !reflect.DeepEqual(eventIDs(got), want) {
		t.Fatalf("expected compactable events %v, got %v", want, eventIDs(got))
	}

	// Verify the fold really produces those messages (guards the fixture).
	if messages := ModelMessagesFromEvents(events[:3]); len(messages) != 3 {
		t.Fatalf("fixture fold expected 3 messages, got %#v", messages)
	}
}

func jsonRawString(value string) []byte {
	return []byte(value)
}
