package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/sessions"
)

// A goal keeps the agent working across runs. When a run ends and the model did
// not call finish (the harness stopped it: reminders spent, a guard, the turn
// limit), the next run starts by itself. When the model does call finish, a
// reviewer checks the claim (goal_judge.go) before the goal is complete. The goal
// is saved in the session, so /resume brings it back. It also ends when the user
// stops it or cancels a run, or when it stalls; a stopped goal is gone.
const (
	maxGoalRounds = 50 // automatic restarts before the goal pauses
	maxGoalStalls = 3  // restarts in a row where the run did no new work
)

type goalState struct {
	objective string
	rounds    int      // automatic restarts so far
	stalls    int      // restarts in a row where the run did no new work
	judging   bool     // the reviewer is checking a finish call; not saved
	evidence  []string // recent tool results across the goal's runs, for the reviewer; not saved
}

func goalKickoffPrompt(objective string) string {
	return "Goal: " + objective + "\n\n" +
		"Work on this until it is fully done. Do not stop to ask what to do next. " +
		"When the whole goal is done and checked, call finish with a summary. " +
		"If you cannot continue without the user, call ask_user."
}

func goalContinuePrompt(objective string) string {
	return "Continue working toward the goal: " + objective + "\n\n" +
		"Take the next concrete step with a tool call. Check your work against the goal. " +
		"Call finish only when the whole goal is met."
}

// handleGoalCommand runs `/goal <objective>`, `/goal` (status), `/goal resume`
// and `/goal stop`.
func (m model) handleGoalCommand(args string) (model, tea.Cmd) {
	args = strings.TrimSpace(args)
	switch strings.ToLower(args) {
	case "":
		if m.goal == nil {
			return m.appendGoalSystem("No goal is active. Start one with /goal <objective>."), nil
		}
		return m.appendGoalSystem(fmt.Sprintf("Goal: %s (restarted %d of %d times)", m.goal.objective, m.goal.rounds, maxGoalRounds)), nil
	case "stop", "clear":
		if m.goal == nil {
			return m.appendGoalSystem("No goal is active."), nil
		}
		m.goal = nil
		return m.recordGoal().appendGoalSystem("Goal stopped."), nil
	case "resume":
		if m.goal == nil {
			return m.appendGoalSystem("No goal to resume. Start one with /goal <objective>."), nil
		}
		if m.pending {
			return m.appendGoalSystem("Wait for the current run to finish, then resume the goal."), nil
		}
		m.goal.judging = false
		m.promptEchoOverride = "/goal resume"
		return m.launchPrompt(goalContinuePrompt(m.goal.objective))
	}
	if m.pending {
		return m.appendGoalSystem("Wait for the current run to finish, then start the goal."), nil
	}
	if m.planMode {
		return m.appendGoalSystem("Turn plan mode off first: a goal ends with the finish tool, and plan mode has none."), nil
	}
	m.goal = &goalState{objective: args}
	m.promptEchoOverride = "/goal " + args
	m, cmd := m.launchPrompt(goalKickoffPrompt(args))
	return m.recordGoal(), cmd // after the launch: the first prompt creates the session
}

// advanceGoal runs when a run ends. It finishes the goal, pauses it, asks the
// reviewer to check a finish call, or queues the next run. It returns the line to
// show the user ("" for none), which the caller shows after the run's own rows so
// the answer comes first, and a command to run (the reviewer call).
func (m model) advanceGoal(msg agentResponseMsg) (model, string, tea.Cmd) {
	goal := m.goal
	if goal == nil || msg.planApproved || m.activeLoopID != "" || m.pendingAskUser != nil || m.pendingPermission != nil {
		return m, "", nil // no goal, or this run is not the goal's: a loop run, or waiting for the user
	}
	goal.evidence = lastLines(append(goal.evidence, evidenceLines(msg.rows)...), goalEvidenceCalls)
	switch {
	case msg.err != nil:
		m.goal = nil
		return m.recordGoal(), "Goal stopped: the run ended with an error. Start it again with /goal " + goal.objective, nil
	case m.planMode:
		m.goal = nil
		return m.recordGoal(), "Goal stopped: plan mode is on, and plan mode has no finish tool. Start it again with /goal <objective> after leaving plan mode.", nil
	case msg.finished && m.provider != nil:
		goal.judging = true
		return m, "", m.judgeGoalCmd(msg)
	case msg.finished:
		m.goal = nil
		return m.recordGoal(), "Goal complete.", nil
	}
	m, notice := m.restartGoal(msg.madeProgress, "")
	return m, notice, nil
}

// restartGoal counts a restart, then pauses the goal or queues the next run.
// reviewerReason, when set, is why the reviewer sent the model back to work.
func (m model) restartGoal(madeProgress bool, reviewerReason string) (model, string) {
	goal := m.goal
	goal.rounds++
	if madeProgress {
		goal.stalls = 0
	} else {
		goal.stalls++
	}
	switch {
	case goal.stalls >= maxGoalStalls:
		m.goal = nil
		return m.recordGoal(), fmt.Sprintf("Goal stopped: %d runs in a row did no new work. Start it again with /goal <objective>, with more detail if it needs it.", maxGoalStalls)
	case goal.rounds >= maxGoalRounds:
		m.goal = nil
		return m.recordGoal(), fmt.Sprintf("Goal stopped after %d restarts. Start it again with /goal <objective> if more work remains.", maxGoalRounds)
	}
	m = m.recordGoal()
	if m.hasQueuedMessage() {
		return m, "" // the user's own message goes first
	}
	m.queuedMessage = goalContinuePrompt(goal.objective)
	if reviewerReason != "" {
		m.queuedMessage += "\n\nA reviewer checked your finish call and says the goal is not met yet: " + reviewerReason
	}
	m.promptEchoOverride = fmt.Sprintf("goal: continuing (restart %d)", goal.rounds)
	return m, ""
}

func (m model) appendGoalSystem(text string) model {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	return m
}

// recordGoal saves the goal's state in the session, so /resume can bring it
// back. An empty objective means no goal.
func (m model) recordGoal() model {
	payload := map[string]any{"objective": ""}
	if m.goal != nil {
		payload = map[string]any{"objective": m.goal.objective, "rounds": m.goal.rounds, "stalls": m.goal.stalls}
	}
	m, rows := m.appendSessionEvents([]pendingSessionEvent{{Type: sessions.EventGoal, Payload: payload}})
	for _, row := range rows {
		m.transcript = appendTranscriptRow(m.transcript, row)
	}
	return m
}

// goalFromEvents returns the goal a session ended with, or nil.
func goalFromEvents(events []sessions.Event) *goalState {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != sessions.EventGoal {
			continue
		}
		payload := sessionPayload(events[i])
		objective := strings.TrimSpace(payloadString(payload, "objective"))
		if objective == "" {
			return nil
		}
		rounds, _ := strconv.Atoi(payloadString(payload, "rounds"))
		stalls, _ := strconv.Atoi(payloadString(payload, "stalls"))
		return &goalState{objective: objective, rounds: rounds, stalls: stalls}
	}
	return nil
}
