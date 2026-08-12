package harness

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// fakeProvider streams a canned text completion, recording the request.
type fakeProvider struct {
	response string
	lastReq  *kajicoderuntime.CompletionRequest
}

func (p *fakeProvider) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p.lastReq = &request
	ch := make(chan kajicoderuntime.StreamEvent, 2)
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventText, Content: p.response}
	ch <- kajicoderuntime.StreamEvent{Type: kajicoderuntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func fixedNow() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }

func TestParseLearningPlan(t *testing.T) {
	raw := "```json\n" +
		`{"summary":"sum","rationale":"rat","edits":[` +
		`{"action":"create","kind":"memory","id":"fact","title":"F","content":"c"},` +
		`{"action":"create","kind":"recipe","id":"rr","title":"R","recipe":{"name":"rr","commands":[{"id":"a","tool":"bash","args":{"command":"x"}}]}}` +
		`]}` + "\n```"
	plan, err := ParseLearningPlan(raw)
	if err != nil {
		t.Fatalf("ParseLearningPlan: %v", err)
	}
	if plan.Summary != "sum" || len(plan.Proposals) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Proposals[1].Kind != KindRecipe || plan.Proposals[1].Recipe == nil {
		t.Fatalf("recipe proposal missing recipe: %#v", plan.Proposals[1])
	}
}

func TestParseLearningPlanRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing title":      `{"edits":[{"action":"update","kind":"memory","id":"x"}]}`,
		"base prompt":        `{"edits":[{"action":"update","kind":"prompt","id":"base_system_prompt","title":"x"}]}`,
		"unknown action":     `{"edits":[{"action":"nuke","kind":"memory","id":"x","title":"x"}]}`,
		"recipe no contract": `{"edits":[{"action":"create","kind":"recipe","id":"x","title":"x"}]}`,
	}
	for name, raw := range cases {
		raw := raw
		if _, err := ParseLearningPlan(raw); err == nil {
			t.Fatalf("%s: expected error for %q", name, raw)
		}
	}
	if _, err := ParseLearningPlan("no object here"); err == nil {
		t.Fatal("expected error for non-JSON output")
	}
}

func TestPlanLearningCallsProviderAndParses(t *testing.T) {
	p := &fakeProvider{response: `{"summary":"s","rationale":"r","edits":[{"action":"create","kind":"memory","id":"fact","title":"F","content":"cmake"}]}`}
	plan, err := PlanLearning(context.Background(), PlanOptions{
		Provider:     p,
		Conversation: "user: use cmake\nassistant: ok",
		State:        State{Scope: ScopeGlobal},
	})
	if err != nil {
		t.Fatalf("PlanLearning: %v", err)
	}
	if len(plan.Proposals) != 1 {
		t.Fatalf("proposals = %#v", plan.Proposals)
	}
	if p.lastReq == nil || !strings.Contains(p.lastReq.Messages[1].Content, "cmake") {
		t.Fatalf("provider did not receive conversation")
	}
}

func TestApplyLearningAppliesAndRecordsRefinement(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	plan := LearningPlan{
		Summary: "learn cmake",
		Proposals: []EditProposal{
			{Action: ActionCreate, Kind: KindMemory, ID: "fact", Title: "F", Content: "Use cmake"},
			{Action: ActionCreate, Kind: KindRecipe, ID: "rr", Title: "R", Recipe: &Recipe{Name: "rr", Commands: []RecipeCommand{{ID: "a", Tool: "bash", Args: map[string]any{"command": "make"}}}}},
		},
	}
	result := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %v", result.Errors)
	}
	if result.RefinementID == "" {
		t.Fatal("missing refinement id")
	}
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(state.Entries))
	}
	if len(state.Refinements) != 1 || state.Refinements[0].Trigger != "review" {
		t.Fatalf("refinements = %#v", state.Refinements)
	}
	if len(state.Refinements[0].Changes) != 2 {
		t.Fatalf("changes = %v, want 2", state.Refinements[0].Changes)
	}
}

func TestApplyLearningConflictDetection(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	// The plan saw the entry at version 1.
	plan := LearningPlan{
		Baseline: State{Scope: ScopeGlobal, Entries: []Entry{
			{ID: "fact", Kind: KindMemory, Title: "fact", Content: "old", Scope: ScopeGlobal, Version: 1},
		}},
		Proposals: []EditProposal{{Action: ActionUpdate, Kind: KindMemory, ID: "fact", Content: "new", Title: "fact"}},
	}
	// Concurrent writer bumps to version 2 BEFORE apply.
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries, Entry{ID: "fact", Kind: KindMemory, Title: "fact", Content: "old", Scope: ScopeGlobal, Version: 2})
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	result := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "conflict") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected conflict error, got %v", result.Errors)
	}
	// The concurrent version-2 content must be untouched.
	state, _ := store.Load()
	if len(state.Entries) != 1 || state.Entries[0].Content != "old" || state.Entries[0].Version != 2 {
		t.Fatalf("concurrent entry clobbered: %#v", state.Entries)
	}
}

func TestApplyLearningDeniesImmutableBase(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	plan := LearningPlan{Proposals: []EditProposal{{Action: ActionUpdate, Kind: KindPrompt, ID: BasePromptID, Title: "x"}}}
	result := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})
	if len(result.Errors) == 0 {
		t.Fatal("expected base-prompt error")
	}
}

func TestRollbackInvertsApply(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	plan := LearningPlan{Proposals: []EditProposal{{Action: ActionCreate, Kind: KindMemory, ID: "fact", Title: "F", Content: "c"}}}
	applied := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})

	rollback := RollbackInverts(store, RollbackOptions{Outcomes: applied.Outcomes, Now: fixedNow})
	if len(rollback.Errors) != 0 {
		t.Fatalf("rollback errors = %v", rollback.Errors)
	}
	state, _ := store.Load()
	if len(state.Entries) != 0 {
		t.Fatalf("entries after rollback = %#v, want none", state.Entries)
	}
	if len(state.Refinements) != 2 { // apply + rollback
		t.Fatalf("refinements = %d, want 2", len(state.Refinements))
	}
}

func TestReviewGateParsesDecision(t *testing.T) {
	p := &fakeProvider{response: `{"shouldLearn": true, "rationale": "repeated build flow", "instructions": "capture build recipe"}`}
	decision, err := RunReview(context.Background(), ReviewOptions{Provider: p, Conversation: "x"})
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if !decision.ShouldLearn || decision.Instructions == "" {
		t.Fatalf("decision = %#v", decision)
	}
	// Fence-wrapped false case.
	p.response = "```json\n{\"shouldLearn\": false, \"rationale\": \"nothing durable\"}\n```"
	decision, err = RunReview(context.Background(), ReviewOptions{Provider: p, Conversation: "x"})
	if err != nil {
		t.Fatalf("RunReview false: %v", err)
	}
	if decision.ShouldLearn {
		t.Fatal("expected shouldLearn=false")
	}
}

func TestRunReviewDegradesOnBadOutput(t *testing.T) {
	p := &fakeProvider{response: "not json at all"}
	if _, err := RunReview(context.Background(), ReviewOptions{Provider: p, Conversation: "x"}); err == nil {
		t.Fatal("expected error for non-JSON review output")
	}
}

func TestRecordRefinement(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	ref := NewRefinementEvent("manual", []string{"create:memory:f"}, "evidence", fixedNow())
	if err := RecordRefinement(store, ref); err != nil {
		t.Fatalf("RecordRefinement: %v", err)
	}
	state, _ := store.Load()
	if len(state.Refinements) != 1 || state.Refinements[0].Trigger != "manual" {
		t.Fatalf("refinements = %#v", state.Refinements)
	}
}

func TestPlanPromptCarriesAnchorAndRecencyOrder(t *testing.T) {
	older := "2025-01-01T00:00:00Z"
	newer := "2026-02-02T00:00:00Z"
	state := State{Scope: ScopeGlobal, Entries: []Entry{
		{ID: "old", Kind: KindMemory, Title: "Old", Content: "o", Scope: ScopeGlobal, UpdatedAt: older},
		{ID: "used", Kind: KindMemory, Title: "Used", Content: "u", Scope: ScopeGlobal, UpdatedAt: older, LastUsedAt: newer},
	}}
	prompt := buildPlanPrompt(PlanOptions{Conversation: "x", State: state, Refinements: nil})
	if !strings.Contains(prompt, "<anchor>") {
		t.Fatal("plan prompt missing <anchor> preserved-state directive")
	}
	if !strings.Contains(prompt, "prefer updating the existing entry") {
		t.Fatal("plan prompt missing update-over-create directive")
	}
	// The current learning state must surface the re-used lesson first.
	youngIdx := strings.Index(prompt, "used")
	oldIdx := strings.Index(prompt, "old")
	if youngIdx < 0 || oldIdx < 0 || youngIdx > oldIdx {
		t.Fatalf("expected re-used entry 'used' before 'old' in plan state:\n%s", prompt)
	}
}
