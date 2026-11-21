package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// When the model calls finish during a goal, a separate model call checks the
// claim before the goal is complete. The model saying it is done is not proof, so
// the reviewer also sees the tool calls the run really made.
const (
	goalJudgeTimeout    = 60 * time.Second
	goalEvidenceCalls   = 30  // most recent tool results shown to the reviewer
	goalEvidenceHead    = 60  // characters kept from the start of each tool result
	goalEvidenceTail    = 100 // and from its end, where a test or build prints its verdict
	goalJudgeSystemText = "You check whether an agent finished a goal. You get the goal, the agent's closing summary, " +
		"and the most recent results of the tool calls it made while working on the goal; earlier work may not be shown. " +
		"The agent saying it is done is not evidence. Look for proof in the tool calls: files changed, checks run and passing. " +
		"Reply with one line of JSON and nothing else: {\"verdict\":\"done\"|\"continue\",\"reason\":\"one short sentence\"}. " +
		"When in doubt, answer continue."
)

// goalJudgedMsg carries the reviewer's answer back to the Update loop.
type goalJudgedMsg struct {
	goal         *goalState // the goal that was checked; a verdict for any other goal is ignored
	verdict      string     // "done" or "continue"
	reason       string
	madeProgress bool // whether the run the reviewer checked did new work
	err          error
}

// judgeGoalCmd asks the reviewer about the run that just called finish.
func (m model) judgeGoalCmd(run agentResponseMsg) tea.Cmd {
	provider, goal := m.provider, m.goal
	objective, summary := goal.objective, finalAnswerText(run.rows)
	evidence := "(no tool calls so far)"
	if len(goal.evidence) > 0 {
		evidence = strings.Join(goal.evidence, "\n")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), goalJudgeTimeout)
		defer cancel()
		verdict, reason, err := judgeGoal(ctx, provider, objective, summary, evidence)
		return goalJudgedMsg{goal: goal, verdict: verdict, reason: reason, madeProgress: run.madeProgress, err: err}
	}
}

// judgeGoal returns the reviewer's verdict ("done" or "continue") and reason.
func judgeGoal(ctx context.Context, provider kajicoderuntime.Provider, objective, summary, evidence string) (string, string, error) {
	request := kajicoderuntime.CompletionRequest{Messages: []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: goalJudgeSystemText},
		{Role: kajicoderuntime.MessageRoleUser, Content: "Goal:\n" + objective +
			"\n\nClosing summary:\n" + summary + "\n\nTool call results (oldest first):\n" + evidence},
	}}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return "", "", err
	}
	collected := kajicoderuntime.CollectStreamWithOptions(ctx, stream, kajicoderuntime.CollectOptions{})
	if collected.Error != "" {
		return "", "", errors.New(collected.Error)
	}
	return parseGoalVerdict(collected.Text)
}

// parseGoalVerdict reads the reviewer's one-line JSON answer.
func parseGoalVerdict(text string) (string, string, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return "", "", fmt.Errorf("the reviewer did not answer with JSON: %q", sessions.CutRunes(text, 80))
	}
	var answer struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &answer); err != nil {
		return "", "", fmt.Errorf("the reviewer's answer is not valid JSON: %w", err)
	}
	if answer.Verdict != "done" && answer.Verdict != "continue" {
		return "", "", fmt.Errorf("the reviewer's verdict %q is not done or continue", answer.Verdict)
	}
	return answer.Verdict, strings.TrimSpace(answer.Reason), nil
}

// applyGoalVerdict completes the goal or sends the model back to work. It returns
// the line to show the user. A reviewer that cannot answer does not complete the
// goal: it stays, and the user decides.
func (m model) applyGoalVerdict(msg goalJudgedMsg) (model, string) {
	if m.goal == nil || m.goal != msg.goal || !m.goal.judging {
		return m, "" // the goal was stopped or replaced while the reviewer was working
	}
	m.goal.judging = false
	if msg.err != nil {
		return m, "The reviewer could not check the finish call: " + msg.err.Error() +
			"\nSend /goal resume to try again, or /goal stop to accept the work as it is."
	}
	if msg.verdict == "done" {
		m.goal = nil
		return m.recordGoal(), "Goal complete."
	}
	m, pause := m.restartGoal(msg.madeProgress, msg.reason)
	notice := "Reviewer: the goal is not met yet: " + msg.reason
	if pause != "" {
		notice += "\n" + pause
	}
	return m, notice
}

// finalAnswerText is the run's final assistant text.
func finalAnswerText(rows []transcriptRow) string {
	answer := ""
	for _, row := range rows {
		if row.kind == rowAssistant && row.final {
			answer = row.text
		}
	}
	return answer
}

// evidenceLines turns a run's tool results into one line each: the start and the
// end of the result, since a test or build prints its verdict last.
func evidenceLines(rows []transcriptRow) []string {
	lines := []string{}
	for _, row := range rows {
		if row.kind != rowToolResult {
			continue
		}
		text := []rune(strings.Join(strings.Fields(row.text), " "))
		if len(text) > goalEvidenceHead+goalEvidenceTail {
			text = []rune(string(text[:goalEvidenceHead]) + " … " + string(text[len(text)-goalEvidenceTail:]))
		}
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", row.tool, row.status, string(text)))
	}
	return lines
}

// lastLines keeps the newest n lines.
func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
