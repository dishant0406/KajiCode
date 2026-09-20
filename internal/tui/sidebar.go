// sidebar.go renders the right-hand context sidebar for the two-column chat
// layout (alt-screen managed mode only). The sidebar surfaces three sections —
// the spawned AGENTS and their live working detail, the live PLAN (the same data
// the pinned plan panel reads), and a token/context readout at the bottom — so
// the chat column stays focused on the conversation. It is a set of pure
// helpers: the layout in
// transcriptView renders the chat at a reduced width via the existing scroll
// engine, builds a sidebar block of the same height here, and joins the two
// columns row-by-row through joinColumns.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// sidebar geometry. The sidebar takes ~30% of the width, clamped so it never
// crowds the chat on a narrow terminal nor sprawls on a wide one. A 1-cell
// divider sits between the two columns.
const (
	sidebarMinWidth  = 26
	sidebarMaxWidth  = 40
	sidebarMinColumn = 60 // below this total width the sidebar is suppressed
)

// sidebarWidth returns the sidebar column width for a given total width, or 0
// when the terminal is too narrow to justify a second column (the caller then
// renders the single-column chat at full width).
func sidebarWidth(total int) int {
	if total < sidebarMinColumn {
		return 0
	}
	return clamp(total*30/100, sidebarMinWidth, sidebarMaxWidth)
}

// sidebarActive reports whether the two-column layout should render right now:
// the sidebar is available AND the user hasn't collapsed it with Ctrl+B.
func (m model) sidebarActive() bool {
	return !m.sidebarHidden && m.sidebarAvailable()
}

// sidebarToggleAllowed reports whether the toggle-sidebar keybinding should
// respond. Unlike sidebarAvailable it OMITS the content check
// (sidebarHasContent) so the user can toggle their show/hide preference even
// when the sidebar auto-hid due to having nothing to show. The content gate
// is still applied at render time (sidebarActive chains sidebarAvailable), so
// toggling on when there's no content just records the preference for when
// content arrives — the sidebar stays hidden until then.
func (m model) sidebarToggleAllowed() bool {
	if !m.altScreen || m.height <= 0 || m.subchat.active {
		return false
	}
	if sidebarWidth(m.width) <= 0 {
		return false
	}
	if widthTier(m.width) < tierMedium {
		return false
	}
	if m.setup.visible || m.providerWizard != nil || m.mcpAddWizard != nil ||
		m.mcpManager != nil || m.picker != nil || m.suggestionsActive() {
		return false
	}
	// Home/welcome screen: stay single-column until there's real conversation.
	if m.transcriptEmpty() {
		return false
	}
	return true
}

// sidebarAvailable reports whether the two-column layout CAN render: only in
// alt-screen managed mode, with a measured height, on a wide-enough terminal,
// outside subchat/overlays, with real conversation. It ignores the user's Ctrl+B
// hide preference (sidebarHidden) so the toggle handler can tell whether Ctrl+B
// would have any visible effect. The subchat drill-in keeps its own single-column
// view, so the sidebar is suppressed there.
func (m model) sidebarAvailable() bool {
	if !m.altScreen || m.height <= 0 || m.subchat.active || m.transcriptDetailed {
		return false
	}
	if sidebarWidth(m.width) <= 0 {
		return false
	}
	// Only split once the chat column survives it: require the medium tier (>=80
	// cols). Between 60-79 the sidebar would starve the chat to ~30 cells, so the
	// layout commits to two healthy columns or stays cleanly single-column.
	if widthTier(m.width) < tierMedium {
		return false
	}
	// Full-screen overlays (setup, wizards, pickers) take over the chat column and
	// render at full width; suppress the second column while any is active so their
	// geometry and mouse hit-testing stay full-width. Autocomplete is intentionally
	// excluded: keeping the sidebar width stable avoids re-rendering long history
	// at a different chat width on the first "/" keypress.
	if m.setup.visible || m.helpOverlay || m.leaderHelpOverlay || m.providerWizard != nil || m.mcpAddWizard != nil ||
		m.mcpManager != nil || m.promptEditor != nil || m.picker != nil {
		return false
	}
	// Home/welcome screen: stay single-column until there's real conversation, so
	// the empty home screen isn't split by an (empty) sidebar.
	if m.transcriptEmpty() {
		return false
	}
	// Auto-hide when the panel has nothing to show (no sub-agents and no active
	// plan): a fixed-width column of mostly empty space is wasted, so reclaim it
	// for the full-width chat. The panel returns the moment an agent spawns or a
	// plan starts. (Ctrl+B still force-hides it when there IS content.)
	if !m.sidebarHasContent() {
		return false
	}
	return true
}

// sidebarHasContent reports whether the context sidebar has anything worth a
// column: at least one agent (a sub-agent delegation) or a non-empty plan. Used
// to auto-hide the panel — and reclaim its width for the chat — during plain idle
// stretches with neither.
func (m model) sidebarHasContent() bool {
	if len(m.sidebarSpecialists()) > 0 {
		return true
	}
	if len(m.touchedFiles()) > 0 || m.liveEditingPath() != "" {
		// A live in-flight write counts before its result row exists, so the
		// FILES pulse for the session's first mutation isn't hidden.
		return true
	}
	return !m.plan.isEmpty()
}

// chatColumnWidth is the chat's render width: the full chat width normally, and
// the reduced left-column width when the two-column layout is active (total
// minus the sidebar and the 1-cell divider). All frame/geometry callers route
// through this so the rendered chat, the scroll engine, and mouse hit-testing
// agree on where the chat column ends.
func (m model) chatColumnWidth() int {
	if sw := m.sidebarWidthForLayout(); sw > 0 {
		// Reserve 3 cells for the padded " │ " divider (a cell of air on each side
		// of the rule) — see joinColumns.
		return chatWidth(m.width - sw - 3)
	}
	return chatWidth(m.width)
}

// transcriptGutter is the left indent applied to transcript body rows. Keep this
// at zero so content starts at the chat edge and tool/code blocks can use the
// full available width.
func transcriptGutter(columnWidth int) int {
	return 0
}

// transcriptContentWidth is the wrap width for transcript body rows.
func transcriptContentWidth(columnWidth int) int {
	cw := columnWidth - transcriptGutter(columnWidth)
	if cw < 24 {
		return columnWidth
	}
	return cw
}

// sidebarWidthForLayout returns the active sidebar column width, or 0 when the
// two-column layout is not active.
func (m model) sidebarWidthForLayout() int {
	if !m.sidebarActive() {
		return 0
	}
	return sidebarWidth(m.width)
}

// sidebarSpecialists returns the sub-agent delegations worth surfacing in the
// AGENTS panel, EXCLUDING failed tool-misroutes: when a model calls a tool name
// as if it were an agent, the lookup fails with "agent <name> not found". That
// was never a real sub-agent, so it would otherwise pile up bogus "×" rows. Real
// agents (e.g. "worker") and genuine run failures still show.
func (m model) sidebarSpecialists() []specialistInfo {
	all := m.specialists.all()
	out := all[:0:0]
	for _, a := range all {
		if a.status == specialistError && strings.Contains(strings.ToLower(a.errorMsg), "not found") {
			continue
		}
		// Linger a finished specialist for sidebarAgentLinger (a fading ✓), then
		// drop it — a smooth exit rather than an abrupt pop.
		if a.status != specialistRunning && !a.completedAt.IsZero() &&
			m.now().Sub(a.completedAt) >= sidebarAgentLinger {
			continue
		}
		out = append(out, a)
	}
	return out
}

// sidebarHasAgents reports whether the two-column sidebar is active AND has at
// least one agent line to animate. The spinner tick keeps firing while this holds
// so a running agent's spinner stays alive even when no run is in flight; gating
// it on the sidebar+agent presence means a plain idle session schedules no timer.
func (m model) sidebarHasAgents() bool {
	if !m.sidebarActive() {
		return false
	}
	return len(m.sidebarSpecialists()) > 0
}

// sidebarAgentHeader renders the AGENTS section header with the total count of
// active sub-agent delegations.
func (m model) sidebarAgentHeader(width int) string {
	n := len(m.sidebarSpecialists())
	if n == 0 {
		return sidebarHeader("AGENTS", width)
	}
	return sidebarHeaderWithCount("AGENTS", fmt.Sprintf("%d", n), kajicodeTheme.muted, width)
}

// sidebarAgentLinger is how long a finished agent stays in the AGENTS panel with
// a fading ✓ before it's removed, so the exit reads as "done" rather than an
// abrupt pop.
const sidebarAgentLinger = 1500 * time.Millisecond

// sidebarAgentHit marks a rendered agent line (by its index within the agent
// lines block) that is clickable, carrying the member session to drill into.
type sidebarAgentHit struct {
	lineOffset int
	sessionID  string
	title      string
}

// sidebarAgentLines renders one line per active agent. Sub-agent delegations show
// a live status glyph (• running, ✓ done, ✗ error) plus a "↳ <tool>" working line.
// Returns nil when there are none (the caller shows a placeholder).
func (m model) sidebarAgentLines(width int) []string {
	lines, _ := m.sidebarAgentRows(width)
	return lines
}

// sidebarAgentRows renders the agent lines and, alongside, records which lines
// are clickable (those whose child session is known), so a click in the sidebar
// can drill into the child's subchat. lineOffset indexes the returned lines slice.
func (m model) sidebarAgentRows(width int) ([]string, []sidebarAgentHit) {
	specialists := m.sidebarSpecialists()
	if len(specialists) == 0 {
		return nil, nil
	}
	room := maxInt(4, width-3)
	var lines []string
	var hits []sidebarAgentHit
	for _, a := range specialists {
		// A sub-agent with a known child session is clickable: record the hit at
		// the index this line will occupy before appending it.
		if a.childSessionID != "" {
			hits = append(hits, sidebarAgentHit{lineOffset: len(lines), sessionID: a.childSessionID, title: a.name})
		}
		var icon string
		switch a.status {
		case specialistRunning:
			// A working specialist spins (same glyph its transcript card uses) so
			// the sidebar reads "this one is busy" at a glance; the tick is kept
			// alive by sidebarHasAgents. Static "•" stays for idle/parked members.
			icon = kajicodeTheme.accent.Render(m.spinnerGlyph())
		case specialistError:
			icon = kajicodeTheme.red.Render("✗")
		default: // completed
			icon = kajicodeTheme.green.Render("✓")
		}
		name := strings.TrimSpace(a.name)
		if name == "" {
			name = "agent"
		}
		nameStyle := kajicodeTheme.ink
		// As a finished specialist nears the end of its linger, dim the whole row
		// toward faint so its removal reads as a fade-out rather than a pop.
		if a.status != specialistRunning && m.agentExitFading(a.completedAt) {
			glyph := "✓"
			if a.status == specialistError {
				glyph = "✗"
			}
			icon = kajicodeTheme.faint.Render(glyph)
			nameStyle = kajicodeTheme.faint
		}
		lines = append(lines, " "+icon+" "+nameStyle.Render(truncateStep(name, room)))
		if a.status != specialistRunning {
			continue
		}
		// Live working detail for a running subagent: current tool + arg hint,
		// falling back to the running tool count.
		detail := strings.TrimSpace(a.currentTool)
		if d := strings.TrimSpace(a.currentDetail); d != "" {
			if detail != "" {
				detail += " " + d
			} else {
				detail = d
			}
		}
		if detail == "" && a.toolCount > 0 {
			detail = fmt.Sprintf("%d tools", a.toolCount)
		}
		if detail != "" {
			lines = append(lines, "   "+kajicodeTheme.faint.Render("↳ "+truncateStep(detail, maxInt(2, room-2))))
		}
	}
	return lines, hits
}

// sidebarAgentSelectables returns the clickable agent lines with their
// ABSOLUTE index inside the rendered sidebar (the AGENTS header occupies index 0,
// so agent rows start at index 1). Recomputed on demand by the mouse hit-test —
// View cannot persist a registry on the value-receiver model — mirroring
// transcriptLineAtMouse.
func (m model) sidebarAgentSelectables(width int) []sidebarAgentHit {
	_, hits := m.sidebarAgentRows(width)
	for i := range hits {
		hits[i].lineOffset++ // shift past the AGENTS header at sidebar index 0
	}
	return hits
}

// agentExitFading reports whether a finished agent is in the later half of its
// linger window (sidebarAgentLinger), so its row dims toward faint just before
// it's removed. A zero finishedAt (not yet stamped) is not fading.
func (m model) agentExitFading(finishedAt time.Time) bool {
	return !finishedAt.IsZero() && m.now().Sub(finishedAt) >= sidebarAgentLinger/2
}

// renderContextSidebar builds the sidebar block: exactly height lines, each
// exactly width cells (after fitStyledLine + padding). Sections render top to
// bottom — FILES, PLAN — with the token readout pinned to the bottom line. Each
// section header is a faint uppercase label; items use ink/muted. Empty
// sections render a quiet placeholder rather than vanishing so the layout stays
// stable.
func (m model) renderContextSidebar(width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}

	var lines []string
	add := func(s string) { lines = append(lines, s) }

	// AGENTS section — spawned subagents and their live working detail.
	add(m.sidebarAgentHeader(width))
	agentLines := m.sidebarAgentLines(width)
	if len(agentLines) == 0 {
		add(sidebarPlaceholder("no agents spawned", width))
	} else {
		lines = append(lines, agentLines...)
	}

	// PLAN section.
	add("")
	add(m.sidebarPlanHeader(width))
	planLines := m.sidebarPlanLines(width)
	if len(planLines) == 0 {
		add(sidebarPlaceholder("no active plan", width))
	} else {
		lines = append(lines, planLines...)
	}

	// FILES section: the files this session has touched (files_panel.go).
	// Rendered BELOW the plan steps so it never shifts sidebarPlanSelectables'
	// click offsets; its own hits (sidebarFileSelectables) account for the
	// sections above it.
	add("")
	add(m.sidebarFilesHeader(width))
	fileLines, _ := m.sidebarFileLines(width)
	if len(fileLines) == 0 {
		add(sidebarPlaceholder("no files touched", width))
	} else {
		lines = append(lines, fileLines...)
	}

	// ACTIVITY section: recent completed work + a live "generating…" pulse. Shown
	// BELOW the plan steps so it never shifts sidebarPlanSelectables' click offsets,
	// and budgeted (height-1 minus what's used) so it clips ITSELF from the bottom
	// rather than letting the end-truncation eat into the plan. Absent when empty.
	if activityLines := m.sidebarActivityLines(width, maxInt(0, height-1-len(lines))); len(activityLines) > 0 {
		add("")
		add(sidebarHeader("ACTIVITY", width))
		lines = append(lines, activityLines...)
	}

	// Token readout pinned to the bottom.
	tokenLine := m.sidebarTokenLine(width)
	// Reserve the bottom row for tokens; pad the gap so it sits at the floor.
	for len(lines) < height-1 {
		add("")
	}
	if len(lines) > height-1 {
		lines = lines[:height-1]
	}
	add(tokenLine)

	// Hover highlight: resolved by STABLE IDENTITY (sessionID / stepIndex), not a
	// cached line offset — see hoveredSidebarLineOffset. A row whose identity no
	// longer resolves (it disappeared since the hover was last set from a real
	// mouse motion) simply doesn't highlight, rather than a coincidentally-matching
	// unrelated row lighting up.
	if lineOffset, ok := m.hoveredSidebarLineOffset(width); ok && lineOffset >= 0 && lineOffset < len(lines) {
		lines[lineOffset] = kajicodeTheme.hover.Render(ansi.Strip(lines[lineOffset]))
	}

	// Normalize every row to exactly width cells.
	for i := range lines {
		lines[i] = padStyledLine(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// hoveredSidebarLineOffset resolves the hovered sidebar row's CURRENT line offset
// fresh on every call, by re-matching m.hover's stable identity (sessionID for an
// agent, stepIndex for a plan step) against a freshly computed hit list — never a
// cached index. Returns false when the hover isn't sidebar-scoped, or when the
// identity no longer resolves (that row disappeared since the hover was last set
// by a real mouse motion — a linger window elapsing, a plan step completing —
// with no intervening motion to re-target it).
func (m model) hoveredSidebarLineOffset(width int) (int, bool) {
	switch m.hover.kind {
	case hoverSidebarAgent:
		for _, hit := range m.sidebarAgentSelectables(width) {
			if hit.sessionID == m.hover.sessionID {
				return hit.lineOffset, true
			}
		}
	case hoverPlanStep:
		for _, hit := range m.sidebarPlanSelectables(width) {
			if hit.stepIndex == m.hover.stepIndex {
				return hit.lineOffset, true
			}
		}
	case hoverFileRow:
		for _, hit := range m.sidebarFileSelectables(width) {
			if hit.path == m.hover.filePath {
				return hit.lineOffset, true
			}
		}
	}
	return 0, false
}

// sidebarHeader renders a bold-muted uppercase section label. Bold muted (vs the
// faint body items and placeholders) gives the header enough weight to read as a
// section heading rather than more filler. The width arg is unused — kept so it
// shares a signature with sidebarHeaderWithCount.
func sidebarHeader(label string, _ int) string {
	return kajicodeTheme.muted.Bold(true).Render(strings.ToUpper(label))
}

// sidebarHeaderWithCount renders a bold-muted section label with a right-aligned
// count (e.g. "PLAN   2/5") rendered in countStyle, so a section can colour its
// count by state — accent while in-flight, green when complete.
func sidebarHeaderWithCount(label, count string, countStyle lipgloss.Style, width int) string {
	left := kajicodeTheme.muted.Bold(true).Render(strings.ToUpper(label))
	right := countStyle.Render(count)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// sidebarPlaceholder renders a quiet placeholder line for an empty section.
func sidebarPlaceholder(text string, width int) string {
	return " " + kajicodeTheme.faint.Render(truncateRunes(text, maxInt(1, width-1)))
}

// sidebarPlanHeader renders the PLAN section header with the done/total count.
func (m model) sidebarPlanHeader(width int) string {
	state := m.plan
	if state.isEmpty() {
		return sidebarHeader("PLAN", width)
	}
	total := len(state.steps)
	done := 0
	for _, step := range state.steps {
		if step.status == "completed" || step.status == "failed" {
			done++
		}
	}
	// Stateful count: green once every step is done, accent while in-flight.
	countStyle := kajicodeTheme.accent
	if done == total {
		countStyle = kajicodeTheme.green
	}
	return sidebarHeaderWithCount("PLAN", fmt.Sprintf("%d/%d", done, total), countStyle, width)
}

// sidebarPlanLines renders the plan step list for the sidebar using the same
// status glyphs as the pinned panel (✓ done, • in-progress, ○ pending, ✗
// failed), reading m.plan directly so it stays in sync. Returns nil for an
// empty plan (the caller then shows a placeholder).
func (m model) sidebarPlanLines(width int) []string {
	state := m.plan
	if state.isEmpty() {
		return nil
	}
	room := maxInt(4, width-3)
	lines := make([]string, 0, len(state.steps))
	for _, step := range state.steps {
		var icon, body string
		switch step.status {
		case "completed":
			icon = kajicodeTheme.green.Render("✓")
			body = kajicodeTheme.muted.Render(truncateStep(step.content, room))
		case "in_progress":
			icon = kajicodeTheme.accent.Render("•")
			body = kajicodeTheme.ink.Render(truncateStep(step.content, room))
		case "failed":
			icon = kajicodeTheme.red.Render("✗")
			body = kajicodeTheme.muted.Render(truncateStep(step.content, room))
		default: // pending
			icon = kajicodeTheme.faint.Render("○")
			body = kajicodeTheme.faint.Render(truncateStep(step.content, room))
		}
		lines = append(lines, " "+icon+" "+body)
	}
	return lines
}

// maxSidebarActivityLines caps the ACTIVITY feed so it stays a glanceable tail,
// not a scrolling log.
const maxSidebarActivityLines = 5

// maxSidebarActivityScan bounds how many trailing transcript rows the activity
// feed inspects per render, so a sparse-work transcript stays O(window).
const maxSidebarActivityScan = 200

// sidebarActivityLines builds the ACTIVITY feed: a live "generating…" pulse (when
// the run has gone quiet) atop the most recent completed work (files written,
// commands run). It scans the transcript BACKWARD and stops after the cap, so the
// cost is bounded by the cap — never a full-transcript walk per frame. budget is
// the rows available before the token floor; the feed clips itself to it.
func (m model) sidebarActivityLines(width, budget int) []string {
	if budget <= 0 {
		return nil
	}
	room := maxInt(4, width-3)
	limit := minInt(maxSidebarActivityLines, budget)
	var work []string
	// Bound the rows inspected per render so a long, work-sparse transcript can't
	// turn this hot path into a full O(transcript) walk; recent work sits near the
	// end, so a window comfortably finds the latest items.
	scanned := 0
	for i := len(m.transcript) - 1; i >= 0 && len(work) < limit && scanned < maxSidebarActivityScan; i-- {
		scanned++
		row := m.transcript[i]
		if row.kind != rowToolResult || !isPlanWorkTool(row.tool) {
			continue
		}
		glyph := kajicodeTheme.green.Render("✓")
		if row.status == tools.StatusError {
			glyph = kajicodeTheme.red.Render("✗")
		}
		work = append(work, " "+glyph+" "+kajicodeTheme.muted.Render(truncateStep(m.activitySummary(row), room)))
	}
	live := ""
	if m.activeRunID != 0 {
		if hint := m.quietGenerationHint(); hint != "" {
			live = " " + kajicodeTheme.accent.Render("•") + " " + kajicodeTheme.faint.Render(truncateStep(hint, room))
		}
	}
	// Compacta count surfaces as its own pinned ACTIVITY line so repeated agent
	// auto-compactions are glanceable ("♻ compacted N×") without cluttering the
	// work feed beneath it.
	compactionLine := ""
	if m.compactions > 0 {
		label := "♻ compacted"
		if m.compactions > 1 {
			label = fmt.Sprintf("♻ compacted %dx", m.compactions)
		}
		compactionLine = " " + kajicodeTheme.amber.Render(label)
	}
	lines := make([]string, 0, len(work)+2)
	if live != "" {
		lines = append(lines, live)
	}
	if compactionLine != "" {
		lines = append(lines, compactionLine)
	}
	lines = append(lines, work...)
	if len(lines) > budget {
		lines = lines[:budget]
	}
	return lines
}

// activitySummary renders one ACTIVITY line: a command's command-line (recovered
// from its call row) for bash/exec, else the tool result's first line with the
// "tool result: <tool> <status> " prefix stripped (e.g. "Created styles.css
// (1045 lines).").
func (m model) activitySummary(row transcriptRow) string {
	if isPlanCommandTool(row.tool) {
		if cmd := m.activityCommandForRow(row.id, row.runID); cmd != "" {
			return row.tool + " · " + cmd
		}
		return row.tool
	}
	text := strings.TrimSpace(strings.SplitN(row.text, "\n", 2)[0])
	status := row.status
	if status == "" {
		status = tools.StatusOK
	}
	text = strings.TrimPrefix(text, fmt.Sprintf("tool result: %s %s ", row.tool, status))
	if strings.TrimSpace(text) == "" {
		return row.tool
	}
	return text
}

// activityCommandForRow recovers a command tool's command-line from its paired
// call row (whose arg hint carries the command), matched by BOTH id and runID so
// a reused tool-call id from a later run can't attribute the wrong command.
func (m model) activityCommandForRow(id string, runID int) string {
	if id == "" {
		return ""
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		row := m.transcript[i]
		if row.kind == rowToolCall && row.id == id && row.runID == runID {
			return row.arg
		}
	}
	return ""
}

// sidebarTokenLine renders the bottom token/context readout. It prefers the
// live context-fill figure (last request's input tokens) and falls back to the
// session's cumulative token count.
func (m model) sidebarTokenLine(width int) string {
	label := m.sidebarTokenText()
	if label == "" {
		label = "0 tokens"
	}
	// Append the graded context-fill % — the at-a-glance "how full is the window"
	// the compaction trigger reasons about. Reserve its width so the token label
	// truncates around it rather than overflowing.
	chip := ""
	if pct, _, _, style, ok := m.contextFillPercent(); ok {
		chip = kajicodeTheme.faint.Render(" · ") + style.Render(fmt.Sprintf("%d%%", pct))
	}
	budget := maxInt(1, width-1-lipgloss.Width(chip))
	return " " + kajicodeTheme.faint.Render(truncateRunes(label, budget)) + chip
}

// sidebarTokenText computes the token figure shown at the sidebar floor from
// the latest provider step's token footprint.
func (m model) sidebarTokenText() string {
	if m.usageTracker == nil {
		return ""
	}
	summary := m.usageTracker.Summary()
	used := m.latestUsageTokens(summary)
	if used <= 0 {
		return ""
	}
	if window := m.modelContextWindow(m.modelName); window > 0 {
		return fmt.Sprintf("%s / %s tokens", humanCount(used), humanCount(window))
	}
	return humanCount(used) + " tokens"
}

// joinColumns splices a chat block and a sidebar block side-by-side, one
// divider cell between them, into total-width rows. Both blocks are normalized
// to their column widths and to the same row count first, so every joined row
// is exactly chatWidth + 1 + sidebarWidth cells and the columns stay aligned.
func joinColumns(chat []string, sidebar []string, chatW, sidebarW int) []string {
	rows := len(chat)
	if len(sidebar) > rows {
		rows = len(sidebar)
	}
	// A cell of air on each side of the rule (" │ ") so the columns don't butt
	// flush against it. The chat side gets its gutter from the leading space; the
	// sidebar side from the trailing space (plus items' own leading inset, which
	// nests them under the flush section headers). Budgeted by chatColumnWidth(-3).
	divider := " " + kajicodeTheme.line.Render("│") + " "
	out := make([]string, rows)
	for i := 0; i < rows; i++ {
		left := ""
		if i < len(chat) {
			left = chat[i]
		}
		right := ""
		if i < len(sidebar) {
			right = sidebar[i]
		}
		left = padStyledLine(left, chatW)
		right = padStyledLine(right, sidebarW)
		out[i] = left + divider + right
	}
	return out
}
