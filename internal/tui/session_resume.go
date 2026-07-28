package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

const (
	resumeTranscriptPageRows      = 96
	resumeTranscriptScrollGapRows = 12
)

type resumePreparedMsg struct {
	seq     int
	session *sessions.Metadata
	events  []sessions.Event
	rows    []transcriptRow
	err     error
}

func (m model) startResumeCommand(args string) (model, string, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		return m, m.resumeText(), nil
	}
	if m.sessionStore == nil {
		return m, "Sessions\nerror: session store unavailable", nil
	}

	m.resumeSeq++
	m.resumeInFlight = true
	m.resumePrefixRows = 0
	m.resumePendingRows = nil
	m.resumeHistorySeq++
	m.resumeHistoryLoading = false
	m.chatScrollOffset = 0
	m.chatBodyLines = 0
	seq := m.resumeSeq
	m.transcript = reduceTranscript(m.transcript, transcriptAction{
		kind: actionAppendSystem,
		text: fmt.Sprintf("Sessions\nloading %s…", args),
	})
	return m, "", func() tea.Msg {
		session, err := m.resolveResumeSession(args)
		if err != nil {
			return resumePreparedMsg{seq: seq, err: err}
		}
		events, err := m.resumeEvents(session.SessionID)
		if err != nil {
			return resumePreparedMsg{seq: seq, err: err}
		}
		rows := transcriptRowsFromSessionEvents(events)
		return resumePreparedMsg{
			seq:     seq,
			session: session,
			events:  events,
			rows:    appendTranscriptRowsDedup(nil, rows),
		}
	}
}

func (m model) cancelResumeLoading() model {
	m.resumeSeq++
	m.resumeInFlight = false
	m.resumePrefixRows = 0
	m.resumePendingRows = nil
	m.resumeHistorySeq++
	m.resumeHistoryLoading = false
	return m
}

func (m model) applyResumePrepared(msg resumePreparedMsg) (model, tea.Cmd) {
	if msg.seq != m.resumeSeq || !m.resumeInFlight {
		return m, nil
	}
	if msg.err != nil || msg.session == nil {
		m.resumeInFlight = false
		m.resumePrefixRows = 0
		m.resumePendingRows = nil
		m.resumeHistorySeq++
		m.resumeHistoryLoading = false
		text := "unknown resume error"
		if msg.err != nil {
			text = msg.err.Error()
		}
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Sessions\nerror: " + text,
		})
		return m, nil
	}

	previousID := m.activeSession.SessionID
	m.activeSession = *msg.session
	m.sessionEvents = append([]sessions.Event(nil), msg.events...)
	if m.providerName == "" {
		m.providerName = msg.session.Provider
	}
	if m.modelName == "" {
		m.modelName = msg.session.ModelID
	}
	if mode := resumedPermissionProfile(msg.session.PermissionProfile); mode != "" {
		m.permissionMode = mode
	}

	loopsCleared := 0
	if msg.session.SessionID != previousID {
		m, loopsCleared = m.clearLoopsForSessionSwitch()
	}
	prefix := initialTranscript()
	prefix = appendRow(prefix, rowSystem, m.formatResumeSummary(*msg.session, len(msg.events)))
	if loopsCleared > 0 {
		prefix = appendRow(prefix, rowSystem, fmt.Sprintf("Stopped %d loop(s) tied to the previous session.", loopsCleared))
	}
	m.resumePrefixRows = len(prefix)
	m.resetFlushFrontier("· resumed ·")
	m.chatScrollOffset = 0
	m.chatBodyLines = 0

	if !m.altScreen || len(msg.rows) <= resumeTranscriptPageRows {
		m.transcript = append(prefix, msg.rows...)
		m.resumeInFlight = false
		m.resumePrefixRows = 0
		m.resumePendingRows = nil
		m.resumeHistorySeq++
		m.resumeHistoryLoading = false
		m.refreshComposerHistory()
		return m, nil
	}

	split := len(msg.rows) - resumeTranscriptPageRows
	m.resumePendingRows = append([]transcriptRow(nil), msg.rows[:split]...)
	m.transcript = append(prefix, msg.rows[split:]...)
	m.resumeInFlight = false
	m.resumeHistorySeq++
	m.resumeHistoryLoading = false
	m.refreshComposerHistory()
	return m, nil
}

func (m model) hasResumeHistoryBacklog() bool {
	return !m.resumeInFlight && len(m.resumePendingRows) > 0
}

func (m model) loadResumeHistoryPage(pageRows int) model {
	if !m.hasResumeHistoryBacklog() {
		return m
	}
	if m.resumePrefixRows < 0 || m.resumePrefixRows > len(m.transcript) {
		m.resumePrefixRows = 0
		m.resumePendingRows = nil
		return m
	}
	if pageRows <= 0 {
		pageRows = resumeTranscriptPageRows
	}
	if pageRows > len(m.resumePendingRows) {
		pageRows = len(m.resumePendingRows)
	}
	start := len(m.resumePendingRows) - pageRows
	page := append([]transcriptRow(nil), m.resumePendingRows[start:]...)
	m.resumePendingRows = m.resumePendingRows[:start]
	prefix := append([]transcriptRow(nil), m.transcript[:m.resumePrefixRows]...)
	tail := append([]transcriptRow(nil), m.transcript[m.resumePrefixRows:]...)
	m.transcript = append(prefix, page...)
	m.transcript = append(m.transcript, tail...)
	if len(m.resumePendingRows) == 0 {
		m.resumePrefixRows = 0
	}
	return m
}

func (m model) insertResumeHistoryPage(page []transcriptRow) model {
	if len(page) == 0 {
		return m
	}
	prefix := append([]transcriptRow(nil), m.transcript[:m.resumePrefixRows]...)
	tail := append([]transcriptRow(nil), m.transcript[m.resumePrefixRows:]...)
	m.transcript = append(prefix, page...)
	m.transcript = append(m.transcript, tail...)
	if len(m.resumePendingRows) == 0 {
		m.resumePrefixRows = 0
	}
	return m
}
