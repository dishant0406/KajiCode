package tui

import (
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

func TestStreamRenderSnapshotStartsAtStableBoundary(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	text := strings.Repeat("alpha beta gamma ", 400) + "live tail"
	m.streamingText = []byte(text)
	m.streamingTextHasContent = true
	m.streamingTextTail = boundedUTF8TailString(text, streamPreviewTailBytes)

	snapshot, ok := m.streamRenderSnapshot(m.chatColumnWidth())
	if !ok {
		t.Fatal("expected streaming snapshot")
	}
	if snapshot.textStartBytes <= 0 {
		t.Fatalf("snapshot start = %d, want bounded window start", snapshot.textStartBytes)
	}
	before, _ := utf8LastRune(m.streamingText[:snapshot.textStartBytes])
	if !unicode.IsSpace(before) {
		t.Fatalf("snapshot started mid-token at byte %d before %q in %q", snapshot.textStartBytes, before, snapshot.text[:minInt(len(snapshot.text), 40)])
	}
	first := strings.Fields(snapshot.text)[0]
	if first != "alpha" && first != "beta" && first != "gamma" {
		t.Fatalf("snapshot starts with partial word %q in %q", first, snapshot.text[:minInt(len(snapshot.text), 80)])
	}
	if !strings.HasSuffix(snapshot.text, "live tail") {
		t.Fatalf("snapshot should include live tail, got suffix %q", snapshot.text[maxInt(0, len(snapshot.text)-40):])
	}
}

func TestStreamRenderReadyDoesNotPadAnswerContent(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.streamRenderSeq = 2
	m.streamRenderResult = streamRenderResult{
		seq:         1,
		runID:       1,
		width:       m.chatColumnWidth(),
		themeGen:    currentThemeGeneration(),
		answerLines: []string{"one", "two", "three", "four"},
	}

	next, _ := m.handleStreamRenderReady(streamRenderReadyMsg{
		snapshot: streamRenderSnapshot{seq: 2, runID: 1, width: m.chatColumnWidth(), themeGen: currentThemeGeneration()},
		result: streamRenderResult{
			seq:         2,
			runID:       1,
			width:       m.chatColumnWidth(),
			themeGen:    currentThemeGeneration(),
			answerLines: []string{"new three", "new four"},
		},
	})

	if got := len(next.streamRenderResult.answerLines); got != 2 {
		t.Fatalf("answer content lines = %d, want unpadded content height 2", got)
	}
	if strings.Join(next.streamRenderResult.answerLines, "\n") != "new three\nnew four" {
		t.Fatalf("new lines should not receive leading blank padding, got %#v", next.streamRenderResult.answerLines)
	}
}

func TestStyleStreamingLineFadesOnlyLatestLine(t *testing.T) {
	m := model{
		fadeActive:         true,
		lineAges:           []time.Time{time.Unix(0, 0), time.Unix(0, 0)},
		lastStreamActivity: time.Unix(0, 0),
		now:                func() time.Time { return time.Unix(0, 0) },
	}
	base := kajicodeTheme.ink.Render("settled")
	if got := m.styleStreamingLine("settled", 0, 2); got != base {
		t.Fatalf("non-latest streaming line should stay base ink, got %q want %q", got, base)
	}
}

func TestPlainStreamingPreviewKeepsLeadingBoundarySpaceOutOfWords(t *testing.T) {
	text := strings.Repeat("alpha beta gamma ", 200) + "tail"
	lines := plainStreamingPreviewLines(text, 60)
	if len(lines) == 0 {
		t.Fatal("expected preview lines")
	}
	plain := ansiPattern.ReplaceAllString(lipgloss.JoinVertical(lipgloss.Left, lines...), "")
	first := strings.Fields(plain)[0]
	if first != "alpha" && first != "beta" && first != "gamma" {
		t.Fatalf("preview starts with partial word %q in %q", first, plain[:minInt(len(plain), 80)])
	}
}

func TestBoundedStreamingWindowKeepsUTF8Valid(t *testing.T) {
	text := strings.Repeat("世界", 300) + "tail"
	window := boundedStreamingWindowBytes([]byte(text), 511)
	if window.start <= 0 {
		t.Fatalf("window start = %d, want bounded start", window.start)
	}
	if !utf8.ValidString(window.text) {
		t.Fatalf("window text starts inside a UTF-8 rune: %q", window.text[:minInt(len(window.text), 16)])
	}
	if !strings.HasSuffix(window.text, "tail") {
		t.Fatalf("window should keep live tail, got suffix %q", window.text[maxInt(0, len(window.text)-16):])
	}
}

func utf8LastRune(value []byte) (rune, bool) {
	r, size := utf8.DecodeLastRune(value)
	if r == utf8.RuneError && size == 0 {
		return 0, false
	}
	return r, true
}
