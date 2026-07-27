package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const subchatTranscriptPageRows = 96

type subchatPreparedMsg struct {
	seq            int
	childSessionID string
	rows           []transcriptRow
	err            error
}

type subchatContinueMsg struct {
	seq int
}

// subchatState manages the drill-in view for a specialist's child session.
// When active, the transcript body swaps to show the child session's events
// instead of the parent's. ArrowUp/Esc pops back to the parent view.
type subchatState struct {
	// active is true when the transcript is showing a child session.
	active bool
	// childSessionID is the session being viewed.
	childSessionID string
	// childSessionTitle is the display title for the subchat nav bar.
	childSessionTitle string
	// parentScrollOffset preserves the chat scroll position so popping back
	// returns to the same view.
	parentScrollOffset int
	// childRows are the rehydrated transcript rows from the child session.
	childRows []transcriptRow
	loadSeq   int
	loading   bool
	pending   []transcriptRow
}

// startSubchat activates the child surface immediately and materializes its
// persisted transcript outside Bubble Tea's update goroutine.
func (m model) startSubchat(childSessionID, title string, parentScrollOffset int) (model, tea.Cmd, string) {
	store := m.sessionStore
	if store == nil || childSessionID == "" {
		return m, nil, "No session store available."
	}
	m.subchat.loadSeq++
	seq := m.subchat.loadSeq
	m.subchat.active = true
	m.subchat.loading = true
	m.subchat.childSessionID = childSessionID
	m.subchat.childSessionTitle = title
	m.subchat.parentScrollOffset = parentScrollOffset
	m.subchat.childRows = appendRow(nil, rowSystem, "Loading thread…")
	m.subchat.pending = nil
	return m, func() tea.Msg {
		events, err := store.ReadEvents(childSessionID)
		if err != nil {
			return subchatPreparedMsg{seq: seq, childSessionID: childSessionID, err: err}
		}
		rows := transcriptRowsFromSessionEvents(events)
		return subchatPreparedMsg{
			seq:            seq,
			childSessionID: childSessionID,
			rows:           appendTranscriptRowsDedup(nil, rows),
		}
	}, ""
}

func (m model) applySubchatPrepared(msg subchatPreparedMsg) (model, tea.Cmd) {
	if !m.subchat.active || !m.subchat.loading || msg.seq != m.subchat.loadSeq || msg.childSessionID != m.subchat.childSessionID {
		return m, nil
	}
	if msg.err != nil {
		m.chatScrollOffset = m.subchat.exit()
		return m.appendSystemNotice("Could not load subagent session: " + msg.err.Error()), nil
	}
	if !m.altScreen || len(msg.rows) <= subchatTranscriptPageRows {
		m.subchat.childRows = msg.rows
		m.subchat.pending = nil
		m.subchat.loading = false
		return m, nil
	}
	split := len(msg.rows) - subchatTranscriptPageRows
	m.subchat.pending = append([]transcriptRow(nil), msg.rows[:split]...)
	m.subchat.childRows = append([]transcriptRow(nil), msg.rows[split:]...)
	return m, subchatContinueCmd(msg.seq)
}

func (m model) continueSubchatTranscript(msg subchatContinueMsg) (model, tea.Cmd) {
	if !m.subchat.active || !m.subchat.loading || msg.seq != m.subchat.loadSeq {
		return m, nil
	}
	pageRows := subchatTranscriptPageRows
	if len(m.subchat.childRows) > pageRows {
		pageRows = len(m.subchat.childRows)
	}
	start := len(m.subchat.pending) - pageRows
	if start < 0 {
		start = 0
	}
	page := append([]transcriptRow(nil), m.subchat.pending[start:]...)
	m.subchat.pending = m.subchat.pending[:start]
	m.subchat.childRows = append(page, m.subchat.childRows...)
	if len(m.subchat.pending) == 0 {
		m.subchat.loading = false
		return m, nil
	}
	return m, subchatContinueCmd(msg.seq)
}

func subchatContinueCmd(seq int) tea.Cmd {
	return tea.Tick(time.Millisecond, func(time.Time) tea.Msg {
		return subchatContinueMsg{seq: seq}
	})
}

// exit deactivates the subchat view and returns the saved parent scroll offset.
func (s *subchatState) exit() int {
	offset := s.parentScrollOffset
	s.loadSeq++
	s.active = false
	s.loading = false
	s.childSessionID = ""
	s.childSessionTitle = ""
	s.parentScrollOffset = 0
	s.childRows = nil
	s.pending = nil
	return offset
}

// renderSubchatNavBar renders the navigation bar shown at the top of the
// subchat view, telling the user how to get back to the main chat.
func renderSubchatNavBar(title string, width int) string {
	nav := "← Back to main chat (ArrowUp/Esc)"
	if title != "" {
		nav += "  ·  " + truncateRunes(title, width-40)
	}
	return kajicodeTheme.accent.Render(nav)
}
