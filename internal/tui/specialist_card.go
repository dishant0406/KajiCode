// specialist_card.go renders specialist/subagent cards in the transcript.
//
// A specialist card summarises one spawned sub-agent (worker, explorer, code
// review, ...): its name, task description, elapsed time, tool-call count, and
// token usage. The specialistTracker holds the live state that the transcript
// view consults each render. Each delegation is keyed by its parent tool-call id
// (stable across the whole run); the model feeds the tracker start/complete/
// incrementToolCount/addTokens/setCurrentTool/setChildSessionID as the runtime
// messages arrive.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/streamjson"
)

// specialistStatus is the lifecycle state of a single specialist invocation.
type specialistStatus int

const (
	specialistRunning specialistStatus = iota
	specialistCompleted
	specialistError
)

// specialistInfo is the rendered view of one specialist invocation.
type specialistInfo struct {
	// toolCallID is the parent's Task tool-call id. It is the entry's STABLE
	// identity for the whole run: the child session id only becomes known once
	// the child reports its first progress/session event, and the completion
	// result arrives with yet another id. Every live update is keyed by this so
	// one delegation maps to exactly one card.
	toolCallID     string
	name           string
	description    string
	childSessionID string
	status         specialistStatus
	startedAt      time.Time
	completedAt    time.Time
	exitCode       int
	errorMsg       string
	toolCount      int // number of tool calls made by this specialist
	tokenCount     int // total tokens consumed
	currentTool    string
	currentDetail  string
}

// specialistTracker holds the live state for every specialist the parent agent
// has spawned in the current turn. Lookups are by toolCallID (the stable key).
type specialistTracker struct {
	specialists []specialistInfo
}

// start adds a new specialist entry, or updates the existing entry with the same
// tool-call id in place (so a duplicate start, or a start arriving after the
// child's first progress event, never creates a second entry).
func (t *specialistTracker) start(toolCallID, name, description string, now time.Time) {
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		entry := &t.specialists[index]
		// Keep an already-known name/description: a start arriving after the
		// child's session event carries "" for name and must not downgrade the
		// real agent name the child reported.
		if name = strings.TrimSpace(name); name != "" {
			entry.name = name
		}
		if description = strings.TrimSpace(description); description != "" {
			entry.description = description
		}
		entry.status = specialistRunning
		entry.startedAt = now
		entry.completedAt = time.Time{}
		entry.exitCode = 0
		entry.errorMsg = ""
		return
	}
	t.specialists = append(t.specialists, specialistInfo{
		toolCallID:  toolCallID,
		name:        name,
		description: description,
		status:      specialistRunning,
		startedAt:   now,
	})
}

// indexByToolCallID returns the slice index of the entry with toolCallID.
func (t *specialistTracker) indexByToolCallID(toolCallID string) (int, bool) {
	for index := range t.specialists {
		if t.specialists[index].toolCallID == toolCallID {
			return index, true
		}
	}
	return 0, false
}

// complete marks the specialist with toolCallID as finished, recording the
// terminal status, exit code, and any error message. Specialists that are not
// tracked are ignored.
func (t *specialistTracker) complete(toolCallID string, status specialistStatus, exitCode int, errorMsg string, now time.Time) {
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		t.specialists[index].status = status
		t.specialists[index].exitCode = exitCode
		t.specialists[index].errorMsg = errorMsg
		t.specialists[index].completedAt = now
	}
}

// incrementToolCount bumps the tool-call counter for the specialist with
// toolCallID. Unknown specialists are ignored.
func (t *specialistTracker) incrementToolCount(toolCallID string) {
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		t.specialists[index].toolCount++
	}
}

// addTokens adds tokens to the running total for the specialist with toolCallID.
// Unknown specialists are ignored.
func (t *specialistTracker) addTokens(toolCallID string, tokens int) {
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		t.specialists[index].tokenCount += tokens
	}
}

// setCurrentTool updates the live tool-call progress for the specialist with
// toolCallID. Used by specialistProgressMsg to show ↳ toolName detail.
func (t *specialistTracker) setCurrentTool(toolCallID, toolName, detail string) {
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		t.specialists[index].currentTool = toolName
		t.specialists[index].currentDetail = detail
	}
}

// setChildSessionID records the real child session id once the child reports it,
// so a running card can be drilled into before the delegation completes. No-op
// (creating a placeholder entry) when the start has not arrived yet — for a
// concurrently-batched delegation the child's first progress event can land
// BEFORE OnToolCall fires, and the following start fills in the name/description.
func (t *specialistTracker) setChildSessionID(toolCallID, childSessionID, name, description string, now time.Time) {
	if childSessionID == "" {
		return
	}
	if index, ok := t.indexByToolCallID(toolCallID); ok {
		t.specialists[index].childSessionID = childSessionID
		return
	}
	t.specialists = append(t.specialists, specialistInfo{
		toolCallID:     toolCallID,
		name:           name,
		description:    description,
		childSessionID: childSessionID,
		status:         specialistRunning,
		startedAt:      now,
	})
}

// clear resets the tracker to an empty state.
func (t *specialistTracker) clear() {
	t.specialists = nil
}

// all returns a copy of the specialists slice so callers may iterate without
// the underlying array mutating underneath them.
func (t *specialistTracker) all() []specialistInfo {
	if len(t.specialists) == 0 {
		return nil
	}
	out := make([]specialistInfo, len(t.specialists))
	copy(out, t.specialists)
	return out
}

// specialistStatusString returns the lowercase human label for a status.
func specialistStatusString(s specialistStatus) string {
	switch s {
	case specialistRunning:
		return "running"
	case specialistCompleted:
		return "completed"
	case specialistError:
		return "error"
	default:
		return "error"
	}
}

// parseSpecialistStatus maps the status string carried by specialist events to
// the internal specialistStatus enum. Unknown values default to error so a
// malformed event never reads as a silent success.
func parseSpecialistStatus(s string) specialistStatus {
	switch s {
	case "running":
		return specialistRunning
	case "completed":
		return specialistCompleted
	default:
		return specialistError
	}
}

// parseTaskCallArgs extracts the agent name and description from a Task tool
// call's JSON arguments. The Task schema names the target "agent" (required)
// with an optional "description" (falling back to "prompt"); "name" is accepted
// as a legacy alias. Both fallbacks matter: a missing name renders the card with
// an empty agent name.
func parseTaskCallArgs(rawArgs string) (name, description string) {
	name = firstArgValue(rawArgs, []string{"agent", "name"})
	description = firstArgValue(rawArgs, []string{"description", "prompt"})
	return name, description
}

// formatTokenCount renders an integer token count with comma thousands
// separators: 1840 -> "1,840", 5210 -> "5,210".
func formatTokenCount(n int) string {
	if n < 0 {
		n = -n
	}
	digits := strconv.Itoa(n)
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	first := len(digits) % 3
	if first > 0 {
		b.WriteString(digits[:first])
		if len(digits) > first {
			b.WriteByte(',')
		}
	}
	for i := first; i < len(digits); i += 3 {
		b.WriteString(digits[i : i+3])
		if i+3 < len(digits) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// formatSpecialistElapsed renders a duration as the compact "Ns" / "NmNs" form
// shown on specialist card headers (e.g. 18s, 45s, 1m5s). Durations under a
// second round up to 1s so a freshly started card never shows "0s".
func formatSpecialistElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Seconds())
	if seconds < 1 {
		return "1s"
	}
	if seconds < 60 {
		return strconv.Itoa(seconds) + "s"
	}
	minutes := seconds / 60
	remainder := seconds % 60
	if remainder == 0 {
		return strconv.Itoa(minutes) + "m"
	}
	return strconv.Itoa(minutes) + "m" + strconv.Itoa(remainder) + "s"
}

// renderSpecialistCard renders one specialist as a left-rule card of the given
// width: a status-tinted │ on the left, no top/right/bottom borders. The card
// has a header (icon + name + description + elapsed) and a body line (status +
// tool calls + tokens). An optional error detail line is shown when the
// specialist errored. The whole card is mouse-clickable to drill into its
// subchat (wired in transcript_selection.go); no text hint is rendered.
// Widths below the minimum are clamped to 30.
func (m model) renderSpecialistCard(info specialistInfo, width int) string {
	if width < 30 {
		width = 30
	}

	// Elapsed: live while running, frozen at completion once the specialist is
	// done.
	var elapsed time.Duration
	if info.status == specialistRunning {
		elapsed = m.now().Sub(info.startedAt)
	} else if !info.completedAt.IsZero() {
		elapsed = info.completedAt.Sub(info.startedAt)
	} else {
		elapsed = m.now().Sub(info.startedAt)
	}
	elapsedStr := formatSpecialistElapsed(elapsed)

	// Description truncation. The header reserves room for the icon, the name,
	// the two " · " separators, the elapsed string, and a safety margin. Clamp
	// to zero so very long names never underflow.
	descMax := width - len(info.name) - 25
	if descMax < 0 {
		descMax = 0
	}
	description := truncateRunes(info.description, descMax)

	// Header line: icon + name + " · " + description + " · " + elapsed.
	var header string
	switch info.status {
	case specialistRunning:
		icon := m.spinnerGlyph()
		header = kajicodeTheme.accent.Render(fmt.Sprintf("%s%s · %s · %s", icon, info.name, description, elapsedStr))
	case specialistCompleted:
		header = kajicodeTheme.green.Render(fmt.Sprintf("✓ %s · %s · %s", info.name, description, elapsedStr))
	case specialistError:
		header = kajicodeTheme.red.Render(fmt.Sprintf("✗ %s · %s · %s", info.name, description, elapsedStr))
	default:
		header = kajicodeTheme.accent.Render(fmt.Sprintf("• %s · %s · %s", info.name, description, elapsedStr))
	}

	// Body line: "  status · N tool calls · M,NNN tokens".
	toolLabel := "tool calls"
	statusLabel := specialistStatusString(info.status)
	// Only show an exit code when one was actually reported — Task results carry
	// none, and "error (exit code 0)" would read as a real, misleading code.
	if info.status == specialistError && info.exitCode != 0 {
		statusLabel = fmt.Sprintf("error (exit code %d)", info.exitCode)
	}
	// The token total is only populated when usage was bridged from the child; omit
	// the segment when it is zero rather than advertise a misleading "0 tokens" (M18).
	bodyText := fmt.Sprintf("  %s · %d %s", statusLabel, info.toolCount, toolLabel)
	if info.tokenCount > 0 {
		bodyText += fmt.Sprintf(" · %s tokens", formatTokenCount(info.tokenCount))
	}
	var body string
	if info.status == specialistError {
		body = kajicodeTheme.red.Render(bodyText)
	} else {
		body = kajicodeTheme.muted.Render(bodyText)
	}
	// Surface the otherwise-invisible drill-in affordance: a left-click or Enter on
	// the card opens its subchat (transcript_selection.go). A faint hint makes that
	// discoverable instead of hidden; it truncates first on narrow cards.
	body += kajicodeTheme.faint.Render("   · enter to open")

	lines := []string{header, body}

	// Live tool-call progress while running.
	if info.status == specialistRunning && info.currentTool != "" {
		progressLine := fmt.Sprintf("  ↳ %s", info.currentTool)
		if info.currentDetail != "" {
			progressLine += " " + info.currentDetail
		}
		lines = append(lines, kajicodeTheme.muted.Render(progressLine))
	}

	// Optional error detail line.
	if info.status == specialistError && strings.TrimSpace(info.errorMsg) != "" {
		errMax := width - 4
		if errMax < 1 {
			errMax = 1
		}
		errMsg := truncateRunes(strings.TrimSpace(info.errorMsg), errMax)
		lines = append(lines, kajicodeTheme.red.Render("  "+errMsg))
	}

	// Left-rule card: status-tinted │ on the left, no box borders.
	rule := specialistBorderStyle(info.status)
	return renderLeftRuleCard(width, lines, rule)
}

// specialistBorderStyle picks the card border style for a specialist status:
// the running tint while in flight, the error tint on failure, and the default
// line once completed cleanly.
func specialistBorderStyle(status specialistStatus) lipgloss.Style {
	switch status {
	case specialistRunning:
		return kajicodeTheme.cardRun
	case specialistError:
		return kajicodeTheme.cardErr
	default:
		return kajicodeTheme.line
	}
}

// upsertSpecialistRow refreshes the specialist card row for toolCallID, or
// appends one if none exists. The row is keyed by the delegation's stable id, so
// the same card is updated in place as start, session, progress, usage, and
// completion messages arrive — exactly one card per delegation, visible from the
// moment the delegation starts.
func (m model) upsertSpecialistRow(runID int, toolCallID string) model {
	index, ok := m.specialists.indexByToolCallID(toolCallID)
	if !ok {
		return m
	}
	rowID := "specialist:" + toolCallID
	for i := range m.transcript {
		if m.transcript[i].kind == rowSpecialist && m.transcript[i].runID == runID && m.transcript[i].id == rowID {
			info := m.specialists.specialists[index]
			m.transcript[i].specialistInfo = &info
			// The fingerprint is computed once at append time and every render/
			// height/scroll cache key derives from it, so an in-place update that
			// left it stale would render the old card body (same reason as
			// toggledTranscriptRow).
			m.transcript[i].renderFingerprint = transcriptRowFingerprint(m.transcript[i])
			return m
		}
	}
	info := m.specialists.specialists[index]
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind:           rowSpecialist,
		id:             rowID,
		runID:          runID,
		specialistInfo: &info,
	})
	return m
}

// specialistTitleFor returns the display title (name + " · " + description) for
// the specialist whose child session is childSessionID, for the subchat nav bar.
// Returns "" when not found. Falls back to the specialist info carried by
// transcript rows when the tracker has been cleared.
func (m model) specialistTitleFor(childSessionID string) string {
	for _, info := range m.specialists.all() {
		if info.childSessionID == childSessionID {
			return info.name + " · " + info.description
		}
	}
	for _, row := range m.transcript {
		if row.kind == rowSpecialist && row.specialistInfo != nil && row.specialistInfo.childSessionID == childSessionID {
			return row.specialistInfo.name + " · " + row.specialistInfo.description
		}
	}
	return ""
}

// toolCallSummary extracts a short detail string from a stream-json tool_call
// event's arguments, for the live progress line in specialist cards.
func toolCallSummary(event streamjson.Event) string {
	args, ok := event.Args.(map[string]any)
	if !ok {
		return ""
	}
	switch event.Name {
	case "read_file", "read_minified_file", "list_directory", "write_file", "edit_file":
		if path, ok := args["path"].(string); ok {
			return truncateRunes(path, 50)
		}
	case "grep":
		if pattern, ok := args["pattern"].(string); ok {
			return truncateRunes(pattern, 40)
		}
	case "glob":
		if pattern, ok := args["pattern"].(string); ok {
			return truncateRunes(pattern, 40)
		}
	case "bash", "exec_command":
		if cmd, ok := args["command"].(string); ok {
			return truncateRunes(singleLineToolHeadText(cmd), 40)
		}
		if cmd, ok := args["cmd"].(string); ok {
			return truncateRunes(singleLineToolHeadText(cmd), 40)
		}
	case "write_stdin":
		sessionID := toolCallIntArg(args, "session_id")
		chars, _ := args["chars"].(string)
		switch {
		case chars == "":
			return fmt.Sprintf("poll session %d", sessionID)
		case chars == "\x03":
			return fmt.Sprintf("interrupt session %d", sessionID)
		default:
			return fmt.Sprintf("send input to session %d", sessionID)
		}
	case "todo_write":
		return "plan"
	}
	return ""
}

func toolCallIntArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(value)
		return parsed
	default:
		return 0
	}
}

// truncateRunes is provided by view.go; specialist_card.go relies on it for
// rune-safe description and error-message truncation.

// renderLeftRuleCard renders lines with a single status-tinted left rule and
// no other borders. Each line is prefixed with "│ " in the rule style; the
// content is padded to the given width so cards align. No top/bottom/right
// borders — lighter than styledBlock, matching the borderless inline tool
// render style used by reference TUIs.
func renderLeftRuleCard(width int, lines []string, ruleStyle lipgloss.Style) string {
	if width < 4 {
		width = 4
	}
	inner := width - 2 // "│ " takes 2 cells
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		fitted := fitStyledLine(line, inner)
		pad := strings.Repeat(" ", maxInt(0, inner-lipgloss.Width(fitted)))
		out = append(out, ruleStyle.Render("│ ")+fitted+pad)
	}
	return strings.Join(out, "\n")
}

// renderSpecialistSummary renders a one-line rollup shown above the specialist
// cards: live spinner, total/running/completed/error counts, and total tokens.
// Returns "" when there are no specialists.
func renderSpecialistSummary(specialists []specialistInfo, spinnerView string) string {
	if len(specialists) == 0 {
		return ""
	}
	running, completed, errors, totalTokens := 0, 0, 0, 0
	for _, sp := range specialists {
		totalTokens += sp.tokenCount
		switch sp.status {
		case specialistRunning:
			running++
		case specialistCompleted:
			completed++
		case specialistError:
			errors++
		}
	}
	icon := "✓"
	if errors > 0 {
		icon = "✗"
	}
	if running > 0 {
		icon = spinnerView
	}
	summary := fmt.Sprintf("  %s %d specialists · %d running · %d done",
		icon, len(specialists), running, completed)
	if errors > 0 {
		summary += fmt.Sprintf(" · %d error", errors)
		if errors > 1 {
			summary += "s"
		}
	}
	summary += " · " + formatTokenCount(totalTokens) + " tokens"
	// summary is "  " + spinnerView + " N specialists ...". The spinner sits
	// at byte offset 2 (after the 2-space indent), so the muted tail must skip
	// both the indent and the spinner's bytes to avoid splitting a multi-byte
	// rune and losing the indent.
	tailStart := 2 + len(icon)
	iconStyle := kajicodeTheme.green
	if errors > 0 {
		iconStyle = kajicodeTheme.red
	}
	if running > 0 {
		iconStyle = kajicodeTheme.accent
	}
	return iconStyle.Render(icon) + kajicodeTheme.muted.Render(summary[tailStart:])
}
