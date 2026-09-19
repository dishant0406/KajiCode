package tui

import (
	"strings"
	"testing"
)

// TestTranscriptFoldSplitMatchesOnePass guards foldTranscriptRange's layout
// continuity: folding a transcript in two ranges at ANY split point must render
// exactly the same body lines as folding it in one pass. The live-run shortcut
// folds the frozen prefix and the volatile tail as two ranges, so a broken
// boundary (a lost separator, a duplicated one, or a dropped specialist
// summary) would silently corrupt the view.
func TestTranscriptFoldSplitMatchesOnePass(t *testing.T) {
	m := mouseTestModel()
	m.transcript = initialTranscript()
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "do the thing"})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowReasoning, id: "r1", runID: 1, text: "thinking"})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, id: "c1", runID: 1, tool: "bash", detail: "ls"})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolResult, id: "c1", runID: 1, tool: "bash", detail: "a\nb"})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: "done", final: true})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: "again"})
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: "ok", final: true})

	width := m.chatColumnWidth()
	rc := buildRowContext(m.transcript)
	fn := transcriptRowDispatchFn(m.renderTranscriptRow)

	whole := layoutTranscriptBodyItems(m.foldTranscriptRange(transcriptFoldState{}, 0, len(m.transcript), width, false, rc, fn).items)
	want := whole.String()

	for split := 1; split < len(m.transcript); split++ {
		state := m.foldTranscriptRange(transcriptFoldState{}, 0, split, width, false, rc, fn)
		state = m.foldTranscriptRange(state, split, len(m.transcript), width, false, rc, fn)
		got := layoutTranscriptBodyItems(state.items).String()
		if got != want {
			t.Fatalf("fold split at %d differs from one-pass fold:\n--- want ---\n%s\n--- got ---\n%s", split, want, got)
		}
	}
}

// TestTranscriptPrefixCacheReusesFrozenPrefix guards the streaming fast path:
// while a run is live, changing only the streaming tail must not change the
// frozen prefix's body items, so the cache can reuse them instead of re-folding
// the whole history every frame.
func TestTranscriptPrefixCacheReusesFrozenPrefix(t *testing.T) {
	m := mouseTestModel()
	m.transcript = initialTranscript()
	for i := 0; i < 40; i++ {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, id: stringKey(i), text: "history row", final: true})
	}
	m.pending = true
	m.activeRunID = 7
	m.runStartIndex = len(m.transcript)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, id: "live", tool: "bash", runID: 7, detail: "sleep"})
	m.streamingText = []byte("streaming")
	m.streamingTextHasContent = true
	m.streamingTextTail = "streaming"

	width := m.chatColumnWidth()
	first := m.transcriptBodyItemSet(width, "", false)

	// Only the volatile tail changes; the frozen prefix must be reused verbatim.
	m.streamingText = []byte("streaming more tokens arriving")
	m.streamingTextTail = "arriving"
	second := m.transcriptBodyItemSet(width, "", false)

	prefix := m.runStartIndex
	if len(first.items) < prefix || len(second.items) < prefix {
		t.Fatalf("body item counts %d/%d shorter than prefix %d", len(first.items), len(second.items), prefix)
	}
	for i := 0; i < prefix; i++ {
		if first.items[i].rowIndex != second.items[i].rowIndex || first.items[i].kind != second.items[i].kind {
			t.Fatalf("prefix item %d changed across a tail-only update: %+v vs %+v", i, first.items[i], second.items[i])
		}
	}
}

// TestTranscriptPrefixCacheKeyStableWhileStreaming confirms the O(1) prefix key
// does not change as the run streams, which is what lets the prefix stay cached.
func TestTranscriptPrefixCacheKeyStableWhileStreaming(t *testing.T) {
	m := mouseTestModel()
	m.transcript = initialTranscript()
	for i := 0; i < 10; i++ {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, id: stringKey(i), text: "row", final: true})
	}
	m.pending = true
	m.activeRunID = 3
	m.runStartIndex = len(m.transcript)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: "live", runID: 3})

	width := m.chatColumnWidth()
	key1, ok1 := m.transcriptPrefixCacheKey(m.flushed, m.runStartIndex, width, "", false)
	m.streamingText = []byte(strings.Repeat("x", 500))
	m.spinnerPhase++
	key2, ok2 := m.transcriptPrefixCacheKey(m.flushed, m.runStartIndex, width, "", false)

	if !ok1 || !ok2 {
		t.Fatalf("prefix should be cacheable while streaming: %v/%v", ok1, ok2)
	}
	if key1 != key2 {
		t.Fatalf("prefix key changed while streaming: %q vs %q", key1, key2)
	}
}

// TestTranscriptPrefixCacheInvalidatesOnRowRewrite guards the in-place-rewrite
// case: toggling a collapsible row inside the frozen prefix must invalidate the
// memoized items, or the view would keep the stale collapsed/expanded visual.
func TestTranscriptPrefixCacheInvalidatesOnRowRewrite(t *testing.T) {
	m := mouseTestModel()
	m.transcript = initialTranscript()
	for i := 0; i < 10; i++ {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, id: stringKey(i), text: "row", final: true})
	}
	frozenIdx := len(m.transcript)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolResult, id: "frozen", tool: "bash", detail: "line a\nline b\nline c"})
	m.pending = true
	m.activeRunID = 5
	m.runStartIndex = len(m.transcript)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowToolCall, id: "live", tool: "bash", runID: 5})

	width := m.chatColumnWidth()
	before, okBefore := m.transcriptPrefixCacheKey(m.flushed, m.runStartIndex, width, "", false)

	m = m.toggleTranscriptRow(frozenIdx)
	after, okAfter := m.transcriptPrefixCacheKey(m.flushed, m.runStartIndex, width, "", false)

	if m.transcriptMutations == 0 {
		t.Fatal("toggling a row must bump transcriptMutations")
	}
	if !okBefore || !okAfter {
		t.Fatalf("prefix should stay cacheable: %v/%v", okBefore, okAfter)
	}
	if before == after {
		t.Fatalf("prefix cache key must change after an in-prefix rewrite: %q", before)
	}
}
