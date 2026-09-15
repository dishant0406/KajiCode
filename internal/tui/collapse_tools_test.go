package tui

import (
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestCollapseRepeatedStatusCardDropsDuplicate(t *testing.T) {
	detail := "Swarm status (team default): 4 task(s) — 4 running"
	rows := []transcriptRow{
		{kind: rowAssistant, text: "let me check"},
		{kind: rowToolCall, tool: "swarm_status"},
		{kind: rowToolResult, tool: "swarm_status", detail: detail},
		{kind: rowToolCall, tool: "swarm_status"}, // the new check's call row
	}
	out := collapseRepeatedStatusCard(rows, transcriptRow{kind: rowToolResult, tool: "swarm_status", detail: detail})
	if len(out) != 2 {
		t.Fatalf("duplicate status pair should collapse to 2 rows, got %d: %+v", len(out), out)
	}
	if out[0].kind != rowAssistant || out[1].kind != rowToolCall {
		t.Fatalf("collapse kept the wrong rows: %+v", out)
	}
}

func TestCollapseRepeatedStatusCardKeepsChangedState(t *testing.T) {
	rows := []transcriptRow{
		{kind: rowToolCall, tool: "swarm_status"},
		{kind: rowToolResult, tool: "swarm_status", detail: "4 running"},
		{kind: rowToolCall, tool: "swarm_status"},
	}
	out := collapseRepeatedStatusCard(rows, transcriptRow{kind: rowToolResult, tool: "swarm_status", detail: "2 running, 2 done"})
	if len(out) != 3 {
		t.Fatalf("a changed status must not collapse, got %d rows", len(out))
	}
}

func TestCollapseRepeatedStatusCardIgnoresInterveningContent(t *testing.T) {
	rows := []transcriptRow{
		{kind: rowToolCall, tool: "swarm_status"},
		{kind: rowToolResult, tool: "swarm_status", detail: "4 running"},
		{kind: rowReasoning, text: "thinking"},
		{kind: rowToolCall, tool: "swarm_status"},
	}
	out := collapseRepeatedStatusCard(rows, transcriptRow{kind: rowToolResult, tool: "swarm_status", detail: "4 running"})
	if len(out) != 4 {
		t.Fatalf("intervening content must prevent collapse, got %d rows", len(out))
	}
}

func TestToolResultCollapsesLongOutputByDefault(t *testing.T) {
	m := transcriptViewTestModel()
	long := numberedLines(cardBodyMaxLines + 10)
	rc := buildRowContext(nil)

	row := transcriptRow{kind: rowToolResult, id: "t1", tool: "mcp_exa_web_search_exa", status: tools.StatusOK, detail: long}
	collapsed := plainRender(t, m.renderRow(row, m.width, rc))
	if strings.Contains(collapsed, "line-005") {
		t.Errorf("collapsed card must hide the body, got:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "click to expand") {
		t.Errorf("collapsed card must show the expand hint, got:\n%s", collapsed)
	}

	row.expanded = true
	expanded := plainRender(t, m.renderRow(row, m.width, rc))
	if !strings.Contains(expanded, "line-005") {
		t.Errorf("expanded card must show the body, got:\n%s", expanded)
	}
}

func TestToolResultShortOutputStaysInline(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t2", tool: "mcp_exa_web_search_exa", status: tools.StatusOK, detail: numberedLines(3)}
	out := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
	if strings.Contains(out, "click to expand") {
		t.Errorf("short output must not collapse, got:\n%s", out)
	}
	if !strings.Contains(out, "line-003") {
		t.Errorf("short output must render inline, got:\n%s", out)
	}
}

func TestDiffToolOutputNeverCollapses(t *testing.T) {
	m := transcriptViewTestModel()
	long := numberedLines(cardBodyMaxLines + 10)
	for _, tool := range []string{"edit_file", "apply_patch", "write_file"} {
		row := transcriptRow{kind: rowToolResult, id: "d", tool: tool, status: tools.StatusOK, detail: long}
		out := plainRender(t, m.renderRow(row, m.width, buildRowContext(nil)))
		if strings.Contains(out, "click to expand") {
			t.Errorf("%s output must stay reviewable, not collapse:\n%s", tool, out)
		}
	}
}

func TestToggleTranscriptRowTogglesToolResult(t *testing.T) {
	m := transcriptViewTestModel()
	m.transcript = []transcriptRow{{kind: rowToolResult, id: "t", tool: "custom_tool"}}
	if m.transcript[0].expanded {
		t.Fatal("tool result must default to collapsed")
	}
	m = m.toggleTranscriptRow(0)
	if !m.transcript[0].expanded {
		t.Fatal("toggle should expand the tool result")
	}
	m = m.toggleTranscriptRow(0)
	if m.transcript[0].expanded {
		t.Fatal("toggle should collapse the tool result again")
	}
}

func TestToolResultRowExposesClickToggle(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "custom_tool", status: tools.StatusOK, detail: numberedLines(cardBodyMaxLines + 5)}
	_, selectable := m.renderSelectableToolResultRow(0, row, m.width, buildRowContext(nil), 0)
	if len(selectable) < 1 || !selectable[0].toggle {
		t.Fatalf("tool result head must be a clickable toggle line, got %#v", selectable)
	}
	if len(selectable) < 2 || selectable[1].text == "" {
		t.Fatalf("tool result visible body/footer must stay selectable, got %#v", selectable)
	}
}

// TestCollapsedToolFooterIsClickToggle guards the regression where the
// "click to expand" footer read as an affordance but was not a real target.
func TestCollapsedToolFooterIsClickToggle(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "custom_tool", status: tools.StatusOK, detail: numberedLines(cardBodyMaxLines + 5)}
	_, selectable := m.renderSelectableToolResultRow(0, row, m.width, buildRowContext(nil), 0)

	footerToggle := false
	for _, line := range selectable {
		if line.toggle && strings.Contains(line.text, "click to expand") {
			footerToggle = true
		}
	}
	if !footerToggle {
		t.Fatalf("collapsed 'click to expand' footer must be a toggle target, got %#v", selectable)
	}
}

// TestShortCardBodyWithExpandPhraseIsNotToggle guards against the footer target
// being matched by substring: a short card renders its full body inline (no
// collapse footer), so a body line that merely contains the words
// "click to expand" must stay plain selectable text, not become a dead toggle.
func TestShortCardBodyWithExpandPhraseIsNotToggle(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "custom_tool", status: tools.StatusOK,
		detail: "line one\nclick to expand me\nline three"}
	_, selectable := m.renderSelectableToolResultRow(0, row, m.width, buildRowContext(nil), 0)

	for _, line := range selectable {
		if strings.Contains(line.text, "click to expand") && line.toggle && line.bodyY != 0 {
			t.Fatalf("a body line containing 'click to expand' must not become a toggle, got %#v", line)
		}
	}
}

// TestExpandedToolResultFooterIsClickToggle covers the other half of the
// affordance: once expanded, the terminal "▾ collapse" footer collapses the card.
func TestExpandedToolResultFooterIsClickToggle(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "custom_tool", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 5), expanded: true}
	_, selectable := m.renderSelectableToolResultRow(0, row, m.width, buildRowContext(nil), 0)

	footerToggle := false
	for _, line := range selectable {
		if line.toggle && line.text == expandCardCollapseFooter {
			footerToggle = true
		}
	}
	if !footerToggle {
		t.Fatalf("expanded '▾ collapse' footer must be a toggle target, got %#v", selectable)
	}
}

// TestDetailedToolResultHasNoCollapseToggle: the detailed transcript renders
// bodies uncapped (bodyCap 0), so there is no collapse footer and only the head
// stays clickable — a body/footer line must never become a dead toggle target.
func TestDetailedToolResultHasNoCollapseToggle(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "mcp_exa_web_search_exa", status: tools.StatusOK,
		detail: numberedLines(cardBodyMaxLines + 10)}
	_, selectable := m.renderTranscriptDetailedRow(0, row, m.width, buildRowContext(nil), 0)

	toggles := 0
	for _, line := range selectable {
		if line.toggle {
			toggles++
		}
	}
	if toggles != 1 || !selectable[0].toggle {
		t.Fatalf("detailed mode must expose only the head toggle, got %d toggles: %#v", toggles, selectable)
	}
}

// TestToggleTranscriptRowInvalidatesRenderFingerprint is the root-cause
// regression: renderFingerprint is cached at append time and every render,
// height, and scroll-metrics cache key derives from it, so a toggle that did
// not recompute it produced no visible expansion.
func TestToggleTranscriptRowInvalidatesRenderFingerprint(t *testing.T) {
	m := transcriptViewTestModel()
	row := transcriptRow{kind: rowToolResult, id: "t", tool: "mcp_exa_web_search_exa", status: tools.StatusOK, detail: numberedLines(cardBodyMaxLines + 20)}
	m.transcript = appendTranscriptRow(m.transcript, row)
	idx := len(m.transcript) - 1

	// Warm the caches the way the live view would before the user clicks.
	_, set, _ := m.transcriptHitTestItemSet()
	before := m.measureTranscriptBodyItemSet(set).totalLines()

	m = m.toggleTranscriptRow(idx)
	row = m.transcript[idx]
	if !row.expanded {
		t.Fatal("toggle should expand the row")
	}
	// A fresh fingerprint must be materialized so downstream caches miss.
	if row.renderFingerprint != transcriptRowFingerprint(row) {
		t.Fatalf("fingerprint not recomputed on toggle: cached=%q fresh=%q",
			row.renderFingerprint, transcriptRowFingerprint(row))
	}

	_, set2, _ := m.transcriptHitTestItemSet()
	after := m.measureTranscriptBodyItemSet(set2).totalLines()
	if after <= before {
		t.Fatalf("expanded body must grow: collapsed=%d expanded=%d", before, after)
	}

	// And the rendered card itself must actually grow.
	width := m.chatColumnWidth()
	rc := buildRowContext(m.transcript)
	collapsedRow := row
	collapsedRow.expanded = false
	collapsedRow.renderFingerprint = transcriptRowFingerprint(collapsedRow)
	collapsedLines := len(viewLines(plainRender(t, m.renderRow(collapsedRow, width, rc))))
	expandedLines := len(viewLines(plainRender(t, m.renderRow(row, width, rc))))
	if expandedLines <= collapsedLines {
		t.Fatalf("expanded card must be taller: collapsed=%d expanded=%d", collapsedLines, expandedLines)
	}
}

// TestToggleTranscriptRowRoundTrips keeps the collapse → expand → collapse
// cycle stable so a card can be toggled repeatedly with warm caches.
func TestToggleTranscriptRowRoundTrips(t *testing.T) {
	m := transcriptViewTestModel()
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowReasoning, id: "r", runID: 1, text: "line one\nline two\nline three"})
	idx := len(m.transcript) - 1

	m = m.toggleTranscriptRow(idx)
	if !m.transcript[idx].expanded {
		t.Fatal("first toggle should expand")
	}
	expandedFingerprint := m.transcript[idx].renderFingerprint
	if expandedFingerprint != transcriptRowFingerprint(m.transcript[idx]) {
		t.Fatal("fingerprint must be recomputed when expanding")
	}
	m = m.toggleTranscriptRow(idx)
	if m.transcript[idx].expanded {
		t.Fatal("second toggle should collapse")
	}
	if m.transcript[idx].renderFingerprint == expandedFingerprint {
		t.Fatal("fingerprint must change again when collapsing")
	}
	if m.transcript[idx].renderFingerprint != transcriptRowFingerprint(m.transcript[idx]) {
		t.Fatal("fingerprint must track the collapsed state after a round trip")
	}
}

// TestToggleTranscriptRowClearsHover ensures a stale hover target cannot
// highlight the wrong row after the geometry changes under the cursor.
func TestToggleTranscriptRowClearsHover(t *testing.T) {
	m := transcriptViewTestModel()
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowReasoning, id: "r", runID: 1, text: "line one\nline two"})
	idx := len(m.transcript) - 1
	m.hover = hoverTarget{kind: hoverTranscript, bodyY: 0}

	m = m.toggleTranscriptRow(idx)
	if m.hover.kind == hoverTranscript {
		t.Fatalf("toggle should clear the transcript hover, got %#v", m.hover)
	}
}
