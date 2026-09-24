package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

const (
	// sessionTitleTimeout bounds a single title generation so a hung provider can
	// never wedge the background command.
	sessionTitleTimeout = 30 * time.Second
)

// sessionTitleGeneratedMsg carries the outcome of a background title generation
// back to the Update loop. backfill distinguishes a /retitle queue step (which
// advances the queue and updates a status row) from a silent auto-title.
type sessionTitleGeneratedMsg struct {
	sessionID string
	title     string
	backfill  bool
	err       error
}

// sessionTitleDigest renders a compact, bounded transcript of a session for the
// title prompt. The shared implementation lives in internal/sessions so the TUI,
// CLI, and ACP produce identical digests.
func sessionTitleDigest(events []sessions.Event) string {
	return sessions.TitleDigest(events, agent.IsNoProgressStop)
}

// cleanGeneratedTitle normalizes a raw model response into a single short title
// line. The shared implementation lives in internal/sessions.
func cleanGeneratedTitle(raw string) string {
	return sessions.CleanTitle(raw)
}

// generateSessionTitle asks the provider for a concise title for digest and
// returns the cleaned result. The shared implementation lives in
// internal/sessions; the TUI keeps this thin wrapper so its callers are unchanged.
func generateSessionTitle(ctx context.Context, provider kajicoderuntime.Provider, digest string) (string, error) {
	return sessions.GenerateTitle(ctx, provider, digest)
}

// generateSessionTitleCmd builds the background command that generates and
// persists a title for sessionID. When precomputedDigest is empty the command
// reads the session's events itself (the backfill path), keeping that I/O off the
// Update goroutine; the auto-title path passes the in-memory digest directly.
func (m model) generateSessionTitleCmd(sessionID string, precomputedDigest string, backfill bool) tea.Cmd {
	provider := m.provider
	store := m.sessionStore
	return func() tea.Msg {
		digest := precomputedDigest
		if strings.TrimSpace(digest) == "" {
			events, err := store.ReadEvents(sessionID)
			if err != nil {
				return sessionTitleGeneratedMsg{sessionID: sessionID, backfill: backfill, err: err}
			}
			digest = sessionTitleDigest(events)
		}
		ctx, cancel := context.WithTimeout(context.Background(), sessionTitleTimeout)
		defer cancel()
		title, err := generateSessionTitle(ctx, provider, digest)
		if err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, backfill: backfill, err: err}
		}
		updated, err := store.UpdateTitle(sessionID, title)
		if err != nil {
			return sessionTitleGeneratedMsg{sessionID: sessionID, title: title, backfill: backfill, err: err}
		}
		return sessionTitleGeneratedMsg{sessionID: sessionID, title: updated.Title, backfill: backfill}
	}
}

// firstUserMessageTitle is the auto title a session would get from its first user
// message (the same derivation Create uses), or "" if it has no user message.
func firstUserMessageTitle(events []sessions.Event) string {
	for _, event := range events {
		if event.Type != sessions.EventMessage {
			continue
		}
		payload := sessionPayload(event)
		if !strings.EqualFold(payloadString(payload, "role"), "user") {
			continue
		}
		if content := strings.TrimSpace(payloadString(payload, "content")); content != "" {
			return tuiSessionTitle(content)
		}
	}
	return ""
}

// sessionTitleIsAuto reports whether a session still carries its default
// first-message title (so it is worth replacing with a model-generated one). A
// title the model already produced differs from the first message and is left
// alone.
func sessionTitleIsAuto(currentTitle string, events []sessions.Event) bool {
	trimmed := strings.TrimSpace(currentTitle)
	if trimmed == "" || trimmed == tuiSessionTitle("") {
		return true
	}
	if first := firstUserMessageTitle(events); first != "" && trimmed == first {
		return true
	}
	return false
}

// maybeAutoTitleActiveSession fires a one-shot title generation for the active
// session after a successful turn, if it still has its default first-message
// title and we have not already attempted it this process. It is a no-op when
// there is no provider, no session, or nothing worth titling.
func (m model) maybeAutoTitleActiveSession() (model, tea.Cmd) {
	// Both are required: the title cmd captures the provider AND calls
	// store.UpdateTitle, so a nil store (e.g. a fallback model in tests) would
	// nil-deref on the auto-title path. Mirrors the nil-store guards on resume.
	if m.provider == nil || m.sessionStore == nil {
		return m, nil
	}
	sessionID := m.activeSession.SessionID
	if sessionID == "" || m.titledSessions[sessionID] {
		return m, nil
	}
	if !sessionTitleIsAuto(m.activeSession.Title, m.sessionEvents) {
		return m, nil
	}
	digest := sessionTitleDigest(m.sessionEvents)
	if digest == "" {
		return m, nil
	}
	if m.titledSessions == nil {
		m.titledSessions = map[string]bool{}
	}
	m.titledSessions[sessionID] = true
	return m, m.generateSessionTitleCmd(sessionID, digest, false)
}

// startSessionRetitle scans resumable sessions for ones still carrying their
// default first-message title and queues a model-generated title for each,
// firing them one at a time. It returns a status line for the transcript.
func (m model) startSessionRetitle() (model, tea.Cmd, string) {
	if m.provider == nil {
		return m, nil, "Cannot retitle sessions: no active provider is configured."
	}
	if m.retitleActive {
		return m, nil, fmt.Sprintf("Already generating titles (%d/%d). Let it finish first.", m.retitleDone, m.retitleTotal)
	}
	list, err := m.sessionStore.ListResumable()
	if err != nil {
		return m, nil, "Sessions\nFailed to list sessions: " + err.Error()
	}
	candidates := make([]string, 0, len(list))
	for _, session := range list {
		events, err := m.sessionStore.ReadEvents(session.SessionID)
		if err != nil {
			continue
		}
		if !eventsHaveResumableContent(events) {
			continue // empty/failed run — nothing worth titling
		}
		if !sessionTitleIsAuto(session.Title, events) {
			continue // already has a model-generated title
		}
		candidates = append(candidates, session.SessionID)
	}
	if len(candidates) == 0 {
		return m, nil, "All resumable sessions already have a generated title."
	}
	if m.titledSessions == nil {
		m.titledSessions = map[string]bool{}
	}
	for _, id := range candidates {
		m.titledSessions[id] = true
	}
	m.retitleQueue = append([]string(nil), candidates[1:]...)
	m.retitleActive = true
	m.retitleTotal = len(candidates)
	m.retitleDone = 0
	m.retitleOK = 0
	cmd := m.generateSessionTitleCmd(candidates[0], "", true)
	return m, cmd, fmt.Sprintf("Generating titles for %d session(s)… this runs in the background.", len(candidates))
}

// handleSessionTitleGenerated applies a finished title and, for the /retitle
// backfill, advances the sequential queue and reports completion.
func (m model) handleSessionTitleGenerated(msg sessionTitleGeneratedMsg) (model, tea.Cmd) {
	titleOK := msg.err == nil && msg.title != ""
	if titleOK {
		if msg.sessionID == m.activeSession.SessionID {
			m.activeSession.Title = msg.title
		}
	} else {
		// titledSessions is marked optimistically when the cmd is scheduled (so a
		// second turn can't double-fire a title for the same session while the
		// first is in flight). A FAILED generation — provider error, empty title,
		// or store write error — must not leave that gate set forever, so release
		// it here; a later turn or /retitle can then retry. Success keeps the gate.
		delete(m.titledSessions, msg.sessionID)
	}
	if !msg.backfill {
		// Auto-title is silent: on failure the first-message title simply stays
		// (and the retry gate above was released).
		return m, nil
	}
	m.retitleDone++
	if titleOK {
		m.retitleOK++
	}
	if len(m.retitleQueue) > 0 {
		next := m.retitleQueue[0]
		m.retitleQueue = m.retitleQueue[1:]
		return m, m.generateSessionTitleCmd(next, "", true)
	}
	m.retitleActive = false
	summary := fmt.Sprintf("Generated titles for %d of %d session(s). Open /resume to see them.", m.retitleOK, m.retitleTotal)
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "sessions", text: summary})
	return m, nil
}
