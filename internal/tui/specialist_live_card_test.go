package tui

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

// specialistRows returns the live specialist card rows in the transcript.
func specialistRows(m model) []transcriptRow {
	var rows []transcriptRow
	for _, row := range m.transcript {
		if row.kind == rowSpecialist {
			rows = append(rows, row)
		}
	}
	return rows
}

// liveSpecialistModel builds an idle model whose run 0 is active, so the
// specialist messages (which carry runID 0) are accepted by Update.
func liveSpecialistModel(t *testing.T) model {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	m := newModel(context.Background(), Options{ModelName: "gpt-4", SessionStore: store})
	m.width = 80
	m.height = 40
	m.altScreen = true
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	return m
}

// TestSpecialistCardAppearsOnStart proves the running card is visible from the
// moment the delegation starts — not only after it completes.
func TestSpecialistCardAppearsOnStart(t *testing.T) {
	m := liveSpecialistModel(t)

	updated, _ := m.Update(specialistStartMsg{runID: 0, name: "worker", description: "fix tests", childSessionID: "call-1"})
	m = updated.(model)

	rows := specialistRows(m)
	if len(rows) != 1 {
		t.Fatalf("a start must append one running card, got %d", len(rows))
	}
	info := rows[0].specialistInfo
	if info == nil || info.name != "worker" || info.status != specialistRunning {
		t.Fatalf("running card info = %+v, want worker/running", info)
	}
	if info.toolCallID != "call-1" {
		t.Fatalf("card toolCallID = %q, want call-1", info.toolCallID)
	}
}

// TestSpecialistCardCompletionUpdatesInPlace proves start → progress → session →
// completion updates ONE card rather than appending a second one at the end.
func TestSpecialistCardCompletionUpdatesInPlace(t *testing.T) {
	m := liveSpecialistModel(t)
	steps := []tea.Msg{
		specialistStartMsg{runID: 0, name: "worker", description: "fix tests", childSessionID: "call-1"},
		specialistProgressMsg{runID: 0, toolCallID: "call-1", toolName: "read_file", detail: "model.go"},
		specialistUsageMsg{runID: 0, toolCallID: "call-1", tokens: 1200},
		specialistSessionMsg{runID: 0, toolCallID: "call-1", childSessionID: "sess-9"},
		specialistCompleteMsg{runID: 0, toolCallID: "call-1", childSessionID: "sess-9", status: specialistCompleted},
	}
	for _, msg := range steps {
		updated, _ := m.Update(msg)
		m = updated.(model)
		if got := len(specialistRows(m)); got != 1 {
			t.Fatalf("after %T: specialist rows = %d, want exactly 1", msg, got)
		}
	}
	info := specialistRows(m)[0].specialistInfo
	if info.status != specialistCompleted {
		t.Fatalf("status = %v, want completed", info.status)
	}
	if info.childSessionID != "sess-9" {
		t.Fatalf("childSessionID = %q, want sess-9", info.childSessionID)
	}
	if info.toolCount != 1 {
		t.Fatalf("toolCount = %d, want 1", info.toolCount)
	}
	if info.tokenCount != 1200 {
		t.Fatalf("tokenCount = %d, want 1200", info.tokenCount)
	}
}

// TestSpecialistCardLiveDrillIn proves a running card is drillable: once the
// child reports its real session id, clicking the card reads that session's
// events (not the empty fallback from an unknown tool-call id).
func TestSpecialistCardLiveDrillIn(t *testing.T) {
	m := liveSpecialistModel(t)
	const childID = "sess-live"
	if _, err := m.sessionStore.Create(sessions.CreateInput{SessionID: childID}); err != nil {
		t.Fatalf("create child session: %v", err)
	}
	if _, err := m.sessionStore.AppendEvent(childID, sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: json.RawMessage(`{"role":"user","content":"child working"}`),
	}); err != nil {
		t.Fatalf("append child event: %v", err)
	}

	for _, msg := range []tea.Msg{
		specialistStartMsg{runID: 0, name: "worker", description: "fix tests", childSessionID: "call-1"},
		specialistSessionMsg{runID: 0, toolCallID: "call-1", childSessionID: childID},
	} {
		updated, _ := m.Update(msg)
		m = updated.(model)
	}

	rows := specialistRows(m)
	if len(rows) != 1 || rows[0].specialistInfo.childSessionID != childID {
		t.Fatalf("running card should carry the real child session id, got %+v", rows)
	}

	next, cmd, errMsg := m.startSubchat(rows[0].specialistInfo.childSessionID, "worker", 0)
	if errMsg != "" {
		t.Fatalf("startSubchat error: %s", errMsg)
	}
	if cmd == nil {
		t.Fatal("startSubchat should load the child session")
	}
	loaded, _ := next.Update(execCmd(cmd))
	loadedModel := loaded.(model)
	if len(loadedModel.subchat.childRows) == 0 {
		t.Fatal("drill-in into a running child should show its events, not the empty fallback")
	}
}

// TestSpecialistCardSessionBeforeStart covers a concurrently-batched delegation
// where the child's first progress event (carrying the session id) arrives before
// OnToolCall's start: exactly one card must result.
func TestSpecialistCardSessionBeforeStart(t *testing.T) {
	m := liveSpecialistModel(t)
	for _, msg := range []tea.Msg{
		specialistSessionMsg{runID: 0, toolCallID: "call-1", childSessionID: "sess-9"},
		specialistStartMsg{runID: 0, name: "worker", description: "fix tests", childSessionID: "call-1"},
	} {
		updated, _ := m.Update(msg)
		m = updated.(model)
	}
	rows := specialistRows(m)
	if len(rows) != 1 {
		t.Fatalf("session-before-start must yield one card, got %d", len(rows))
	}
	info := rows[0].specialistInfo
	if info.name != "worker" || info.description != "fix tests" || info.childSessionID != "sess-9" {
		t.Fatalf("merged card info = %+v, want worker/fix tests/sess-9", info)
	}
}

// TestSpecialistCardIgnoresOtherRuns proves a specialist message for a non-active
// run does not leak a card into the current transcript.
func TestSpecialistCardIgnoresOtherRuns(t *testing.T) {
	m := liveSpecialistModel(t)
	m.activeRunID = 7

	updated, _ := m.Update(specialistStartMsg{runID: 8, name: "worker", description: "x", childSessionID: "call-1"})
	m = updated.(model)
	if got := len(specialistRows(m)); got != 0 {
		t.Fatalf("a message for another run must not add a card, got %d", got)
	}
}
