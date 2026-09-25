package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// fakeLearningProvider has two modes: review (returns shouldLearn) and plan
// (returns a plan). The engine calls review then plan, in order, on successive
// StreamCompletion calls.
type fakeLearningProvider struct {
	learn     bool
	planResp  string
	delay     time.Duration
	lastReq   *kajicoderuntime.CompletionRequest
	callCount int

	mu       sync.Mutex
	inFlight int
	maxIn    int
}

func (p *fakeLearningProvider) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p.mu.Lock()
	p.lastReq = &request
	p.callCount++
	p.inFlight++
	if p.inFlight > p.maxIn {
		p.maxIn = p.inFlight
	}
	p.mu.Unlock()

	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()

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

func newEngineStores(t *testing.T) (global, project, session *harness.Store) {
	t.Helper()
	base := t.TempDir()
	now := func() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }
	return harness.NewStore(harness.StoreOptions{Dir: filepath.Join(base, "global"), Scope: harness.ScopeGlobal, Now: now}),
		harness.NewStore(harness.StoreOptions{Dir: filepath.Join(base, "project"), Scope: harness.ScopeProject, Now: now}),
		harness.NewStore(harness.StoreOptions{Dir: filepath.Join(base, "session"), Scope: harness.ScopeSession, Now: now})
}

func testNow() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestLearningEngineDisabledWithoutProvider(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	if eng.Enabled() {
		t.Fatal("engine should be disabled with nil provider")
	}
	// Must not panic.
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.RunTurn(context.Background(), nil)
}

func TestLearningEngineRunsPipelineOnManualRequest(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","rationale":"r","edits":[{"action":"create","kind":"memory","id":"fact","title":"F","content":"cmake"}]}`}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	// Manual learn-tool request arms a pass with no other signal.
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "use cmake to build"},
	})

	state, err := ps.Load()
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

func TestLearningEngineFiresOnFailureThenFix(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"retry","title":"Retry","content":"retry flaky test"}]}`}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	// A tool fails then succeeds: the eager event trigger.
	eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusError})
	eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusOK})
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "build"}})

	state, _ := ps.Load()
	if len(state.Entries) != 1 {
		t.Fatalf("failure→fix should have produced a lesson, got %#v", state.Entries)
	}
	// The signal must have been passed to the plan as focus.
	if p.lastReq == nil {
		t.Fatal("no plan request captured")
	}
}

func TestLearningEngineNoSignalSkipsPass(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"x","title":"X","content":"c"}]}`}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	// No signal, no manual request, no compaction: nothing runs.
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hi"}})
	state, _ := ps.Load()
	if len(state.Entries) != 0 {
		t.Fatalf("expected no learning without a signal, got %#v", state.Entries)
	}
	if p.callCount != 0 {
		t.Fatalf("expected no provider calls, got %d", p.callCount)
	}
}

func TestLearningEngineDebounceSuppressesSignals(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"x","title":"X","content":"c"}]}`}
	eng := NewLearningEngine(config.LearningConfig{DebounceMs: 60_000}, p, gs, ps, ss)
	fire := func() {
		eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusError})
		eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusOK})
		eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	}
	fire() // runs
	fire() // within debounce: suppressed
	state, _ := ps.Load()
	if len(state.Entries) != 1 {
		t.Fatalf("debounce failed to suppress re-review: %#v", state.Entries)
	}
}

func TestLearningEngineRoutesProposalsByScope(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	plan := `{"summary":"s","edits":[` +
		`{"action":"create","kind":"memory","id":"proj","title":"P","content":"project","scope":"project"},` +
		`{"action":"create","kind":"memory","id":"sess","title":"S","content":"session","scope":"session"}]}`
	p := &fakeLearningProvider{learn: true, planResp: plan}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})

	project, _ := ps.Load()
	session, _ := ss.Load()
	if len(project.Entries) != 1 || project.Entries[0].ID != "proj" {
		t.Fatalf("project store = %#v", project.Entries)
	}
	if len(session.Entries) != 1 || session.Entries[0].ID != "sess" {
		t.Fatalf("session store = %#v", session.Entries)
	}
}

func TestLearningEngineRoutesGlobalScope(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	plan := `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"glob","title":"G","content":"c","scope":"global"}]}`
	p := &fakeLearningProvider{learn: true, planResp: plan}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	global, _ := gs.Load()
	if len(global.Entries) != 1 || global.Entries[0].ID != "glob" {
		t.Fatalf("global store = %#v", global.Entries)
	}
	project, _ := ps.Load()
	if len(project.Entries) != 0 {
		t.Fatalf("global lesson leaked into project store: %#v", project.Entries)
	}
}

func TestLearningEngineSessionProposalFallsBackToProject(t *testing.T) {
	_, ps, _ := newEngineStores(t)
	plan := `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"sess","title":"S","content":"c","scope":"session"}]}`
	p := &fakeLearningProvider{learn: true, planResp: plan}
	eng := NewLearningEngine(config.LearningConfig{}, p, nil, ps, nil) // no session store
	eng.NoteToolResult(ToolResult{Meta: map[string]string{requestLearnMeta: "true"}})
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	project, _ := ps.Load()
	if len(project.Entries) != 1 {
		t.Fatalf("session lesson should fall back to the project store, got %#v", project.Entries)
	}
}

func TestLooksLikeCorrection(t *testing.T) {
	yes := []string{"no, use cmake instead", "that's wrong", "don't do that", "actually, use go test", "revert that"}
	no := []string{"build the project", "what does this do?", ""}
	for _, s := range yes {
		if !looksLikeCorrection(s) {
			t.Fatalf("expected correction: %q", s)
		}
	}
	for _, s := range no {
		if looksLikeCorrection(s) {
			t.Fatalf("unexpected correction: %q", s)
		}
	}
}

func TestLearningEngineContext(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seed := harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeProject, "agent", testNow())
	recipe := harness.NewEntry(harness.KindRecipe, "Build", "build recipe", "build", "general", harness.ScopeProject, "agent", testNow())
	if err := ps.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, seed, recipe)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
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

func TestLearningEngineReinforcesSurfacedLessons(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seed := harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeProject, "agent", testNow())
	if err := ps.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, seed)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	eng.Context() // surfaces and records the lesson
	eng.Reinforce()

	state, _ := ps.Load()
	if state.Entries[0].Reinforcements != 1 {
		t.Fatalf("reinforcements = %d, want 1", state.Entries[0].Reinforcements)
	}
	if state.Entries[0].LastUsedAt == "" {
		t.Fatal("LastUsedAt should be stamped by reinforcement")
	}
}

func TestLearningPromptSection(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	if err := ps.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeProject, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	prompt := buildSystemPrompt(Options{Learning: eng})
	if !strings.Contains(prompt, "<learned_memory>") || !strings.Contains(prompt, "cmake") {
		t.Fatalf("learned memory section not injected:\n%s", prompt)
	}
	if prompt := buildSystemPrompt(Options{}); strings.Contains(prompt, "<learned_memory>") {
		t.Fatal("learned memory section should not appear with nil engine")
	}
}

func TestLearningContextBoundsAndMergesScopes(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seed := func(kind harness.Kind, id, content string, store *harness.Store) {
		t.Helper()
		if err := store.WithLock(func(state harness.State) (harness.State, error) {
			state.Entries = append(state.Entries, harness.NewEntry(kind, id, content, id, "general", store.Scope, "agent", testNow()))
			return state, nil
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	for i := 0; i < learnedPromptMaxPerKind+3; i++ {
		store := ps
		if i%2 == 0 {
			store = ss
		}
		seed(harness.KindMemory, "mem"+string('a'+rune(i)), strings.Repeat("x", 400), store)
	}
	// A session memory entry must shadow the project one with the same id.
	seed(harness.KindMemory, "dup", "session-wins", ss)
	seed(harness.KindMemory, "dup", "project-value", ps)

	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	ctx := eng.Context()
	if !strings.Contains(ctx, "session-wins") || strings.Contains(ctx, "project-value") {
		t.Fatalf("session shadowing broken: %q", ctx)
	}
	if got := strings.Count(ctx, "- ["); got > learnedPromptMaxPerKind {
		t.Fatalf("surfaced %d entries, want <= %d: %q", got, learnedPromptMaxPerKind, ctx)
	}
	for _, line := range strings.Split(ctx, "\n") {
		if len(line) > learnedPromptMaxContentLen+40 {
			t.Fatalf("line too long (%d): %q", len(line), line)
		}
	}
}

func TestEnsurePromptHasMemorySplicesIdempotently(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	if err := ps.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeProject, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
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
	out2 := eng.EnsurePromptHasMemory(out1)
	if strings.Count(out2[0].Content, learnedMemoryOpen) != 1 {
		t.Fatal("splice is not idempotent")
	}
	if out2[0].Content != out1[0].Content {
		t.Fatalf("second splice should be a no-op:\n%q\n!=\n%q", out2[0].Content, out1[0].Content)
	}
	gsEmpty, psEmpty, ssEmpty := newEngineStores(t)
	empty := NewLearningEngine(config.LearningConfig{}, nil, gsEmpty, psEmpty, ssEmpty)
	unchanged := empty.EnsurePromptHasMemory(baseMsgs())
	if unchanged[0].Content != baseMsgs()[0].Content {
		t.Fatal("empty store spliced memory")
	}
}

func TestSpliceMemoryBlockEdgeCases(t *testing.T) {
	block := learningMemoryBlock("a lesson")
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
	plain := "sys only"
	appended := spliceMemoryBlock(plain, block)
	if !strings.Contains(appended, learnedMemoryOpen) || !strings.Contains(appended, "sys only") {
		t.Fatalf("append failed: %q", appended)
	}
	removed := spliceMemoryBlock(src, "")
	if strings.Contains(removed, learnedMemoryOpen) || strings.Contains(removed, "a lesson") {
		t.Fatalf("remove failed: %q", removed)
	}
	if spliceMemoryBlock(plain, "") != plain {
		t.Fatal("no-op expected")
	}
}

func TestRunTurnReportsApplied(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	provider := &fakeLearningProvider{learn: true, planResp: `{"summary":"capture cmake","rationale":"evidence","edits":[{"action":"create","kind":"memory","id":"cmake","title":"cmake","content":"use cmake","scope":"project"}]}`}
	eng := NewLearningEngine(config.LearningConfig{Enabled: boolP(true), Compact: boolP(true)}, provider, gs, ps, ss)
	msgs := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "hi"}}
	eng.NoteCompaction()
	applied := eng.RunTurn(context.Background(), msgs)
	if !applied {
		t.Fatal("expected RunTurn to report an applied lesson")
	}
	promptMsg := []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleSystem, Content: "core"}}
	refreshed := eng.EnsurePromptHasMemory(promptMsg)
	if !strings.Contains(refreshed[0].Content, "cmake") {
		t.Fatalf("applied lesson not pickup-able in-session: %q", refreshed[0].Content)
	}
}

func boolP(v bool) *bool { return &v }

// TestLearningEngineRunTurnDoesNotBlock proves the pass runs off the caller's
// goroutine: RunTurn must return well before a slow provider (two completions at
// fakeLearningProvider.delay) could have finished. This is the regression guard
// for learning no longer blocking the agent loop.
func TestLearningEngineRunTurnDoesNotBlock(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, delay: 300 * time.Millisecond, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"x","title":"X","content":"c"}]}`}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusError})
	eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusOK})

	start := time.Now()
	eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	elapsed := time.Since(start)
	// Two 300ms provider calls would need ≥600ms if RunTurn blocked; the bounded
	// wait is 250ms, so anything near that proves the work moved off the caller.
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("RunTurn blocked for %v; the pass should run in the background", elapsed)
	}

	// Finish waits for the in-flight pass and reinforces, so the lesson lands.
	eng.Finish(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	state, _ := ps.Load()
	if len(state.Entries) != 1 || state.Entries[0].ID != "x" {
		t.Fatalf("background pass did not persist the lesson: %#v", state.Entries)
	}
}

// TestLearningEngineFinishCoalescesInFlightPass proves a burst of signals while a
// pass is in flight does not start a second concurrent pass (single-flight), and
// that Finish still lets the in-flight pass complete.
func TestLearningEngineFinishCoalescesInFlightPass(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	p := &fakeLearningProvider{learn: true, delay: 50 * time.Millisecond, planResp: `{"summary":"s","edits":[{"action":"create","kind":"memory","id":"x","title":"X","content":"c"}]}`}
	eng := NewLearningEngine(config.LearningConfig{}, p, gs, ps, ss)
	fire := func() {
		eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusError})
		eng.NoteToolResult(ToolResult{Name: "exec_command", Status: tools.StatusOK})
		eng.RunTurn(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	}
	fire()
	fire() // a second signal while the first pass may still be running
	eng.Finish(context.Background(), []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleUser, Content: "x"}})
	if p.maxIn > 1 {
		t.Fatalf("single-flight violated: %d concurrent provider calls", p.maxIn)
	}
}

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
		resp = `{"summary":"s","rationale":"r","edits":[{"action":"create","kind":"memory","id":"fact","title":"Fact","content":"always use cmake","scope":"project"}]}`
	default:
		resp = "Done."
	}
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: resp}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

// TestRunSplicesFreshLearnedMemoryIntoSameSessionRequests proves that a lesson
// applied during a live Run is spliced into the actual provider request of that
// same session. The opening prompt is a correction, which is the in-run signal
// that arms an eager pass.
func TestRunSplicesFreshLearnedMemoryIntoSameSessionRequests(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	provider := &learnScriptedProvider{}
	eng := NewLearningEngine(config.LearningConfig{}, provider, gs, ps, ss)
	_, err := Run(context.Background(), "no, build with cmake instead", provider, Options{
		SessionID:    "learn-splice",
		Cwd:          t.TempDir(),
		ProviderName: "test-provider",
		Model:        "test-model",
		Learning:     eng,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The lesson was applied mid-run and is visible to the provider request.
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
	if !strings.Contains(loopReq.Messages[0].Content, learnedMemoryOpen) ||
		!strings.Contains(loopReq.Messages[0].Content, "always use cmake") {
		t.Fatalf("fresh learned_memory block not spliced into loop request:\n%.500s", loopReq.Messages[0].Content)
	}
	// The lesson is durably stored too.
	state, _ := ps.Load()
	if len(state.Entries) != 1 || state.Entries[0].ID != "fact" {
		t.Fatalf("mid-run capture did not persist the lesson: %#v", state.Entries)
	}
}

func baseMsgs() []kajicoderuntime.Message {
	return []kajicoderuntime.Message{{Role: kajicoderuntime.MessageRoleSystem, Content: "core instructions"}}
}

func TestLearningContextRecencyFirstWithinBudget(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seed := func(id, content, updated, lastUsed string) {
		t.Helper()
		if err := ps.WithLock(func(state harness.State) (harness.State, error) {
			e := harness.NewEntry(harness.KindMemory, id, content, id, "general", harness.ScopeProject, "agent", testNow())
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

	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	ctx := eng.Context()
	if !strings.Contains(ctx, "reused") || !strings.Contains(ctx, "plain_old") {
		t.Fatalf("context missing entries: %q", ctx)
	}
	if strings.Index(ctx, "reused") > strings.Index(ctx, "plain_old") {
		t.Fatalf("recency ordering broken:\n%s", ctx)
	}
}

func TestLearningContextTokenBudgetCapsBlock(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	if err := ps.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, harness.NewEntry(harness.KindMemory, "big", strings.Repeat("very long content ", 4000), "big", "general", harness.ScopeProject, "agent", testNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	ctx := eng.Context()
	if ApproxTextTokens(ctx) > learnedMemoryTokenBudgetValue {
		t.Fatalf("context exceeded token budget: %d > %d", ApproxTextTokens(ctx), learnedMemoryTokenBudgetValue)
	}
	for _, line := range strings.Split(ctx, "\n") {
		if len(line) > learnedPromptMaxContentLen+40 {
			t.Fatalf("line too long: %q", line)
		}
	}
}
