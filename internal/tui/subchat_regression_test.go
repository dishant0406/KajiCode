package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// The subchat drill-in (viewing a subagent/swarm child session) swaps the on-screen
// transcript to m.subchat.childRows, but mouse hit-testing and the footer's pinned
// plan panel were never made subchat-aware — they kept operating on/showing the
// PARENT run's state while the child session was on screen. These tests guard the
// fix: mouse selection must resolve against the child rows, the plan panel must not
// show in that view, and a selection in progress must extend across a wheel-scroll
// instead of freezing at whatever was visible when the drag started.

func TestFooterHidesPlanPanelDuringSubchat(t *testing.T) {
	m := runningPlanModel(t, 3)
	m.altScreen = true
	m.height = 30
	width := m.chatColumnWidth()

	withoutSubchat := m.footerView(width)
	if !strings.Contains(withoutSubchat, "Step number 1 here") {
		t.Fatalf("sanity check failed: pinned plan panel should render outside subchat, got:\n%s", withoutSubchat)
	}

	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	withSubchat := m.footerView(width)
	if strings.Contains(withSubchat, "Step number 1 here") {
		t.Fatalf("plan panel must NOT show while viewing a subchat child session, got:\n%s", withSubchat)
	}
}

func TestTranscriptSelectionInSubchatUsesChildSessionRows(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	// Parent transcript has DIFFERENT text than the child session: if selection
	// hit-tests against the wrong (parent) rows, this proves it by either matching
	// nothing or matching the wrong text.
	m.transcript = appendRow(m.transcript, rowUser, "parent transcript text")
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	m.subchat.childRows = appendRow(nil, rowUser, "hello world")

	textY := topmostVisibleTranscriptMouseY(t, m)
	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 3, textY))
	m = updated.(model)
	updated, _ = m.Update(testMouseMotion(tea.MouseLeft, 8, textY))
	m = updated.(model)

	if got := m.selectedTranscriptText(); got != "hello" {
		t.Fatalf("selectedTranscriptText() in subchat = %q, want hello (from the CHILD session's rows)", got)
	}
}

func TestLongSubchatRowsUseStableHeightCache(t *testing.T) {
	m := mouseTestModel()
	rows := make([]transcriptRow, 0, defaultTranscriptBodyHeightCacheMaxEntries+64)
	for index := range defaultTranscriptBodyHeightCacheMaxEntries + 64 {
		rows = appendRow(rows, rowUser, stringKey(index))
	}
	items := m.transcriptBodyItemsFromRows(rows, m.chatColumnWidth())
	stableRows := 0
	for _, item := range items {
		if item.kind == transcriptBodyItemRow && item.heightCacheStable && item.heightCacheKey != "" {
			stableRows++
		}
	}
	if stableRows != len(rows) {
		t.Fatalf("stable subchat rows = %d, want %d", stableRows, len(rows))
	}

	first := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	second := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	if first.totalLines() != second.totalLines() || first.totalLines() == 0 {
		t.Fatalf("subchat total lines = %d/%d, want matching positive layouts", first.totalLines(), second.totalLines())
	}
}

func TestLongSubchatScrollUsesChildViewport(t *testing.T) {
	m := mouseTestModel()
	m.transcript = appendRow(m.transcript, rowUser, "short parent")
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	for index := range defaultTranscriptBodyHeightCacheMaxEntries + 64 {
		m.subchat.childRows = appendRow(m.subchat.childRows, rowUser, stringKey(index))
	}

	viewport, ok := m.chatTranscriptViewport()
	if !ok {
		t.Fatal("expected an alt-screen subchat viewport")
	}
	if viewport.maxOffset() <= 0 {
		t.Fatalf("subchat max offset = %d, want child transcript to overflow", viewport.maxOffset())
	}
	if viewport.totalLines <= len(m.transcript)*2 {
		t.Fatalf("viewport total lines = %d, looks like short parent transcript was measured", viewport.totalLines)
	}

	scrolled := m.scrollChat(m.chatPageScrollLines())
	if scrolled.chatScrollOffset <= 0 {
		t.Fatalf("subchat scroll offset = %d, want child transcript to scroll", scrolled.chatScrollOffset)
	}
	_, _ = scrolled.chatTranscriptViewport()
	scrolled.transcriptBodyHeights.mu.Lock()
	defer scrolled.transcriptBodyHeights.mu.Unlock()
	if len(scrolled.transcriptBodyHeights.items) <= defaultTranscriptBodyHeightCacheMaxEntries {
		t.Fatalf("cached child heights = %d, want full long child layout retained", len(scrolled.transcriptBodyHeights.items))
	}
}

func TestTranscriptSelectionExtendsAcrossWheelScroll(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	// Enough rows that the transcript overflows the viewport, so a wheel-scroll is
	// a real (non-clamped) scroll. chatScrollOffset=0 anchors to the BOTTOM (newest
	// content, see transcriptViewport.window: start = totalLines-height-offset), so
	// with overflow content the topmost VISIBLE line is not the transcript's first
	// line — topmostVisibleTranscriptMouseY (window-aware) finds it correctly.
	for i := 0; i < 80; i++ {
		m.transcript = appendRow(m.transcript, rowUser, "line content")
	}
	textY := topmostVisibleTranscriptMouseY(t, m)

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 0, textY))
	m = updated.(model)
	if !m.transcriptSelection.active {
		t.Fatal("selection should be active after a left click on transcript text")
	}
	cursorBefore := m.transcriptSelection.cursor.bodyY
	scrollBefore := m.chatScrollOffset

	// Wheel UP reveals OLDER content above (offset increases -> window.start
	// decreases): re-evaluating the SAME on-screen Y afterward must land on an
	// EARLIER (smaller bodyY) line now that the viewport has shifted, extending the
	// selection upward instead of leaving the cursor pinned to the pre-scroll line.
	updated, _ = m.Update(testMouseWheel(tea.MouseWheelUp, 0, textY))
	m = updated.(model)

	if m.chatScrollOffset == scrollBefore {
		t.Fatal("sanity check failed: wheel-up should have scrolled (80 rows overflow a 30-row terminal)")
	}
	if !m.transcriptSelection.active {
		t.Fatal("selection must survive a wheel-scroll, not be cleared")
	}
	if m.transcriptSelection.cursor.bodyY >= cursorBefore {
		t.Fatalf("selection cursor bodyY = %d, want < %d (it must extend to follow the scroll, not freeze)", m.transcriptSelection.cursor.bodyY, cursorBefore)
	}
}

// TestSubchatToolToggleTargetsChildRows is a regression guard: a click on a
// collapsible tool card while a subchat is open must toggle the CHILD row the
// hit-test resolved, not whichever parent row happens to share that index.
func TestSubchatToolToggleTargetsChildRows(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolResult, id: "parent", tool: "parent_tool", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 5)})
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	m.subchat.childRows = appendTranscriptRow(m.subchat.childRows, transcriptRow{
		kind: rowToolResult, id: "child", tool: "child_tool", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 5)})

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 0, firstTranscriptToggleMouseY(t, m)))
	next := updated.(model)
	if next.transcript[0].expanded {
		t.Fatal("clicking the child card must not toggle the parent transcript row")
	}
	if !next.subchat.childRows[0].expanded {
		t.Fatal("clicking the child card must expand the child row")
	}
}

// firstTranscriptToggleMouseY returns the on-screen Y of the topmost visible
// clickable toggle line, resolved against the same subchat-aware hit-test source
// the mouse handlers use.
func firstTranscriptToggleMouseY(t *testing.T, m model) int {
	t.Helper()
	_, items, width := m.transcriptHitTestSource()
	header, _ := m.transcriptViewportHeaderWidth()
	footer := m.footerView(width)
	if m.transcriptDetailed {
		footer = m.detailedTranscriptFooter(width)
	}
	frame := m.scrollableTranscriptFrame(header, footer)
	metrics := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	window := transcriptViewportForLayout(metrics, frame, m.chatScrollOffset).window()
	layout := layoutVisibleTranscriptBodyItems(items, metrics, window)
	for _, line := range layout.selectable {
		if line.toggle && line.bodyY >= window.start {
			return frame.bodyRect.y + (line.bodyY - window.start)
		}
	}
	t.Fatalf("no visible toggle line found: %#v", layout.selectable)
	return 0
}

// TestSubchatReasoningToggleTargetsChildRows mirrors the tool-card subchat test
// for a reasoning ("thinking") row, whose header is the toggle target.
func TestSubchatReasoningToggleTargetsChildRows(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowReasoning, id: "pr", text: "parent thought"})
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	m.subchat.childRows = appendTranscriptRow(m.subchat.childRows, transcriptRow{kind: rowReasoning, id: "cr", text: "child thought"})

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 0, firstTranscriptToggleMouseY(t, m)))
	next := updated.(model)
	if next.transcript[0].expanded {
		t.Fatal("clicking the child reasoning row must not toggle the parent row")
	}
	if !next.subchat.childRows[0].expanded {
		t.Fatal("clicking the child reasoning row must expand it")
	}
}

// TestDetailedSubchatToggleTargetsParentRows guards the precedence between the
// detailed view and a subchat: the detailed view renders the PARENT transcript
// even while a subchat is open, so a toggle click must hit the parent row set,
// not the child rows.
func TestDetailedSubchatToggleTargetsParentRows(t *testing.T) {
	m := mouseTestModel()
	m.mouseCapture = true
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolResult, id: "parent", tool: "parent_tool", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 5)})
	m.subchat.active = true
	m.subchat.childSessionID = "child-1"
	m.subchat.childRows = appendTranscriptRow(m.subchat.childRows, transcriptRow{
		kind: rowToolResult, id: "child", tool: "child_tool", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 5)})
	m.transcriptDetailed = true

	updated, _ := m.Update(testMouseClick(tea.MouseLeft, 0, firstTranscriptToggleMouseY(t, m)))
	next := updated.(model)
	if !rowExpandedByID(next.transcript, "parent") {
		t.Fatal("detailed view must toggle the parent row it renders")
	}
	if rowExpandedByID(next.subchat.childRows, "child") {
		t.Fatal("detailed view must not toggle the hidden child rows")
	}
}

// TestSubchatRehydrationCoalescesLegacyPerDeltaMessages guards already-recorded
// child sessions: older builds persisted one assistant EventMessage per streamed
// delta, which replayed as one word per line on drill-in. Consecutive assistant
// messages must rehydrate into a single row.
func TestSubchatRehydrationCoalescesLegacyPerDeltaMessages(t *testing.T) {
	ev := func(payload string) sessions.Event {
		return sessions.Event{Type: sessions.EventMessage, Payload: json.RawMessage(payload)}
	}
	rows := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(`{"role":"assistant","content":"The "}`),
		ev(`{"role":"assistant","content":"parser "}`),
		ev(`{"role":"assistant","content":"lives in parse.go"}`),
	})
	if len(rows) != 1 {
		t.Fatalf("per-delta assistant messages must coalesce to one row, got %d: %#v", len(rows), rows)
	}
	if rows[0].text != "The parser lives in parse.go" {
		t.Fatalf("coalesced text = %q", rows[0].text)
	}
}

// TestSubchatRehydrationKeepsDistinctAssistantMessages guards the other side of
// the coalescing rule: assistant messages separated by a tool event must NOT be
// merged, and a row-less event (empty-content message) must not bridge two
// distinct segments.
func TestSubchatRehydrationKeepsDistinctAssistantMessages(t *testing.T) {
	ev := func(payload string) sessions.Event {
		return sessions.Event{Type: sessions.EventMessage, Payload: json.RawMessage(payload)}
	}
	tool := sessions.Event{
		Type:    sessions.EventToolCall,
		Payload: json.RawMessage(`{"name":"read_file","id":"c1","arguments":"{}"}`),
	}

	rows := transcriptRowsFromSessionEvents([]sessions.Event{
		ev(`{"role":"assistant","content":"before the tool"}`),
		tool,
		ev(`{"role":"assistant","content":"after the tool"}`),
	})
	if len(rows) != 3 {
		t.Fatalf("assistant messages separated by a tool must stay distinct, got %d rows: %#v", len(rows), rows)
	}

	// A row-less event between two assistant messages must also keep them apart.
	rows = transcriptRowsFromSessionEvents([]sessions.Event{
		ev(`{"role":"assistant","content":"first"}`),
		ev(`{"role":"assistant","content":""}`),
		ev(`{"role":"assistant","content":"second"}`),
	})
	if len(rows) != 2 {
		t.Fatalf("a row-less event must not bridge two assistant segments, got %d rows: %#v", len(rows), rows)
	}
}

// rowExpandedByID reports whether the transcript row with the given id is expanded.
func rowExpandedByID(rows []transcriptRow, id string) bool {
	for _, row := range rows {
		if row.id == id {
			return row.expanded
		}
	}
	return false
}

// topmostVisibleTranscriptMouseY returns the on-screen Y of the topmost currently
// VISIBLE selectable text line — window-aware (unlike firstTranscriptTextMouseY,
// which walks the full unwindowed layout and only lands on-screen when everything
// fits in one viewport). It resolves against transcriptHitTestSource, the same
// subchat-aware source transcriptLineAtMouse uses, so it works for both the parent
// transcript and a subchat child session.
func topmostVisibleTranscriptMouseY(t *testing.T, m model) int {
	t.Helper()
	header, items, width := m.transcriptHitTestSource()
	frame := m.scrollableTranscriptFrame(header, m.footerView(width))
	metrics := measureTranscriptBodyItems(items, m.transcriptBodyHeights)
	window := transcriptViewportForLayout(metrics, frame, m.chatScrollOffset).window()
	layout := layoutVisibleTranscriptBodyItems(items, metrics, window)
	for _, line := range layout.selectable {
		if line.text != "" && !line.toggle {
			return frame.bodyRect.y + (line.bodyY - window.start)
		}
	}
	t.Fatalf("no selectable visible transcript text line found: %#v", layout.selectable)
	return 0
}
