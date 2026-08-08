package harness

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "learning")
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeGlobal})

	state := State{Scope: ScopeGlobal}
	state.Entries = append(state.Entries, NewEntry(KindMemory, "C build is cmake", "Use cmake --build .", "", "general", ScopeGlobal, "agent", time.Now()))
	state.Entries = append(state.Entries, NewEntry(KindRecipe, "check dirty", "Check working tree", "", "general", ScopeGlobal, "agent", time.Now()))
	state.Refinements = append(state.Refinements, RefinementEvent{ID: "refine_1", Trigger: "manual", Changes: []string{"+memory:c_build"}})

	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Scope != ScopeGlobal {
		t.Fatalf("scope = %q, want global", loaded.Scope)
	}
	if len(loaded.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(loaded.Entries))
	}
	// Sorted by kind then id: memory (c_build) then recipe (check_dirty).
	if loaded.Entries[0].Kind != KindMemory || loaded.Entries[1].Kind != KindRecipe {
		t.Fatalf("entry kinds out of order: %#v", loaded.Entries)
	}
	if loaded.Entries[0].Version != 1 || loaded.Entries[0].CreatedAt == "" {
		t.Fatalf("entry metadata missing: %#v", loaded.Entries[0])
	}
	if len(loaded.Refinements) != 1 {
		t.Fatalf("refinements = %d, want 1", len(loaded.Refinements))
	}
}

func TestLoadMissingFileDegradesToEmpty(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "nope"), Scope: ScopeLocal})
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if state.Scope != ScopeLocal {
		t.Fatalf("scope = %q, want local", state.Scope)
	}
	if len(state.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(state.Entries))
	}
}

func TestLoadCorruptFileDegradesToEmptyWithError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "learning")
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeGlobal})
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	state, err := store.Load()
	if err == nil {
		t.Fatal("expected a corrupt-file error")
	}
	if len(state.Entries) != 0 {
		t.Fatalf("corrupt load should degrade to empty, got %d entries", len(state.Entries))
	}
}

func TestMergeHarnessStatesLocalWins(t *testing.T) {
	global := State{Scope: ScopeGlobal, Entries: []Entry{
		NewEntry(KindMemory, "global fact", "v1", "fact", "general", ScopeGlobal, "agent", time.Now()),
		NewEntry(KindPrompt, "global note", "keep", "note", "policy", ScopeGlobal, "agent", time.Now()),
	}}
	local := State{Scope: ScopeLocal, Entries: []Entry{
		// Same id+kind => local wins.
		NewEntry(KindMemory, "global fact", "v2 local override", "fact", "general", ScopeLocal, "agent", time.Now()),
	}}

	merged := MergeHarnessStates(global, local)
	if len(merged) != 2 {
		t.Fatalf("merged = %d, want 2", len(merged))
	}
	for _, e := range merged {
		if e.ID == "fact" {
			if e.Content != "v2 local override" {
				t.Fatalf("local should win for fact, got %q", e.Content)
			}
		}
	}
}

func TestSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Check working tree", "check_working_tree"},
		{"  Multi--word   Name  ", "multi_word_name"},
		{"----", "entry"},
		{"C# Features", "c_features"},
	}
	for _, c := range cases {
		if got := Slug(c.in, "entry"); got != c.want {
			t.Fatalf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatHarnessStateForPromptBounded(t *testing.T) {
	entries := make([]Entry, 30)
	for i := range entries {
		e := NewEntry(KindMemory, "fact", strings.Repeat("x", 300), "", "general", ScopeLocal, "agent", time.Now())
		e.ID = "fact"
		entries[i] = e
	}
	out := FormatHarnessStateForPrompt(ScopeLocal, entries, 5)
	if out == "" {
		t.Fatal("expected non-empty overview")
	}
	if contains(out, strings.Repeat("x", 300)) {
		t.Fatal("overview must truncate long content")
	}
	if !contains(out, "+25 more") {
		t.Fatalf("overview should report overflow: %s", out)
	}
}

func TestFormatHarnessStateForPromptEmpty(t *testing.T) {
	if out := FormatHarnessStateForPrompt(ScopeLocal, nil, 20); out != "" {
		t.Fatalf("empty overview = %q, want empty", out)
	}
}

func TestStateJSONRoundTripIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "learning")
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeLocal})
	state := State{Scope: ScopeLocal, Entries: []Entry{
		NewEntry(KindRecipe, "greet", "Say hello", "greet", "general", ScopeLocal, "agent", time.Now()),
	}}
	state.Entries[0].Recipe = &Recipe{
		Name:        "greet",
		Description: "Greet the world",
		Commands:    []RecipeCommand{{ID: "run", Tool: "bash", Args: map[string]any{"command": "echo hello"}}},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.Save(first); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	second, err := store.Load()
	if err != nil {
		t.Fatalf("Load2: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("resave changed state")
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
