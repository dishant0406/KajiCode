package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The /loop command repeats a prompt or slash command in one of two modes:
//
//   - Fixed interval — `/loop 5m <prompt>` re-fires on a wall-clock cadence.
//   - Self-paced     — bare `/loop <prompt>` lets the model finish an iteration,
//     pick its own next wake (via the loop_control tool), and stop when done.
//
// Loops are foreground and session-scoped: a tick only fires while the session is
// idle between turns (it never interrupts a streaming turn), and the loop lives
// with its session. Everything here is pure/loop-local; the wiring into the turn
// lifecycle (ticker, launch, continuation) lives in model.go.

type loopMode int

const (
	loopModeFixed     loopMode = iota // re-fire every fixed interval
	loopModeSelfPaced                 // model chooses the next wake each iteration
)

// Safety bounds. A loop is foreground + session-scoped, but these keep a runaway
// or forgotten loop from burning tokens forever.
const (
	loopMinInterval   = 30 * time.Second
	loopMaxInterval   = 24 * time.Hour
	loopMaxIterations = 500 // hard ceiling; the loop auto-stops after this many

	loopSelfPaceMin     = 1 * time.Minute
	loopSelfPaceMax     = 1 * time.Hour
	loopSelfPaceDefault = 15 * time.Minute

	// loopDoomThreshold ends a loop after this many consecutive identical final
	// answers — a self-paced or interval loop that gets stuck repeating the same
	// no-progress result should stop rather than churn.
	loopDoomThreshold = 5

	// loopPollInterval is how often the controller checks whether a loop is due.
	// A loop only fires while idle, so this is a cheap between-turn poll.
	loopPollInterval = 1 * time.Second
)

// loopState is one active loop owned by the current session.
type loopState struct {
	id        string
	mode      loopMode
	prompt    string // the prompt or "/command" re-run each iteration
	interval  time.Duration
	iteration int
	maxIter   int // 0 => loopMaxIterations
	createdAt time.Time
	nextRunAt time.Time
	// lastResult / repeatRun drive doom-loop detection: consecutive identical
	// final answers past loopDoomThreshold end the loop.
	lastResult string
	repeatRun  int
	paused     bool
}

func (l *loopState) iterationCap() int {
	if l.maxIter > 0 {
		return l.maxIter
	}
	return loopMaxIterations
}

// due reports whether the loop should fire at now (active, not paused, past its
// next-run mark).
func (l *loopState) due(now time.Time) bool {
	return !l.paused && !l.nextRunAt.IsZero() && !now.Before(l.nextRunAt)
}

// cadenceText renders the loop's cadence for status lines.
func (l *loopState) cadenceText() string {
	if l.mode == loopModeSelfPaced {
		return "self-paced"
	}
	return "every " + formatLoopDuration(l.interval)
}

type loopAction int

const (
	loopActionUsage   loopAction = iota // show usage / list when empty
	loopActionStart                     // start a new loop
	loopActionStop                      // stop one by id
	loopActionStopAll                   // stop every loop
	loopActionList                      // list active loops
)

// loopCommand is the parsed form of a `/loop ...` invocation (the text after the
// command name).
type loopCommand struct {
	action   loopAction
	targetID string
	mode     loopMode
	prompt   string
	interval time.Duration
	// note carries a non-fatal advisory (e.g. an interval that was clamped) to
	// surface back to the user.
	note string
}

var loopIntervalRe = regexp.MustCompile(`^(\d+)(s|m|h|d)$`)

// parseLoopCommand interprets the arguments to `/loop`. Forms:
//
//	(empty)                 -> usage/list
//	list | status          -> list active loops
//	stop | stop all | stop <id>
//	<interval> <prompt>     -> fixed-interval loop (e.g. "5m /babysit")
//	<prompt> every <interval>
//	<prompt>                -> self-paced loop (no interval)
func parseLoopCommand(args string) loopCommand {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return loopCommand{action: loopActionUsage}
	}

	first := strings.ToLower(strings.Fields(trimmed)[0])
	switch first {
	case "list", "status", "ls":
		return loopCommand{action: loopActionList}
	case "stop", "cancel", "kill":
		rest := strings.TrimSpace(trimmed[len(strings.Fields(trimmed)[0]):])
		if rest == "" || strings.EqualFold(rest, "all") {
			if strings.EqualFold(rest, "all") {
				return loopCommand{action: loopActionStopAll}
			}
			// bare "stop" stops all when ambiguous; the caller may special-case a
			// single active loop.
			return loopCommand{action: loopActionStop, targetID: ""}
		}
		return loopCommand{action: loopActionStop, targetID: strings.TrimSpace(rest)}
	}

	// Leading interval token: "5m <prompt>".
	if fields := strings.Fields(trimmed); len(fields) >= 2 {
		if d, ok := parseLoopInterval(fields[0]); ok {
			prompt := strings.TrimSpace(trimmed[len(fields[0]):])
			cmd := loopCommand{action: loopActionStart, mode: loopModeFixed, prompt: prompt}
			cmd.interval, cmd.note = clampLoopInterval(d)
			return cmd
		}
	}

	// Trailing "every <interval>".
	if prompt, d, ok := splitTrailingEvery(trimmed); ok {
		cmd := loopCommand{action: loopActionStart, mode: loopModeFixed, prompt: prompt}
		cmd.interval, cmd.note = clampLoopInterval(d)
		return cmd
	}

	// No interval anywhere -> self-paced.
	return loopCommand{action: loopActionStart, mode: loopModeSelfPaced, prompt: trimmed}
}

// splitTrailingEvery matches "<prompt> every <interval>" (the interval must be a
// real duration token so "check every PR" is not mis-parsed as a cadence).
func splitTrailingEvery(input string) (string, time.Duration, bool) {
	fields := strings.Fields(input)
	if len(fields) < 3 {
		return "", 0, false
	}
	last := fields[len(fields)-1]
	prev := fields[len(fields)-2]
	if !strings.EqualFold(prev, "every") {
		return "", 0, false
	}
	d, ok := parseLoopInterval(last)
	if !ok {
		return "", 0, false
	}
	prompt := strings.TrimSpace(strings.Join(fields[:len(fields)-2], " "))
	if prompt == "" {
		return "", 0, false
	}
	return prompt, d, true
}

// parseLoopInterval parses a bare "<N><unit>" token (s/m/h/d) into a duration.
func parseLoopInterval(token string) (time.Duration, bool) {
	m := loopIntervalRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(token)))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	unit := map[string]time.Duration{
		"s": time.Second,
		"m": time.Minute,
		"h": time.Hour,
		"d": 24 * time.Hour,
	}[m[2]]
	return time.Duration(n) * unit, true
}

// clampLoopInterval bounds an interval to [loopMinInterval, loopMaxInterval] and
// returns an advisory note when it was adjusted.
func clampLoopInterval(d time.Duration) (time.Duration, string) {
	switch {
	case d < loopMinInterval:
		return loopMinInterval, fmt.Sprintf("interval raised to the %s minimum", formatLoopDuration(loopMinInterval))
	case d > loopMaxInterval:
		return loopMaxInterval, fmt.Sprintf("interval lowered to the %s maximum", formatLoopDuration(loopMaxInterval))
	default:
		return d, ""
	}
}

// loopTickMsg is the between-turn poll that fires a due loop while the session is
// idle. seq invalidates a tick left over from before the loop set changed.
type loopTickMsg struct{ seq int }

// handleLoopCommand dispatches a parsed `/loop ...` invocation.
func (m model) handleLoopCommand(args string) (model, tea.Cmd) {
	cmd := parseLoopCommand(args)
	switch cmd.action {
	case loopActionList:
		return m.appendLoopSystem(m.loopListText()), nil
	case loopActionUsage:
		if len(m.loops) > 0 {
			return m.appendLoopSystem(m.loopListText()), nil
		}
		return m.appendLoopSystem(loopUsageText()), nil
	case loopActionStop:
		return m.stopLoop(cmd.targetID)
	case loopActionStopAll:
		return m.stopAllLoops()
	case loopActionStart:
		return m.startLoop(cmd)
	}
	return m, nil
}

// startLoop registers a new loop and (re)starts the idle poll ticker. The first
// iteration fires on the next idle tick (nextRunAt = now).
func (m model) startLoop(cmd loopCommand) (model, tea.Cmd) {
	if reason, ok := validateLoopTarget(cmd.prompt); !ok {
		return m.appendLoopSystem(reason), nil
	}
	m.loopCounter++
	id := "L" + strconv.Itoa(m.loopCounter)
	loop := &loopState{
		id:        id,
		mode:      cmd.mode,
		prompt:    cmd.prompt,
		interval:  cmd.interval,
		createdAt: m.now(),
		nextRunAt: m.now(), // fire the first iteration on the next idle tick
	}
	m.loops = append(m.loops, loop)
	note := ""
	if cmd.note != "" {
		note = " (" + cmd.note + ")"
	}
	m = m.appendLoopSystem(fmt.Sprintf(
		"Loop %s started — %s%s. Runs while this session is idle; stop with /loop stop %s.",
		id, loop.cadenceText(), note, id))
	return m.ensureLoopTick()
}

// stopLoop stops one loop by id, or the single active loop when the id is omitted.
func (m model) stopLoop(id string) (model, tea.Cmd) {
	if len(m.loops) == 0 {
		return m.appendLoopSystem("No active loops."), nil
	}
	if id == "" {
		if len(m.loops) == 1 {
			return m.removeLoop(m.loops[0].id, "Loop "+m.loops[0].id+" stopped."), nil
		}
		return m.appendLoopSystem("Multiple loops active — use /loop stop <id> or /loop stop all.\n" + m.loopListText()), nil
	}
	if m.findLoop(id) == nil {
		return m.appendLoopSystem("No loop " + id + ". " + m.loopListText()), nil
	}
	return m.removeLoop(id, "Loop "+id+" stopped."), nil
}

func (m model) stopAllLoops() (model, tea.Cmd) {
	if len(m.loops) == 0 {
		return m.appendLoopSystem("No active loops."), nil
	}
	n := len(m.loops)
	m.loops = nil
	m.loopSeq++ // invalidate the pending poll tick
	m.loopTicking = false
	return m.appendLoopSystem(fmt.Sprintf("Stopped all %d loop(s).", n)), nil
}

// fireDueLoopIfIdle fires the earliest due loop when the session is idle. Called
// from the poll tick; a no-op while a turn, modal, or queued user message is
// pending (the loop simply waits for the next idle tick).
func (m model) fireDueLoopIfIdle() (model, tea.Cmd) {
	if m.loopBusy() || len(m.loops) == 0 {
		return m, nil
	}
	now := m.now()
	var due *loopState
	for _, l := range m.loops {
		if l.due(now) && (due == nil || l.nextRunAt.Before(due.nextRunAt)) {
			due = l
		}
	}
	if due == nil {
		return m, nil
	}
	m.activeLoopID = due.id
	due.nextRunAt = time.Time{} // mark running
	m.loopNextWake = 0
	m.loopDone = false
	return m.fireLoopPrompt(due.prompt)
}

// fireLoopPrompt runs a loop's prompt as a turn — a custom /command if it resolves
// to one, otherwise a plain prompt.
func (m model) fireLoopPrompt(prompt string) (model, tea.Cmd) {
	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		if next, teaCmd, ok := m.handleUserCommand(prompt); ok {
			return next, teaCmd
		}
	}
	return m.launchPrompt(prompt)
}

// advanceLoop updates a loop after one of its iterations completes: doom-loop and
// cap checks, then schedules the next wake (or stops the loop).
func (m model) advanceLoop(id, finalAnswer string, runErr error) model {
	l := m.findLoop(id)
	if l == nil {
		return m // stopped mid-run
	}
	l.iteration++
	res := strings.TrimSpace(finalAnswer)
	if res != "" && res == l.lastResult {
		l.repeatRun++
	} else {
		l.repeatRun = 0
	}
	l.lastResult = res

	switch {
	case m.loopDone:
		return m.removeLoop(id, fmt.Sprintf("Loop %s finished — the task reported done after %d iteration(s).", id, l.iteration))
	case l.iteration >= l.iterationCap():
		return m.removeLoop(id, fmt.Sprintf("Loop %s stopped at its %d-iteration cap.", id, l.iteration))
	case l.repeatRun >= loopDoomThreshold:
		return m.removeLoop(id, fmt.Sprintf("Loop %s stopped — %d identical results in a row (no progress).", id, l.repeatRun))
	}

	if l.mode == loopModeSelfPaced {
		l.nextRunAt = m.now().Add(clampSelfPaceDelay(m.loopNextWake))
	} else {
		l.nextRunAt = m.now().Add(l.interval)
	}
	return m
}

func (m model) findLoop(id string) *loopState {
	for _, l := range m.loops {
		if l.id == id {
			return l
		}
	}
	return nil
}

func (m model) removeLoop(id, note string) model {
	kept := make([]*loopState, 0, len(m.loops))
	for _, l := range m.loops {
		if l.id != id {
			kept = append(kept, l)
		}
	}
	m.loops = kept
	m.loopSeq++ // invalidate the pending poll tick; a fresh one is scheduled if loops remain
	m.loopTicking = false
	if note != "" {
		m = m.appendLoopSystem(note)
	}
	return m
}

// ensureLoopTick starts the idle poll ticker if it is not already running.
func (m model) ensureLoopTick() (model, tea.Cmd) {
	if m.loopTicking || len(m.loops) == 0 {
		return m, nil
	}
	m.loopTicking = true
	return m, m.scheduleLoopTick()
}

func (m model) scheduleLoopTick() tea.Cmd {
	seq := m.loopSeq
	return tea.Tick(loopPollInterval, func(time.Time) tea.Msg {
		return loopTickMsg{seq: seq}
	})
}

// loopBusy reports whether the session is too busy for a loop to fire — a turn,
// modal, queued user message, or compaction is in flight. Mirrors the guard in
// launchQueuedMessageIfReady so loops and queued prompts never collide.
func (m model) loopBusy() bool {
	return m.pending || m.exiting || m.compactInFlight || m.hasQueuedMessage() ||
		m.pendingPermission != nil || m.pendingAskUser != nil || m.pendingSpecReview != nil
}

func (m model) appendLoopSystem(text string) model {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	return m
}

// loopActive reports whether any loop is running (for the footer + leave warning).
func (m model) loopActive() bool { return len(m.loops) > 0 }

// loopFooterSummary renders the persistent "N loops · next 3:05pm" footer segment,
// or "" when no loops are active.
func (m model) loopFooterSummary() string {
	if len(m.loops) == 0 {
		return ""
	}
	var next time.Time
	for _, l := range m.loops {
		if l.nextRunAt.IsZero() || l.paused {
			continue
		}
		if next.IsZero() || l.nextRunAt.Before(next) {
			next = l.nextRunAt
		}
	}
	label := fmt.Sprintf("%d loop", len(m.loops))
	if len(m.loops) != 1 {
		label += "s"
	}
	if next.IsZero() {
		return label
	}
	return label + " · next " + next.Format("3:04pm")
}

func (m model) loopListText() string {
	if len(m.loops) == 0 {
		return "No active loops."
	}
	var b strings.Builder
	b.WriteString("Active loops:\n")
	now := m.now()
	for _, l := range m.loops {
		when := "running"
		if !l.nextRunAt.IsZero() {
			if d := l.nextRunAt.Sub(now); d > 0 {
				when = "next in " + formatLoopDuration(d.Round(time.Second))
			} else {
				when = "due"
			}
		}
		b.WriteString(fmt.Sprintf("  %s · %s · iter %d · %s · %s\n",
			l.id, l.cadenceText(), l.iteration, when, truncateLoopPrompt(l.prompt)))
	}
	b.WriteString("Stop with /loop stop <id> or /loop stop all.")
	return b.String()
}

func loopUsageText() string {
	return "Repeat a prompt or command:\n" +
		"  /loop 5m /babysit-prs   — run a command every 5 minutes\n" +
		"  /loop watch CI every 2m — trailing interval form\n" +
		"  /loop keep tidying docs — self-paced (the model picks its own cadence and stops when done)\n" +
		"  /loop list              — show active loops\n" +
		"  /loop stop [id|all]     — stop a loop\n" +
		"Loops run while this session is idle and stop when you close it."
}

// validateLoopTarget rejects looping a built-in command (only a prompt or a custom
// /command may loop).
func validateLoopTarget(prompt string) (string, bool) {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return "Nothing to loop — give a prompt or a /command.", false
	}
	if strings.HasPrefix(p, "/") {
		parsed := parseCommand(p)
		if parsed.kind != commandPrompt && parsed.kind != commandUnknown {
			return "A loop can only run a prompt or a custom /command, not a built-in like " + strings.Fields(p)[0] + ".", false
		}
	}
	return "", true
}

func truncateLoopPrompt(prompt string) string {
	const max = 48
	p := strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
	runes := []rune(p)
	if len(runes) <= max {
		return p
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}

// clampSelfPaceDelay bounds a model-chosen self-paced delay to the self-pace
// band, defaulting an unset/invalid value.
func clampSelfPaceDelay(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return loopSelfPaceDefault
	case d < loopSelfPaceMin:
		return loopSelfPaceMin
	case d > loopSelfPaceMax:
		return loopSelfPaceMax
	default:
		return d
	}
}

// formatLoopDuration renders a duration compactly (90s, 5m, 2h, 1d) for status
// text, preferring the largest whole unit.
func formatLoopDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0 && d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0 && d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0 && d >= time.Minute:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
}
