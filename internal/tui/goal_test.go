package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func goalTestModel(t *testing.T, objective string) model {
	t.Helper()
	m := newModel(context.Background(), Options{
		Cwd: "repo", ProviderName: "openai", ModelName: "gpt-4.1", Registry: tools.NewRegistry(),
	})
	m.goal = &goalState{objective: objective}
	return m
}

func TestAdvanceGoalCompletesWhenTheModelCalledFinish(t *testing.T) {
	m, notice, _ := goalTestModel(t, "ship it").advanceGoal(agentResponseMsg{finished: true, madeProgress: true})
	if m.goal != nil || m.queuedMessage != "" || !strings.Contains(notice, "Goal complete") {
		t.Fatalf("goal = %v, queued = %q, notice = %q", m.goal, m.queuedMessage, notice)
	}
}

func TestAdvanceGoalRestartsARunTheHarnessStopped(t *testing.T) {
	m, notice, _ := goalTestModel(t, "ship it").advanceGoal(agentResponseMsg{madeProgress: true})
	if m.goal == nil || m.goal.rounds != 1 || !strings.Contains(m.queuedMessage, "ship it") || notice != "" {
		t.Fatalf("goal = %v, queued = %q, notice = %q", m.goal, m.queuedMessage, notice)
	}
}

// A run that only repeated earlier calls, or only talked, did no new work.
func TestAdvanceGoalPausesAfterRunsThatDidNoNewWork(t *testing.T) {
	m := goalTestModel(t, "ship it")
	notice := ""
	for i := 0; i < maxGoalStalls; i++ {
		m.queuedMessage = ""
		m, notice, _ = m.advanceGoal(agentResponseMsg{turnTools: 4, madeProgress: false})
	}
	if m.goal != nil || !strings.Contains(notice, "no new work") {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
}

func TestAdvanceGoalResetsTheStallCountWhenARunDidNewWork(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m, _, _ = m.advanceGoal(agentResponseMsg{})
	m, _, _ = m.advanceGoal(agentResponseMsg{})
	m, _, _ = m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.goal == nil || m.goal.stalls != 0 {
		t.Fatalf("goal = %v", m.goal)
	}
}

func TestAdvanceGoalPausesAtTheRoundLimit(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.goal.rounds = maxGoalRounds - 1
	m, notice, _ := m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.goal != nil || !strings.Contains(notice, "50 restarts") {
		t.Fatalf("goal = %v, notice = %q", m.goal, notice)
	}
}

func TestAdvanceGoalStopsOnAnErrorAndKeepsAUserMessageFirst(t *testing.T) {
	m, notice, _ := goalTestModel(t, "ship it").advanceGoal(agentResponseMsg{err: errors.New("boom")})
	if m.goal != nil || m.queuedMessage != "" || !strings.Contains(notice, "stopped") {
		t.Fatalf("goal = %v, queued = %q, notice = %q", m.goal, m.queuedMessage, notice)
	}
	m = goalTestModel(t, "ship it")
	m.queuedMessage = "change of plan"
	m, _, _ = m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.queuedMessage != "change of plan" {
		t.Fatalf("queued = %q, the user's message must go first", m.queuedMessage)
	}
}

// Plan mode has no finish tool, so a goal there could never end by itself.
func TestGoalDoesNotRunInPlanMode(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.goal = nil
	m.planMode = true
	m, cmd := m.handleGoalCommand("ship it")
	if m.goal != nil || cmd != nil || !transcriptContains(m.transcript, "plan mode") {
		t.Fatalf("goal = %v", m.goal)
	}
	m = goalTestModel(t, "ship it")
	m.planMode = true
	m, notice, _ := m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.goal != nil || m.queuedMessage != "" || !strings.Contains(notice, "plan mode") {
		t.Fatalf("goal = %v, queued = %q, notice = %q", m.goal, m.queuedMessage, notice)
	}
}

// Runs that are not the goal's leave it alone: a /loop iteration, a plan
// approval, a run waiting for the user.
func TestAdvanceGoalIgnoresRunsThatAreNotTheGoals(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.activeLoopID = "L1"
	m, _, _ = m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.goal.rounds != 0 || m.queuedMessage != "" {
		t.Fatalf("a loop run must not restart the goal: %v %q", m.goal, m.queuedMessage)
	}
	m = goalTestModel(t, "ship it")
	m, _, _ = m.advanceGoal(agentResponseMsg{planApproved: true})
	if m.goal.rounds != 0 || m.queuedMessage != "" {
		t.Fatalf("a plan approval must not restart the goal: %v %q", m.goal, m.queuedMessage)
	}
}

func TestAdvanceGoalDoesNothingWithoutAGoal(t *testing.T) {
	m := goalTestModel(t, "x")
	m.goal = nil
	m, notice, _ := m.advanceGoal(agentResponseMsg{madeProgress: true})
	if m.queuedMessage != "" || notice != "" {
		t.Fatalf("queued = %q, notice = %q", m.queuedMessage, notice)
	}
}

func TestGoalCommandStatusAndStop(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m, _ = m.handleGoalCommand("")
	if !transcriptContains(m.transcript, "ship it") {
		t.Fatal("status should show the objective")
	}
	m, _ = m.handleGoalCommand("stop")
	if m.goal != nil {
		t.Fatal("stop should clear the goal")
	}
}

func TestCancellingARunStopsTheGoal(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.pending = true
	m.cancelRun()
	if m.goal != nil {
		t.Fatal("cancel should clear the goal")
	}
}

// Ctrl+C while idle is not a cancelled run, so it does not end the goal.
func TestCancelWhileIdleKeepsTheGoal(t *testing.T) {
	m := goalTestModel(t, "ship it")
	m.cancelRun()
	if m.goal == nil {
		t.Fatal("an idle cancel must not clear the goal")
	}
}

func TestNewSessionClearsTheGoal(t *testing.T) {
	m := goalTestModel(t, "ship it").startNewSession()
	if m.goal != nil {
		t.Fatal("/new should clear the goal")
	}
}

// goalProvider plays the agent's turns in order and answers the reviewer from a
// separate list, so the order of the two kinds of request does not matter.
type goalProvider struct {
	turns    [][]kajicoderuntime.StreamEvent
	verdicts []string
	requests []kajicoderuntime.CompletionRequest
}

func (provider *goalProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	provider.requests = append(provider.requests, request)
	var events []kajicoderuntime.StreamEvent
	first := ""
	if len(request.Messages) > 0 {
		first = request.Messages[0].Content
	}
	if first == sessions.TitleSystemPrompt { // the TUI names the session after a run
		events = []kajicoderuntime.StreamEvent{{Type: kajicoderuntime.StreamEventText, Content: "A title"}, {Type: kajicoderuntime.StreamEventDone}}
	} else if strings.Contains(first, "You check whether an agent finished a goal") {
		events = []kajicoderuntime.StreamEvent{{Type: kajicoderuntime.StreamEventText, Content: provider.verdicts[0]}, {Type: kajicoderuntime.StreamEventDone}}
		provider.verdicts = provider.verdicts[1:]
	} else {
		events = provider.turns[0]
		provider.turns = provider.turns[1:]
	}
	ch := make(chan kajicoderuntime.StreamEvent, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (provider *goalProvider) sawContinuePromptWith(want string) bool {
	for _, request := range provider.requests {
		if text := lastUserText(request); strings.Contains(text, "Continue working toward the goal") && strings.Contains(text, want) {
			return true
		}
	}
	return false
}

var (
	talkTurn = []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: "Next I will do X."},
		{Type: kajicoderuntime.StreamEventDone},
	}
	finishTurn = []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "f", ToolName: tools.FinishToolName},
		{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "f", ArgumentsFragment: `{"summary":"all done"}`},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "f"},
		{Type: kajicoderuntime.StreamEventDone},
	}
)

func goalRunModel(provider kajicoderuntime.Provider, store *sessions.Store) model {
	registry := tools.NewRegistry()
	registry.Register(tools.NewFinishTool())
	return newModel(context.Background(), Options{
		Cwd: "repo", ProviderName: "openai", ModelName: "gpt-4.1",
		Provider: provider, Registry: registry, SessionStore: store,
		AgentOptions: agent.Options{Registry: registry, RequireFinish: true},
	})
}

// find runs a possibly-batched command and returns the first message of type T,
// skipping the other commands a run completion starts.
func find[T any](cmd tea.Cmd) (T, bool) {
	var zero T
	if cmd == nil {
		return zero, false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if found, ok := find[T](sub); ok {
				return found, true
			}
		}
		return zero, false
	}
	found, ok := msg.(T)
	return found, ok
}

// step feeds the next message of type T from cmd into the model.
func step[T any](t *testing.T, m model, cmd tea.Cmd) (model, tea.Cmd) {
	t.Helper()
	msg, ok := find[T](cmd)
	if !ok {
		t.Fatalf("no %T in the command", msg)
	}
	updated, next := m.Update(msg)
	return updated.(model), next
}

// /goal starts a run with the objective. The model's finish call is checked by
// the reviewer, and "done" completes the goal after the answer.
// A reviewer that says "continue" sends the model back to work with its reason,
// and a later finish that the reviewer accepts completes the goal.
// When the harness stops a run (the model only talked), the goal starts the next
// run by itself.
func TestGoalRestartsAfterAHarnessStop(t *testing.T) {
	provider := &goalProvider{
		turns:    [][]kajicoderuntime.StreamEvent{talkTurn, talkTurn, talkTurn, talkTurn, finishTurn},
		verdicts: []string{`{"verdict":"done","reason":"ok"}`},
	}
	m, cmd := goalRunModel(provider, nil).handleGoalCommand("ship it")
	m, cmd = step[agentResponseMsg](t, m, cmd)
	if m.goal == nil || m.goal.rounds != 1 {
		t.Fatalf("run 1 should restart the goal, goal = %v", m.goal)
	}
	m, cmd = step[agentResponseMsg](t, m, cmd)
	m, _ = step[goalJudgedMsg](t, m, cmd)
	if m.goal != nil || !provider.sawContinuePromptWith("ship it") {
		t.Fatalf("goal = %v", m.goal)
	}
}

// With a provider, a finish call waits for the reviewer; without one it completes.
// The goal is saved in the session and read back, and a cleared goal reads as none.
func TestGoalIsSavedInTheSessionAndRestored(t *testing.T) {
	store := testSessionStore(t)
	provider := &goalProvider{turns: [][]kajicoderuntime.StreamEvent{finishTurn}}
	m, cmd := goalRunModel(provider, store).handleGoalCommand("ship it")
	if cmd == nil {
		t.Fatal("/goal should start a run")
	}
	got := goalFromEvents(readOnlySessionEvents(t, store))
	if got == nil || got.objective != "ship it" || got.rounds != 0 {
		t.Fatalf("restored goal = %v", got)
	}
	m, _ = m.handleGoalCommand("stop")
	if got := goalFromEvents(readOnlySessionEvents(t, store)); got != nil {
		t.Fatalf("a stopped goal should not come back: %v", got)
	}
	_ = m
}

func TestGoalFromEventsReadsTheLatestState(t *testing.T) {
	event := func(payload string) sessions.Event {
		return sessions.Event{Type: sessions.EventGoal, Payload: json.RawMessage(payload)}
	}
	events := []sessions.Event{
		event(`{"objective":"first","rounds":1,"stalls":0}`),
		{Type: sessions.EventMessage, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)},
		event(`{"objective":"second","rounds":4,"stalls":2}`),
	}
	got := goalFromEvents(events)
	if got == nil || got.objective != "second" || got.rounds != 4 || got.stalls != 2 || got.judging {
		t.Fatalf("goal = %v", got)
	}
	if goalFromEvents(nil) != nil {
		t.Fatal("no events, no goal")
	}
}

func TestGoalResumeRestartsTheRestoredGoal(t *testing.T) {
	provider := &goalProvider{turns: [][]kajicoderuntime.StreamEvent{finishTurn}}
	m := goalRunModel(provider, nil)
	m.goal = &goalState{objective: "ship it", rounds: 2}
	m, cmd := m.handleGoalCommand("resume")
	if cmd == nil || m.goal.rounds != 2 {
		t.Fatalf("resume should start a run and keep the restart count, goal = %v", m.goal)
	}
	step[agentResponseMsg](t, m, cmd)
	if !provider.sawContinuePromptWith("ship it") {
		t.Fatal("the resumed run should carry the continue prompt")
	}
	m = goalTestModel(t, "x")
	m.goal = nil
	if _, cmd := m.handleGoalCommand("resume"); cmd != nil {
		t.Fatal("nothing to resume")
	}
}

// rowIndex returns the index of the first transcript row containing want, or -1.
func rowIndex(rows []transcriptRow, want string) int {
	for i, row := range rows {
		if strings.Contains(row.text, want) {
			return i
		}
	}
	return -1
}

func lastUserText(request kajicoderuntime.CompletionRequest) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == kajicoderuntime.MessageRoleUser {
			return request.Messages[i].Content
		}
	}
	return ""
}

// Resuming a session brings its goal back, tells the user, and does not start a
// run by itself.
func TestResumeRestoresTheGoalWithoutStartingARun(t *testing.T) {
	m := goalTestModel(t, "an old goal from this window")
	m.resumeSeq, m.resumeInFlight = 1, true
	events := []sessions.Event{{Type: sessions.EventGoal, Payload: json.RawMessage(`{"objective":"ship it","rounds":3,"stalls":1}`)}}

	updated, cmd := m.applyResumePrepared(resumePreparedMsg{seq: 1, session: &sessions.Metadata{SessionID: "s1"}, events: events})

	if updated.goal == nil || updated.goal.objective != "ship it" || updated.goal.rounds != 3 {
		t.Fatalf("goal = %v", updated.goal)
	}
	if !transcriptContains(updated.transcript, "Goal restored: ship it") || updated.pending || cmd != nil {
		t.Fatalf("resume must show the goal and wait for /goal resume (pending = %v)", updated.pending)
	}
}

// Resuming a session that had no goal leaves none behind from the old window.
func TestResumeWithoutAGoalClearsTheOldOne(t *testing.T) {
	m := goalTestModel(t, "an old goal from this window")
	m.resumeSeq, m.resumeInFlight = 1, true
	updated, _ := m.applyResumePrepared(resumePreparedMsg{seq: 1, session: &sessions.Metadata{SessionID: "s2"}})
	if updated.goal != nil {
		t.Fatalf("goal = %v", updated.goal)
	}
}
