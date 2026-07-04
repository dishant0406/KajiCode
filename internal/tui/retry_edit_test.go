package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCommandsRegistered(t *testing.T) {
	for _, name := range []string{"/retry", "/edit", "/copy", "/export"} {
		if cmd, ok := resolveCommand(name); !ok {
			t.Fatalf("%s should be a registered command", name)
		} else if cmd.name != name {
			t.Fatalf("resolveCommand(%q) = %q", name, cmd.name)
		}
	}
}

// /edit recalls the last prompt into the composer for editing.
func TestEditRecallsLastPrompt(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.lastPrompt = "refactor the parser"
	m.input.SetValue("/edit")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)

	if got := next.composerValue(); got != "refactor the parser" {
		t.Fatalf("/edit should recall last prompt into composer, got %q", got)
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
