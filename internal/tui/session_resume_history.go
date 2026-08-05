package tui

import tea "charm.land/bubbletea/v2"

type resumeHistoryPreparedMsg struct {
	seq       int
	rows      []transcriptRow
	heights   map[string]int
	delta     int
	oldTop    int
	width     int
	detailed  bool
	pageCount int
}

func (m model) startResumeHistoryLoad(delta int, oldTop int) (model, tea.Cmd) {
	if !m.hasResumeHistoryBacklog() || m.resumeHistoryLoading {
		return m, nil
	}
	if m.resumePrefixRows < 0 || m.resumePrefixRows > len(m.transcript) {
		m.resumePrefixRows = 0
		m.resumePendingRows = nil
		return m, nil
	}
	pageRows := minInt(resumeTranscriptScrollGapRows, len(m.resumePendingRows))
	start := len(m.resumePendingRows) - pageRows
	page := append([]transcriptRow(nil), m.resumePendingRows[start:]...)
	prefix := append([]transcriptRow(nil), m.transcript[:m.resumePrefixRows]...)
	tail := append([]transcriptRow(nil), m.transcript[m.resumePrefixRows:]...)
	transcript := append(prefix, page...)
	transcript = append(transcript, tail...)

	m.resumeHistorySeq++
	m.resumeHistoryLoading = true
	seq := m.resumeHistorySeq
	width := m.chatColumnWidth()
	detailed := m.transcriptDetailed
	scratch := m
	scratch.transcript = transcript
	scratch.resumePendingRows = nil
	scratch.transcriptBodyHeights = newTranscriptBodyHeightCache(defaultTranscriptBodyHeightCacheMaxEntries)
	scratch.transcriptBodyCache = newTranscriptBodyItemCache()
	scratch.transcriptScrollCache = newTranscriptScrollMetricsCache()

	return m, func() tea.Msg {
		return resumeHistoryPreparedMsg{
			seq:       seq,
			rows:      page,
			heights:   warmResumeHistoryHeights(scratch, width, detailed),
			delta:     delta,
			oldTop:    oldTop,
			width:     width,
			detailed:  detailed,
			pageCount: pageRows,
		}
	}
}

func (m model) applyResumeHistoryPrepared(msg resumeHistoryPreparedMsg) (model, tea.Cmd) {
	if msg.seq != m.resumeHistorySeq || !m.resumeHistoryLoading {
		return m, nil
	}
	m.resumeHistoryLoading = false
	if msg.width != m.chatColumnWidth() || msg.detailed != m.transcriptDetailed {
		return m, nil
	}
	if !m.hasResumeHistoryBacklog() || len(msg.rows) == 0 {
		return m, nil
	}
	if msg.pageCount <= 0 || msg.pageCount > len(m.resumePendingRows) {
		return m, nil
	}
	start := len(m.resumePendingRows) - msg.pageCount
	m.resumePendingRows = m.resumePendingRows[:start]
	for key, height := range msg.heights {
		m.transcriptBodyHeights.set(key, height)
	}
	m = m.insertResumeHistoryPage(msg.rows)
	if m.chatScrollOffset >= msg.oldTop {
		viewport, ok := m.chatTranscriptViewport()
		if ok {
			m.chatScrollOffset = clampInt(m.chatScrollOffset+msg.delta, 0, viewport.maxOffset())
			if m.chatScrollOffset == 0 {
				m.chatBodyLines = 0
			}
		}
	}
	return m, nil
}

func warmResumeHistoryHeights(m model, width int, detailed bool) map[string]int {
	items := m.transcriptBodyItems(width, "", detailed)
	cache := newTranscriptBodyHeightCache(defaultTranscriptBodyHeightCacheMaxEntries)
	heights := make(map[string]int, len(items))
	for _, item := range items {
		if !item.heightCacheStable || item.heightCacheKey == "" {
			continue
		}
		heights[item.heightCacheKey] = transcriptBodyItemHeight(item, cache)
	}
	return heights
}
