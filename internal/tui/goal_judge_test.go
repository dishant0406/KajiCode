package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestGoalCompletesWhenTheReviewerAgreesWithFinish(t *testing.T) {
	provider := &goalProvider{
		turns:    [][]kajicoderuntime.StreamEvent{finishTurn},
		verdicts: []string{`{"verdict":"done","reason":"tests pass"}`},
	}
	m, cmd := goalRunModel(provider, nil).handleGoalCommand("ship it")
	m, cmd = step[agentResponseMsg](t, m, cmd)
	if m.goal == nil || !m.goal.judging {
		t.Fatal("the goal should wait for the reviewer, not complete on the model's word")
	}
	m, _ = step[goalJudgedMsg](t, m, cmd)

	if m.goal != nil || !transcriptContains(m.transcript, "Goal complete") {
		t.Fatalf("goal = %v", m.goal)
	}
	if answer, done := rowIndex(m.transcript, "all done"), rowIndex(m.transcript, "Goal complete"); answer < 0 || done < answer {
		t.Fatalf("the answer (row %d) must come before the goal line (row %d)", answer, done)
	}
	if !strings.Contains(lastUserText(provider.requests[0]), "ship it") {
		t.Fatal("the first run should carry the objective")
	}
}

func TestGoalContinuesWhenTheReviewerRejectsFinish(t *testing.T) {
	provider := &goalProvider{
		turns: [][]kajicoderuntime.StreamEvent{finishTurn, finishTurn},
		verdicts: []string{
			`{"verdict":"continue","reason":"the tests were never run"}`,
			`{"verdict":"done","reason":"tests pass"}`,
		},
	}
	m, cmd := goalRunModel(provider, nil).handleGoalCommand("ship it")
	m, cmd = step[agentResponseMsg](t, m, cmd)
	m, cmd = step[goalJudgedMsg](t, m, cmd) // the reviewer rejects; run 2 starts
	if m.goal == nil || m.goal.rounds != 1 || !transcriptContains(m.transcript, "the tests were never run") {
		t.Fatalf("goal = %v", m.goal)
	}
	m, cmd = step[agentResponseMsg](t, m, cmd)
	m, _ = step[goalJudgedMsg](t, m, cmd)

	if m.goal != nil || !transcriptContains(m.transcript, "Goal complete") {
		t.Fatalf("goal = %v", m.goal)
	}
	if !provider.sawContinuePromptWith("the tests were never run") {
		t.Fatal("run 2 should carry the reviewer's reason")
	}
}

func TestParseGoalVerdict(t *testing.T) {
	for _, text := range []string{
		`{"verdict":"done","reason":"ok"}`,
		"Here you go:\n{\"verdict\": \"done\", \"reason\": \"ok\"}\n",
	} {
		if verdict, _, err := parseGoalVerdict(text); err != nil || verdict != "done" {
			t.Fatalf("%q: verdict = %q, err = %v", text, verdict, err)
		}
	}
	if verdict, reason, err := parseGoalVerdict(`{"verdict":"continue","reason":" run the tests "}`); err != nil || verdict != "continue" || reason != "run the tests" {
		t.Fatalf("verdict = %q, reason = %q, err = %v", verdict, reason, err)
	}
	for _, text := range []string{"", "done", `{"verdict":"maybe"}`, `{"verdict":`} {
		if _, _, err := parseGoalVerdict(text); err == nil {
			t.Fatalf("%q should be an error", text)
		}
	}
}

func TestApplyGoalVerdict(t *testing.T) {
	judging := func() model {
		m := goalTestModel(t, "ship it")
		m.goal.judging = true
		return m
	}
	// "done" completes the goal.
	m := judging()
	m, notice := m.applyGoalVerdict(goalJudgedMsg{goal: m.goal, verdict: "done"})
	if m.goal != nil || notice != "Goal complete." {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
	// A verdict for a goal that was stopped meanwhile is ignored.
	stopped := goalTestModel(t, "ship it")
	stopped.goal = nil
	if _, notice := stopped.applyGoalVerdict(goalJudgedMsg{verdict: "done"}); notice != "" {
		t.Fatalf("notice = %q", notice)
	}
	// So is a verdict nobody asked for.
	notJudging := goalTestModel(t, "ship it")
	if m, notice := notJudging.applyGoalVerdict(goalJudgedMsg{goal: notJudging.goal, verdict: "done"}); m.goal == nil || notice != "" {
		t.Fatal("a verdict nobody asked for must be ignored")
	}
	// Rejections that make no progress still stop the goal.
	m = judging()
	for i := 0; i < maxGoalStalls; i++ {
		m.goal.judging = true
		m.queuedMessage = ""
		m, notice = m.applyGoalVerdict(goalJudgedMsg{goal: m.goal, verdict: "continue", reason: "not yet"})
	}
	if m.goal != nil || !strings.Contains(notice, "not yet") || !strings.Contains(notice, "stopped") {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
}

// With a provider, a finish call waits for the reviewer; without one it completes.
func TestAdvanceGoalSendsFinishToTheReviewer(t *testing.T) {
	m := goalRunModel(&goalProvider{}, nil)
	m.goal = &goalState{objective: "ship it"}
	m, notice, cmd := m.advanceGoal(agentResponseMsg{finished: true})
	if cmd == nil || notice != "" || !m.goal.judging {
		t.Fatalf("cmd = %v, notice = %q, goal = %v", cmd != nil, notice, m.goal)
	}
}

func toolResultRows(count int, text string) []transcriptRow {
	rows := []transcriptRow{{kind: rowAssistant, text: "talk"}}
	for i := 0; i < count; i++ {
		rows = append(rows, transcriptRow{kind: rowToolResult, tool: "bash", status: tools.StatusOK, text: text})
	}
	return rows
}

// A test or build prints its verdict last, so a long result keeps both ends.
func TestEvidenceLinesKeepTheStartAndTheEndOfAResult(t *testing.T) {
	long := "START" + strings.Repeat(" noise", 100) + " FAIL: 3 tests"
	lines := evidenceLines(toolResultRows(1, long))
	if len(lines) != 1 || !strings.Contains(lines[0], "START") || !strings.HasSuffix(lines[0], "FAIL: 3 tests") {
		t.Fatalf("lines = %q", lines)
	}
	if len([]rune(lines[0])) > goalEvidenceHead+goalEvidenceTail+30 {
		t.Fatalf("the line is too long: %d runes", len([]rune(lines[0])))
	}
	if got := evidenceLines(toolResultRows(1, "ok")); len(got) != 1 || !strings.HasSuffix(got[0], ": ok") {
		t.Fatalf("a short result is kept whole: %q", got)
	}
	if len(evidenceLines([]transcriptRow{{kind: rowAssistant, text: "talk"}})) != 0 {
		t.Fatal("rows that are not tool results are not evidence")
	}
}

func TestLastLinesKeepsTheNewest(t *testing.T) {
	got := lastLines([]string{"a", "b", "c"}, 2)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("got %q", got)
	}
	if got := lastLines([]string{"a"}, 2); len(got) != 1 {
		t.Fatalf("got %q", got)
	}
}

// The reviewer sees the work of every run of the goal, not only the last one: a
// run that only calls finish after an earlier run did the work must not look empty.
func TestReviewerSeesToolResultsFromEarlierRuns(t *testing.T) {
	provider := &goalProvider{verdicts: []string{`{"verdict":"done","reason":"ok"}`}}
	m := goalRunModel(provider, nil)
	m.goal = &goalState{objective: "ship it"}
	m, _, _ = m.advanceGoal(agentResponseMsg{rows: toolResultRows(2, "go test: PASS"), madeProgress: true}) // run 1, stopped by the harness
	m.queuedMessage = ""
	finishRun := agentResponseMsg{finished: true, rows: []transcriptRow{{kind: rowAssistant, final: true, text: "all done"}}}
	_, _, cmd := m.advanceGoal(finishRun) // run 2 only calls finish
	judged, ok := find[goalJudgedMsg](cmd)
	if !ok || judged.verdict != "done" {
		t.Fatalf("judged = %v", judged)
	}
	var request kajicoderuntime.CompletionRequest
	for _, r := range provider.requests {
		if strings.Contains(r.Messages[0].Content, "You check whether an agent finished a goal") {
			request = r
		}
	}
	if text := lastUserText(request); !strings.Contains(text, "go test: PASS") || !strings.Contains(text, "all done") {
		t.Fatalf("the reviewer should see the earlier work and the summary, got %q", text)
	}
}

// A verdict that belongs to a goal that was replaced meanwhile must not touch the
// new goal.
func TestAStaleVerdictDoesNotCompleteANewGoal(t *testing.T) {
	m := goalTestModel(t, "old goal")
	oldGoal := m.goal
	m.goal = &goalState{objective: "new goal", judging: true}
	m, notice := m.applyGoalVerdict(goalJudgedMsg{goal: oldGoal, verdict: "done"})
	if m.goal == nil || !m.goal.judging || notice != "" {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
}

// A reviewer that fails (a rate limit, a bad answer) must not complete the goal:
// the goal stays so the user can try again or accept the work.
func TestAReviewerThatFailsDoesNotCompleteTheGoal(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.goal.judging = true
	m, notice := m.applyGoalVerdict(goalJudgedMsg{goal: m.goal, err: errors.New("429 too many requests")})
	if m.goal == nil || m.goal.judging || !strings.Contains(notice, "429") || !strings.Contains(notice, "/goal resume") {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
}

// A failed /resume keeps the live goal: the user is still in the same session.
func TestAFailedResumeKeepsTheLiveGoal(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.resumeSeq, m.resumeInFlight = 1, true
	updated, _ := m.applyResumePrepared(resumePreparedMsg{seq: 1, err: errors.New("no such session")})
	if updated.goal == nil {
		t.Fatal("a failed resume must not end the goal")
	}
}

// Cancelling a /loop run is not the user stopping the goal.
func TestCancellingALoopRunKeepsTheGoal(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.pending, m.activeLoopID = true, "L1"
	m.cancelRun()
	if m.goal == nil {
		t.Fatal("cancelling a loop run must not end the goal")
	}
}
