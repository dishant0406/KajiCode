package tui

import (
	"strings"
	"time"
)

func (m *model) clearStreamRender() {
	m.streamRenderSeq++
	m.streamRenderInFlight = false
	m.streamRenderDirty = false
	m.streamRenderWakeSeq++
	m.streamRenderWakeScheduled = false
	m.streamRenderLastStarted = time.Time{}
	m.streamRenderResult = streamRenderResult{}
}

func (m *model) clearStreamingText() {
	m.streamingText = nil
	m.streamingTextHasContent = false
	m.streamingTextTail = ""
	m.clearStreamRender()
}

func (m *model) clearStreamingReasoning() {
	m.streamingReasoning = nil
	m.streamingReasoningHasText = false
	m.streamingReasoningTail = ""
	m.streamingReasoningExpanded = false
}

func (m model) validStreamRender(width int) (streamRenderResult, bool) {
	result := m.streamRenderResult
	if result.seq == 0 || result.runID != m.activeRunID || result.width != width {
		return streamRenderResult{}, false
	}
	if result.themeGen != currentThemeGeneration() {
		return streamRenderResult{}, false
	}
	return result, true
}

func (m model) streamingTextSuffixAfter(byteOffset int) string {
	if byteOffset < 0 || byteOffset >= len(m.streamingText) {
		return ""
	}
	return boundedUTF8TailBytes(m.streamingText[byteOffset:], streamPreviewSuffixBytes)
}

func (m model) streamingTextHasVisibleContent() bool {
	if m.streamingTextHasContent {
		return true
	}
	return len(m.streamingText) > 0
}

func (m model) streamingReasoningHasVisibleContent() bool {
	if m.streamingReasoningHasText {
		return true
	}
	return len(m.streamingReasoning) > 0
}

func plainStreamingPreviewLines(text string, width int) []string {
	text = strings.TrimSpace(boundedUTF8TailString(text, streamPreviewSuffixBytes))
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	measure := assistantMeasure(width)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapANSITextWithPrefixes("", "", strings.TrimRight(line, "\r"), measure)...)
	}
	return out
}
