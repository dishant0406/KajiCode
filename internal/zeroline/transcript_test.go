package zeroline

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func chatWith(rows []Row) ChatData {
	return ChatData{Variant: 0, Dark: true, Width: 90, Height: 24, Header: Header{Model: "m"}, Rows: rows}
}

func TestTranscriptBlocks(t *testing.T) {
	// user: accent ❯ + ink, no "you" label
	out := stripANSI(RenderChat(chatWith([]Row{{Kind: "user", Text: "refactor the loop"}})))
	if !strings.Contains(out, "❯ refactor the loop") {
		t.Errorf("user block missing ❯ + text")
	}
	if strings.Contains(out, "you ") {
		t.Errorf("user block should not show the 'you' label")
	}

	// assistant say: muted text, no "✦ zero" label
	out = stripANSI(RenderChat(chatWith([]Row{{Kind: "assistant", Text: "Here is the plan."}})))
	if !strings.Contains(out, "Here is the plan.") {
		t.Errorf("say text missing")
	}
	if strings.Contains(out, "✦ zero") {
		t.Errorf("say block should not show the zero label")
	}

	// final: accent rail + ink text
	out = stripANSI(RenderChat(chatWith([]Row{{Kind: "final", Text: "All done."}})))
	if !strings.Contains(out, "│") || !strings.Contains(out, "All done.") {
		t.Errorf("final block missing rail/text: %q", out)
	}

	// done: ■ + faint meta
	out = stripANSI(RenderChat(chatWith([]Row{{Kind: "done", Text: "12 tools · 1,284 tok · $0.04", Status: "ok"}})))
	if !strings.Contains(out, "■") || !strings.Contains(out, "12 tools") {
		t.Errorf("done block missing dot/meta: %q", out)
	}

	// notes: sys + deny
	if out = stripANSI(RenderChat(chatWith([]Row{{Kind: "system", Text: "compacted older turns"}}))); !strings.Contains(out, "compacted older turns") {
		t.Errorf("sys note missing")
	}
	if out = stripANSI(RenderChat(chatWith([]Row{{Kind: "error", Text: "denied: bash"}}))); !strings.Contains(out, "denied: bash") {
		t.Errorf("deny note missing")
	}
}

func TestStreamingCaretOnlyMidStream(t *testing.T) {
	// Streaming → accent caret ▌ trails the say text.
	d := chatWith(nil)
	d.Stream = "partial answer"
	d.Working = true
	stream := stripANSI(RenderChat(d))
	if !strings.Contains(stream, "partial answer") || !strings.Contains(stream, "▌") {
		t.Errorf("streaming should show text + caret: %q", stream)
	}
	// Not streaming → no caret.
	still := stripANSI(RenderChat(chatWith([]Row{{Kind: "assistant", Text: "settled answer"}})))
	if strings.Contains(still, "▌") {
		t.Errorf("non-streaming transcript must not show the caret")
	}
}

func TestMultiLineNotePreserved(t *testing.T) {
	// A multi-line system note (e.g. a resume-session summary) must keep all lines.
	out := stripANSI(RenderChat(chatWith([]Row{{Kind: "system", Text: "Resumed session\nid: abc123\nmodel: opus"}})))
	for _, want := range []string{"Resumed session", "id: abc123", "model: opus"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-line note dropped %q", want)
		}
	}
}

func TestStreamingPreservesNewlines(t *testing.T) {
	d := chatWith(nil)
	d.Stream = "first paragraph\n\nsecond paragraph"
	d.Working = true
	out := stripANSI(RenderChat(d))
	if !strings.Contains(out, "first paragraph") || !strings.Contains(out, "second paragraph") {
		t.Errorf("streaming flattened line structure: %q", out)
	}
}

func TestLongTokenReclipped(t *testing.T) {
	// wrap() does not hard-break a single long word; the block helpers must re-clip.
	s := newStyles(Resolve(0, true), 0, true)
	for _, line := range s.renderFinal(strings.Repeat("X", 300), 74, false) {
		if lipgloss.Width(line) > 76 { // rail "│ " (2) + 74
			t.Fatalf("renderFinal line overflows budget: %d cells", lipgloss.Width(line))
		}
	}
	for _, line := range s.renderSay(strings.Repeat("Y", 300), 74, false) {
		if lipgloss.Width(line) > 74 {
			t.Fatalf("renderSay line overflows budget: %d cells", lipgloss.Width(line))
		}
	}
}

func TestComposerRunStopAffordance(t *testing.T) {
	idle := stripANSI(RenderChat(chatWith([]Row{{Kind: "user", Text: "hi"}})))
	if !strings.Contains(idle, "run ↵") {
		t.Errorf("idle composer should show 'run ↵', got: %q", idle)
	}
	d := chatWith([]Row{{Kind: "user", Text: "hi"}})
	d.Working = true
	working := stripANSI(RenderChat(d))
	if !strings.Contains(working, "■ stop") || strings.Contains(working, "run ↵") {
		t.Errorf("working composer should show '■ stop' not 'run ↵', got: %q", working)
	}
}

func TestComposerButtonNotClippedByLongInput(t *testing.T) {
	d := chatWith([]Row{{Kind: "user", Text: "hi"}})
	d.Input = strings.Repeat("x", 200)
	d.Width, d.Height = 50, 12
	out := stripANSI(RenderChat(d))
	if !strings.Contains(out, "run ↵") {
		t.Errorf("run button must survive a long input (was clipped): %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 50 {
			t.Fatalf("composer line overflows width 50: %d", lipgloss.Width(line))
		}
	}
}

func TestTranscriptFrameExact(t *testing.T) {
	d := chatWith([]Row{
		{Kind: "user", Text: "do the thing"},
		{Kind: "assistant", Text: "Working on it."},
		{Kind: "final", Text: "Finished."},
		{Kind: "done", Text: "2 tools · 100 tok", Status: "ok"},
	})
	d.Width, d.Height = 100, 22
	out := RenderChat(d)
	if h := lipgloss.Height(out); h != 22 {
		t.Fatalf("chat height = %d, want 22 (frame-exact)", h)
	}
	for _, line := range strings.Split(out, "\n") {
		if lipgloss.Width(line) > 100 {
			t.Fatalf("chat line exceeds width 100: %d", lipgloss.Width(line))
		}
	}
}
