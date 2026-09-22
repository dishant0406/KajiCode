package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// TestSidebarActivityLines: the ACTIVITY feed is a bounded, newest-first list of
// recent completed work (stripped of the "tool result:" prefix), with a live
// "generating…" pulse when the run is active and quiet.
func TestSidebarActivityLines(t *testing.T) {
	m := model{now: time.Now}
	m.transcript = []transcriptRow{
		{kind: rowToolCall, tool: "bash", id: "c1", arg: "mkdir -p boutique-site"},
		{kind: rowToolResult, tool: "bash", id: "c1", status: tools.StatusOK, text: "tool result: bash ok Command completed with no output."},
		{kind: rowToolResult, tool: "write_file", id: "c2", status: tools.StatusOK, text: "tool result: write_file ok Created styles.css (1045 lines)."},
		{kind: rowAssistant, text: "Now the JS…"}, // ignored: not a work result
	}
	joined := plainRender(t, strings.Join(m.sidebarActivityLines(40, 10), "\n"))

	wfIdx := strings.Index(joined, "Created styles.css (1045 lines).")
	bashIdx := strings.Index(joined, "mkdir -p boutique-site")
	if wfIdx < 0 || bashIdx < 0 {
		t.Fatalf("activity should list the write_file summary and the bash command:\n%s", joined)
	}
	if wfIdx > bashIdx {
		t.Errorf("activity should be newest-first (write_file before bash):\n%s", joined)
	}
	if strings.Contains(joined, "tool result:") {
		t.Errorf("activity must strip the 'tool result:' prefix:\n%s", joined)
	}
	if got := m.sidebarActivityLines(40, 0); got != nil {
		t.Errorf("kajicode budget: want nil, got %v", got)
	}

	// Active + quiet run -> a live "generating…" pulse.
	base := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	live := model{now: func() time.Time { return base.Add(30 * time.Second) }}
	live.activeRunID = 7
	live.turnStartedAt = base
	live.lastStreamActivity = base.Add(2 * time.Second) // 28s quiet
	if got := plainRender(t, strings.Join(live.sidebarActivityLines(40, 10), "\n")); !strings.Contains(got, "generating") {
		t.Errorf("active+quiet run should show a generating pulse:\n%s", got)
	}
}

// agentSidebarTestModel builds a sidebar with two running sub-agent
// delegations, the first with a known child session so it is clickable.
func agentSidebarTestModel(t *testing.T, firstSessionID string) model {
	t.Helper()
	m := sidebarTestModel()
	now := time.Now()
	if firstSessionID == "" {
		firstSessionID = "child-1"
	}
	startSpecialist(&m.specialists, "call-worker", "worker", "build the homepage", firstSessionID, now)
	startSpecialist(&m.specialists, "call-explorer", "explorer", "build the stylesheet", "child-2", now)
	return m
}

func TestAgentRowCarriesSessionID(t *testing.T) {
	m := agentSidebarTestModel(t, "sess-1")
	agents := m.sidebarSpecialists()
	if len(agents) != 2 {
		t.Fatalf("expected 2 members, got %d", len(agents))
	}
	if agents[0].childSessionID != "sess-1" {
		t.Fatalf("agent 1 should carry its session id, got %q", agents[0].childSessionID)
	}
	if agents[1].childSessionID != "child-2" {
		t.Fatalf("agent 2 should carry its session id, got %q", agents[1].childSessionID)
	}
}

func TestSidebarAgentSelectablesMapToScreenRows(t *testing.T) {
	m := agentSidebarTestModel(t, "sess-1")
	sel := m.sidebarAgentSelectables(sidebarWidth(m.width))
	if len(sel) != 2 {
		t.Fatalf("expected 2 selectable agent rows, got %d: %+v", len(sel), sel)
	}
	// AGENTS header occupies sidebar index 0, so the two agents are at 1 and 2.
	if sel[0].lineOffset != 1 || sel[1].lineOffset != 2 {
		t.Fatalf("selectable offsets = %d,%d, want 1,2", sel[0].lineOffset, sel[1].lineOffset)
	}
	if sel[0].sessionID != "sess-1" || sel[1].sessionID != "child-2" {
		t.Fatalf("selectable session ids = %q,%q", sel[0].sessionID, sel[1].sessionID)
	}
}

func TestSidebarLineAtMouseHitsMemberRow(t *testing.T) {
	m := agentSidebarTestModel(t, "sess-1")
	// Sidebar starts at screen X = chatColumnWidth + 3 (the " │ " divider); the
	// first member row is at sidebar line 1 → screen Y 1.
	x := m.chatColumnWidth() + 3 + 2
	hit, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 1))
	if !ok || hit.sessionID != "sess-1" {
		t.Fatalf("expected to hit member row (sess-1), got ok=%v hit=%+v", ok, hit)
	}
	// A click in the chat column (left of the divider) must miss the sidebar.
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, 2, 1)); ok {
		t.Fatal("a click in the chat column should not hit the sidebar")
	}
	// The AGENTS header row (Y 0) is not a clickable member.
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 0)); ok {
		t.Fatal("the AGENTS header row should not be clickable")
	}
	// The second agent below it is also clickable (Y 2).
	if _, ok := m.sidebarLineAtMouse(testMouseClick(tea.MouseLeft, x, 2)); !ok {
		t.Fatal("the second agent row with a session id should be clickable")
	}
}

func TestSidebarMemberClickRoutesToSubchatDrillIn(t *testing.T) {
	// A real session so the click can actually drill in (not just be "handled").
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{Title: "member: build the homepage", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: map[string]any{"role": "assistant", "content": "member work output"},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	m := agentSidebarTestModel(t, session.SessionID)
	m.sessionStore = store
	x := m.chatColumnWidth() + 3 + 2
	next, cmd, handled := m.handleTranscriptSelectionMouse(testMouseClick(tea.MouseLeft, x, 1))
	if !handled {
		t.Fatal("clicking a clickable member row should be handled")
	}
	if !next.subchat.active || !next.subchat.loading || cmd == nil || next.subchat.childSessionID != session.SessionID {
		t.Fatalf("click should start loading member session %q, got active=%v loading=%v id=%q",
			session.SessionID, next.subchat.active, next.subchat.loading, next.subchat.childSessionID)
	}
	loaded, _ := next.Update(execCmd(cmd))
	next = loaded.(model)
	if next.subchat.loading || !transcriptContains(next.subchat.childRows, "member work output") {
		t.Fatalf("member session should hydrate asynchronously, got %#v", next.subchat.childRows)
	}
}

func sidebarTestModel() model {
	m := newModel(context.Background(), Options{ProviderName: "test-provider", ModelName: "test-model"})
	m.width = 100
	m.height = 30
	m.altScreen = true
	m.headerPrinted = true
	// Real conversation content so the home-screen gate doesn't suppress the
	// sidebar (it stays single-column until the transcript has non-welcome rows).
	m.transcript = append(m.transcript, transcriptRow{kind: rowToolCall, tool: "read_file", detail: "main.go"})
	// A plan gives the sidebar content so it isn't auto-hidden as empty (the panel
	// only claims a column when there are agents or an active plan). Tests that
	// exercise specific agent/plan states set their own and override this.
	m.plan.steps = []planStep{{content: "wire it up", status: "in_progress"}}
	return m
}

func TestSidebarWidthClampsAndSuppresses(t *testing.T) {
	if got := sidebarWidth(40); got != 0 {
		t.Fatalf("sidebarWidth(40) = %d, want 0 (too narrow for a second column)", got)
	}
	if got := sidebarWidth(100); got < sidebarMinWidth || got > sidebarMaxWidth {
		t.Fatalf("sidebarWidth(100) = %d, want within [%d,%d]", got, sidebarMinWidth, sidebarMaxWidth)
	}
	if got := sidebarWidth(400); got != sidebarMaxWidth {
		t.Fatalf("sidebarWidth(400) = %d, want clamped to %d", got, sidebarMaxWidth)
	}
}

func TestSidebarActiveGating(t *testing.T) {
	m := sidebarTestModel()
	if !m.sidebarActive() {
		t.Fatalf("expected sidebar active for wide alt-screen model")
	}

	// Home/welcome screen (no real conversation yet): single column.
	home := m
	home.transcript = nil
	if home.sidebarActive() {
		t.Fatalf("sidebar should be inactive on the empty home screen")
	}

	// Too narrow: single column only.
	narrow := m
	narrow.width = 50
	if narrow.sidebarActive() {
		t.Fatalf("sidebar should be inactive on a narrow terminal")
	}

	// Inline (non-alt-screen) mode keeps the legacy single-column layout.
	inline := m
	inline.altScreen = false
	if inline.sidebarActive() {
		t.Fatalf("sidebar should be inactive in inline mode")
	}

	// Subchat drill-in owns the full width.
	sub := m
	sub.subchat.active = true
	if sub.sidebarActive() {
		t.Fatalf("sidebar should be inactive during subchat drill-in")
	}
}

func TestSidebarToggleHidesAndShows(t *testing.T) {
	m := sidebarTestModel()
	if !m.sidebarActive() || !m.sidebarAvailable() {
		t.Fatal("sidebar should be active and available for the test model")
	}

	// Ctrl+B hide preference suppresses the sidebar even though it's available.
	m.sidebarHidden = true
	if m.sidebarActive() {
		t.Fatal("sidebar should be inactive when hidden by the user")
	}
	if !m.sidebarAvailable() {
		t.Fatal("sidebarAvailable must ignore the hide preference (so Ctrl+B can re-show)")
	}
	// Hidden → the chat reflows to full width.
	if got, want := m.chatColumnWidth(), chatWidth(m.width); got != want {
		t.Fatalf("hidden sidebar: chat width = %d, want full %d", got, want)
	}

	// Toggling back restores the two-column layout.
	m.sidebarHidden = false
	if !m.sidebarActive() {
		t.Fatal("sidebar should be active again after un-hiding")
	}
}

func TestChatColumnWidthLeavesRoomForSidebar(t *testing.T) {
	m := sidebarTestModel()
	chatW := m.chatColumnWidth()
	sidebarW := sidebarWidth(m.width)
	if chatW+3+sidebarW != m.width {
		t.Fatalf("chat(%d) + divider(3) + sidebar(%d) = %d, want total width %d",
			chatW, sidebarW, chatW+3+sidebarW, m.width)
	}

	// When the sidebar is inactive, chat width is the full chat width.
	narrow := m
	narrow.width = 50
	if got := narrow.chatColumnWidth(); got != chatWidth(narrow.width) {
		t.Fatalf("narrow chatColumnWidth = %d, want full chatWidth %d", got, chatWidth(narrow.width))
	}
}

func TestRenderContextSidebarDimensions(t *testing.T) {
	m := sidebarTestModel()
	width := sidebarWidth(m.width)
	const height = 20
	lines := m.renderContextSidebar(width, height)
	if len(lines) != height {
		t.Fatalf("sidebar produced %d lines, want exactly %d", len(lines), height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != width {
			t.Fatalf("sidebar line %d width = %d, want exactly %d", i, w, width)
		}
	}
	// Section headers and the token floor should be present.
	plain := stripSidebar(lines)
	if !strings.Contains(plain, "AGENTS") {
		t.Fatalf("sidebar missing AGENTS header:\n%s", plain)
	}
	if !strings.Contains(plain, "PLAN") {
		t.Fatalf("sidebar missing PLAN header:\n%s", plain)
	}
	if !strings.Contains(plain, "tokens") {
		t.Fatalf("sidebar missing token floor:\n%s", plain)
	}
}

// TestSidebarAutoHidesWhenEmpty: with no agents and no active plan the panel
// auto-hides and the chat reclaims the full width; adding a plan or an agent
// brings it back.
func TestSidebarAutoHidesWhenEmpty(t *testing.T) {
	m := sidebarTestModel() // has a plan -> sidebar active
	if !m.sidebarActive() {
		t.Fatal("expected sidebar active when the model has a plan")
	}

	// Clear the only content (the plan) -> empty -> auto-hidden.
	m.plan.steps = nil
	if m.sidebarHasContent() {
		t.Fatal("model should have no sidebar content after clearing the plan")
	}
	if m.sidebarActive() {
		t.Error("sidebar should auto-hide with no agents and no active plan")
	}
	if got, want := m.chatColumnWidth(), chatWidth(m.width); got != want {
		t.Errorf("empty sidebar: chat width = %d, want full %d", got, want)
	}

	// A spawned agent brings the panel back.
	startSpecialist(&m.specialists, "call-1", "explorer", "look around", "sess-x", time.Now())
	if !m.sidebarHasContent() || !m.sidebarActive() {
		t.Error("sidebar should return once an agent spawns")
	}
}

func TestSidebarShowsSpawnedAgents(t *testing.T) {
	m := sidebarTestModel()
	now := time.Now()
	// One running subagent with live tool activity, one completed.
	startSpecialist(&m.specialists, "call-1", "explorer", "map the codebase", "sess-1", now)
	m.specialists.setCurrentTool("call-1", "grep", "auth")
	m.specialists.incrementToolCount("call-1")
	startSpecialist(&m.specialists, "call-2", "reviewer", "review diff", "sess-2", now)
	m.specialists.complete("call-2", specialistCompleted, 0, "", now)

	width := sidebarWidth(m.width)
	plain := stripSidebar(m.sidebarAgentLines(width))
	if !strings.Contains(plain, "explorer") {
		t.Fatalf("running subagent name missing:\n%s", plain)
	}
	if !strings.Contains(plain, "reviewer") {
		t.Fatalf("completed subagent name missing:\n%s", plain)
	}
	// The running subagent surfaces its live working detail (current tool).
	if !strings.Contains(plain, "grep") {
		t.Fatalf("running subagent working detail missing:\n%s", plain)
	}
	// Header shows the total agent count.
	hdr := stripSidebar([]string{m.sidebarAgentHeader(width)})
	if !strings.Contains(hdr, "AGENTS") || !strings.Contains(hdr, "2") {
		t.Fatalf("agent header should show AGENTS 2, got: %s", hdr)
	}
}

func TestSidebarHidesNotFoundSpecialistMisroutes(t *testing.T) {
	m := sidebarTestModel()
	now := time.Now()
	// A real running agent + a failed tool-misroute (a made-up tool name called
	// as an agent → "agent not found"), which should be filtered out.
	startSpecialist(&m.specialists, "call-real", "worker", "build frontend", "sess-real", now)
	startSpecialist(&m.specialists, "call-bogus", "bogus_tool", "coordinate", "sess-bogus", now)
	m.specialists.complete("call-bogus", specialistError, 0, `agent "bogus_tool" not found`, now)

	got := m.sidebarSpecialists()
	if len(got) != 1 || got[0].name != "worker" {
		t.Fatalf("not-found misroute should be filtered; want only worker, got %+v", got)
	}
	plain := stripSidebar(m.sidebarAgentLines(sidebarWidth(m.width)))
	if strings.Contains(plain, "bogus_tool") {
		t.Fatalf("bogus misroute agent should not appear:\n%s", plain)
	}
	if !strings.Contains(plain, "worker") {
		t.Fatalf("real worker specialist should still appear:\n%s", plain)
	}
}

func TestSidebarPlanReflectsState(t *testing.T) {
	m := sidebarTestModel()
	m.plan.steps = []planStep{
		{content: "read code", status: "completed"},
		{content: "refactor auth", status: "in_progress"},
		{content: "run tests", status: "pending"},
	}
	header := plainRender(t, m.sidebarPlanHeader(40))
	if !strings.Contains(header, "PLAN") || !strings.Contains(header, "1/3") {
		t.Fatalf("plan header = %q, want PLAN with 1/3 count", header)
	}
	lines := m.sidebarPlanLines(40)
	if len(lines) != 3 {
		t.Fatalf("plan lines = %d, want 3", len(lines))
	}
	joined := stripSidebar(lines)
	if !strings.Contains(joined, "✓") || !strings.Contains(joined, "•") || !strings.Contains(joined, "○") {
		t.Fatalf("plan lines missing status glyphs:\n%s", joined)
	}
}

func TestJoinColumnsAligns(t *testing.T) {
	chat := []string{"hello", "world", "third row that is longer"}
	sidebar := []string{"A", "B"}
	const chatW, sidebarW = 12, 6
	rows := joinColumns(chat, sidebar, chatW, sidebarW)
	if len(rows) != 3 {
		t.Fatalf("joined %d rows, want max(3,2)=3", len(rows))
	}
	want := chatW + 3 + sidebarW // " │ " padded divider
	for i, row := range rows {
		if w := lipgloss.Width(row); w != want {
			t.Fatalf("row %d width = %d, want %d", i, w, want)
		}
	}
}

func TestTwoColumnTranscriptViewWidth(t *testing.T) {
	m := sidebarTestModel()
	out := m.twoColumnTranscriptView()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("two-column view = %d lines, want terminal height %d", len(lines), m.height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != m.width {
			t.Fatalf("two-column row %d width = %d, want full width %d", i, w, m.width)
		}
	}
}

// stripSidebar joins sidebar lines and strips ANSI for content assertions.
func stripSidebar(lines []string) string {
	return ansiPattern.ReplaceAllString(strings.Join(lines, "\n"), "")
}
