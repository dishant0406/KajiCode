package tui

import "strconv"

// transcriptFoldState carries the incremental state of a transcript body fold:
// the accumulated items plus the continuity flags the next row's layout depends
// on (blank/rule separators, and the one-shot specialist summary). It is
// threaded through transcriptFoldRange so folding the settled prefix and then
// the live tail renders identically to folding the whole transcript in one pass.
type transcriptFoldState struct {
	items                    []transcriptBodyItem
	shownAny                 bool
	previousKind             rowKind
	havePreviousKind         bool
	specialistSummaryEmitted bool
}

// transcriptPrefixCache memoizes the fold of the settled prefix — every
// transcript row before the live run's first row (m.runStartIndex). A streaming
// turn re-folds the volatile tail every frame; without this it would also
// re-fold the entire history (thousands of rows) each frame, which is what made
// long threads lag. The prefix only changes between runs (rows are stamped once
// and never mutated in place while a run is live), so the memo key is O(1).
type transcriptPrefixCache struct {
	key          string
	baseRowCount int
	items        []transcriptBodyItem
	shownAny     bool
	previousKind rowKind
	havePrev     bool
	summarySent  bool
}

func newTranscriptPrefixCache() *transcriptPrefixCache {
	return &transcriptPrefixCache{}
}

// transcriptPrefixFold returns the fold state seeded with the settled prefix
// folded and memoized. When the prefix is empty the caller's state is returned
// unchanged, so the caller can always chain the live-tail fold afterwards.
func (m model) transcriptPrefixFold(state transcriptFoldState, startIdx, end, width int, emptyOverlay string, detailed bool, rc rowContext, renderRowFn transcriptRowDispatchFn) transcriptFoldState {
	if end > len(m.transcript) {
		end = len(m.transcript)
	}
	if end <= startIdx {
		return state
	}
	cache := m.transcriptPrefixCache
	if cache == nil {
		return m.foldTranscriptRange(state, startIdx, end, width, detailed, rc, renderRowFn)
	}
	key, cacheable := m.transcriptPrefixCacheKey(startIdx, end, width, emptyOverlay, detailed)
	if cacheable && cache.key == key && cache.baseRowCount == end {
		// Return a fresh items slice: the tail fold appends to it, and the
		// cached base must never be written through. The copy is an O(prefix)
		// memcpy of slice headers, far cheaper than re-folding the rows.
		items := make([]transcriptBodyItem, len(cache.items), len(cache.items)+4)
		copy(items, cache.items)
		return transcriptFoldState{
			items:                    items,
			shownAny:                 cache.shownAny,
			previousKind:             cache.previousKind,
			havePreviousKind:         cache.havePrev,
			specialistSummaryEmitted: cache.summarySent,
		}
	}
	folded := m.foldTranscriptRange(state, startIdx, end, width, detailed, rc, renderRowFn)
	if cacheable {
		cache.key = key
		cache.baseRowCount = end
		cache.items = folded.items
		cache.shownAny = folded.shownAny
		cache.previousKind = folded.previousKind
		cache.havePrev = folded.havePreviousKind
		cache.summarySent = folded.specialistSummaryEmitted
	}
	return folded
}

// transcriptPrefixCacheKey identifies the (width, region, row content) the
// memoized prefix was folded for. It is O(1): the prefix rows are frozen for
// the whole run, so only the geometry and region inputs need to be captured.
// Modes that render the prefix differently per frame (file drill-in, selection,
// hover, the uncapped detailed view) are not memoized.
func (m model) transcriptPrefixCacheKey(startIdx, end, width int, emptyOverlay string, detailed bool) (string, bool) {
	if detailed || m.fileView.active || m.transcriptSelection.active || m.hover.kind == hoverTranscript {
		return "", false
	}
	hash := newTranscriptFingerprintHash()
	writeFingerprintField(&hash, "transcript-prefix-fold-v1")
	writeFingerprintField(&hash, strconv.Itoa(width))
	writeFingerprintField(&hash, strconv.Itoa(m.flushed))
	writeFingerprintField(&hash, strconv.FormatBool(m.flushedAny))
	writeFingerprintField(&hash, strconv.Itoa(m.transcriptMutations))
	writeFingerprintField(&hash, emptyOverlay)
	writeFingerprintField(&hash, m.selectedFile)
	writeFingerprintField(&hash, m.cwd)
	writeFingerprintField(&hash, strconv.FormatBool(m.titleBarInTranscriptBody()))
	return hash.sumString(), true
}
