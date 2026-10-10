package agent

import (
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Guardrail thresholds for the agent loop. These keep a runaway model from
// burning turns/tokens and nudge it toward keeping the plan current. They are
// deliberately conservative so trivial single-step tasks never trip them.
const (
	// maxEmptyTurns stops the run after this many consecutive turns that
	// produced no visible text AND no tool calls. A turn that produces either
	// resets the counter. Dropped-tool-call turns are handled by the existing
	// retry path and are not counted here.
	maxEmptyTurns = 3

	// staleToolCallThreshold injects a one-shot reminder once this many tool
	// calls have executed since the last todo_write call.
	staleToolCallThreshold = 10

	// stalePlanTurnThreshold injects the same one-shot reminder once this many
	// turns have passed since the last todo_write while plan items are still
	// pending — the turn-based complement to staleToolCallThreshold, catching a
	// plan that drifts stale across many turns that each make few tool calls.
	stalePlanTurnThreshold = 8

	// toolOnlyProgressReminderAt injects a one-shot progress nudge after this
	// many consecutive turns contain tool calls but no visible assistant text.
	// It does not stop the run; it tells the model to synthesize what it already
	// knows before spending more tool turns.
	toolOnlyProgressReminderAt = 6

	// noActionReminderAt injects a one-shot nudge after this many consecutive
	// turns produced no visible text AND no executed tool call. Reasoning proves
	// the model is running, not that it is making progress, so a model narrating
	// its intentions ("Let me write. Let me run. OK.") or emitting only malformed
	// tool calls is told to act.
	noActionReminderAt = 3

	// maxNoActionTurns stops the run after this many consecutive turns with no
	// visible text and no executed tool call. This is the only bound on that
	// loop: such a turn resets emptyTurns whenever it streamed reasoning (the turn
	// was live), so maxEmptyTurns can never fire, and noOpTurns only counts tool
	// calls. Without this bound the model stalls until MaxTurns. Set well above
	// noActionReminderAt so the nudge gets several turns to break the loop.
	maxNoActionTurns = 6

	// planReminderTurn is the turn (1-based) by the end of which a multi-step
	// task should have called todo_write; if it hasn't, a one-time reminder is
	// injected. Set to 3 (not 2) so short, legitimate two-step tasks finish
	// without a spurious planning nag.
	planReminderTurn = 3

	// planToolName is the planning tool the loop watches for by name.
	planToolName = "todo_write"

	// toolFailureHintAt injects a one-shot corrective hint (the tool's schema +
	// the exact error) after a tool fails this many times in a row with the same
	// error, so the model self-corrects instead of repeating the mistake.
	toolFailureHintAt = 2
	// toolFailureStopAt halts the run after a tool fails this many times in a row
	// with the same error, so NO model (weak or strong) burns turns looping on a
	// bad call. Set to 6 (not 4): a corrective hint fires at toolFailureHintAt (2),
	// and a model iterating on a genuinely tricky edit can legitimately fail a few
	// times after the hint while converging — stopping at 4 cut those runs short.
	// The streak still resets the moment the tool succeeds or hits a different
	// error, so this only affects true same-error loops.
	toolFailureStopAt = 6

	// noOpToolCallHintAt injects a one-shot corrective nudge after this many
	// consecutive turns whose tool calls all repeat an earlier call verbatim and
	// change nothing. A repeated identical call cannot produce new information, so
	// the loop tells the model to stop re-issuing it.
	//
	// BOUNDARY: this catches *verbatim* repeats only. A model that varies the
	// arguments each turn ("echo 1", "echo 2", …) issues a new call every time and
	// is not caught here — that class of loop is bounded by MaxTurns alone. The
	// guard is deliberately narrow because a broader "same tool + small output"
	// rule would cut off legitimate iteration, and the blind-agent state that
	// produced the reported loop is prevented at its source by the gate's
	// MaxHiddenRatio ceiling (see internal/agent/tool_gate.go).
	noOpToolCallHintAt = 3
	// noOpToolCallStopAt halts the run after this many consecutive no-op turns.
	// Higher than the hint so a model that self-corrects after the nudge is not
	// cut short.
	noOpToolCallStopAt = 6

	// maxContinueNudges bounds how many times the headless completion gate
	// (Options.RequireCompletionSignal) re-prompts a model that stopped without a
	// tool call while work clearly remained. Once spent, the run finalizes as
	// INCOMPLETE rather than nudging forever (and it is still bounded by maxTurns
	// and the run deadline).
	maxContinueNudges = 3
)

// continueNudgeMarker is a stable substring for tests.
const continueNudgeMarker = "the task is not finished"

// continueNudge tells a model that stopped without a tool call — while work
// clearly remained — to keep going, or to mark the plan complete and summarize if
// it is genuinely done. The second path gives it a clean route to a legitimate
// completion (a finished plan + no continuation cue then exits as success).
func continueNudge(reason string) string {
	return "You stopped without calling a tool, but " + continueNudgeMarker + " (" + reason + "). " +
		"Do not stop here: take the next concrete action with a tool now. " +
		"If you are genuinely finished, first mark the plan complete with todo_write, then give your final summary."
}

// endsWithContinuationCue reports whether an assistant message ends mid-thought —
// the model announcing its OWN next action and then stopping on a colon
// ("…Let me check the SSH configuration:") rather than concluding. It requires
// BOTH a trailing colon AND an action lead-in on the last line, so genuine closers
// are NOT flagged: a forward-looking recommendation ("Next, I suggest reviewing
// the changes."), a sign-off ("Let me know if you need anything"), or a summary
// that merely ends in a colon ("Here is the summary:"). A bare trailing colon alone
// is too common in legitimate final answers to use as a signal.
func endsWithContinuationCue(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lines := strings.Split(trimmed, "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			last = strings.ToLower(s)
			break
		}
	}
	if !strings.HasSuffix(last, ":") {
		return false
	}
	// Inspect the final clause (after the last sentence break) so a mid-line action
	// announcement is caught ("…configure the server. Let me check the config:")
	// while a plain summary colon ("Here is the summary:") or a recommendation is not.
	clause := last
	if idx := strings.LastIndex(last, ". "); idx >= 0 {
		clause = strings.TrimSpace(last[idx+2:])
	}
	if strings.HasPrefix(clause, "let me know") { // sign-off, not a mid-step action
		return false
	}
	for _, cue := range []string{
		"let me ", "let's ", "now i'll ", "now i will ", "now let me ", "let me now ",
		"i'll now ", "i will now ", "next i'll ", "next, i'll ", "first, i'll ", "first let me ",
	} {
		if strings.HasPrefix(clause, cue) {
			return true
		}
	}
	return false
}

// planStatusRemaining reports whether a raw todo_write status string represents
// unfinished work. Anything not clearly completed/failed (incl. empty/unknown,
// which the todo_write tool coerces to "pending") counts as remaining, matching
// that tool's own status normalization.
func planStatusRemaining(status string) bool {
	switch normalizeTaskPlanStatus(status) {
	case "completed", "failed":
		return false
	default:
		return true
	}
}

// acceptanceNudgeMarker is a stable substring for tests; it is embedded verbatim
// in acceptanceVerificationNudge.
const acceptanceNudgeMarker = "verify your work against the task's stated success criterion"

// selfReportPhrases are high-signal admissions of guessing/fabrication or stated
// uncertainty about the produced result. They are matched by plain substring with
// NO context guard, so every entry must be FIRST-PERSON or unambiguously about the
// model's own output — NOT a phrase that also describes implemented behavior.
// (Earlier versions included "fall back to" / "placeholder value" / "as a
// fallback" / bare "best guess" / "without proper", which match legitimate final
// answers like "the parser will fall back to UTF-8" or "I replaced the placeholder
// value", wrongly downgrading completed runs — so those are removed. Admissions of
// INABILITY are handled separately by inabilityStems, which carry a guard.)
var selfReportPhrases = []string{
	// first-person admissions of guessing / fabrication / assumption
	"i guessed", "my best guess", "i'm guessing", "this is a guess", "just a guess",
	"i made it up", "i fabricated", "i assumed a", "i had to assume",
	// first-person lack of capability (inability stems also cover cannot/could-not)
	"i do not have the ability", "i don't have the ability", "i lack the ability",
	"no way for me to", "not possible for me to",
	// stated uncertainty about the correctness of the produced result
	"may not be correct", "might not be correct", "may be incorrect", "might be incorrect",
	"this may not work", "this might not work", "not fully functional", "not fully working",
}

// inabilityStems are first-person "I cannot / can't / could not / am unable to /
// do not have" stems. Matching the STEM (not a fixed verb) generalizes over
// whatever action the model claims it could not perform — "analyze", "determine",
// "do", "complete", "verify", "see", etc. — so the detector is not defeated by
// re-phrasing (the chess case slipped a fixed "cannot analyze" list by writing
// "…which I cannot do without proper image analysis capabilities").
var inabilityStems = []string{
	"i cannot ", "i can't ", "i can not ", "i could not ", "i couldn't ",
	"i am unable to", "i'm unable to", "i was unable to", "i wasn't able to",
	"i was not able to", "i do not have", "i don't have", "unable to ",
	"without being able to",
}

// successNegationTails are negated phrasings that indicate SUCCESS, not an
// admission ("I could not find any remaining issues", "I cannot reproduce the
// bug"). When an inability stem is immediately followed by one of these, it is not
// treated as an admission, so a clean result is not misreported as INCOMPLETE.
var successNegationTails = []string{
	"find any", "found any", "find a ", "see any", "detect any", "identify any",
	"reproduce", "spot any", "locate any",
}

// narrativeMarkers flag a sentence as RETELLING a past exchange rather than
// reporting the outcome of the current objective, so an inability admission in
// that sentence is about THEN, not NOW. Grounded in a real false positive: a
// conversational recap ("You asked if I could work autonomously … I was honest
// that my sandbox had no repo … so I couldn't actually do it at the time") was
// downgraded to INCOMPLETE on the quoted "i couldn't " stem even though the
// current turn's objective (summarize where we left off) was fully met. Every
// marker is second-person or explicitly past-referential, so a first-person
// admission about the current task ("I couldn't complete the refactor") is
// never masked.
var narrativeMarkers = []string{
	"you asked", "you said", "you wanted", "you mentioned",
	"we discussed", "we talked", "we covered",
	"when we last", "last time", "last session", "previous session",
	"previous conversation", "earlier session", "earlier conversation",
	"at the time", "back then",
}

// stripQuoted removes spans enclosed in double quotes (straight or curly) or
// backticks, so an admission the model merely QUOTES — its own earlier message,
// a log line, an error string — cannot fire the detector. Only BALANCED spans
// are removed: an opening delimiter that never closes is kept as literal text,
// so a stray quote cannot swallow the rest of the message (and with it a
// genuine admission the detector must see). Single quotes are left alone: they
// are overwhelmingly apostrophes ("couldn't"), and treating them as quote
// delimiters would swallow the text between two contractions.
func stripQuoted(s string) string {
	var b strings.Builder
	var span strings.Builder // pending text since the open delimiter, kept if it never closes
	open := rune(0)
	for _, r := range s {
		switch {
		case open != 0:
			if (open == '"' && r == '"') || (open == '“' && r == '”') || (open == '`' && r == '`') {
				open = 0
				span.Reset()
				continue
			}
			span.WriteRune(r)
		case r == '"' || r == '“' || r == '`':
			open = r
			span.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(span.String()) // dangling delimiter: restore the span verbatim
	return b.String()
}

// admissionSentences splits lowered text into sentence-ish fragments so the
// detector judges each claim in its own context. Newlines split too (markdown
// bullets are separate claims); the exact boundaries only need to keep an
// admission next to its own narrative/negation context, not be grammatical.
func admissionSentences(lower string) []string {
	return strings.FieldsFunc(lower, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
}

// selfReportedIncompletion returns a short reason when the model's final text
// admits it guessed or could not meet the objective, else "". Case-insensitive.
// Matching is per sentence, after dropping quoted spans, and a sentence that
// retells a past exchange (narrativeMarkers) is skipped entirely — an admission
// must be the model's own report about the CURRENT objective, not general
// language that merely resembles one.
func selfReportedIncompletion(text string) string {
	for _, sentence := range admissionSentences(strings.ToLower(stripQuoted(text))) {
		if containsAny(sentence, narrativeMarkers) {
			continue
		}
		for _, phrase := range selfReportPhrases {
			if strings.Contains(sentence, phrase) {
				return selfReportReason(phrase)
			}
		}
		for _, stem := range inabilityStems {
			// Scan EVERY occurrence of the stem, not just the first: an earlier
			// success-negation use ("I could not find any examples, so I could not
			// implement it") must not mask a later genuine admission with the same stem.
			for start := 0; ; {
				rel := strings.Index(sentence[start:], stem)
				if rel < 0 {
					break
				}
				abs := start + rel
				tail := strings.TrimSpace(sentence[abs+len(stem):])
				if !hasAnyPrefix(tail, successNegationTails) {
					return selfReportReason(strings.TrimSpace(stem) + " …")
				}
				start = abs + len(stem)
			}
		}
	}
	return ""
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func selfReportReason(marker string) string {
	return `the final message admits the objective was not met ("` + marker + `")`
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// acceptanceVerificationNudge forces a TASK-GROUNDED acceptance check before a run
// may finalize as success. It explicitly rejects the three false-success patterns
// the bug-hunt surfaced: well-formed output treated as correct; pre-existing tests
// passing treated as the objective being met; and a result that merely matches a
// baseline the task asked to beat or improve. General — no task-specific content.
func acceptanceVerificationNudge(objective string) string {
	nudge := "Before this task can be marked complete, " + acceptanceNudgeMarker + " — " +
		"NOT the shape or format of your output, NOT that pre-existing tests pass, and NOT that your " +
		"result merely matches a baseline you were asked to beat or improve. " +
		"Re-read the original task, then run a concrete check that exercises the actual requirement: " +
		"execute the program or tests that demonstrate the required behavior, or directly probe the specific " +
		"thing the task asked you to produce, recover, fix, or optimize. " +
		"If that check passes, reply PASS and cite the evidence. " +
		"If it does not pass — or you cannot run such a check — say so plainly and keep working; do not claim success."
	if objective = capTaskObjective(objective); objective != "" {
		nudge += "\n\nTask objective: " + objective
	}
	return nudge
}

// toolFailureHintMarker is a stable substring for tests.
const toolFailureHintMarker = "kept failing with the same error"

type toolFailureRecord struct {
	count     int
	errSig    string
	hintShown bool
}

type toolFailureOutcome struct {
	InjectHint bool
	Stop       bool
	Count      int
}

// noOpToolOutcome reports what the no-op guard wants the loop to do after a turn
// whose tool calls were all verbatim repeats and changed nothing.
type noOpToolOutcome struct {
	InjectHint bool
	Stop       bool
	Count      int
}

// errorSignature normalizes a tool error to a short, comparable signature so
// repeated identical failures are detected while a genuinely different error
// resets the streak.
func errorSignature(output string) string {
	s := strings.ToLower(strings.Join(strings.Fields(output), " "))
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// toolFailureHint tells the model exactly how a tool's arguments must look after
// it has repeated the same failing call. Injected at most once per failure streak.
func toolFailureHint(toolName, schemaJSON, errOutput string) string {
	return "Your calls to the `" + toolName + "` tool " + toolFailureHintMarker + ":\n" +
		strings.TrimSpace(errOutput) +
		"\n\nThe `" + toolName + "` tool expects arguments matching this schema — match it exactly:\n" +
		strings.TrimSpace(schemaJSON) +
		"\n\nFix the arguments and try once more, or take a different approach."
}

// toolFailureStopAnswer is the final answer when the repeated-failure guard halts
// a run.
func toolFailureStopAnswer(toolName string, count int) string {
	return "Agent stopped: the `" + toolName + "` tool failed " + strconv.Itoa(count) +
		" times in a row with the same error, so I halted instead of looping further. " +
		"Please check the request or adjust the tool arguments."
}

// The no-output stop answer is assembled from these fixed parts (only the turn
// count varies). IsNoProgressStop matches all three so a legitimate message that
// merely quotes the marker substring is not misclassified as a failed empty run.
const (
	noOutputStopPrefix = "Agent stopped after "
	noOutputStopMarker = "with no output (no visible text and no tool calls)"
	noOutputStopSuffix = "to avoid consuming tokens without making progress."
)

// noOutputStopAnswer is the final answer returned when the no-output guard
// stops the run. The turn count is interpolated at the call site.
func noOutputStopAnswer(turns int) string {
	return noOutputStopPrefix + strconv.Itoa(turns) + " turns " + noOutputStopMarker + " " + noOutputStopSuffix
}

// guardStopTails are the exact tails (after "Agent stopped after <n> turns ") of
// the runaway-guard halt answers, so IsNoProgressStop recognizes them all. The
// repeated-failure guard uses a different prefix ("Agent stopped: …") and is not
// covered here.
var guardStopTails = []string{
	noOutputStopMarker + " " + noOutputStopSuffix,
	noActionStopTail,
	droppedCallStopTail,
	degenerateStopTail,
	noOpToolCallStopTail,
}

// IsNoProgressStop reports whether content IS a guardrail stop answer — a run
// halted by one of the runaway guards (no output, no action, or repeated no-op
// tool calls). It matches the EXACT structure the stop answers emit — prefix +
// "<int> turns " + one of the known tails — rather than just looking for the
// parts in order. A loose check (prefix && contains-marker && suffix) would
// misclassify a genuine assistant/tool message that merely quotes the marker amid
// other prose, which would wrongly hide a real session from /resume and skip its
// title generation.
func IsNoProgressStop(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, noOutputStopPrefix) {
		return false
	}
	rest := trimmed[len(noOutputStopPrefix):]
	const turnsSep = " turns "
	sep := strings.Index(rest, turnsSep)
	if sep < 0 {
		return false
	}
	// The text between the prefix and " turns " must be exactly the bare integer
	// count; anything else means this isn't the guard's own answer.
	if _, err := strconv.Atoi(rest[:sep]); err != nil {
		return false
	}
	// The remainder must be exactly one of the guard's own tails — no arbitrary
	// text wedged in.
	tail := rest[sep+len(turnsSep):]
	for _, known := range guardStopTails {
		if tail == known {
			return true
		}
	}
	return false
}

// Reminder markers are stable substrings used both to build the reminder text
// and to assert in tests that the right reminder was injected exactly once.
const (
	planNotCalledReminderMarker    = "you have not called todo_write"
	planStaleReminderMarker        = "haven't updated the plan via todo_write"
	toolOnlyProgressReminderMarker = "consecutive tool-only turns"
)

// planNotCalledReminder nudges the model to track a multi-step task with
// todo_write. Injected at most once per run.
func planNotCalledReminder() string {
	return "Reminder: this looks like a multi-step task and " + planNotCalledReminderMarker +
		". Use the todo_write tool to record the steps and keep progress visible. " +
		"Continue with your work after updating the plan."
}

// planStaleReminder nudges the model to refresh the plan (via todo_write) after
// a stretch of tool calls without a plan update. Injected at most once per
// stale interval.
func planStaleReminder(callsSinceUpdate int) string {
	return "Reminder: you've made " + strconv.Itoa(callsSinceUpdate) +
		" tool calls but " + planStaleReminderMarker +
		" in a while. Update the plan to reflect completed and remaining steps, then continue."
}

func toolOnlyProgressReminder(turns int) string {
	return "Reminder: you've made " + strconv.Itoa(turns) + " " + toolOnlyProgressReminderMarker +
		" without visible progress. Before calling more tools, summarize what you already know, state the next concrete step, and finish if you have enough information."
}

// noActionProgressReminderMarker is a stable substring for tests.
const noActionProgressReminderMarker = "consecutive turns with no visible output and no executed tool call"

// noActionProgressReminder tells a model that has spent several turns without
// acting to stop deliberating and take a concrete action.
func noActionProgressReminder(turns int) string {
	return "Reminder: your last " + strconv.Itoa(turns) + " " + noActionProgressReminderMarker +
		". Stop deliberating: call a tool now to make progress, or give your final answer."
}

// noActionStopMarker and droppedCallStopMarker are stable substrings for tests.
// The two exist so the halt message names what actually happened: a turn that
// produced nothing, or a turn whose tool call could not run (which may still have
// streamed visible text).
const (
	noActionStopMarker    = "without producing output or running a tool"
	droppedCallStopMarker = "whose tool calls could not run"
	degenerateStopMarker  = "whose output was stuck repeating itself"
)

// noActionStopTail / droppedCallStopTail are the fixed parts of noActionStopAnswer
// after the turn count. Both share the noOutputStopPrefix shape ("Agent stopped
// after <n> turns …") so IsNoProgressStop recognizes them structurally.
const (
	noActionStopTail    = noActionStopMarker + ", to avoid consuming tokens without making progress."
	droppedCallStopTail = droppedCallStopMarker + ", to avoid consuming tokens without making progress."
	degenerateStopTail  = degenerateStopMarker + ", to avoid consuming tokens without making progress."
)

// noActionStopAnswer is the final answer when the no-action guard halts the run.
func noActionStopAnswer(turns int, droppedCall bool) string {
	tail := noActionStopTail
	if droppedCall {
		tail = droppedCallStopTail
	}
	return noOutputStopPrefix + strconv.Itoa(turns) + " turns " + tail
}

// degenerateStopAnswer is the final answer when turns keep being cut off for
// repeating themselves.
func degenerateStopAnswer(turns int) string {
	return noOutputStopPrefix + strconv.Itoa(turns) + " turns " + degenerateStopTail
}

// guardStopAnswer builds the final answer for whichever runaway guard just
// tripped. When both the empty-turn and no-action counters reach their cap on the
// same turn the no-action reason is reported, which is the more specific one.
func guardStopAnswer(state *guardState, turns int) string {
	if state.noActionTurns >= maxNoActionTurns {
		if state.noActionDegenerate {
			return degenerateStopAnswer(turns)
		}
		return noActionStopAnswer(turns, state.noActionDroppedCall)
	}
	return noOutputStopAnswer(turns)
}

// guardState tracks the per-run signals the guardrails need. It is observable
// purely from tool-call names and per-turn output, matching what the loop holds.
type guardState struct {
	emptyTurns               int
	totalToolCalls           int
	toolCallsSincePlanUpdate int
	// turnsSincePlanUpdate counts turns (not individual tool calls) since the last
	// todo_write, so a plan that goes stale across many low-tool-call turns is
	// still caught — the tool-call counter alone can take many turns to trip when
	// the model makes only one call per turn.
	turnsSincePlanUpdate  int
	planEverCalled        bool
	notCalledReminderSent bool
	// staleReminderSent records whether the stale reminder has already fired for
	// the current stale interval. It is cleared when a plan update opens a new
	// interval, making the reminder one-shot per interval rather than per turn.
	staleReminderSent    bool
	toolOnlyTurns        int
	toolOnlyReminderSent bool
	// noActionTurns counts consecutive turns that produced no visible text and no
	// executed tool call — whether they streamed reasoning ("thinking out loud")
	// or emitted only malformed tool calls that could not run. It is deliberately
	// separate from emptyTurns: reasoning resets emptyTurns (the turn was live),
	// and a dropped-call turn skips the empty-turn guard entirely, so this is the
	// only counter that can bound a model that never actually acts.
	noActionTurns        int
	noActionReminderSent bool
	// noActionDroppedCall records whether the current no-action streak was last
	// extended by a turn whose tool call could not run. Such a turn may still have
	// streamed visible text, so the halt message must not claim it produced no
	// output. It is reset whenever the streak is extended by a silent turn.
	noActionDroppedCall bool
	// noActionDegenerate records that the streak was last extended by a turn cut
	// off for repeating itself, so the halt message says so.
	noActionDegenerate bool
	// planItemsPending is the number of remaining (pending/in_progress) items in
	// the most recent todo_write call, so the headless completion gate can tell
	// whether work is unfinished when the model stops without a tool call.
	planItemsPending int
	// toolFailures tracks consecutive same-error failures per tool, keyed by tool
	// name, so the loop can hint then halt instead of looping forever.
	toolFailures map[string]*toolFailureRecord
	// seenToolCalls is the set of call keys (tool name + arguments) issued so far
	// this run, so a repeat is recognized without keeping the bodies.
	seenToolCalls map[string]bool
	// noOpTurns counts consecutive turns whose every tool call was a verbatim
	// repeat of an earlier call in this run and which changed no file. Such a turn
	// cannot add information, which is the one runaway pattern the other counters
	// miss: the model emits a short preamble (resetting emptyTurns) and a call that
	// succeeds (resetting the failure streak), so it can loop indefinitely without
	// ever tripping a guard. Hinted at noOpToolCallHintAt, halted at
	// noOpToolCallStopAt.
	noOpTurns        int
	noOpReminderSent bool
}

func newGuardState() *guardState {
	return &guardState{
		toolFailures:  map[string]*toolFailureRecord{},
		seenToolCalls: map[string]bool{},
	}
}

// toolCallKey identifies a tool call by what makes it the same call: its name and
// its exact arguments. A repeat of this key cannot return anything the model has
// not already seen.
func toolCallKey(call kajicoderuntime.ToolCall) string {
	return call.Name + "\x00" + call.Arguments
}

// observeNoOpToolCalls tracks turns whose tool calls are all verbatim repeats of
// calls already issued in this run and which changed no file, and reports whether
// to nudge and/or halt.
//
// A turn with no tool calls does not touch the counter: stopping there is the
// model's decision, not a loop. A turn with at least one NEW call, or one that
// changed a file, resets it — new work is progress even when it is later
// repeated. `changedFiles` is the turn's mutated-file set, so an edit that
// happens to repeat a call is still progress.
func (state *guardState) observeNoOpToolCalls(calls []kajicoderuntime.ToolCall, changedFiles []string) noOpToolOutcome {
	if state.seenToolCalls == nil {
		state.seenToolCalls = map[string]bool{}
	}
	if len(calls) == 0 {
		return noOpToolOutcome{}
	}
	repeated := len(changedFiles) == 0
	for _, call := range calls {
		key := toolCallKey(call)
		if !state.seenToolCalls[key] {
			state.seenToolCalls[key] = true
			repeated = false
		}
	}
	if !repeated {
		state.noOpTurns = 0
		state.noOpReminderSent = false
		return noOpToolOutcome{}
	}
	state.noOpTurns++
	outcome := noOpToolOutcome{Count: state.noOpTurns}
	if state.noOpTurns >= noOpToolCallStopAt {
		outcome.Stop = true
		return outcome
	}
	if state.noOpTurns >= noOpToolCallHintAt && !state.noOpReminderSent {
		state.noOpReminderSent = true
		outcome.InjectHint = true
	}
	return outcome
}

// observeToolResult tracks repeated identical failures of a tool. A successful
// result clears that tool's failure streak. Returns whether to inject a one-shot
// corrective hint and/or stop the run.
func (state *guardState) observeToolResult(name string, failed bool, output string) toolFailureOutcome {
	if state.toolFailures == nil {
		state.toolFailures = map[string]*toolFailureRecord{}
	}
	if !failed {
		delete(state.toolFailures, name) // success resets the streak
		return toolFailureOutcome{}
	}
	sig := errorSignature(output)
	record := state.toolFailures[name]
	if record == nil || record.errSig != sig {
		record = &toolFailureRecord{count: 1, errSig: sig}
		state.toolFailures[name] = record
	} else {
		record.count++
	}
	outcome := toolFailureOutcome{Count: record.count}
	if record.count >= toolFailureStopAt {
		outcome.Stop = true
		return outcome
	}
	if record.count >= toolFailureHintAt && !record.hintShown {
		record.hintShown = true
		outcome.InjectHint = true
	}
	return outcome
}

// observeTurn updates counters from a turn's collected stream. It returns
// whether a runaway guard should stop the run.
//
// Callers must NOT invoke this for turns handled by the dropped-tool-call retry
// path; those are not "empty" in the runaway sense and are handled separately.
func (state *guardState) observeTurn(collected kajicoderuntime.CollectedStream) (stop bool) {
	hasToolCalls := len(collected.ToolCalls) > 0
	hasVisibleText := strings.TrimSpace(collected.Text) != ""
	hasReasoning := collected.HasReasoning || len(collected.ReasoningBlocks) > 0

	if hasToolCalls || hasVisibleText || hasReasoning {
		state.emptyTurns = 0
	} else {
		state.emptyTurns++
	}
	if hasToolCalls && !hasVisibleText {
		state.toolOnlyTurns++
	} else {
		state.toolOnlyTurns = 0
		state.toolOnlyReminderSent = false
	}
	// A turn with no visible text and no executed tool call is live but not
	// productive — the model is deliberating or emitting calls that cannot run.
	// Count the streak so it is bounded; visible text or an executed tool call
	// proves progress and resets it.
	if !hasToolCalls && !hasVisibleText {
		state.noActionTurns++
		state.noActionDroppedCall = false
		state.noActionDegenerate = false
	} else {
		state.noActionTurns = 0
		state.noActionReminderSent = false
	}

	// One turn has passed; the plan-update below resets this to 0 when the model
	// refreshes the plan this turn.
	state.turnsSincePlanUpdate++

	for _, call := range collected.ToolCalls {
		state.totalToolCalls++
		if call.Name == planToolName {
			state.planEverCalled = true
			state.toolCallsSincePlanUpdate = 0
			state.turnsSincePlanUpdate = 0
			// A fresh plan update opens a new stale interval.
			state.staleReminderSent = false
			// Record how many items remain so the completion gate knows whether
			// work is unfinished if the model later stops without a tool call.
			state.observePlanUpdate(call.Arguments)
		} else {
			state.toolCallsSincePlanUpdate++
		}
	}

	return state.emptyTurns >= maxEmptyTurns || state.noActionTurns >= maxNoActionTurns
}

// observeNoActionTurn counts a turn that could not act at all — currently only
// the dropped-tool-call path, which returns before observeTurn. It shares the
// noActionTurns streak with observeTurn so a model that only ever emits
// malformed calls is bounded by the same guard, and returns whether to stop. The
// turn is marked as a dropped call so the halt message says so: unlike a silent
// turn, it may have streamed visible text that was not a usable final answer.
func (state *guardState) observeNoActionTurn() bool {
	state.noActionTurns++
	state.noActionDroppedCall = true
	state.noActionDegenerate = false
	return state.noActionTurns >= maxNoActionTurns
}

// observeDegenerateTurn counts a turn that was cut off for repeating itself. It
// shares the noActionTurns streak, so such a model is stopped by the same cap.
func (state *guardState) observeDegenerateTurn() bool {
	state.noActionTurns++
	state.noActionDroppedCall = false
	state.noActionDegenerate = true
	return state.noActionTurns >= maxNoActionTurns
}

// pendingPlanItems reports whether the most recent todo_write call still has
// unfinished (pending/in_progress) items. False when no plan was ever recorded.
func (state *guardState) pendingPlanItems() bool {
	return state.planItemsPending > 0
}

// observePlanUpdate parses a todo_write call's raw arguments and records how
// many items are still remaining. Malformed arguments leave the prior count
// unchanged (best-effort — the plan panel itself tolerates the same).
func (state *guardState) observePlanUpdate(arguments string) {
	plan, ok := parseTaskPlan(arguments)
	if !ok {
		return
	}
	pending := 0
	for _, item := range plan {
		if planStatusRemaining(item.Status) {
			pending++
		}
	}
	state.planItemsPending = pending
}

func (state *guardState) progressReminder() string {
	if state.toolOnlyReminderSent || state.toolOnlyTurns < toolOnlyProgressReminderAt {
		return ""
	}
	state.toolOnlyReminderSent = true
	return toolOnlyProgressReminder(state.toolOnlyTurns)
}

// noActionReminder returns a one-shot nudge once the model has spent several
// consecutive turns without producing output or running a tool, or "" when none
// applies.
func (state *guardState) noActionReminder() string {
	if state.noActionReminderSent || state.noActionTurns < noActionReminderAt {
		return ""
	}
	state.noActionReminderSent = true
	return noActionProgressReminder(state.noActionTurns)
}

// noOpToolCallReminder tells the model that the calls it just made were verbatim
// repeats, so it stops re-issuing them and either changes approach or answers.
func noOpToolCallReminder(turns int) string {
	return "Your last " + strconv.Itoa(turns) + " turns called tools with arguments identical to earlier calls in this run, " +
		"so they returned nothing new. Do not repeat a call you have already made: " +
		"use the results you already have, change the arguments, or state the answer with what you know."
}

// noOpToolCallStopMarker is a stable substring for tests.
const noOpToolCallStopMarker = "repeated earlier tool calls verbatim"

// noOpToolCallStopTail is the fixed part of noOpToolCallStopAnswer after the
// "Agent stopped after <n> turns " prefix, so IsNoProgressStop recognizes it
// structurally like the other guard halt answers.
const noOpToolCallStopTail = "that " + noOpToolCallStopMarker +
	", which returned nothing new, to avoid consuming tokens without making progress."

// noOpToolCallStopAnswer is the final answer when the run halts on repeated
// identical calls.
func noOpToolCallStopAnswer(turns int) string {
	return noOutputStopPrefix + strconv.Itoa(turns) + " turns " + noOpToolCallStopTail
}

// planReminder returns a one-shot reminder message to inject before the next
// turn, or an empty string when no reminder applies. `turn` is 1-based (the
// number of turns completed so far).
func (state *guardState) planReminder(turn int) string {
	// STALE reminder takes priority: a long run without a plan update is the
	// stronger signal. Fires on either the tool-call streak OR a turn streak with
	// pending items (so a plan drifting stale across many low-call turns is caught,
	// while a fully-completed plan is left alone). One-shot per stale interval.
	if state.planEverCalled && !state.staleReminderSent &&
		(state.toolCallsSincePlanUpdate >= staleToolCallThreshold ||
			(state.turnsSincePlanUpdate >= stalePlanTurnThreshold && state.planItemsPending > 0)) {
		state.staleReminderSent = true
		return planStaleReminder(state.toolCallsSincePlanUpdate)
	}

	// NOT-CALLED reminder: by the end of planReminderTurn the model should have
	// called todo_write if it's doing a multi-step task (>=1 other tool call).
	// One-shot for the whole run.
	if !state.notCalledReminderSent &&
		!state.planEverCalled &&
		turn >= planReminderTurn &&
		state.totalToolCalls >= 1 {
		state.notCalledReminderSent = true
		return planNotCalledReminder()
	}

	return ""
}
