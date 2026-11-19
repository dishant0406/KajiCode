package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// finishRegistry holds the two tools the finish rule reads: todo_write (open
// todo items) and finish itself.
func finishRegistry() *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(tools.NewTodoWriteTool())
	registry.Register(tools.NewFinishTool())
	return registry
}

func finishTurn(summary string) []kajicoderuntime.StreamEvent {
	return toolTurn("fin", tools.FinishToolName, `{"summary":"`+summary+`"}`)
}

func runWithFinish(t *testing.T, turns ...[]kajicoderuntime.StreamEvent) (Result, *mockProvider) {
	t.Helper()
	provider := &mockProvider{turns: turns}
	result, err := Run(context.Background(), "build the reel", provider, Options{
		Registry:      finishRegistry(),
		MaxTurns:      20,
		RequireFinish: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result, provider
}

// The real endings from the sessions that motivated the rule. Each one ended a
// run. None may end it now.
var prematureEndings = []string{
	"Doing that now.",
	"Now the Blender props — 7 new builders, rendered one at a time. Let me look at an existing builder to match the quality bar.",
	"Next step: compose the mp4 from the current minimal stations so the file on disk matches the code.",
	"Stopping here to report honestly rather than keep iterating silently.",
	"Reply **yes** and I go on to the look, storyboard, voice, props and render. Or tell me what to change.",
	"I’ll compare the actual query and source-count paths, then trace your Anime Kitsu addon’s exact endpoint.",
	"I found two actual gaps beyond the unpublished config. I’m fixing both with strict episode checks, then I’ll run the picker-facing tests.",
	"I’ll get this to a compiled, tested implementation—not stop at adapter names. First I’m locating the exact current errors.",
}

// A text-only reply in the middle of a run (tools used, todo items open) gets a
// reminder, and the run goes on to finish.
func TestFinishRuleRemindsOnPrematureEndingsMidRun(t *testing.T) {
	for _, ending := range prematureEndings {
		result, provider := runWithFinish(t,
			planTurn("completed", "pending"),
			textTurn(ending),
			planTurn("completed", "completed"),
			finishTurn("reel is ready"),
		)
		if len(provider.requests) != 4 {
			t.Fatalf("ending %q: run stopped after %d requests, want 4", ending, len(provider.requests))
		}
		if !someRequestContains(provider.requests, noToolReminderMarker) {
			t.Fatalf("ending %q: no reminder was injected", ending)
		}
		if result.FinalAnswer != "reel is ready" || result.StopNotice != "" {
			t.Fatalf("ending %q: final=%q notice=%q", ending, result.FinalAnswer, result.StopNotice)
		}
	}
}

// The case that stopped session kaji_20261119041549_1e956dfa three times: a new
// run (every "continue" is one) whose FIRST reply is text-only. No tool has run
// yet in this run, so nothing about the run itself says the task is unfinished.
func TestFinishRuleRemindsOnTheFirstReplyOfARun(t *testing.T) {
	for _, ending := range prematureEndings {
		result, provider := runWithFinish(t,
			textTurn(ending),
			finishTurn("done"),
		)
		if len(provider.requests) != 2 {
			t.Fatalf("ending %q: run stopped after %d requests, want 2", ending, len(provider.requests))
		}
		if !someRequestContains(provider.requests, noToolReminderMarker) {
			t.Fatalf("ending %q: no reminder was injected", ending)
		}
		if result.FinalAnswer != "done" || result.StopNotice != "" {
			t.Fatalf("ending %q: final=%q notice=%q", ending, result.FinalAnswer, result.StopNotice)
		}
	}
}

// openTodosHistory is the conversation of an earlier run: a todo_write call with
// 3 of 5 items open. It stands for a session resumed after a restart, when the
// todo_write tool's in-memory list is empty.
func openTodosHistory() []kajicoderuntime.Message {
	args := `{"todos":[` +
		`{"content":"a","status":"completed"},{"content":"b","status":"completed"},` +
		`{"content":"c","status":"in_progress"},{"content":"d","status":"pending"},{"content":"e","status":"pending"}]}`
	return []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "port torrentio"},
		{Role: kajicoderuntime.MessageRoleAssistant, ToolCalls: []kajicoderuntime.ToolCall{{ID: "old", Name: "todo_write", Arguments: args}}},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "old", Content: "ok"},
	}
}

// After a restart the in-memory todo list is empty, but the history still holds
// the last todo_write call. The open items must be seen from there.
func TestFinishRuleSeesTodosFromEarlierHistory(t *testing.T) {
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		textTurn("I’m fixing both, then I’ll run the tests."),
		finishTurn("too early"),
		finishTurn("really done"),
	}}
	result, err := Run(context.Background(), "continue", provider, Options{
		Registry:        finishRegistry(),
		MaxTurns:        10,
		RequireFinish:   true,
		InitialMessages: openTodosHistory(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 3 || result.FinalAnswer != "really done" {
		t.Fatalf("requests=%d final=%q", len(provider.requests), result.FinalAnswer)
	}
	if !someRequestContains(provider.requests, "still has 3 open item(s)") {
		t.Fatal("the reminder did not report the 3 open items from the history")
	}
	if !someRequestContains(provider.requests, "3 todo item(s) are still open") {
		t.Fatal("finish was not refused once for the 3 open items from the history")
	}
}

func TestOpenTodoCountPrefersHistoryAndFallsBackToTheTool(t *testing.T) {
	registry := finishRegistry()
	if got := openTodoCount(nil, registry); got != 0 {
		t.Fatalf("no history and an empty tool list = %d, want 0", got)
	}
	if got := openTodoCount(openTodosHistory(), registry); got != 3 {
		t.Fatalf("history with 3 open items = %d, want 3", got)
	}
	// History with a malformed todo_write call falls back to the tool's list.
	bad := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleAssistant,
		ToolCalls: []kajicoderuntime.ToolCall{{ID: "x", Name: "todo_write", Arguments: "{not json"}}}}
	if got := openTodoCount(bad, registry); got != 0 {
		t.Fatalf("malformed history with an empty tool list = %d, want 0", got)
	}
}

// A plain question also goes through finish now. It can answer in the summary,
// so it still costs one model call.
func TestFinishRuleLetsAPlainQuestionAnswerThroughFinish(t *testing.T) {
	result, provider := runWithFinish(t, finishTurn("The function parses the config file."))
	if len(provider.requests) != 1 {
		t.Fatalf("answer through finish took %d requests, want 1", len(provider.requests))
	}
	if result.FinalAnswer != "The function parses the config file." || result.StopNotice != "" {
		t.Fatalf("final=%q notice=%q", result.FinalAnswer, result.StopNotice)
	}
}

// After maxNoToolReminders ignored reminders in a row the run stops and tells
// the user why.
func TestFinishRuleStopsAfterIgnoredReminders(t *testing.T) {
	result, provider := runWithFinish(t,
		planTurn("completed", "completed"),
		textTurn("I will continue."), textTurn("I will continue."), textTurn("I will continue."),
		textTurn("I will continue."), textTurn("never requested"),
	)
	if want := 1 + maxNoToolReminders + 1; len(provider.requests) != want {
		t.Fatalf("got %d requests, want %d", len(provider.requests), want)
	}
	if result.StopNotice != noToolStopNotice() || result.FinalAnswer != "I will continue." {
		t.Fatalf("final=%q notice=%q", result.FinalAnswer, result.StopNotice)
	}
}

// A tool call between text-only replies resets the reminder count.
func TestFinishRuleToolCallResetsReminderCount(t *testing.T) {
	result, provider := runWithFinish(t,
		planTurn("completed", "completed"),
		textTurn("one"), textTurn("two"), textTurn("three"),
		planTurn("completed", "completed"),
		textTurn("four"), textTurn("five"), textTurn("six"),
		finishTurn("done"),
	)
	if len(provider.requests) != 9 || result.FinalAnswer != "done" || result.StopNotice != "" {
		t.Fatalf("requests=%d final=%q notice=%q", len(provider.requests), result.FinalAnswer, result.StopNotice)
	}
}

// finish is refused once while todo items are open; a second call is accepted,
// because the model has the last word on whether the task is done.
func TestFinishRefusedOnceForOpenTodos(t *testing.T) {
	result, provider := runWithFinish(t,
		planTurn("completed", "pending", "pending"),
		finishTurn("too early"),
		finishTurn("really done"),
	)
	if len(provider.requests) != 3 || result.FinalAnswer != "really done" {
		t.Fatalf("requests=%d final=%q", len(provider.requests), result.FinalAnswer)
	}
	if !someRequestContains(provider.requests, "2 todo item(s) are still open") {
		t.Fatal("the refusal did not name the open todo items")
	}
}

func TestFinishRefusedWithEmptySummaryOrSiblingCall(t *testing.T) {
	withSibling := []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "plan", ToolName: "todo_write"},
		{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "plan", ArgumentsFragment: `{"todos":[{"content":"a","status":"completed"}]}`},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "plan"},
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "fin", ToolName: tools.FinishToolName},
		{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "fin", ArgumentsFragment: `{"summary":"done"}`},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "fin"},
		{Type: kajicoderuntime.StreamEventDone},
	}
	result, provider := runWithFinish(t, withSibling, finishTurn("  "), finishTurn("done alone"))
	if len(provider.requests) != 3 || result.FinalAnswer != "done alone" {
		t.Fatalf("requests=%d final=%q", len(provider.requests), result.FinalAnswer)
	}
	if !someRequestContains(provider.requests, "only tool call in a reply") {
		t.Fatal("finish next to another tool call was not refused")
	}
	if !someRequestContains(provider.requests, "non-empty `summary`") {
		t.Fatal("finish with a blank summary was not refused")
	}
}

// Surfaces show and store the answer from the text stream.
func TestFinishStreamsSummaryAsText(t *testing.T) {
	var streamed strings.Builder
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{finishTurn("all checks pass")}}
	_, err := Run(context.Background(), "fix the bug", provider, Options{
		Registry:      finishRegistry(),
		MaxTurns:      5,
		RequireFinish: true,
		OnText:        func(delta string) { streamed.WriteString(delta) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "all checks pass" {
		t.Fatalf("streamed %q, want the finish summary", streamed.String())
	}
}

func advertisesFinish(request kajicoderuntime.CompletionRequest) bool {
	for _, tool := range request.Tools {
		if tool.Name == tools.FinishToolName {
			return true
		}
	}
	return false
}

// finish is advertised, and the rule is on, only for runs that require it and
// are not in plan mode. Every other run keeps the text-reply ending.
func TestFinishRuleOnlyAppliesWhenRequired(t *testing.T) {
	cases := []struct {
		name    string
		options Options
		want    bool
	}{
		{"required", Options{RequireFinish: true}, true},
		{"not required", Options{}, false},
		{"plan mode", Options{RequireFinish: true, PlanMode: true}, false},
		{"finish disabled", Options{RequireFinish: true, DisabledTools: []string{tools.FinishToolName}}, false},
	}
	for _, tc := range cases {
		provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
			planTurn("completed", "completed"),
			textTurn("Doing that now."),
			finishTurn("done"),
		}}
		tc.options.Registry = finishRegistry()
		tc.options.MaxTurns = 10
		result, err := Run(context.Background(), "do the task", provider, tc.options)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := advertisesFinish(provider.requests[0]); got != tc.want {
			t.Fatalf("%s: finish advertised = %v, want %v", tc.name, got, tc.want)
		}
		wantRequests, wantFinal := 2, "Doing that now."
		if tc.want {
			wantRequests, wantFinal = 3, "done"
		}
		if len(provider.requests) != wantRequests || result.FinalAnswer != wantFinal {
			t.Fatalf("%s: requests=%d final=%q, want %d %q", tc.name, len(provider.requests), result.FinalAnswer, wantRequests, wantFinal)
		}
	}
}

func TestMaxTurnsSetsStopNotice(t *testing.T) {
	provider := &mockProvider{turns: [][]kajicoderuntime.StreamEvent{
		planTurn("pending"), planTurn("pending"), textTurn("summary of remaining work"),
	}}
	result, err := Run(context.Background(), "long task", provider, Options{Registry: finishRegistry(), MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopNotice != maxTurnsStopNotice(2) {
		t.Fatalf("StopNotice = %q, want the tool-turn limit notice", result.StopNotice)
	}
}
