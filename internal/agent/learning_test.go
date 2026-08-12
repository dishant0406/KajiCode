package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// fakeLearningProvider has two modes: review (returns shouldLearn) and plan
// (returns a plan). The engine calls review then plan, in order, on successive
// StreamCompletion calls.
type fakeLearningProvider struct {
	mu        int
	learn     bool
	planResp  string
	lastReq   *kajicoderuntime.CompletionRequest
	callCount int
}

func (p *fakeLearningProvider) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p.lastReq = &request
	p.callCount++
	resp := `{"shouldLearn": false, "rationale": "nothing durable"}`
	if p.callCount == 1 && p.learn {
		resp = `{"shouldLearn": true, "rationale": "durable cmake lesson", "instructions": "capture cmake"}`
	} else if p.callCount == 2 {
		resp = p.planResp
	}
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: resp}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func newEngineStores(t *testing.T) (*harness.Store, *harness.Store) {
	t.Helper()
	base := t.TempDir()
	now := func() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }
	return harness.NewStore(harness.StoreOptions{Dir: filepath.Join(base, "global"), Scope: harness.ScopeGlobal, Now: now}),
		harness.NewStore(harness.StoreOptions{Dir: filepath.Join(base, "local"), Scope: harness.ScopeLocal, Now: now})
}

func TestLearningEngineDisabledWithoutProvider(t *testing.T) {
	gs, ls := newEngineStores(t)
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	if eng.Enabled() {
		t.Fatal("engine should be disabled with nil provider")
	}
	// Must not panic.
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.TurnElapsed(context.Background(), nil, false)
}

func TestLearningEngineRunsPipelineOnManualRequest(t *testing.T) {
	gs, ls := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","rationale":"r","edits":[{"action":"create","kind":"memory","id":"fact","title":"F","content":"cmake"}]}`}
	eng := NewLearningEngine(config.LearningConfig{TurnInterval: 100, CooldownMs: 0}, p, gs, ls)
	if !eng.Enabled() {
		t.Fatal("engine should be enabled")
	}
	// Manual learn-tool request arms a pass regardless of interval.
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.TurnElapsed(context.Background(), []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "use cmake to build"},
	}, false)

	state, err := gs.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.Entries) != 1 || state.Entries[0].ID != "fact" {
		t.Fatalf("entries = %#v", state.Entries)
	}
	if len(state.Refinements) != 1 || state.Refinements[0].Trigger != "auto" {
		t.Fatalf("refinements = %#v", state.Refinements)
	}
	if p.callCount < 2 {
		t.Fatalf("expected review+plan calls, got %d", p.callCount)
	}
}

func TestLearningEngineReviewGateSkipsWhenNoLesson(t *testing.T) {
	gs, ls := newEngineStores(t)
	p := &fakeLearningProvider{planResp: `{"summary":"s","edits":[]}`}
	eng := NewLearningEngine(config.LearningConfig{TurnInterval: 1, CooldownMs: 0}, p, gs, ls)
	// Interval gate fires, but review says no lesson.
	eng.TurnElapsed(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hi"}}, false)
	state, _ := gs.Load()
	if len(state.Entries) != 0 || len(state.Refinements) != 0 {
		t.Fatalf("expected no learning, got %#v", state)
	}
}

func TestLearningEngineCooldownSuppressesInterval(t *testing.T) {
	gs, ls := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"x","title":"X","content":"c"}]}`}
	eng := NewLearningEngine(config.LearningConfig{TurnInterval: 1, CooldownMs: 60_000}, p, gs, ls)
	// First pass runs.
	eng.TurnElapsed(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}}, false)
	// Second pass within cooldown is suppressed (no new entry).
	eng.TurnElapsed(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}}, false)
	state, _ := gs.Load()
	if len(state.Entries) != 1 {
		t.Fatalf("cooldown failed to suppress re-review: %#v", state.Entries)
	}
}

func testNow() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestLearningEngineContext(t *testing.T) {
	gs, ls := newEngineStores(t)
	seed := harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeGlobal, "agent", testNow())
	recipe := harness.NewEntry(harness.KindRecipe, "Build", "build recipe", "build", "general", harness.ScopeGlobal, "agent", testNow())
	if err := gs.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, seed, recipe)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	ctx := eng.Context()
	if !strings.Contains(ctx, "cmake") {
		t.Fatalf("context missing cmake entry: %q", ctx)
	}
	if strings.Contains(ctx, "build recipe") {
		t.Fatalf("recipe entries should not appear in memory context: %q", ctx)
	}
	if (*LearningEngine)(nil).Context() != "" {
		t.Fatal("nil engine context should be empty")
	}
}

func TestLearningPromptSection(t *testing.T) {
	gs, ls := newEngineStores(t)
	if err := gs.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeGlobal, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	prompt := buildSystemPrompt(Options{Learning: eng})
	if !strings.Contains(prompt, "<learned_memory>") || !strings.Contains(prompt, "cmake") {
		t.Fatalf("learned memory section not injected:\n%s", prompt)
	}
	if prompt := buildSystemPrompt(Options{}); strings.Contains(prompt, "<learned_memory>") {
		t.Fatal("learned memory section should not appear with nil engine")
	}
}

func TestLearningContextBoundsAndMergesLocal(t *testing.T) {
	gs, ls := newEngineStores(t)
	seed := func(kind harness.Kind, id, content string, store *harness.Store) {
		t.Helper()
		if err := store.WithLock(func(state harness.State) (harness.State, error) {
			state.Entries = append(state.Entries, harness.NewEntry(kind, id, content, id, "general", store.Scope, "agent", testNow()))
			return state, nil
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	// Populate more memory entries than the per-kind cap, split across scopes.
	for i := 0; i < learnedPromptoMaxPerKind+3; i++ {
		store := gs
		if i%2 == 0 {
			store = ls
		}
		seed(harness.KindMemory, "mem"+string('a'+rune(i)), strings.Repeat("x", 400), store)
	}
	// A local memory entry must shadow the global one with the same id.
	seed(harness.KindMemory, "dup", "local-wins", ls)
	seed(harness.KindMemory, "dup", "global-value", gs)

	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	ctx := eng.Context()
	// Local merge included and local entry wins on duplicate id.
	if !strings.Contains(ctx, "local-wins") || strings.Contains(ctx, "global-value") {
		t.Fatalf("local shadowing broken: %q", ctx)
	}
	// Bound: at most cap memory entries are surfaced.
	if got := strings.Count(ctx, "- ["); got > learnedPromptoMaxPerKind {
		t.Fatalf("surfaced %d entries, want <= %d: %q", got, learnedPromptoMaxPerKind, ctx)
	}
	// Truncation: no full 400-char blob survives; every line is bounded.
	for _, line := range strings.Split(ctx, "\n") {
		if len(line) > learnedPromptoMaxContentLen+40 {
			t.Fatalf("line too long (%d): %q", len(line), line)
		}
	}
}

func TestEnsurePromptHasMemorySplicesIdempotently(t *testing.T) {
	gs, ls := newEngineStores(t)
	if err := gs.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeGlobal, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	base := kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleSystem, Content: "core instructions"}
	msgs := []kajicoderuntime.Message{base}
	out1 := eng.EnsurePromptHasMemory(msgs)
	if out1[0].Content == base.Content {
		t.Fatal("splice should have changed the system message")
	}
	if strings.Count(out1[0].Content, learnedMemoryOpen) != 1 {
		t.Fatalf("expected exactly one learned_memory block, got %d", strings.Count(out1[0].Content, learnedMemoryOpen))
	}
	if !strings.Contains(out1[0].Content, "cmake") {
		t.Fatalf("spliced block missing cmake: %q", out1[0].Content)
	}
	// Idempotent: a second splice replaces rather than duplicates.
	out2 := eng.EnsurePromptHasMemory(out1)
	if strings.Count(out2[0].Content, learnedMemoryOpen) != 1 {
		t.Fatal("splice is not idempotent")
	}
	if out2[0].Content != out1[0].Content {
		t.Fatalf("second splice should be a no-op:\n%q\n!=\n%q", out2[0].Content, out1[0].Content)
	}
	// A store with no entries is a byte-identical no-op.
	gsEmpty, lsEmpty := newEngineStores(t)
	empty := NewLearningEngine(config.LearningConfig{}, nil, gsEmpty, lsEmpty)
	unchanged := empty.EnsurePromptHasMemory(baseMsgs())
	if unchanged[0].Content != baseMsgs()[0].Content {
		t.Fatal("empty store spliced memory")
	}
}

func TestSpliceMemoryBlockEdgeCases(t *testing.T) {
	block := learningMemoryBlock("a lesson")
	// Replace an existing block in the middle of other content.
	src := "sys" + block + " tail"
	got := spliceMemoryBlock(src, learningMemoryBlock("new lesson"))
	if strings.Contains(got, "a lesson") || !strings.Contains(got, "new lesson") {
		t.Fatalf("replace failed: %q", got)
	}
	if !strings.Contains(got, "sys") || !strings.Contains(got, "tail") {
		t.Fatalf("adjacent content lost: %q", got)
	}
	if strings.Count(got, learnedMemoryOpen) != 1 {
		t.Fatalf("duplicate block after replace: %q", got)
	}
	// Append when no block present.
	plain := "sys only"
	appended := spliceMemoryBlock(plain, block)
	if !strings.Contains(appended, learnedMemoryOpen) || !strings.Contains(appended, "sys only") {
		t.Fatalf("append failed: %q", appended)
	}
	// Empty replacement removes the block.
	removed := spliceMemoryBlock(src, "")
	if strings.Contains(removed, learnedMemoryOpen) || strings.Contains(removed, "a lesson") {
		t.Fatalf("remove failed: %q", removed)
	}
	// No block, empty replacement: no change.
	if spliceMemoryBlock(plain, "") != plain {
		t.Fatal("no-op expected")
	}
}

func TestTurnElapsedReportsApplied(t *testing.T) {
	gs, ls := newEngineStores(t)
	provider := &fakeLearningProvider{learn: true, planResp: `{"summary":"capture cmake","rationale":"evidence","edits":[{"action":"create","kind":"memory","id":"cmake","title":"cmake","content":"use cmake","scope":"local"}]}`}
	eng := NewLearningEngine(config.LearningConfig{Enabled: boolP(true), TurnInterval: 10, Compact: boolP(true)}, provider, gs, ls)
	msgs := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hi"}}
	applied := eng.TurnElapsed(context.Background(), msgs, true) // compact trigger
	if !applied {
		t.Fatal("expected TurnElapsed to report an applied lesson")
	}
	// The applied entry should now be splicable into a fresh system message.
	promptMsg := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleSystem, Content: "core"}}
	refreshed := eng.EnsurePromptHasMemory(promptMsg)
	if !strings.Contains(refreshed[0].Content, "cmake") {
		t.Fatalf("applied lesson not pickup-able in-session: %q", refreshed[0].Content)
	}
}

func boolP(v bool) *bool { return &v }

// learnScriptedProvider drives both the auto-learning pipeline calls (review +
// plan) and the agent loop from a single StreamCompletion, selecting its
// response by the leading system prompt content.
type learnScriptedProvider struct {
	requests []kajicoderuntime.CompletionRequest
}

func (p *learnScriptedProvider) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p.requests = append(p.requests, request)
	joined := ""
	for _, m := range request.Messages {
		joined += m.Content
	}
	var resp string
	switch {
	case strings.Contains(joined, "auto-learning review gate"):
		resp = `{"shouldLearn": true, "rationale": "cmake is durable", "instructions": "capture cmake"}`
	case strings.Contains(joined, "optimizer for KajiCode's self-learning memory"):
		resp = `{"summary":"s","rationale":"r","edits":[{"action":"create","kind":"memory","id":"fact","title":"Fact","content":"always use cmake"}]}`
	default:
		resp = "Done."
	}
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: resp}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// TestRunSplicesFreshLearnedMemoryIntoSameSessionRequests is the loop-level
// counterpart to the unit splice tests: it proves that a lesson applied during
// a live Run (loop.go TurnElapsed → EnsurePromptHasMemory) is spliced into the
// actual provider request of that same session.
func TestRunSplicesFreshLearnedMemoryIntoSameSessionRequests(t *testing.T) {
	gs, ls := newEngineStores(t)
	provider := &learnScriptedProvider{}
	eng := NewLearningEngine(config.LearningConfig{TurnInterval: 1, CooldownMs: 100_000}, provider, gs, ls)
	_, err := Run(context.Background(), "build the project", provider, Options{
		SessionID:    "learn-splice",
		Cwd:          t.TempDir(),
		ProviderName: "test-provider",
		Model:        "test-model",
		Learning:     eng,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Locate the first agent-loop request, excluding the review/plan pipeline
	// calls (they carry a distinctive system prompt).
	var loopReq *kajicoderuntime.CompletionRequest
	for i := range provider.requests {
		joined := ""
		for _, m := range provider.requests[i].Messages {
			joined += m.Content
		}
		if strings.Contains(joined, "auto-learning review gate") ||
			strings.Contains(joined, "optimizer for KajiCode's self-learning memory") {
			continue
		}
		loopReq = &provider.requests[i]
		break
	}
	if loopReq == nil {
		t.Fatal("no agent-loop request captured")
	}
	sys := loopReq.Messages[0].Content
	if !strings.Contains(sys, learnedMemoryOpen) {
		t.Fatalf("fresh learned_memory block not spliced into loop request:\n%.500s", sys)
	}
	if !strings.Contains(sys, "always use cmake") {
		t.Fatalf("applied lesson content missing from spliced block:\n%.500s", sys)
	}
}

func baseMsgs() []kajicoderuntime.Message {
	return []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleSystem, Content: "core instructions"}}
}

func TestLearningContextRecencyFirstWithinBudget(t *testing.T) {
	gs, ls := newEngineStores(t)
	seed := func(id, content, updated, lastUsed string) {
		t.Helper()
		if err := gs.WithLock(func(state harness.State) (harness.State, error) {
			e := harness.NewEntry(harness.KindMemory, id, content, id, "general", harness.ScopeGlobal, "agent", testNow())
			e.UpdatedAt = updated
			e.LastUsedAt = lastUsed
			state.Entries = append(state.Entries, e)
			return state, nil
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	older := "2025-01-01T00:00:00Z"
	used := "2026-02-02T00:00:00Z"
	seed("reused", "lesson A", older, used)
	seed("plain_old", strings.Repeat("old content ", 20), older, "")

	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	ctx := eng.Context()
	if !strings.Contains(ctx, "reused") || !strings.Contains(ctx, "plain_old") {
		t.Fatalf("context missing entries: %q", ctx)
	}
	// Reused lesson must surface before the unused one.
	if strings.Index(ctx, "reused") > strings.Index(ctx, "plain_old") {
		t.Fatalf("recency ordering broken:\n%s", ctx)
	}
}

func TestLearningContextTokenBudgetCapsBlock(t *testing.T) {
	gs, ls := newEngineStores(t)
	// A single large entry that exceeds the whole-block budget must not blow it.
	if err := gs.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "big", strings.Repeat("very long content ", 4000), "big", "general", harness.ScopeGlobal, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ls)
	ctx := eng.Context()
	if ApproxTextTokens(ctx) > learnedMemoryTokenBudget() {
		t.Fatalf("context exceeded token budget: %d > %d", ApproxTextTokens(ctx), learnedMemoryTokenBudget())
	}
	// Even so, content is per-line truncated to the per-kind content cap.
	for _, line := range strings.Split(ctx, "\n") {
		if len(line) > learnedPromptoMaxContentLen+40 {
			t.Fatalf("line too long: %q", line)
		}
	}
}
