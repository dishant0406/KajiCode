package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
