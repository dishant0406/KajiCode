package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func sizedTestModel(width int) model {
	m := newModel(context.Background(), Options{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	// Ack the one-time header print so later settles aren't queued behind it.
	updated, _ = updated.(model).Update(flushedMsg{})
	return updated.(model)
}

func TestSettledRowsAdvanceFrontierAndLeaveLiveView(t *testing.T) {
	m := sizedTestModel(80)
	m.transcript = appendRow(m.transcript, rowUser, "hello there")
	m.transcript = appendRow(m.transcript, rowSystem, "noted")

	next, cmd := m.settleTranscript()
	if next.flushed != len(next.transcript) {
		t.Fatalf("expected frontier at %d, got %d", len(next.transcript), next.flushed)
	}
	if cmd == nil {
		t.Fatal("expected a scrollback print command for the settled rows")
	}
	view := viewString(next.View())
	if strings.Contains(view, "hello there") || strings.Contains(view, "noted") {
		t.Fatalf("flushed rows must not re-render in the live view, got %q", view)
	}
}

func TestAltScreenKeepsSettledRowsInManagedView(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(model)
	m.transcript = appendRow(m.transcript, rowUser, "hello there")
	m.transcript = appendRow(m.transcript, rowSystem, "noted")

	next, cmd := m.settleTranscript()
	if cmd != nil {
		t.Fatal("alt-screen mode should not print rows into native scrollback")
	}
	if next.flushed != 0 {
		t.Fatalf("alt-screen mode should keep the flush frontier unchanged, got %d", next.flushed)
	}
	view := viewString(next.View())
	if !strings.Contains(view, "hello there") || !strings.Contains(view, "noted") {
		t.Fatalf("settled rows should remain in the managed alt-screen view, got %q", view)
	}
}

func TestRunningToolCallBlocksFrontierUntilResult(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = true
	m.activeRunID = 3
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolCall, id: "call_1", text: "tool call: read_file", tool: "read_file", runID: 3,
	})

	next, _ := m.settleTranscript()
	if next.flushed != 1 { // welcome row settles; the live call must block
		t.Fatalf("running tool call should block the frontier at 1, got %d", next.flushed)
	}

	next.transcript = appendTranscriptRow(next.transcript, transcriptRow{
		kind: rowToolResult, id: "call_1", text: "tool result: read_file ok", tool: "read_file",
		status: tools.StatusOK, detail: "data", runID: 3,
	})
	settled, cmd := next.settleTranscript()
	if settled.flushed != len(settled.transcript) {
		t.Fatalf("resolved call should settle through, frontier=%d rows=%d", settled.flushed, len(settled.transcript))
	}
	if cmd == nil {
		t.Fatal("expected the result card to flush to scrollback")
	}
}

func TestOrphanToolCallSettlesAfterRunEnds(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = false // run is over; the unresolved call is an orphan
	m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
		kind: rowToolCall, id: "call_9", text: "tool call: bash", tool: "bash", runID: 2,
	})
	next, _ := m.settleTranscript()
	if next.flushed != len(next.transcript) {
		t.Fatalf("orphan call should settle once its run is over, frontier=%d", next.flushed)
	}
}

func TestClearResetsFlushFrontier(t *testing.T) {
	m := sizedTestModel(80)
	m.transcript = appendRow(m.transcript, rowUser, "first")
	m, _ = m.settleTranscript()
	if m.flushed == 0 {
		t.Fatal("precondition: something flushed")
	}

	m.input.SetValue("/clear")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if len(next.transcript) != 2 || next.transcript[0].kind != rowWelcome {
		t.Fatalf("expected /clear to reset transcript to welcome + note, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, "/new") {
		t.Fatalf("expected /clear to point users to /new, got %#v", next.transcript)
	}
	if next.flushed != len(next.transcript) {
		t.Fatalf("expected frontier reset to match cleared transcript (%d rows), got %d", len(next.transcript), next.flushed)
	}
}

func TestEscCancellationLeavesVisibleMarker(t *testing.T) {
	m := sizedTestModel(80)
	m.pending = true
	m.activeRunID = 5
	m.runCancel = func() {}
	m.streamingText = []byte("half an answer")

	updated, _ := m.Update(testKey(tea.KeyEsc))
	next := updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if !transcriptContains(next.transcript, "Run cancelled.") {
		t.Fatalf("expected visible cancellation marker, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, "half an answer") {
		t.Fatalf("expected the partial streamed answer to be preserved, got %#v", next.transcript)
	}
	if len(next.streamingText) != 0 {
		t.Fatal("streaming text should be cleared after cancel")
	}
}

func TestRepeatedProviderToolIDsKeepDistinctRows(t *testing.T) {
	rows := initialTranscript()
	// Same provider id in two different runs (Gemini-style) must NOT dedupe.
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "gemini_tool_0", runID: 1, tool: "grep"})
	rows = appendTranscriptRow(rows, transcriptRow{kind: rowToolCall, id: "gemini_tool_0", runID: 2, tool: "grep"})
	if countTranscriptRows(rows, rowToolCall) != 2 {
		t.Fatalf("run-scoped dedup should keep both calls, got %#v", rows)
	}
	// And within one run, the ordinal suffix disambiguates repeats.
	if got := effectiveToolRowID("gemini_tool_0", 2); got != "gemini_tool_0#2" {
		t.Fatalf("effectiveToolRowID = %q", got)
	}
	if got := effectiveToolRowID("gemini_tool_0", 1); got != "gemini_tool_0" {
		t.Fatalf("first occurrence should keep the raw id, got %q", got)
	}
}

func TestComposerHistoryRecall(t *testing.T) {
	m := sizedTestModel(80)
	for _, prompt := range []string{"first input", "second input"} {
		m.input.SetValue(prompt)
		updated, _ := m.Update(testKey(tea.KeyEnter))
		m = updated.(model)
	}

	updated, _ := m.Update(testKey(tea.KeyUp))
	m = updated.(model)
	if got := m.input.Value(); got != "second input" {
		t.Fatalf("first ↑ should recall the newest input, got %q", got)
	}
	updated, _ = m.Update(testKey(tea.KeyUp))
	m = updated.(model)
	if got := m.input.Value(); got != "first input" {
		t.Fatalf("second ↑ should recall the older input, got %q", got)
	}
	updated, _ = m.Update(testKey(tea.KeyDown))
	m = updated.(model)
	if got := m.input.Value(); got != "second input" {
		t.Fatalf("↓ should walk back toward the newest input, got %q", got)
	}
}

func TestCutRunesNeverSplitsUTF8(t *testing.T) {
	text := "héllo wörld — ünïcode"
	for limit := 0; limit <= len(text); limit++ {
		cut := cutRunes(text, limit)
		if !strings.HasPrefix(text, cut) {
			t.Fatalf("cutRunes(%d) = %q is not a prefix", limit, cut)
		}
		for _, r := range cut {
			if r == '�' {
				t.Fatalf("cutRunes(%d) produced invalid UTF-8: %q", limit, cut)
			}
		}
	}
}

func TestWrapPlainTextPreservesIndentation(t *testing.T) {
	lines := wrapPlainText("plain\n    indented code line", 40)
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, "    indented") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected indentation preserved, got %#v", lines)
	}
}

func TestLooksLikeDiffIgnoresPlainSeparators(t *testing.T) {
	if looksLikeDiff("build output\n--- summary ---\nall good") {
		t.Fatal("a '---' separator line must not trigger the diff renderer")
	}
	if !looksLikeDiff("--- a/f.go\n+++ b/f.go\n+new line") {
		t.Fatal("real unified diff headers must trigger the diff renderer")
	}
	if !looksLikeDiff("context\n@@ -1,2 +1,3 @@\n+x") {
		t.Fatal("a hunk header must trigger the diff renderer")
	}
}

func TestTruncateStyledLineClosesOpenHyperlink(t *testing.T) {
	line := hyperlink("file:///tmp/a.go", "averyveryverylongclickablepathsegment")
	cut := truncateStyledLine(line, 10)
	if !strings.Contains(cut, "\x1b]8;;\x1b\\") {
		t.Fatalf("truncated line must close its hyperlink, got %q", cut)
	}
}

// messagesContainAssistant reports whether any assistant message carries want.
func messagesContainAssistant(messages []kajicoderuntime.Message, want string) bool {
	for _, message := range messages {
		if message.Role == kajicoderuntime.MessageRoleAssistant && strings.Contains(message.Content, want) {
			return true
		}
	}
	return false
}

// blockingProseProvider streams one prose delta then blocks until the run's
// context is cancelled, returning WITHOUT closing the stream so the collector
// observes ctx cancellation rather than a clean end-of-stream.
type blockingProseProvider struct{}

func (provider *blockingProseProvider) StreamCompletion(
	ctx context.Context,
	request kajicoderuntime.CompletionRequest,
) (<-chan kajicoderuntime.StreamEvent, error) {
	ch := make(chan kajicoderuntime.StreamEvent)
	go func() {
		select {
		case ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: "half an answer"}:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}()
	return ch, nil
}

// TestInterruptedRunPersistsPartialAssistantProse is the regression for the
// reported bug: a run that streams prose then fails must persist that prose, so a
// follow-up "continue" (which rebuilds model context from the store) still knows
// what the agent already said. Before the fix only an EventError was stored.
func TestInterruptedRunPersistsPartialAssistantProse(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: "half an answer"},
		{Type: kajicoderuntime.StreamEventError, Error: "provider stream failed"},
	}}
	m := newModel(context.Background(), Options{
		Cwd:          "repo",
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.input.SetValue("do the thing")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	events := readOnlySessionEvents(t, store)
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventComposerInput,
		sessions.EventMessage,
		sessions.EventMessage,
		sessions.EventError,
	}) {
		t.Fatalf("unexpected event sequence after interruption: %#v", got)
	}
	assistant := nthSessionEvent(t, events, sessions.EventMessage, 2)
	assertPayloadField(t, assistant, "role", "assistant")
	assertPayloadField(t, assistant, "content", "half an answer")

	if messages := sessions.ModelMessagesFromEvents(events); !messagesContainAssistant(messages, "half an answer") {
		t.Fatalf("rehydrated model history lost the partial answer: %#v", messages)
	}
}

// TestInterruptedRunWithoutProsePersistsNoAssistantMessage guards the no-op: an
// error before any text streamed must not fabricate an empty assistant message.
func TestInterruptedRunWithoutProsePersistsNoAssistantMessage(t *testing.T) {
	store := testSessionStore(t)
	provider := &fakeProvider{events: []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventError, Error: "provider stream failed"},
	}}
	m := newModel(context.Background(), Options{
		Cwd:          "repo",
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.input.SetValue("do the thing")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	events := readOnlySessionEvents(t, store)
	if got := countSessionEvents(events, sessions.EventMessage); got != 1 {
		t.Fatalf("expected only the user message when nothing streamed, got %d in %#v", got, eventTypes(events))
	}
}

// TestCancelMidStreamPersistsPartialAssistantProse covers the Esc cancel path: the
// partial answer buffered before the cancel must reach the store via the
// cancelled run's flushed events (flushableSessionEvents keeps everything but the
// duplicate EventError).
func TestCancelMidStreamPersistsPartialAssistantProse(t *testing.T) {
	store := testSessionStore(t)
	provider := &blockingProseProvider{}
	runtimeCh := make(chan tea.Msg, 16)
	m := newModel(context.Background(), Options{
		Cwd:          "repo",
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
		RuntimeMessageSink: func(msg tea.Msg) {
			runtimeCh <- msg
		},
	})
	m.input.SetValue("long task")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}

	finalCh := make(chan tea.Msg, 1)
	go func() {
		finalCh <- execCmd(cmd)
	}()

	// Apply live messages until the prose delta has been forwarded (the goroutine's
	// prose buffer already holds it at that point).
	for {
		msg := receiveRuntimeMessage(t, runtimeCh)
		updated, _ = next.Update(msg)
		next = updated.(model)
		if textMsg, ok := msg.(agentTextMsg); ok && strings.Contains(textMsg.delta, "half an answer") {
			break
		}
	}
	// Keep draining so the run goroutine never blocks on the sink once we stop
	// applying messages to the model.
	drained := make(chan struct{})
	defer close(drained)
	go func() {
		for {
			select {
			case <-runtimeCh:
			case <-drained:
				return
			}
		}
	}()

	// Two Esc presses within the confirmation window cancel the in-flight run.
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	updated, _ = next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if next.pending {
		t.Fatal("expected Esc to clear pending state")
	}

	finalMsg := receiveFinalMessage(t, finalCh)
	updated, _ = next.Update(finalMsg)
	next = updated.(model)
	if len(next.flushRunIDs) != 0 {
		t.Fatalf("expected flush set to clear after draining cancelled run, got %v", next.flushRunIDs)
	}

	events := readOnlySessionEvents(t, store)
	if got := countSessionEvents(events, sessions.EventError); got != 1 {
		t.Fatalf("expected exactly one cancellation error, got %d in %#v", got, eventTypes(events))
	}
	assistant := nthSessionEvent(t, events, sessions.EventMessage, 2)
	assertPayloadField(t, assistant, "role", "assistant")
	assertPayloadField(t, assistant, "content", "half an answer")
}

// TestPlanApprovalDoesNotDuplicateFinalAnswer guards the plan-exit path: the run
// ends on a tool-call turn whose streamed text IS result.FinalAnswer. That prose is
// flushed at the tool boundary, so the success branch must not store it again.
func TestPlanApprovalDoesNotDuplicateFinalAnswer(t *testing.T) {
	store := testSessionStore(t)
	provider := &scriptedProvider{scripts: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "Here is the plan."},
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call_plan", ToolName: tools.ExitPlanModeToolName},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call_plan", ArgumentsFragment: "{}"},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call_plan"},
			{Type: kajicoderuntime.StreamEventDone},
		},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewExitPlanModeTool())
	m := newModel(context.Background(), Options{
		Cwd:            "repo",
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Provider:       provider,
		Registry:       registry,
		SessionStore:   store,
		PermissionMode: agent.PermissionModeAuto,
		RuntimeMessageSink: func(msg tea.Msg) {
			if req, ok := msg.(askUserRequestMsg); ok && req.answer != nil {
				req.answer([]string{"Yes, execute the plan"})
			}
		},
	})
	m.planMode = true
	m.input.SetValue("plan it")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	_ = updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	resp, ok := execCmd(cmd).(agentResponseMsg)
	if !ok {
		t.Fatal("expected an agent response message")
	}
	if resp.err != nil {
		t.Fatalf("plan-approval run errored: %v", resp.err)
	}
	if !resp.planApproved {
		t.Fatal("expected the run to report plan approval")
	}

	count := 0
	for _, event := range resp.sessionEvents {
		if event.Type != sessions.EventMessage {
			continue
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok {
			continue
		}
		if payload["role"] == "assistant" && payload["content"] == "Here is the plan." {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected the plan prose persisted exactly once, got %d", count)
	}
}

// TestMaxTurnsSummaryNotDuplicatedWhenPriorProseUnflushed covers the max-turns
// fallback: a dropped-tool-call turn streams prose then continues (no tool call, so
// no boundary flush), and the run ends at the turn cap with the loop streaming a
// final summary via OnText. The summary lands in the same buffer as the prior
// prose, so result.FinalAnswer is a SUFFIX of the flushed segment — the success
// guard must suppress it or the answer is stored twice.
func TestMaxTurnsSummaryNotDuplicatedWhenPriorProseUnflushed(t *testing.T) {
	store := testSessionStore(t)
	provider := &scriptedProvider{scripts: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "Working on it."},
			{Type: kajicoderuntime.StreamEventToolCallDropped},
			{Type: kajicoderuntime.StreamEventDone},
		},
		textScript("Here is the summary."),
	}}
	m := newModel(context.Background(), Options{
		Cwd:          "repo",
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     tools.NewRegistry(),
		SessionStore: store,
	})
	m.agentOptions.MaxTurns = 1
	m.input.SetValue("do it")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	events := readOnlySessionEvents(t, store)
	summaryEvents := 0
	for _, event := range events {
		if event.Type != sessions.EventMessage {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["role"] != "assistant" {
			continue
		}
		if content, _ := payload["content"].(string); strings.Contains(content, "Here is the summary.") {
			summaryEvents++
		}
	}
	if summaryEvents != 1 {
		t.Fatalf("expected the max-turns summary persisted exactly once, got %d in %#v", summaryEvents, eventTypes(events))
	}
}

// TestSuccessfulMultiTurnRunPersistsEachProseSegmentOnce verifies that narration
// before a tool call is persisted as its own segment, and the final answer is
// persisted exactly once (no duplicate from the prose buffer).
func TestSuccessfulMultiTurnRunPersistsEachProseSegmentOnce(t *testing.T) {
	store := testSessionStore(t)
	root := t.TempDir()
	writeTestFile(t, root, "notes.txt", "file contents")
	provider := &scriptedProvider{scripts: [][]kajicoderuntime.StreamEvent{
		{
			{Type: kajicoderuntime.StreamEventText, Content: "Let me check."},
			{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call_1", ToolName: "read_file"},
			{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call_1", ArgumentsFragment: `{"path":"notes.txt"}`},
			{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call_1"},
			{Type: kajicoderuntime.StreamEventDone},
		},
		textScript("read complete"),
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	m := newModel(context.Background(), Options{
		Cwd:          root,
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     provider,
		Registry:     registry,
		SessionStore: store,
	})
	m.input.SetValue("read notes")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = next.Update(execCmd(cmd))
	_ = updated.(model)

	events := readOnlySessionEvents(t, store)
	if got := eventTypes(events); !equalEventTypes(got, []sessions.EventType{
		sessions.EventComposerInput,
		sessions.EventMessage,
		sessions.EventMessage,
		sessions.EventToolCall,
		sessions.EventToolResult,
		sessions.EventMessage,
	}) {
		t.Fatalf("unexpected event sequence: %#v", got)
	}
	if got := countSessionEvents(events, sessions.EventMessage); got != 3 {
		t.Fatalf("expected user + 2 assistant segments (no duplicate final), got %d in %#v", got, eventTypes(events))
	}
	intermediate := nthSessionEvent(t, events, sessions.EventMessage, 2)
	assertPayloadField(t, intermediate, "role", "assistant")
	assertPayloadField(t, intermediate, "content", "Let me check.")
	final := nthSessionEvent(t, events, sessions.EventMessage, 3)
	assertPayloadField(t, final, "content", "read complete")

	if messages := sessions.ModelMessagesFromEvents(events); !messagesContainAssistant(messages, "Let me check.") {
		t.Fatalf("rehydrated model history lost the intermediate prose: %#v", messages)
	}
}
