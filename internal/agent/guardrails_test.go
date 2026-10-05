package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// emptyTurn is a stream that produces no visible text and no tool calls.
func emptyTurn() []kajicoderuntime.StreamEvent {
	return []kajicoderuntime.StreamEvent{{Type: kajicoderuntime.StreamEventDone}}
}

// textTurn produces a turn with visible assistant text.
func textTurn(content string) []kajicoderuntime.StreamEvent {
	return []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: content},
		{Type: kajicoderuntime.StreamEventDone},
	}
}

// reasoningTurn produces live reasoning without visible assistant text.
func reasoningTurn(content string) []kajicoderuntime.StreamEvent {
	return []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventReasoning, Content: content},
		{Type: kajicoderuntime.StreamEventDone},
	}
}

// toolTurn produces a turn that calls a named tool with the given args JSON.
func toolTurn(callID string, toolName string, args string) []kajicoderuntime.StreamEvent {
	return []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: callID, ToolName: toolName},
		{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: callID, ArgumentsFragment: args},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: callID},
		{Type: kajicoderuntime.StreamEventDone},
	}
}

func countUserMessagesContaining(messages []kajicoderuntime.Message, needle string) int {
	count := 0
	for _, message := range messages {
		if message.Role == kajicoderuntime.MessageRoleUser && strings.Contains(message.Content, needle) {
			count++
		}
	}
	return count
}

func TestRunStopsAfterConsecutiveEmptyTurns(t *testing.T) {
	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			emptyTurn(),
			emptyTurn(),
			emptyTurn(),
			// A 4th turn exists but must never be requested.
			textTurn("should never reach here"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: tools.NewRegistry(),
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != maxEmptyTurns {
		t.Fatalf("expected exactly %d turns before the no-output guard fires, got %d", maxEmptyTurns, len(provider.requests))
	}
	if result.Turns != maxEmptyTurns {
		t.Fatalf("expected %d turns recorded, got %d", maxEmptyTurns, result.Turns)
	}
	if !strings.Contains(result.FinalAnswer, "no output") {
		t.Fatalf("expected no-output stop message, got %q", result.FinalAnswer)
	}
	if result.FinalAnswer == maxTurnsAnswer {
		t.Fatalf("no-output guard must stop before reaching maxTurns, got max-turns answer")
	}
}

func TestRunResetsEmptyTurnCounterOnVisibleOutput(t *testing.T) {
	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			emptyTurn(),
			emptyTurn(),
			textTurn("here is real progress"), // resets the counter and is the final answer
			emptyTurn(),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: tools.NewRegistry(),
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The text turn ends the run as the final answer (no tool calls), so we
	// stop at turn 3 — the empty counter was reset and never reached the cap.
	if len(provider.requests) != 3 {
		t.Fatalf("expected the run to end on the text turn (3 requests), got %d", len(provider.requests))
	}
	if result.FinalAnswer != "here is real progress" {
		t.Fatalf("expected the visible text as final answer, got %q", result.FinalAnswer)
	}
}

func TestRunResetsEmptyTurnCounterOnReasoning(t *testing.T) {
	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			reasoningTurn("thinking 1"),
			reasoningTurn("thinking 2"),
			reasoningTurn("thinking 3"),
			textTurn("done"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: tools.NewRegistry(),
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected reasoning-only turns to keep the run live until final answer, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 turns, got %d", len(provider.requests))
	}
}

func TestRunResetsEmptyTurnCounterOnToolCall(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			emptyTurn(),
			emptyTurn(),
			toolTurn("call-1", "read_file", `{"path":"notes.txt"}`), // resets counter
			emptyTurn(),
			emptyTurn(),
			textTurn("done"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Without a reset, three empty turns would stop the run at turn 3. Because
	// the tool call at turn 3 resets the counter, the run survives the later
	// empty turns and ends with the text answer at turn 6.
	if result.FinalAnswer != "done" {
		t.Fatalf("expected the counter to reset on a tool call and the run to finish, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 6 {
		t.Fatalf("expected 6 turns, got %d", len(provider.requests))
	}
}

// The whole point of #702's fix: the unknown-session error's normalized
// signature must not vary with the session id, so a model probing ids 1, 2,
// 3, … produces one repeated signature the failure guard can count. If the id
// leaked into the first 80 normalized chars, each probe would reset the streak
// and the halt would never fire.
func TestUnknownExecSessionErrorSignatureIsIDInvariant(t *testing.T) {
	a := errorSignature(tools.UnknownExecSessionError(1))
	b := errorSignature(tools.UnknownExecSessionError(999999))
	if a != b {
		t.Fatalf("unknown-session signature varies with id:\n  %q\n  %q", a, b)
	}
}

// End to end: with an id-invariant signature, probing a different unknown id
// each turn now trips the repeated-failure halt at toolFailureStopAt, where
// before the fix it never would.
func TestUnknownExecSessionProbingTripsFailureHalt(t *testing.T) {
	var state guardState
	var stoppedAt int
	for i := 1; i <= toolFailureStopAt; i++ {
		out := state.observeToolResult(tools.WriteStdinToolName, true, tools.UnknownExecSessionError(i))
		if out.Stop {
			stoppedAt = i
			break
		}
	}
	if stoppedAt != toolFailureStopAt {
		t.Fatalf("probing distinct unknown ids stopped at %d, want %d", stoppedAt, toolFailureStopAt)
	}
}

func TestGuardStateResetsToolOnlyStreakOnEmptyNonToolTurn(t *testing.T) {
	var state guardState
	toolOnly := kajicoderuntime.CollectedStream{
		ToolCalls: []kajicoderuntime.ToolCall{{ID: "call", Name: "read_file", Arguments: `{}`}},
	}

	for range toolOnlyProgressReminderAt - 1 {
		state.observeTurn(toolOnly)
	}
	state.observeTurn(kajicoderuntime.CollectedStream{})
	state.observeTurn(toolOnly)

	if reminder := state.progressReminder(); reminder != "" {
		t.Fatalf("expected empty non-tool turn to reset tool-only progress reminder, got %q", reminder)
	}
	if state.toolOnlyTurns != 1 {
		t.Fatalf("expected tool-only streak to restart at 1, got %d", state.toolOnlyTurns)
	}
}

func TestRunDoesNotCountDroppedToolCallTurnsAsEmpty(t *testing.T) {
	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			{
				{Type: kajicoderuntime.StreamEventToolCallDropped},
				{Type: kajicoderuntime.StreamEventDone},
			},
			{
				{Type: kajicoderuntime.StreamEventToolCallDropped},
				{Type: kajicoderuntime.StreamEventDone},
			},
			{
				{Type: kajicoderuntime.StreamEventToolCallDropped},
				{Type: kajicoderuntime.StreamEventDone},
			},
			textTurn("recovered"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: tools.NewRegistry(),
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Dropped-call turns take the retry path and must NOT be counted by the
	// no-output guard; the run continues to the text turn.
	if result.FinalAnswer != "recovered" {
		t.Fatalf("expected dropped-call turns to be handled by the retry path, got %q", result.FinalAnswer)
	}
	if len(provider.requests) != 4 {
		t.Fatalf("expected 4 turns, got %d", len(provider.requests))
	}
}

func TestRunInjectsPlanNotCalledReminderForMultiStepTask(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			toolTurn("call-1", "read_file", `{"path":"notes.txt"}`), // turn 1: other tool call
			toolTurn("call-2", "read_file", `{"path":"notes.txt"}`), // turn 2: still no update_plan
			toolTurn("call-3", "read_file", `{"path":"notes.txt"}`), // turn 3: reminder fires here
			textTurn("done"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	count := countUserMessagesContaining(result.Messages, planNotCalledReminderMarker)
	if count != 1 {
		t.Fatalf("expected exactly one not-called plan reminder, got %d", count)
	}
}

func TestRunDoesNotInjectPlanReminderForTrivialTask(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			toolTurn("call-1", "read_file", `{"path":"notes.txt"}`), // single tool call
			textTurn("done"), // immediately answers
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if count := countUserMessagesContaining(result.Messages, planNotCalledReminderMarker); count != 0 {
		t.Fatalf("expected no plan reminder for a trivial task, got %d", count)
	}
}

func TestRunDoesNotInjectNotCalledReminderWhenPlanUsed(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewTodoWriteTool())

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			toolTurn("call-1", "todo_write", `{"todos":[{"content":"step one"}]}`),
			toolTurn("call-2", "read_file", `{"path":"notes.txt"}`),
			textTurn("done"),
		},
	}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if count := countUserMessagesContaining(result.Messages, planNotCalledReminderMarker); count != 0 {
		t.Fatalf("expected no not-called reminder when update_plan was used, got %d", count)
	}
}

func TestRunInjectsStalePlanReminderAfterManyToolCalls(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewTodoWriteTool())

	// Turn 1 calls update_plan (so the not-called reminder never triggers), then
	// many read_file turns accumulate without another plan update. Each read
	// targets a DIFFERENT file: repeating one call verbatim is a no-op loop, which
	// the no-op guard halts before the stale-plan reminder would fire.
	turns := [][]kajicoderuntime.StreamEvent{
		toolTurn("plan-1", "todo_write", `{"todos":[{"content":"step one"}]}`),
	}
	for i := 0; i < staleToolCallThreshold+2; i++ {
		path := "notes" + strconv.Itoa(i) + ".txt"
		writeAgentTestFile(t, root+"/"+path, "alpha")
		turns = append(turns, toolTurn("call-"+strconv.Itoa(i), "read_file", `{"path":"`+path+`"}`))
	}
	turns = append(turns, textTurn("done"))

	provider := &mockProvider{turns: turns}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: len(turns) + 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if count := countUserMessagesContaining(result.Messages, planStaleReminderMarker); count < 1 {
		t.Fatalf("expected at least one stale plan reminder, got %d", count)
	}
}

func TestRunStalePlanReminderIsOneShotPerInterval(t *testing.T) {
	root := t.TempDir()
	writeAgentTestFile(t, root+"/notes.txt", "alpha")
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewTodoWriteTool())

	turns := [][]kajicoderuntime.StreamEvent{
		toolTurn("plan-1", "todo_write", `{"todos":[{"content":"step one"}]}`),
	}
	// Enough tool calls to exceed the threshold by a wide margin; the reminder
	// must fire once for the interval, not on every subsequent turn. Each read
	// targets a different file so the no-op guard does not halt the run first.
	for i := 0; i < staleToolCallThreshold*2; i++ {
		path := "notes" + strconv.Itoa(i) + ".txt"
		writeAgentTestFile(t, root+"/"+path, "alpha")
		turns = append(turns, toolTurn("call-"+strconv.Itoa(i), "read_file", `{"path":"`+path+`"}`))
	}
	turns = append(turns, textTurn("done"))

	provider := &mockProvider{turns: turns}

	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: len(turns) + 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	count := countUserMessagesContaining(result.Messages, planStaleReminderMarker)
	if count != 1 {
		t.Fatalf("expected the stale reminder to be one-shot per interval (exactly 1), got %d", count)
	}
}

func TestRunInjectsToolOnlyProgressReminder(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))

	// Distinct files per turn: the no-op guard must not be the thing under test
	// here, and repeating one call verbatim would trip it.
	turns := make([][]kajicoderuntime.StreamEvent, 0, toolOnlyProgressReminderAt+1)
	for i := 0; i < toolOnlyProgressReminderAt; i++ {
		path := "notes" + strconv.Itoa(i) + ".txt"
		writeAgentTestFile(t, root+"/"+path, "alpha")
		turns = append(turns, toolTurn("call-"+strconv.Itoa(i), "read_file", `{"path":"`+path+`"}`))
	}
	turns = append(turns, textTurn("done"))

	provider := &mockProvider{turns: turns}
	result, err := Run(context.Background(), "go", provider, Options{
		Registry: registry,
		MaxTurns: len(turns) + 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "done" {
		t.Fatalf("expected final answer, got %q", result.FinalAnswer)
	}
	if count := countUserMessagesContaining(result.Messages, toolOnlyProgressReminderMarker); count != 1 {
		t.Fatalf("expected one tool-only progress reminder, got %d", count)
	}
	found := false
	for _, message := range provider.requests[toolOnlyProgressReminderAt].Messages {
		if message.Role == kajicoderuntime.MessageRoleUser && strings.Contains(message.Content, toolOnlyProgressReminderMarker) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected reminder on request after tool-only streak, messages: %+v", provider.requests[toolOnlyProgressReminderAt].Messages)
	}
}

type alwaysFailingTool struct{}

func (alwaysFailingTool) Name() string        { return "flaky" }
func (alwaysFailingTool) Description() string { return "always fails for testing" }
func (alwaysFailingTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (alwaysFailingTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (alwaysFailingTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusError, Output: "Error: Invalid arguments for flaky: thing is required"}
}

func repeatedFlakyTurns(n int) [][]kajicoderuntime.StreamEvent {
	turn := []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "c", ToolName: "flaky"},
		{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "c"},
		{Type: kajicoderuntime.StreamEventDone},
	}
	turns := make([][]kajicoderuntime.StreamEvent, 0, n)
	for i := 0; i < n; i++ {
		turns = append(turns, turn)
	}
	return turns
}

func TestRunStopsAfterRepeatedToolFailures(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(alwaysFailingTool{})
	provider := &mockProvider{turns: repeatedFlakyTurns(10)}

	result, err := Run(context.Background(), "go", provider, Options{Registry: registry, MaxTurns: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.FinalAnswer, "flaky") || !strings.Contains(result.FinalAnswer, "failed") {
		t.Fatalf("expected repeated-failure stop answer, got %q", result.FinalAnswer)
	}
	// Must halt at the failure cap, NOT loop to maxTurns.
	if len(provider.requests) != toolFailureStopAt {
		t.Fatalf("expected stop at %d failures, made %d requests", toolFailureStopAt, len(provider.requests))
	}
}

func TestRunInjectsToolFailureHintWithSchema(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(alwaysFailingTool{})
	provider := &mockProvider{turns: repeatedFlakyTurns(10)}

	if _, err := Run(context.Background(), "go", provider, Options{Registry: registry, MaxTurns: 12}); err != nil {
		t.Fatal(err)
	}
	// After the 2nd failure a one-shot hint is injected, so the 3rd turn's request
	// carries it (with the tool schema).
	found := false
	for _, m := range provider.requests[2].Messages {
		if m.Role == kajicoderuntime.MessageRoleUser && strings.Contains(m.Content, toolFailureHintMarker) {
			found = true
			if !strings.Contains(m.Content, "object") { // schema rendered
				t.Errorf("hint should include the tool schema, got %q", m.Content)
			}
		}
	}
	if !found {
		t.Fatalf("expected a tool-failure hint on the 3rd turn, messages: %+v", provider.requests[2].Messages)
	}
}

// stubTool is a minimal registered tool that always succeeds with a small,
// changeless result — the shape of a repeated `echo` in a runaway session.
type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub" }
func (s stubTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: true}
}
func (s stubTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectNone, Permission: tools.PermissionAllow}
}
func (s stubTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: "ok"}
}

// A model that re-issues the SAME call verbatim turn after turn, changing no
// file, gains nothing each time. The run must halt at the no-op ceiling instead
// of looping to MaxTurns.
func TestRunStopsOnRepeatedNoOpCalls(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "probe"})

	turns := make([][]kajicoderuntime.StreamEvent, 0, 20)
	for i := range 20 {
		turns = append(turns, toolTurn("call-"+strconv.Itoa(i), "probe", `{"n":1}`))
	}
	provider := &mockProvider{turns: turns}

	result, err := Run(context.Background(), "go", provider, Options{Registry: registry, MaxTurns: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer == maxTurnsAnswer {
		t.Fatal("a repeated no-op loop must halt before MaxTurns, got the max-turns answer")
	}
	if !strings.Contains(result.FinalAnswer, noOpToolCallStopMarker) {
		t.Fatalf("expected the no-op stop answer, got %q", result.FinalAnswer)
	}
	// The first turn's call is new; the halt lands on the noOpToolCallStopAt-th
	// consecutive repeat, so the run makes exactly that many more requests.
	if want := noOpToolCallStopAt + 1; len(provider.requests) != want {
		t.Fatalf("expected the run to halt after %d requests, got %d", want, len(provider.requests))
	}
}

// The guard must not cut short a run that keeps issuing genuinely NEW calls: a
// distinct call is progress even when its result is small and nothing changed.
func TestRunDoesNotStopOnDistinctCalls(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(stubTool{name: "probe"})

	turns := make([][]kajicoderuntime.StreamEvent, 0, 20)
	for i := range 20 {
		turns = append(turns, toolTurn("call-"+strconv.Itoa(i), "probe", `{"n":`+strconv.Itoa(i)+`}`))
	}
	provider := &mockProvider{turns: turns}

	result, err := Run(context.Background(), "go", provider, Options{Registry: registry, MaxTurns: 8})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.FinalAnswer, noOpToolCallStopMarker) {
		t.Fatalf("distinct calls must not trip the no-op guard, got %q", result.FinalAnswer)
	}
}

// A NEW call is progress even when it is later repeated, and an edit is progress
// even when its call repeats: neither may accumulate toward the no-op halt.
func TestObserveNoOpToolCallsResetsOnNewCallAndOnEdit(t *testing.T) {
	var state guardState
	first := kajicoderuntime.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}

	// The first call is new, so it seeds the set rather than counting as a no-op.
	state.observeNoOpToolCalls([]kajicoderuntime.ToolCall{first}, nil)
	for range noOpToolCallHintAt {
		state.observeNoOpToolCalls([]kajicoderuntime.ToolCall{first}, nil)
	}
	if state.noOpTurns != noOpToolCallHintAt {
		t.Fatalf("expected %d no-op turns, got %d", noOpToolCallHintAt, state.noOpTurns)
	}

	// A call never seen before resets the streak.
	state.observeNoOpToolCalls([]kajicoderuntime.ToolCall{{Name: "read_file", Arguments: `{"path":"b"}`}}, nil)
	if state.noOpTurns != 0 {
		t.Fatalf("a new call must reset the no-op streak, got %d", state.noOpTurns)
	}

	// A repeated call that changed a file is progress too.
	state.observeNoOpToolCalls([]kajicoderuntime.ToolCall{first}, nil)
	state.observeNoOpToolCalls([]kajicoderuntime.ToolCall{first}, []string{"a"})
	if state.noOpTurns != 0 {
		t.Fatalf("a turn that changed a file must reset the no-op streak, got %d", state.noOpTurns)
	}
}

// A turn with no tool calls is the model's decision to stop, not a loop, so it
// must not accumulate toward the no-op halt.
func TestObserveNoOpToolCallsIgnoresEmptyTurn(t *testing.T) {
	var state guardState
	for range noOpToolCallStopAt + 2 {
		if out := state.observeNoOpToolCalls(nil, nil); out.Stop {
			t.Fatal("a turn with no tool calls must never trip the no-op halt")
		}
	}
	if state.noOpTurns != 0 {
		t.Fatalf("expected the no-op streak to stay 0, got %d", state.noOpTurns)
	}
}
