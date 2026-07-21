package tui

import "github.com/dishant0406/KajiCode/internal/sessions"

type composerHistoryEntry struct {
	sessionID string
	text      string
}

func loadComposerHistory(store *sessions.Store, cwd, activeSessionID string) []composerHistoryEntry {
	if store == nil {
		return nil
	}
	groups, err := store.ComposerHistory(sessions.ComposerHistoryOptions{
		Cwd:             cwd,
		ActiveSessionID: activeSessionID,
	})
	if err != nil {
		return nil
	}
	entries := make([]composerHistoryEntry, 0)
	// Recall walks backward, so flatten older groups first and the active/newest
	// group last while preserving chronology within each session.
	for groupIndex := len(groups) - 1; groupIndex >= 0; groupIndex-- {
		group := groups[groupIndex]
		for _, text := range group.Entries {
			entries = append(entries, composerHistoryEntry{sessionID: group.Session.SessionID, text: text})
		}
	}
	return entries
}

func (m *model) refreshComposerHistory() {
	m.inputHistory = loadComposerHistory(m.sessionStore, m.cwd, m.activeSession.SessionID)
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = composerState{}
}

func (m *model) rememberInput(value string) {
	if value == "" {
		return
	}
	entry := composerHistoryEntry{sessionID: m.activeSession.SessionID, text: value}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1].text != value {
		m.inputHistory = append(m.inputHistory, entry)
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = composerState{}
}

func (m model) recallHistory(direction int) model {
	if m.historyIdx == len(m.inputHistory) {
		if direction > 0 {
			return m
		}
		m.historyDraft = m.currentComposerState()
	}
	next := clamp(m.historyIdx+direction, 0, len(m.inputHistory))
	if next == m.historyIdx {
		return m
	}
	m.historyIdx = next
	state := m.historyDraft
	if next < len(m.inputHistory) {
		text := m.inputHistory[next].text
		state = composerState{text: text, cursor: len([]rune(text))}
	}
	m.setComposerState(state)
	m.recomputeSuggestions()
	return m
}
