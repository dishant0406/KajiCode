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

// compactionProvider services the main turn and answers the out-of-band
// compaction summarizer call with a short summary, exactly like the real
// summarizeClosure path.
type compactionProvider struct {
	requests       []kajicoderuntime.CompletionRequest
	turnEvents     []kajicoderuntime.StreamEvent
	summarizeCalls int
}

func (provider *compactionProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	provider.requests = append(provider.requests, request)
	if isSummarizerRequest(request) {
		provider.summarizeCalls++
	}
	ch := make(chan kajicoderuntime.StreamEvent, 4)
	if isSummarizerRequest(request) {
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: "Summarized earlier turns."}
		ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
		close(ch)
		return ch, nil
	}
	for _, event := range provider.turnEvents {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func isSummarizerRequest(request kajicoderuntime.CompletionRequest) bool {
	if len(request.Tools) > 0 {
		return false
	}
	for _, message := range request.Messages {
		if message.Role == kajicoderuntime.MessageRoleSystem &&
			strings.Contains(message.Content, "You are compacting a coding-assistant conversation") {
			return true
		}
	}
	return false
}

// TestProactiveCompactionPersistenceKeepsTail verifies the FULL loop: a long
// pre-run history triggers proactive compaction during a run, the compaction
// event is persisted with a boundary that elides only the OLD events, and a
// subsequent resume rebuilds model history from the summary + preserved tail.
func TestProactiveCompactionPersistenceKeepsTail(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(sessions.CreateInput{Title: "long thread", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	// Seed enough pre-run history that the agent's compaction has a real middle
	// to summarize: preserveLast=6 keeps the current prompt + the 5 trailing
	// seeded messages, so the oldest 5 seeded messages are elidable.
	prior := []sessions.Event{}
	appendStored := func(role, content string) {
		ev, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{
			Type:    sessions.EventMessage,
			Payload: map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		prior = append(prior, ev)
	}
	names := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	for i, name := range names {
		role := "user"
		content := "old request " + name
		if i%2 == 1 {
			role = "assistant"
			content = "old answer " + name + " " + strings.Repeat("z", 2000)
		}
		appendStored(role, content)
	}
	tail := "preserved tail answer"
	appendStored("assistant", strings.Repeat("y", 2000)+" "+tail)

	provider := &compactionProvider{turnEvents: []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: "Done."},
		{Type: kajicoderuntime.StreamEventDone},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(t.TempDir()))
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "test",
		ModelName:    "tiny-model",
		Provider:     provider,
		Registry:     registry,
		SessionStore: store,
	})
	// Shrink the compaction trigger so the seeded history trips the proactive
	// path on the very first turn instead of relying on the fallback window.
	m.ollamaContextWindowByModel = map[string]int{"tiny-model": 3000}
	m.activeSession = session
	m.sessionEvents = prior
	m.input.SetValue("continue")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = updated.(model).Update(execCmd(cmd))
	_ = updated.(model)

	if provider.summarizeCalls == 0 {
		t.Logf("no summarization happened; provider requests: %d", len(provider.requests))
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	compactionIdx := -1
	for i, event := range events {
		if event.Type == sessions.EventCompaction {
			compactionIdx = i
		}
	}
	t.Logf("summarizeCalls=%d compactionIdx=%d totalEvents=%d", provider.summarizeCalls, compactionIdx, len(events))

	if provider.summarizeCalls == 0 {
		t.Fatal("expected proactive compaction to trigger within the small context window")
	}
	if compactionIdx < 0 {
		t.Fatal("expected a persisted compaction event after a compaction run")
	}

	payload := map[string]any{}
	_ = json.Unmarshal(events[compactionIdx].Payload, &payload)
	compactedThrough := payloadInt(payload, "compactedThroughSequence")
	compactableCount := payloadInt(payload, "compactableCount")
	// The preserved tail event must NOT be elided: 10 old events are seeded, a
	// 6-slot preserved suffix keeps events 6..10, so at most 5 may be compacted.
	if compactedThrough >= 10 {
		t.Fatalf("compaction boundary %d must stay below the preserved tail (event 10)", compactedThrough)
	}
	if compactableCount <= 0 || compactableCount >= 10 {
		t.Fatalf("expected a partial compactable count (1..9), got %d", compactableCount)
	}

	// Resume flow: rehydrate events and rebuild model history.
	rehydrated, err := store.ReadRehydratedEvents(session.SessionID)
	if err != nil {
		t.Fatalf("ReadRehydratedEvents: %v", err)
	}
	messages := sessions.ModelMessagesFromEvents(rehydrated)
	foundSummary := false
	foundTail := false
	for _, msg := range messages {
		if strings.Contains(msg.Content, "Summarized earlier turns") {
			foundSummary = true
		}
		if strings.Contains(msg.Content, tail) {
			foundTail = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected persisted summary in model history, got %#v", messages)
	}
	if !foundTail {
		t.Fatalf("expected preserved tail in model history, got %#v", messages)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "old request one") || strings.Contains(msg.Content, "old request two") {
			t.Fatalf("pre-compaction content leaked into model history: %#v", messages)
		}
	}
}

func TestManualCompactionPersistsModelSnapshotAndKeepsTail(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	m := newModel(context.Background(), Options{
		ModelName:    "gpt-4.1",
		SessionStore: store,
	})
	var err error
	m, err = m.ensureActiveSession("compact this session")
	if err != nil {
		t.Fatal(err)
	}
	contents := []string{
		"alpha old user intent",
		"beta old assistant answer",
		"gamma old tool result",
		"delta old follow-up",
		"epsilon recent",
		"zeta recent",
		"eta recent",
		"theta recent",
		"iota recent",
		"kappa recent",
		"lambda recent",
		"mu recent",
	}
	for _, content := range contents {
		m, err = m.appendSessionEvent(sessions.EventMessage, map[string]any{
			"role":    "user",
			"content": content,
		})
		if err != nil {
			t.Fatal(err)
		}
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: content})
	}
	next, result, err := m.compactActiveSession()
	if err != nil {
		t.Fatalf("compactActiveSession: %v", err)
	}
	if !result.Compacted {
		t.Fatalf("expected compacted result, got %#v", result)
	}

	// The persisted compaction payload must carry a model-history snapshot and
	// the preserved tail must be part of that snapshot.
	events, err := store.ReadEvents(next.activeSession.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	compactionIdx := -1
	for i, event := range events {
		if event.Type == sessions.EventCompaction {
			compactionIdx = i
		}
	}
	if compactionIdx < 0 {
		t.Fatal("expected a persisted compaction event")
	}
	var payload sessions.CompactionPayload
	if err := json.Unmarshal(events[compactionIdx].Payload, &payload); err != nil {
		t.Fatalf("decode compaction payload: %v", err)
	}
	if len(payload.ModelMessages) < 2 {
		t.Fatalf("expected summary + preserved tail snapshot, got %#v", payload.ModelMessages)
	}
	if !strings.Contains(payload.ModelMessages[0].Content, "[Summary of earlier conversation]") {
		t.Fatalf("expected snapshot to open with the summary header, got %#v", payload.ModelMessages[0])
	}
	if !strings.Contains(payload.ModelMessages[0].Content, "Compacted earlier session context") {
		t.Fatalf("expected snapshot summary text, got %#v", payload.ModelMessages[0])
	}
	tailIdx := len(payload.ModelMessages) - 1
	if !strings.Contains(payload.ModelMessages[tailIdx].Content, "mu recent") {
		t.Fatalf("expected preserved tail as the LAST snapshot message, got %#v", payload.ModelMessages[tailIdx])
	}
	for _, msg := range payload.ModelMessages {
		if strings.Contains(msg.Content, "alpha old user intent") || strings.Contains(msg.Content, "delta old follow-up") {
			t.Fatalf("compacted-away content leaked into the snapshot: %#v", payload.ModelMessages)
		}
	}

	// Resume flow: the rehydrated event log + model-history rebuild must expose
	// the summary and the tail, and never leak the compacted-away content.
	rehydrated, err := store.ReadRehydratedEvents(next.activeSession.SessionID)
	if err != nil {
		t.Fatalf("ReadRehydratedEvents: %v", err)
	}
	messages := sessions.ModelMessagesFromEvents(rehydrated)
	foundSummary := false
	foundTail := false
	for _, msg := range messages {
		if strings.Contains(msg.Content, "Compacted earlier session context") {
			foundSummary = true
		}
		if strings.Contains(msg.Content, "mu recent") {
			foundTail = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected manual compaction summary in model history, got %#v", messages)
	}
	if !foundTail {
		t.Fatalf("expected preserved tail in model history, got %#v", messages)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "alpha old user intent") || strings.Contains(msg.Content, "delta old follow-up") {
			t.Fatalf("compacted-away content leaked into model history: %#v", messages)
		}
	}
}

func payloadInt(payload map[string]any, key string) int {
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

// TestProactiveCompactionPersistenceAcrossTurns verifies the SAME-SESSION
// follow-up turn after a proactive compaction: the next launch seeds the agent
// from the compacted snapshot (summary + preserved tail), not from the raw
// oversized history. This is the "keep the loop going" persistence contract.
func TestProactiveCompactionPersistenceAcrossTurns(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(sessions.CreateInput{Title: "long thread", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	prior := []sessions.Event{}
	appendStored := func(role, content string) {
		ev, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{
			Type:    sessions.EventMessage,
			Payload: map[string]any{"role": role, "content": content},
		})
		if err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
		prior = append(prior, ev)
	}
	for i, name := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"} {
		if i%2 == 0 {
			content := "old request " + name
			if i < 5 {
				// Big payloads ONLY in the elidable middle so the preserved tail
				// stays small and the compaction actually sticks below threshold.
				content += " " + strings.Repeat("x", 16000)
			}
			appendStored("user", content)
			continue
		}
		content := "old answer " + name + " " + strings.Repeat("z", 2000)
		if i < 5 {
			content += " " + strings.Repeat("w", 16000)
		}
		appendStored("assistant", content)
	}
	appendStored("assistant", "preserved tail answer")

	provider := &compactionProvider{turnEvents: []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: "First run answer."},
		{Type: kajicoderuntime.StreamEventDone},
	}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(t.TempDir()))
	m := newModel(context.Background(), Options{
		Cwd:          t.TempDir(),
		ProviderName: "test",
		ModelName:    "tiny-model",
		Provider:     provider,
		Registry:     registry,
		SessionStore: store,
		AgentOptions: agent.Options{
			// A small preserved suffix keeps the compacted snapshot far below
			// the threshold; the agent's own low-water guard plus the compacted
			// size are what make a second compaction unnecessary.
			CompactionPreserveLast: 2,
		},
	})
	// A large-but-finite window (normalized, not the tiny 3000 used by the
	// single-turn KeepsTail test) so the compacted snapshot genuinely sits
	// under the compaction threshold, like a real production turn.
	m.ollamaContextWindowByModel = map[string]int{"tiny-model": 30000}
	m.activeSession = session
	m.sessionEvents = prior
	m.input.SetValue("continue")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected prompt submit to start an agent run")
	}
	updated, _ = updated.(model).Update(execCmd(cmd))
	m = updated.(model)
	if provider.summarizeCalls != 1 {
		t.Fatalf("expected proactive compaction on the first turn, got %d", provider.summarizeCalls)
	}

	// Second turn in the SAME session: the model history must now be the
	// compacted snapshot, not the raw oversized history.
	provider.requests = nil
	m.input.SetValue("follow up")
	updated, cmd = m.Update(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected second prompt submit to start an agent run")
	}
	updated, _ = updated.(model).Update(execCmd(cmd))
	_ = updated.(model)

	// The second run's first request should NOT re-request a summarizer (the
	// compacted history is already under threshold) and should NOT contain the
	// old pre-compaction content.
	if provider.summarizeCalls > 1 {
		t.Fatalf("expected no second compaction straight after compaction, got %d", provider.summarizeCalls)
	}
	last := provider.requests[len(provider.requests)-1]
	joined := ""
	for _, msg := range last.Messages {
		joined += msg.Content + "\n"
	}
	if !strings.Contains(joined, "Summarized earlier turns") {
		t.Fatalf("expected compacted summary to seed turn two:\n%s", joined)
	}
	if !strings.Contains(joined, "preserved tail answer") {
		t.Fatalf("expected preserved tail to seed turn two:\n%s", joined)
	}
	if strings.Contains(joined, "old request one") || strings.Contains(joined, "old request two") {
		t.Fatalf("pre-compaction content leaked into turn two:\n%s", joined)
	}
}
