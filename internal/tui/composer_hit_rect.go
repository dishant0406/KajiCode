package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m model) mouseOverComposer(msg tea.MouseMsg) bool {
	return m.composerMouseRect().contains(mouseX(msg), mouseY(msg))
}

func (m model) composerMouseRect() tuiRect {
	if !m.altScreen || m.height <= 0 || m.transcriptDetailed || m.pendingAskUser != nil {
		return tuiRect{}
	}
	width := m.chatColumnWidth()
	if m.homePresentationActive() {
		return m.homeComposerRect(width)
	}
	composerTop, composerHeight, footerLines, footerClip := m.footerComposerGeometry(width)
	if composerHeight <= 0 || footerLines <= 0 {
		return tuiRect{}
	}

	headerLines := 1
	if m.pinnedTitleBar(width) == "" {
		headerLines = 0
	}
	if headerLines+footerLines >= m.height {
		headerLines = maxInt(0, m.height-footerLines-1)
	}
	if headerLines+footerLines >= m.height {
		footerLines = maxInt(0, m.height-headerLines-1)
	}
	bodyHeight := maxInt(1, m.height-headerLines-footerLines)
	visibleTop := maxInt(composerTop, footerClip)
	visibleBottom := minInt(composerTop+composerHeight, footerClip+footerLines)
	if visibleTop >= visibleBottom {
		return tuiRect{}
	}
	return tuiRect{
		y:      headerLines + bodyHeight + visibleTop - footerClip,
		width:  width,
		height: visibleBottom - visibleTop,
	}
}

func (m model) footerComposerGeometry(width int) (composerTop int, composerHeight int, footerLines int, footerClip int) {
	if width <= 0 {
		return 0, 0, 0, 0
	}
	fullLines := 0
	if !m.subchat.active {
		if planLines := m.pinnedPlanFooterLineCount(width); planLines > 0 {
			fullLines += planLines + 1
		}
	}
	fullLines++ // idle/copy/jump hint line
	if queued := renderQueuedMessagePreview(m.queuedMessage, width); queued != "" {
		fullLines += len(viewLines(queued)) + 1
	}
	composerTop = fullLines
	composerHeight = m.composerBoxLineCount(width)
	fullLines += composerHeight
	if strings.TrimSpace(m.composerDescriptionHint(width)) != "" {
		fullLines++
	}
	fullLines++ // status line

	footerLines = fullLines
	maxFooterLines := maxInt(0, m.height-1)
	if footerLines > maxFooterLines {
		footerClip = footerLines - maxFooterLines
		footerLines = maxFooterLines
	}
	return composerTop, composerHeight, footerLines, footerClip
}

func (m model) pinnedPlanFooterLineCount(width int) int {
	if m.hidePinnedPlan || m.sidebarAvailable() || !m.plan.visible(m.now()) {
		return 0
	}
	height := m.plan.height(width, m.now())
	if height == 0 {
		return 0
	}
	if maxHeight := m.pinnedPlanMaxHeight(); maxHeight > 0 && height <= maxHeight {
		return height
	}
	return 1
}

func (m model) composerBoxLineCount(width int) int {
	if width < 8 {
		return 1
	}
	lines := 2 // top border + divider
	if renderAttachmentChips(m.pendingImageLabels, m.pendingDocuments) != "" {
		lines++
	}
	return lines + m.composerContentLineCount(maxInt(1, width-4))
}

func (m model) composerContentLineCount(width int) int {
	if commandArgumentHintForInput(m.input.Value()) != "" && m.input.Position() == len([]rune(m.input.Value())) {
		return 1
	}
	input := m.input
	state := m.currentComposerState()
	if m.suggestionsActive() && (!m.suggestionsAreFiles || fileSuggestionOnlyInput(m.input.Value())) {
		state = composerState{}
	}
	if state.text == "" {
		return 1
	}
	segments, _ := composerVisibleVisualLines(input, state, width)
	return maxInt(1, len(segments))
}
