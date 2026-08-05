package tui

import (
	"encoding/json"
	"strconv"
)

type transcriptScrollMetricsCache struct {
	key        string
	totalLines int
	height     int
}

func newTranscriptScrollMetricsCache() *transcriptScrollMetricsCache {
	return &transcriptScrollMetricsCache{}
}

func (m model) cachedTranscriptViewport(width int, frame transcriptFrameLayout) (transcriptViewport, bool) {
	if m.transcriptScrollCache == nil {
		return transcriptViewport{}, false
	}
	key := m.transcriptScrollMetricsCacheKey(width, frame)
	if m.transcriptScrollCache.key != key {
		return transcriptViewport{}, false
	}
	return newTranscriptViewport(
		m.transcriptScrollCache.totalLines,
		m.transcriptScrollCache.height,
		m.chatScrollOffset,
	), true
}

func (m model) storeTranscriptScrollMetrics(width int, frame transcriptFrameLayout, metrics transcriptBodyLayout) {
	if m.transcriptScrollCache == nil {
		return
	}
	m.transcriptScrollCache.key = m.transcriptScrollMetricsCacheKey(width, frame)
	m.transcriptScrollCache.totalLines = metrics.totalLines()
	m.transcriptScrollCache.height = frame.bodyRect.height
}

func (m model) transcriptScrollMetricsCacheKey(width int, frame transcriptFrameLayout) string {
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "transcript-scroll-metrics-v1")
	writeFingerprintField(&hash, strconv.Itoa(width))
	writeFingerprintField(&hash, strconv.Itoa(m.height))
	writeFingerprintField(&hash, strconv.Itoa(frame.bodyRect.height))
	writeFingerprintField(&hash, strconv.FormatBool(m.transcriptDetailed))
	writeFingerprintField(&hash, strconv.FormatBool(m.subchat.active))
	writeFingerprintField(&hash, strconv.FormatBool(m.fileView.active))
	writeFingerprintField(&hash, strconv.Itoa(m.flushed))
	writeFingerprintField(&hash, strconv.FormatBool(m.flushedAny))
	writeFingerprintField(&hash, m.selectedFile)
	writeFingerprintField(&hash, m.cwd)

	rows := m.transcript
	if m.subchat.active {
		writeFingerprintField(&hash, m.subchat.childSessionTitle)
		rows = m.subchat.childRows
	}
	writeFingerprintField(&hash, strconv.Itoa(len(rows)))
	for _, row := range rows {
		writeFingerprintField(&hash, transcriptRowRenderFingerprint(row))
	}

	m.writePendingScrollMetricFields(&hash)
	return hash.sumString()
}

func (m model) writePendingScrollMetricFields(hash *transcriptFingerprintHash) {
	writeFingerprintField(hash, strconv.FormatBool(m.pending))
	writeFingerprintField(hash, strconv.FormatBool(m.pendingPermission != nil))
	writeFingerprintField(hash, strconv.FormatBool(m.pendingAskUser != nil))
	writeFingerprintField(hash, strconv.FormatBool(m.pendingSpecReview != nil))
	writeFingerprintField(hash, strconv.FormatBool(m.streamingTextHasVisibleContent()))
	writeFingerprintField(hash, strconv.FormatBool(m.streamingReasoningHasVisibleContent()))
	writeFingerprintField(hash, strconv.FormatBool(m.sidebarActive()))
	writeFingerprintField(hash, strconv.FormatBool(m.reducedMotion))
	writeFingerprintField(hash, strconv.Itoa(m.spinnerPhase))
	writeFingerprintField(hash, string(m.activePhase.Kind))
	writeFingerprintField(hash, m.activePhase.Detail)
	m.writePendingPromptMetricFields(hash)
	m.writePlanMetricFields(hash)
	if !m.pending {
		return
	}
	writeFingerprintField(hash, strconv.Itoa(m.activeRunID))
	writeFingerprintField(hash, strconv.Itoa(len(m.streamingText)))
	writeFingerprintField(hash, m.streamingTextTail)
	writeFingerprintField(hash, strconv.Itoa(len(m.streamingReasoning)))
	writeFingerprintField(hash, m.streamingReasoningTail)
	writeFingerprintField(hash, strconv.FormatBool(m.streamingReasoningExpanded))
	writeFingerprintField(hash, strconv.Itoa(m.streamRenderSeq))
	writeFingerprintField(hash, m.streamCallID)
	writeFingerprintField(hash, m.streamCallName)
	if m.streamCallDecoder == nil {
		writeFingerprintField(hash, "")
		return
	}
	d := m.streamCallDecoder
	writeFingerprintField(hash, d.path)
	writeFingerprintField(hash, strconv.Itoa(d.rawLen))
	writeFingerprintField(hash, strconv.Itoa(d.lineTotal()))
	writeFingerprintField(hash, strconv.FormatBool(d.hasContent()))
}

func (m model) writePendingPromptMetricFields(hash *transcriptFingerprintHash) {
	if m.pendingPermission == nil {
		writeFingerprintField(hash, "")
	} else {
		writeJSONFingerprintField(hash, m.pendingPermission.request)
		writeFingerprintField(hash, strconv.Itoa(m.pendingPermission.cursor))
	}
	if m.pendingAskUser == nil {
		writeFingerprintField(hash, "")
	} else {
		writeJSONFingerprintField(hash, m.pendingAskUser.request)
		writeFingerprintField(hash, strconv.Itoa(m.pendingAskUser.active))
		writeFingerprintField(hash, strconv.Itoa(len(m.pendingAskUser.states)))
	}
	if m.pendingSpecReview == nil {
		writeFingerprintField(hash, "")
		return
	}
	review := m.pendingSpecReview
	writeFingerprintField(hash, review.SpecID)
	writeFingerprintField(hash, review.SpecTitle)
	writeFingerprintField(hash, review.SpecFilePath)
	writeFingerprintField(hash, review.RelativePath)
	writeFingerprintField(hash, review.DraftSessionID)
}

func (m model) writePlanMetricFields(hash *transcriptFingerprintHash) {
	writeFingerprintField(hash, strconv.Itoa(len(m.plan.steps)))
	for _, step := range m.plan.steps {
		writeFingerprintField(hash, step.content)
		writeFingerprintField(hash, step.status)
		writeFingerprintField(hash, step.notes)
	}
}

func writeJSONFingerprintField(hash *transcriptFingerprintHash, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeFingerprintField(hash, "")
		return
	}
	writeFingerprintField(hash, string(data))
}
