package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestStreamRenderLatestSnapshotWins(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 7
	m.width = 100
	m.height = 30
	now := time.Unix(100, 0)
	m.now = func() time.Time { return now }
	m.streamingText = []byte("first")

	var cmd tea.Cmd
	m, cmd = m.requestStreamRender(m.chatColumnWidth())
	if cmd == nil || !m.streamRenderInFlight || m.streamRenderSeq != 1 {
		t.Fatalf("first request inFlight/seq/cmd = %v/%d/%v, want true/1/non-nil", m.streamRenderInFlight, m.streamRenderSeq, cmd)
	}

	m.streamingText = []byte("second")
	m, cmd = m.requestStreamRender(m.chatColumnWidth())
	if cmd != nil {
		t.Fatal("request during in-flight render should not start a parallel render")
	}
	if !m.streamRenderDirty {
		t.Fatal("request during in-flight render should mark dirty")
	}
	now = now.Add(streamRenderMinInterval)

	stale := streamRenderReadyMsg{
		snapshot: streamRenderSnapshot{seq: 1, runID: 7, width: m.chatColumnWidth(), themeGen: currentThemeGeneration()},
		result:   streamRenderResult{seq: 1, runID: 7, width: m.chatColumnWidth(), themeGen: currentThemeGeneration(), answerLines: []string{"first"}},
	}
	updated, nextCmd := m.Update(stale)
	m = updated.(model)
	if nextCmd == nil || !m.streamRenderInFlight {
		t.Fatalf("stale ready should schedule latest render, inFlight=%v cmd=%v", m.streamRenderInFlight, nextCmd)
	}
	if m.streamRenderResult.seq != 1 {
		t.Fatalf("ready result = %+v, want last rich render retained", m.streamRenderResult)
	}

	ready := nextCmd().(streamRenderReadyMsg)
	if !strings.Contains(ready.snapshot.text, "second") || ready.result.textBytes != len("second") {
		t.Fatalf("latest render snapshot/result = %q/%+v, want second snapshot", ready.snapshot.text, ready.result)
	}
}

func TestPendingInterimItemUsesFixedHeight(t *testing.T) {
	renders := 0
	item := transcriptBodyItem{
		kind:        transcriptBodyItemPendingInterim,
		fixedHeight: 3,
		render: func(int) transcriptBodyRenderedItem {
			renders++
			return transcriptBodyRenderedItem{lines: []string{"one", "two", "three"}}
		},
	}

	layout := measureTranscriptBodyItems([]transcriptBodyItem{item}, newTranscriptBodyHeightCache(8))
	if got := layout.totalLines(); got != 3 {
		t.Fatalf("fixed height total lines = %d, want 3", got)
	}
	if renders != 0 {
		t.Fatalf("fixed-height measurement rendered item %d times, want 0", renders)
	}
}

func TestInterimBlockReplacesStaleLiveTailWhileNextRenderIsDirty(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.width = 100
	m.height = 30
	m.streamingText = []byte("stable paragraph\n\nThe answer starts with **mar")
	m.streamingTextHasContent = true
	m.streamingTextTail = string(m.streamingText)
	var cmd tea.Cmd
	m, cmd = m.requestStreamRender(m.chatColumnWidth())
	updated, _ := m.Update(cmd().(streamRenderReadyMsg))
	m = updated.(model)

	m.streamingText = append(m.streamingText, []byte("kdown** tail")...)
	m.streamingTextTail = appendBoundedStreamingTail(m.streamingTextTail, "kdown** tail")
	m.streamRenderDirty = true
	m.streamRenderInFlight = true

	got := plainRender(t, m.interimBlock(m.chatColumnWidth()))
	if strings.Contains(got, "**mar\nkdown**") {
		t.Fatalf("interim block split the stale active markdown tail across frames:\n%s", got)
	}
	joined := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(joined, "The answer starts with **markdown** tail") {
		t.Fatalf("interim block should render the latest live tail once, got:\n%s", got)
	}
}

func TestStreamRenderSnapshotUsesBoundedTail(t *testing.T) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.width = 100
	m.height = 30
	long := "beginning " + strings.Repeat("middle ", 2000) + "live tail"
	reasoning := "early " + strings.Repeat("thought ", 2000) + "reasoning tail"
	m.streamingText = []byte(long)
	m.streamingTextHasContent = true
	m.streamingTextTail = boundedUTF8TailString(long, streamPreviewTailBytes)
	m.streamingReasoning = []byte(reasoning)
	m.streamingReasoningHasText = true
	m.streamingReasoningTail = boundedUTF8TailString(reasoning, streamPreviewTailBytes)

	snapshot, ok := m.streamRenderSnapshot(m.chatColumnWidth())
	if !ok {
		t.Fatal("expected stream snapshot")
	}
	if snapshot.textBytes != len(long) || snapshot.reasoningBytes != len(reasoning) {
		t.Fatalf("snapshot byte offsets = %d/%d, want %d/%d", snapshot.textBytes, snapshot.reasoningBytes, len(long), len(reasoning))
	}
	if len(snapshot.text) > streamPreviewTailBytes || len(snapshot.reasoning) > streamPreviewTailBytes {
		t.Fatalf("snapshot not bounded: text=%d reasoning=%d", len(snapshot.text), len(snapshot.reasoning))
	}
	if strings.Contains(snapshot.text, "beginning") || !strings.Contains(snapshot.text, "live tail") {
		t.Fatalf("text snapshot should keep only the live tail, got %q", snapshot.text[:minInt(len(snapshot.text), 80)])
	}
	if strings.Contains(snapshot.reasoning, "early") || !strings.Contains(snapshot.reasoning, "reasoning tail") {
		t.Fatalf("reasoning snapshot should keep only the live tail, got %q", snapshot.reasoning[:minInt(len(snapshot.reasoning), 80)])
	}
}

func BenchmarkInterimBlockWithAsyncRenderResult(b *testing.B) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.width = 120
	m.height = 40
	m.streamingText = []byte(strings.Repeat("This is a long streaming paragraph with **markdown** and a [link](https://example.com). ", 80))
	var cmd tea.Cmd
	m, cmd = m.requestStreamRender(m.chatColumnWidth())
	ready := cmd().(streamRenderReadyMsg)
	updated, _ := m.Update(ready)
	m = updated.(model)

	b.ResetTimer()
	for range b.N {
		_ = m.interimBlock(m.chatColumnWidth())
	}
}

func BenchmarkInterimBlockWhileRenderInFlightLongParagraph(b *testing.B) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.width = 120
	m.height = 40
	long := strings.Repeat("streaming text with no paragraph boundary ", 8000)
	m.streamingText = []byte(long)
	m.streamingTextHasContent = true
	m.streamingTextTail = boundedUTF8TailString(long, streamPreviewTailBytes)
	m.streamRenderSeq = 1
	m.streamRenderInFlight = true

	b.ResetTimer()
	for range b.N {
		_ = m.interimBlock(m.chatColumnWidth())
	}
}

func BenchmarkViewWhileRenderInFlightLongParagraph(b *testing.B) {
	m := mouseTestModel()
	m.pending = true
	m.activeRunID = 1
	m.width = 120
	m.height = 40
	long := strings.Repeat("streaming text with no paragraph boundary ", 8000)
	m.streamingText = []byte(long)
	m.streamingTextHasContent = true
	m.streamingTextTail = boundedUTF8TailString(long, streamPreviewTailBytes)
	m.streamRenderSeq = 1
	m.streamRenderInFlight = true

	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}
