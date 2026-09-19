package tui

import (
	"strconv"

	"github.com/dishant0406/KajiCode/internal/agent"
)

type transcriptBodyItemSet struct {
	items     []transcriptBodyItem
	cacheKey  string
	cacheable bool
}

type transcriptBodyItemCache struct {
	itemsKey         string
	items            []transcriptBodyItem
	previousKind     rowKind
	havePreviousKind bool
	metricsKey       string
	metrics          transcriptBodyLayout
}

func newTranscriptBodyItemCache() *transcriptBodyItemCache {
	return &transcriptBodyItemCache{}
}

func (m model) transcriptBodyItemSet(width int, emptyOverlay string, detailed bool) transcriptBodyItemSet {
	if m.pending || m.pendingSpecReview != nil {
		if set, ok := m.pendingTranscriptBodyItemSet(width, emptyOverlay, detailed); ok {
			return set
		}
	}
	key, cacheable := m.transcriptBodyItemCacheKey(width, emptyOverlay, detailed)
	if cacheable && m.transcriptBodyCache != nil && m.transcriptBodyCache.itemsKey == key {
		return transcriptBodyItemSet{items: m.transcriptBodyCache.items, cacheKey: key, cacheable: true}
	}
	base := m.transcriptBaseBodyItems(width, emptyOverlay, detailed)
	if cacheable && m.transcriptBodyCache != nil {
		base = m.storeTranscriptBaseBodyItems(key, base)
	}
	return transcriptBodyItemSet{items: m.appendPendingTranscriptBodyItems(base, width), cacheKey: key, cacheable: cacheable}
}

func (m model) pendingTranscriptBodyItemSet(width int, emptyOverlay string, detailed bool) (transcriptBodyItemSet, bool) {
	key, cacheable := m.transcriptBodyBaseItemCacheKey(width, emptyOverlay, detailed)
	if !cacheable || m.transcriptBodyCache == nil {
		return transcriptBodyItemSet{}, false
	}
	base := transcriptBaseBodyItemSet{}
	if m.transcriptBodyCache.itemsKey == key {
		base = transcriptBaseBodyItemSet{
			items:            m.transcriptBodyCache.items,
			previousKind:     m.transcriptBodyCache.previousKind,
			havePreviousKind: m.transcriptBodyCache.havePreviousKind,
		}
	} else {
		base = m.storeTranscriptBaseBodyItems(key, m.transcriptBaseBodyItems(width, emptyOverlay, detailed))
	}
	return transcriptBodyItemSet{
		items:     m.appendPendingTranscriptBodyItems(base, width),
		cacheKey:  m.transcriptPendingBodyMetricsCacheKey(key),
		cacheable: true,
	}, true
}

func (m model) storeTranscriptBaseBodyItems(key string, base transcriptBaseBodyItemSet) transcriptBaseBodyItemSet {
	if m.transcriptBodyCache == nil {
		return base
	}
	items := make([]transcriptBodyItem, len(base.items), len(base.items)+4)
	copy(items, base.items)
	m.transcriptBodyCache.itemsKey = key
	m.transcriptBodyCache.items = items
	m.transcriptBodyCache.previousKind = base.previousKind
	m.transcriptBodyCache.havePreviousKind = base.havePreviousKind
	m.transcriptBodyCache.metricsKey = ""
	m.transcriptBodyCache.metrics = transcriptBodyLayout{}
	base.items = items
	return base
}

func (m model) measureTranscriptBodyItemSet(set transcriptBodyItemSet) transcriptBodyLayout {
	if set.cacheable && m.transcriptBodyCache != nil && m.transcriptBodyCache.metricsKey == set.cacheKey {
		return m.transcriptBodyCache.metrics
	}
	metrics := measureTranscriptBodyItems(set.items, m.transcriptBodyHeights)
	if set.cacheable && m.transcriptBodyCache != nil {
		m.transcriptBodyCache.metricsKey = set.cacheKey
		m.transcriptBodyCache.metrics = metrics
	}
	return metrics
}

func (m model) transcriptBodyItemCacheKey(width int, emptyOverlay string, detailed bool) (string, bool) {
	if m.pending || m.pendingSpecReview != nil {
		return "", false
	}
	return m.transcriptBodyBaseItemCacheKey(width, emptyOverlay, detailed)
}

func (m model) transcriptBodyBaseItemCacheKey(width int, emptyOverlay string, detailed bool) (string, bool) {
	if m.fileView.active || m.transcriptEmpty() || m.transcriptSelection.active || m.hover.kind == hoverTranscript {
		return "", false
	}
	if m.titleBarInTranscriptBody() {
		return "", false
	}
	// Check cacheability FIRST, before hashing every row. A live run makes its
	// own tool calls / undecided prompts uncacheable, and the fingerprint loop
	// below would otherwise hash the whole transcript (O(n) per frame) only to
	// throw the result away. One cheap pass to find the blocker keeps a streaming
	// turn proportional to the live tail, not the whole history.
	rc := buildRowContext(m.transcript)
	for _, row := range m.transcript {
		if !transcriptBodyBaseRowCacheable(m.pending, m.activeRunID, row, rc) {
			return "", false
		}
	}
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "transcript-body-items-v1")
	writeFingerprintField(&hash, strconv.Itoa(width))
	writeFingerprintField(&hash, emptyOverlay)
	writeFingerprintField(&hash, strconv.FormatBool(detailed))
	writeFingerprintField(&hash, strconv.Itoa(m.flushed))
	writeFingerprintField(&hash, strconv.FormatBool(m.flushedAny))
	writeFingerprintField(&hash, m.selectedFile)
	writeFingerprintField(&hash, m.cwd)
	writeFingerprintField(&hash, strconv.Itoa(len(m.transcript)))
	for _, row := range m.transcript {
		writeFingerprintField(&hash, transcriptRowRenderFingerprint(row))
	}
	return hash.sumString(), true
}

// transcriptBodyBaseRowCacheable reports whether a row's live render can be
// reused across frames. It is a free function (not a model method) because the
// loop above calls it once per transcript row every frame; a value-receiver
// method would copy the ~22 KB model struct on each call, which dominated the
// per-frame cost of long transcripts.
func transcriptBodyBaseRowCacheable(pending bool, activeRunID int, row transcriptRow, rc rowContext) bool {
	if row.kind == rowSpecialist && row.specialistInfo != nil && row.specialistInfo.status == specialistRunning {
		return false
	}
	if !pending || row.runID == 0 || row.runID != activeRunID || rc.skip(row) {
		return true
	}
	if row.kind == rowToolCall {
		return false
	}
	if row.kind != rowPermission || row.permission == nil {
		return true
	}
	event := row.permission
	return event.ToolCallID == "" || event.Action != agent.PermissionActionPrompt || rc.decided[rcKey(row.runID, event.ToolCallID)]
}

func (m model) transcriptPendingBodyMetricsCacheKey(baseKey string) string {
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "transcript-body-pending-metrics-v1")
	writeFingerprintField(&hash, baseKey)
	m.writePendingScrollMetricFields(&hash)
	return hash.sumString()
}
