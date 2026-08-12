package harness

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyLearningDedupsNearDuplicateTitle(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	// Seed an existing memory entry titled "Always build with cmake".
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries, NewEntry(KindMemory, "Always build with cmake", "v1", "cmake", "general", ScopeGlobal, "agent", fixedNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Proposing a create with the same normalized title must be rejected.
	plan := LearningPlan{Proposals: []EditProposal{{Action: ActionCreate, Kind: KindMemory, ID: "cmake2", Title: "Always Build With CMake!", Content: "v2"}}}
	result := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})
	if len(result.Outcomes) != 1 || result.Outcomes[0].Applied {
		t.Fatalf("expected dedup rejection, got %#v", result.Outcomes)
	}
	if !strings.Contains(result.Outcomes[0].Error, "near-duplicate") {
		t.Fatalf("expected near-duplicate error, got %q", result.Outcomes[0].Error)
	}
	// The original survives, the clone is not added.
	state, _ := store.Load()
	if len(state.Entries) != 1 || state.Entries[0].ID != "cmake" {
		t.Fatalf("entry set = %#v, want only cmake", state.Entries)
	}
}

func TestApplyLearningAllowsDistinctTitleCreate(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal, Now: fixedNow})
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries, NewEntry(KindMemory, "Use cmake", "v1", "cmake", "general", ScopeGlobal, "agent", fixedNow()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan := LearningPlan{Proposals: []EditProposal{{Action: ActionCreate, Kind: KindMemory, ID: "go", Title: "Use go mod", Content: "v2"}}}
	result := ApplyLearning(store, ApplyOptions{Plan: plan, Trigger: "review", Now: fixedNow})
	if len(result.Outcomes) != 1 || !result.Outcomes[0].Applied {
		t.Fatalf("distinct title should apply, got %#v", result.Outcomes)
	}
	state, _ := store.Load()
	if len(state.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(state.Entries))
	}
}
