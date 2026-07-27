package tui

import (
	"strconv"
)

type transcriptBodyItemSet struct {
	items     []transcriptBodyItem
	cacheKey  string
	cacheable bool
}

type transcriptBodyItemCache struct {
	itemsKey   string
	items      []transcriptBodyItem
	metricsKey string
	metrics    transcriptBodyLayout
}

func newTranscriptBodyItemCache() *transcriptBodyItemCache {
	return &transcriptBodyItemCache{}
}

func (m model) transcriptBodyItemSet(width int, emptyOverlay string, detailed bool) transcriptBodyItemSet {
	key, cacheable := m.transcriptBodyItemCacheKey(width, emptyOverlay, detailed)
	if cacheable && m.transcriptBodyCache != nil && m.transcriptBodyCache.itemsKey == key {
		return transcriptBodyItemSet{items: m.transcriptBodyCache.items, cacheKey: key, cacheable: true}
	}
	items := m.transcriptBodyItems(width, emptyOverlay, detailed)
	if cacheable && m.transcriptBodyCache != nil {
		m.transcriptBodyCache.itemsKey = key
		m.transcriptBodyCache.items = items
		m.transcriptBodyCache.metricsKey = ""
		m.transcriptBodyCache.metrics = transcriptBodyLayout{}
	}
	return transcriptBodyItemSet{items: items, cacheKey: key, cacheable: cacheable}
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
	if m.fileView.active || m.pending || m.transcriptEmpty() || m.transcriptSelection.active || m.hover.kind == hoverTranscript {
		return "", false
	}
	if m.titleBarInTranscriptBody() {
		return "", false
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
		if row.kind == rowSpecialist {
			return "", false
		}
		writeFingerprintField(&hash, transcriptRowRenderFingerprint(row))
	}
	return hash.sumString(), true
}
