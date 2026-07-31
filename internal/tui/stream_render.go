package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const streamRenderMinInterval = 120 * time.Millisecond

type streamRenderSnapshot struct {
	seq               int
	runID             int
	width             int
	bodyCap           int
	themeGen          uint64
	textBytes         int
	reasoningBytes    int
	text              string
	reasoning         string
	reasoningExpanded bool
}

type streamRenderResult struct {
	seq                  int
	runID                int
	width                int
	themeGen             uint64
	textBytes            int
	reasoningBytes       int
	answerLines          []string
	reasoningBlockLines  []string
	reasoningPreviewLine []string
}

type streamRenderTickMsg struct {
	seq int
}

type streamRenderReadyMsg struct {
	snapshot streamRenderSnapshot
	result   streamRenderResult
}

func streamRenderCmd(snapshot streamRenderSnapshot) tea.Cmd {
	return func() tea.Msg {
		return streamRenderReadyMsg{
			snapshot: snapshot,
			result:   renderStreamSnapshot(snapshot),
		}
	}
}

func renderStreamSnapshot(snapshot streamRenderSnapshot) streamRenderResult {
	result := streamRenderResult{
		seq:            snapshot.seq,
		runID:          snapshot.runID,
		width:          snapshot.width,
		themeGen:       snapshot.themeGen,
		textBytes:      snapshot.textBytes,
		reasoningBytes: snapshot.reasoningBytes,
	}
	withThemeReadLock(func() {
		if strings.TrimSpace(snapshot.reasoning) != "" {
			block := renderReasoningBlock(snapshot.reasoning, snapshot.reasoningExpanded, snapshot.width, true, 0, snapshot.bodyCap)
			result.reasoningBlockLines = viewLines(block)
			if !snapshot.reasoningExpanded {
				result.reasoningPreviewLine = reasoningPreviewLines(snapshot.reasoning, snapshot.width)
			}
		}
		if strings.TrimSpace(snapshot.text) != "" {
			result.answerLines = renderStreamingAssistantMarkdownText(snapshot.text, assistantMeasure(snapshot.width), snapshot.width)
		}
	})
	return result
}

func (m model) streamRenderSnapshot(width int) (streamRenderSnapshot, bool) {
	if !m.streamingTextHasVisibleContent() && !m.streamingReasoningHasVisibleContent() {
		return streamRenderSnapshot{}, false
	}
	text := ""
	if len(m.streamingText) > 0 {
		text = m.streamingTextTail
		if text == "" {
			text = boundedUTF8TailBytes(m.streamingText, streamPreviewTailBytes)
		}
		text = strings.TrimRight(text, "\n")
	}
	reasoning := ""
	if len(m.streamingReasoning) > 0 {
		reasoning = m.streamingReasoningTail
		if reasoning == "" {
			reasoning = boundedUTF8TailBytes(m.streamingReasoning, streamPreviewTailBytes)
		}
		reasoning = strings.TrimRight(reasoning, "\n")
	}
	return streamRenderSnapshot{
		runID:             m.activeRunID,
		width:             width,
		bodyCap:           m.liveReasoningBodyCap(),
		themeGen:          currentThemeGeneration(),
		textBytes:         len(m.streamingText),
		reasoningBytes:    len(m.streamingReasoning),
		text:              text,
		reasoning:         reasoning,
		reasoningExpanded: m.streamingReasoningExpanded,
	}, true
}

func (m model) requestStreamRender(width int) (model, tea.Cmd) {
	if !m.pending {
		return m, nil
	}
	if m.streamRenderInFlight {
		m.streamRenderDirty = true
		return m, nil
	}
	if cmd := m.deferStreamRenderIfCoolingDown(); cmd != nil {
		return m, cmd
	}
	return m.startStreamRender(width)
}

func (m model) startStreamRender(width int) (model, tea.Cmd) {
	snapshot, ok := m.streamRenderSnapshot(width)
	if !ok {
		m.clearStreamRender()
		return m, nil
	}
	m.streamRenderSeq++
	snapshot.seq = m.streamRenderSeq
	m.streamRenderInFlight = true
	m.streamRenderDirty = false
	m.streamRenderWakeScheduled = false
	m.streamRenderLastStarted = m.now()
	return m, streamRenderCmd(snapshot)
}

func (m *model) deferStreamRenderIfCoolingDown() tea.Cmd {
	if m.streamRenderResult.seq == 0 || m.streamRenderLastStarted.IsZero() {
		return nil
	}
	elapsed := m.now().Sub(m.streamRenderLastStarted)
	if elapsed >= streamRenderMinInterval {
		return nil
	}
	m.streamRenderDirty = true
	if m.streamRenderWakeScheduled {
		return nil
	}
	m.streamRenderWakeSeq++
	seq := m.streamRenderWakeSeq
	m.streamRenderWakeScheduled = true
	return tea.Tick(streamRenderMinInterval-elapsed, func(time.Time) tea.Msg {
		return streamRenderTickMsg{seq: seq}
	})
}

func (m model) handleStreamRenderReady(msg streamRenderReadyMsg) (model, tea.Cmd) {
	if msg.snapshot.runID != m.activeRunID {
		return m, nil
	}
	m.streamRenderInFlight = false
	currentSeq := msg.snapshot.seq == m.streamRenderSeq
	currentTheme := msg.snapshot.themeGen == currentThemeGeneration()
	if currentSeq && currentTheme {
		m.streamRenderResult = msg.result
	}
	if m.streamRenderDirty {
		return m.requestStreamRender(m.chatColumnWidth())
	}
	if currentSeq && !currentTheme {
		return m.requestStreamRender(m.chatColumnWidth())
	}
	return m, nil
}

func (m model) handleStreamRenderTick(msg streamRenderTickMsg) (model, tea.Cmd) {
	if msg.seq != m.streamRenderWakeSeq || !m.streamRenderWakeScheduled {
		return m, nil
	}
	m.streamRenderWakeScheduled = false
	if !m.streamRenderDirty {
		return m, nil
	}
	return m.requestStreamRender(m.chatColumnWidth())
}
