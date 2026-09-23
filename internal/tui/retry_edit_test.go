package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

func TestCommandsRegistered(t *testing.T) {
	for _, name := range []string{"/retry", "/thread", "/export"} {
		if cmd, ok := resolveCommand(name); !ok {
			t.Fatalf("%s should be a registered command", name)
		} else if cmd.name != name {
			t.Fatalf("resolveCommand(%q) = %q", name, cmd.name)
		}
	}
}

// /retry resends the last prompt.
func TestRetryResendsLastPrompt(t *testing.T) {
	m := newModel(context.Background(), Options{Provider: &fakeProvider{}, ProviderName: "openai", ModelName: "gpt-4.1"})
	m.lastPrompt = "do the thing"
	m.input.SetValue("/retry")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !next.pending {
		t.Fatal("/retry should launch a run")
	}
	if cmd == nil {
		t.Fatal("/retry should return a run command")
	}
}

// /retry with no prior prompt reports that there's nothing to resend rather than
// launching an empty run.
func TestRetryWithoutPriorPromptIsNoOp(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/retry")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if !transcriptContains(next.transcript, "no previous prompt") {
		t.Fatalf("/retry with no history should note there's nothing to resend, got %#v", next.transcript)
	}
}

// /retry must not launch a run while compaction is rewriting session state — the
// same guard a normal prompt has.
func TestRetryBlockedDuringCompaction(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.lastPrompt = "do the thing"
	m.compactInFlight = true
	m.input.SetValue("/retry")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.pending {
		t.Fatal("/retry must not start a run during compaction")
	}
	if cmd != nil {
		t.Fatal("/retry during compaction must not return a run command")
	}
	if !transcriptContains(next.transcript, "Compaction is running") {
		t.Fatalf("/retry during compaction should warn, got %#v", next.transcript)
	}
}

// /thread opens a message picker listing the conversation's user and assistant
// messages.
func TestThreadOpensMessagePicker(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = []transcriptRow{
		{kind: rowUser, text: "first question"},
		{kind: rowAssistant, text: "first answer"},
	}
	m.input.SetValue("/thread")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.picker == nil || next.picker.kind != pickerThread {
		t.Fatalf("/thread should open the thread picker, got %#v", next.picker)
	}
	if len(next.picker.items) != 2 {
		t.Fatalf("thread picker should list both messages, got %d", len(next.picker.items))
	}
}

// Entering an assistant message opens its action list with Copy only.
func TestThreadAssistantActionsOfferCopy(t *testing.T) {
	picker := newThreadActionPicker(rowAssistant, 1)
	values := []string{}
	for _, item := range picker.items {
		values = append(values, item.Value)
	}
	if len(values) != 1 || values[0] != "copy" {
		t.Fatalf("assistant actions = %v, want [copy]", values)
	}
}

// Entering a user message offers Copy, Edit, and Revert.
func TestThreadUserActionsOfferCopyEditRevert(t *testing.T) {
	picker := newThreadActionPicker(rowUser, 0)
	values := []string{}
	for _, item := range picker.items {
		values = append(values, item.Value)
	}
	want := []string{"copy", "edit", "revert"}
	if len(values) != len(want) {
		t.Fatalf("user actions = %v, want %v", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("user actions = %v, want %v", values, want)
		}
	}
}

// Edit recalls the chosen message's text into the composer for editing.
func TestThreadEditRecallsMessageIntoComposer(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = []transcriptRow{
		{kind: rowUser, text: "refactor the parser"},
	}
	picker := newThreadActionPicker(rowUser, 0)
	picker.selected = 1 // "edit"
	m.picker = picker

	updated, _ := m.choosePicker()
	next := updated.(model)

	if got := next.composerValue(); got != "refactor the parser" {
		t.Fatalf("edit should recall the message into the composer, got %q", got)
	}
	if next.picker != nil {
		t.Fatal("the action picker should close after running edit")
	}
}

// The full key-driven flow: /thread lists messages, Enter opens the actions for
// the highlighted message, and Enter runs the highlighted action.
func TestThreadKeyboardFlowOpensActionsThenEdits(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = []transcriptRow{
		{kind: rowUser, text: "first question"},
		{kind: rowAssistant, text: "first answer"},
	}
	// Open /thread.
	m.input.SetValue("/thread")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerThread {
		t.Fatalf("expected the thread picker, got %#v", m.picker)
	}

	// Highlight the user message (first item) and open its actions.
	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerThreadAction {
		t.Fatalf("Enter on a message should open its actions, got %#v", m.picker)
	}
	if len(m.picker.items) != 3 {
		t.Fatalf("a user message should offer 3 actions, got %d", len(m.picker.items))
	}

	// Move to "Edit in composer" (index 1) and run it.
	updated, _ = m.Update(testKey(tea.KeyDown))
	m = updated.(model)
	updated, _ = m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	if got := m.composerValue(); got != "first question" {
		t.Fatalf("running Edit should recall the message, got %q", got)
	}
	if m.picker != nil {
		t.Fatal("the picker should close after the action runs")
	}
}

// Esc on the message list closes it without acting.
func TestThreadEscClosesPicker(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = []transcriptRow{{kind: rowUser, text: "q"}}
	m.input.SetValue("/thread")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	m = updated.(model)

	updated, _ = m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	if next.picker != nil {
		t.Fatal("Esc should close the thread picker")
	}
}

// Silent when there is nothing to browse.
func TestThreadWithoutMessagesNotesEmpty(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = nil
	m.input.SetValue("/thread")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if next.picker != nil {
		t.Fatal("no messages should not open a picker")
	}
	if !transcriptContains(next.transcript, "no messages yet") {
		t.Fatalf("expected an empty-thread note, got %#v", next.transcript)
	}
}

// The user-message row must carry the session sequence it was recorded at, both
// live and after a resume, so /thread's Revert targets the right point.
func TestThreadSeqStampedLiveAndOnResume(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "seq", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "user", "content": "hello"})
	appendTestEvent(t, store, session.SessionID, sessions.EventMessage, map[string]any{"role": "assistant", "content": "hi"})

	m := newModel(context.Background(), Options{SessionStore: store})
	m.input.SetValue("/resume " + session.SessionID)
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	m = applyResumeCommand(t, updated.(model), cmd)

	seq := 0
	for _, row := range m.transcript {
		if row.kind == rowUser {
			seq = row.seq
		}
	}
	if seq != 1 {
		t.Fatalf("resumed user row seq = %d, want 1", seq)
	}
}

// The LIVE path must stamp the row with THIS turn's sequence, not the previous
// one (or 0 on the first turn) — otherwise /thread's Revert rewinds too far.
func TestThreadSeqStampedOnLivePrompt(t *testing.T) {
	store := testSessionStore(t)
	m := newModel(context.Background(), Options{
		SessionStore: store,
		Provider:     &fakeProvider{},
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
	})

	next, _ := m.launchPrompt("first prompt")
	m = next

	seq := 0
	for _, row := range m.transcript {
		if row.kind == rowUser {
			seq = row.seq
		}
	}
	if seq <= 0 {
		t.Fatalf("live user row seq = %d, want the recorded sequence", seq)
	}
	// It must point at the row's OWN user event: reverting keeps everything before
	// it, so the target (seq-1) must be a valid keep-through sequence.
	if _, err := store.ReadEvents(m.activeSession.SessionID); err != nil {
		t.Fatalf("reading events: %v", err)
	}
	last := m.lastUserMessageSeq()
	if last != seq {
		t.Fatalf("stamped seq %d does not match the latest user event %d", seq, last)
	}
}
