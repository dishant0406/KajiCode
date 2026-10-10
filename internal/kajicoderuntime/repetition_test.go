package kajicoderuntime

import (
	"context"
	"strings"
	"testing"
)

func feedAll(detector *repetitionDetector, text string) bool {
	for _, chunk := range strings.SplitAfter(text, " ") {
		if detector.feed(chunk) {
			return true
		}
	}
	return false
}

func TestRepetitionDetectorFlagsStuckPhrases(t *testing.T) {
	cases := map[string]string{
		"reported gibberish":     strings.Repeat("Go.\nWriting.\nOK.\n", 8),
		"narration on a line":    strings.Repeat("Let me write. Let me run. Let me go. OK. ", 6),
		"one line over and over": strings.Repeat("I will read the file now\n", 7),
		"case and punctuation":   strings.Repeat("Go!\ngo\nGO.\n", 4),
	}
	for name, text := range cases {
		var detector repetitionDetector
		if !feedAll(&detector, text) {
			t.Errorf("%s: expected the stream to be flagged", name)
		}
	}
}

func TestRepetitionDetectorAllowsNormalOutput(t *testing.T) {
	cases := map[string]string{
		"prose": "I looked at the loader first. It reads the config once. " +
			"Then it builds the registry. The registry is passed to the agent. " +
			"The agent starts a run. Each run has its own state. State is never shared. " +
			"Tools are registered at startup. They are looked up by name. " +
			"Unknown names return an error. Errors are shown to the user. " +
			"The user can retry. Retries use the same request. Nothing is cached. Done.",
		"list":      "- a\n- b\n- c\n- d\n- e\n- f\n- g\n- h\n- i\n- j\n- k\n- l\n- m\n- n\n- o\n- p\n- q\n",
		"rule":      strings.Repeat("---\n", 30),
		"code":      "x := 1\ny := 2\nz := 3\nfmt.Println(x, y, z)\nreturn nil\n}\n\nfunc a() {\n}\nfunc b() {\n}\nfunc c() {\n}\nfunc d() {\n}\n",
		"two lines": strings.Repeat("ok\n", 5),
	}
	for name, text := range cases {
		var detector repetitionDetector
		if feedAll(&detector, text) {
			t.Errorf("%s: normal output was flagged", name)
		}
	}
}

func eventsOf(list ...StreamEvent) <-chan StreamEvent {
	events := make(chan StreamEvent, len(list))
	for _, event := range list {
		events <- event
	}
	close(events)
	return events
}

func TestCollectStreamCutsOffRepeatingReasoning(t *testing.T) {
	var list []StreamEvent
	for i := 0; i < 40; i++ {
		list = append(list, StreamEvent{Type: StreamEventReasoning, Content: "Go.\nWriting.\nOK.\n"})
	}
	list = append(list, StreamEvent{Type: StreamEventText, Content: "should never be read"}, StreamEvent{Type: StreamEventDone})

	cancelled := false
	collected := CollectStreamWithOptions(context.Background(), eventsOf(list...), CollectOptions{Cancel: func() { cancelled = true }})

	if !collected.Degenerate {
		t.Fatal("expected the stream to be cut off")
	}
	if !cancelled {
		t.Error("expected the provider request to be cancelled")
	}
	if collected.Text != "" {
		t.Errorf("text after the cut-off must not be read, got %q", collected.Text)
	}
	if !strings.Contains(collected.DegenerateSample, "Writing.") {
		t.Errorf("expected a sample of the repeated text, got %q", collected.DegenerateSample)
	}
}

func TestCollectStreamKeepsNormalStream(t *testing.T) {
	collected := CollectStream(context.Background(), eventsOf(
		StreamEvent{Type: StreamEventReasoning, Content: "Thinking about the change."},
		StreamEvent{Type: StreamEventText, Content: "Done."},
		StreamEvent{Type: StreamEventDone},
	))
	if collected.Degenerate || collected.Text != "Done." {
		t.Fatalf("unexpected result: %+v", collected)
	}
}
