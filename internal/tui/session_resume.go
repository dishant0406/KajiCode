package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

const resumeTranscriptPageRows = 96

type resumePreparedMsg struct {
	seq     int
	session *sessions.Metadata
	events  []sessions.Event
	rows    []transcriptRow
	err     error
}

type resumeContinueMsg struct {
	seq int
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

	if !m.altScreen || len(msg.rows) <= resumeTranscriptPageRows {
		m.transcript = append(prefix, msg.rows...)
		m.resumeInFlight = false
		m.resumePrefixRows = 0
		m.resumePendingRows = nil
		m.refreshComposerHistory()
		return m, nil
	}

	split := len(msg.rows) - resumeTranscriptPageRows
	m.resumePendingRows = append([]transcriptRow(nil), msg.rows[:split]...)
	m.transcript = append(prefix, msg.rows[split:]...)
	return m, resumeContinueCmd(msg.seq)
}

func (m model) continueResumeTranscript(msg resumeContinueMsg) (model, tea.Cmd) {
	if msg.seq != m.resumeSeq || !m.resumeInFlight {
		return m, nil
	}
	if len(m.resumePendingRows) == 0 {
		m.resumeInFlight = false
		m.resumePrefixRows = 0
		m.refreshComposerHistory()
		return m, nil
	}
	if m.resumePrefixRows < 0 || m.resumePrefixRows > len(m.transcript) {
		return m.cancelResumeLoading(), nil
	}

	pageRows := resumeTranscriptPageRows
	visibleRows := len(m.transcript) - m.resumePrefixRows
	if visibleRows > pageRows {
		pageRows = visibleRows
	}
	start := len(m.resumePendingRows) - pageRows
	if start < 0 {
		start = 0
	}
	page := append([]transcriptRow(nil), m.resumePendingRows[start:]...)
	m.resumePendingRows = m.resumePendingRows[:start]
	prefix := append([]transcriptRow(nil), m.transcript[:m.resumePrefixRows]...)
	tail := append([]transcriptRow(nil), m.transcript[m.resumePrefixRows:]...)
	m.transcript = append(prefix, page...)
	m.transcript = append(m.transcript, tail...)
	if len(m.resumePendingRows) == 0 {
		m.resumeInFlight = false
		m.resumePrefixRows = 0
		m.refreshComposerHistory()
		return m, nil
	}
	return m, resumeContinueCmd(msg.seq)
}

func resumeContinueCmd(seq int) tea.Cmd {
	return tea.Tick(time.Millisecond, func(time.Time) tea.Msg {
		return resumeContinueMsg{seq: seq}
	})
}
