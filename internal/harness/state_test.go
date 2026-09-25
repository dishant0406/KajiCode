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
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "nope"), Scope: ScopeSession})
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if state.Scope != ScopeSession {
		t.Fatalf("scope = %q, want session", state.Scope)
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
	local := State{Scope: ScopeSession, Entries: []Entry{
		// Same id+kind => local wins.
		NewEntry(KindMemory, "global fact", "v2 local override", "fact", "general", ScopeSession, "agent", time.Now()),
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
		e := NewEntry(KindMemory, "fact", strings.Repeat("x", 300), "", "general", ScopeSession, "agent", time.Now())
		e.ID = "fact"
		entries[i] = e
	}
	out := FormatHarnessStateForPrompt(ScopeSession, entries, 5)
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
	if out := FormatHarnessStateForPrompt(ScopeSession, nil, 20); out != "" {
		t.Fatalf("empty overview = %q, want empty", out)
	}
}

func TestStateJSONRoundTripIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "learning")
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeSession})
	state := State{Scope: ScopeSession, Entries: []Entry{
		NewEntry(KindRecipe, "greet", "Say hello", "greet", "general", ScopeSession, "agent", time.Now()),
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

func TestTouchEntryStampsLastUsedAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "learning")
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeGlobal, Now: fixedNow})
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries, NewEntry(KindMemory, "fact", "c", "fact", "general", ScopeGlobal, "agent", time.Now()))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if !store.TouchEntry(KindMemory, "fact", now, false) {
		t.Fatal("TouchEntry returned false for existing entry")
	}
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(state.Entries))
	}
	if state.Entries[0].LastUsedAt != "2026-03-04T05:06:07Z" {
		t.Fatalf("LastUsedAt = %q", state.Entries[0].LastUsedAt)
	}
	if state.Entries[0].Version != 1 {
		t.Fatalf("non-bumping touch must not advance version, got v%d", state.Entries[0].Version)
	}
	// bumpVersion advances the version.
	if !store.TouchEntry(KindMemory, "fact", now.Add(time.Hour), true) {
		t.Fatal("TouchEntry(bump) returned false")
	}
	if state, _ = store.Load(); state.Entries[0].Version != 2 {
		t.Fatalf("touch bump did not advance version, got v%d", state.Entries[0].Version)
	}
}

func TestTouchEntryMissingIsNoop(t *testing.T) {
	store := NewStore(StoreOptions{Dir: filepath.Join(t.TempDir(), "learning"), Scope: ScopeGlobal})
	if store.TouchEntry(KindMemory, "nope", time.Now(), false) {
		t.Fatal("TouchEntry should be false for a missing entry")
	}
	if (*Store)(nil).TouchEntry(KindMemory, "x", time.Now(), false) {
		t.Fatal("nil store TouchEntry should be a no-op")
	}
}

func TestOrderByRecency(t *testing.T) {
	older := "2025-01-01T00:00:00Z"
	newest := "2026-02-02T00:00:00Z"
	mid := "2025-06-01T00:00:00Z"
	entries := []Entry{
		{ID: "a", Kind: KindMemory, Title: "A", Scope: ScopeGlobal, UpdatedAt: older},
		{ID: "b", Kind: KindMemory, Title: "B", Scope: ScopeGlobal, UpdatedAt: mid, LastUsedAt: newest},
		{ID: "c", Kind: KindMemory, Title: "C", Scope: ScopeGlobal, UpdatedAt: mid},
		{ID: "d", Kind: KindMemory, Title: "D", Scope: ScopeGlobal, UpdatedAt: newest},
	}
	OrderByRecency(entries)
	if entries[0].ID != "b" {
		t.Fatalf("most-recently-used should surface first, got %#v", entries[0])
	}
	// d (updated newest but never used) beats c (mid UpdatedAt).
	if entries[1].ID != "d" {
		t.Fatalf("updated-newest should beat mid-updated, got %#v", entries[1])
	}
}

func TestPruneStaleDropsOldUnreinforcedEntries(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject})
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -200).Format(time.RFC3339)
	fresh := now.AddDate(0, 0, -5).Format(time.RFC3339)
	if err := store.WithLock(func(state State) (State, error) {
		stale := NewEntry(KindMemory, "stale", "old", "stale", "general", ScopeProject, "agent", now)
		stale.LastUsedAt = old
		stale.UpdatedAt = old
		kept := NewEntry(KindMemory, "kept", "fresh", "kept", "general", ScopeProject, "agent", now)
		kept.LastUsedAt = fresh
		state.Entries = append(state.Entries, stale, kept)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if removed := store.PruneStale(90, 0, now); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	state, _ := store.Load()
	if len(state.Entries) != 1 || state.Entries[0].ID != "kept" {
		t.Fatalf("entries = %#v", state.Entries)
	}
}

func TestPruneStaleCapsByRecency(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject})
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id string, used time.Time) Entry {
		e := NewEntry(KindMemory, id, id, id, "general", ScopeProject, "agent", now)
		e.LastUsedAt = used.Format(time.RFC3339)
		e.UpdatedAt = used.Format(time.RFC3339)
		return e
	}
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries,
			mk("old", now.AddDate(0, 0, -3)),
			mk("newest", now),
			mk("mid", now.AddDate(0, 0, -1)),
		)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if removed := store.PruneStale(0, 2, now); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	state, _ := store.Load()
	if len(state.Entries) != 2 {
		t.Fatalf("entries = %#v", state.Entries)
	}
	for _, e := range state.Entries {
		if e.ID == "old" {
			t.Fatalf("least-recently-used entry should have been capped: %#v", state.Entries)
		}
	}
}

func TestTouchEntryIncrementsReinforcements(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject, Now: func() time.Time { return now }})
	if err := store.WithLock(func(state State) (State, error) {
		state.Entries = append(state.Entries, NewEntry(KindMemory, "fact", "c", "fact", "general", ScopeProject, "agent", now))
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !store.TouchEntry(KindMemory, "fact", now, false) {
		t.Fatal("TouchEntry returned false for existing entry")
	}
	if !store.TouchEntry(KindMemory, "fact", now, false) {
		t.Fatal("TouchEntry returned false on second call")
	}
	state, _ := store.Load()
	if state.Entries[0].Reinforcements != 2 {
		t.Fatalf("reinforcements = %d, want 2", state.Entries[0].Reinforcements)
	}
}

func TestLoadNormalizesLegacyLocalScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{"scope":"local","entries":[{"id":"f","kind":"memory","title":"F","content":"c","scope":"local","createdAt":"2025-01-01T00:00:00Z","updatedAt":"2025-01-01T00:00:00Z","version":1}]}`
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeSession})
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(state.Entries) != 1 || state.Entries[0].Scope != ScopeSession {
		t.Fatalf("legacy scope not normalized: %#v", state.Entries)
	}
}

func TestPruneStaleCapsRefinementsKeepingNewestRollback(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject})
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := store.WithLock(func(state State) (State, error) {
		for i := 0; i < MaxRefinements*4; i++ {
			event := RefinementEvent{ID: "r", Trigger: "auto", Changes: []string{"x"}, CreatedAt: now.Format(time.RFC3339)}
			if i == MaxRefinements*4-1 {
				// The newest event is the one LatestRollback must still find.
				event.Rollback = []EditOutcome{{Proposal: EditProposal{Action: ActionCreate, Kind: KindMemory, ID: "fact"}, Applied: true}}
			}
			state.Refinements = append(state.Refinements, event)
		}
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store.PruneStale(0, 0, now)
	state, _ := store.Load()
	if len(state.Refinements) != MaxRefinements {
		t.Fatalf("refinements = %d, want %d (bounded)", len(state.Refinements), MaxRefinements)
	}
	if _, ok := LatestRollback(state); !ok {
		t.Fatal("newest rollback-able event should survive the cap")
	}
}

func TestPruneStaleRemovesOrphanedRecipeManifest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject})
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	recipe := &Recipe{Name: "flow", Commands: []RecipeCommand{{Tool: "read_file"}}}
	manifest, err := SaveRecipe(dir, *recipe, nil)
	if err != nil {
		t.Fatalf("SaveRecipe: %v", err)
	}
	old := now.AddDate(0, 0, -200).Format(time.RFC3339)
	if err := store.WithLock(func(state State) (State, error) {
		entry := NewEntry(KindRecipe, "flow", "c", "flow", "general", ScopeProject, "agent", now)
		entry.Recipe = recipe
		entry.LastUsedAt = old
		entry.UpdatedAt = old
		state.Entries = append(state.Entries, entry)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if removed := store.PruneStale(90, 0, now); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("orphaned recipe manifest should be removed, stat err = %v", err)
	}
}

func TestPruneStaleSweepsPreExistingOrphanRecipe(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(StoreOptions{Dir: dir, Scope: ScopeProject})
	now := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// A manifest with no surviving entry (the pre-fix orphan case).
	orphan, err := SaveRecipe(dir, Recipe{Name: "orphan", Commands: []RecipeCommand{{Tool: "read_file"}}}, nil)
	if err != nil {
		t.Fatalf("SaveRecipe: %v", err)
	}
	// A manifest whose entry survives the prune.
	kept, err := SaveRecipe(dir, Recipe{Name: "kept", Commands: []RecipeCommand{{Tool: "read_file"}}}, nil)
	if err != nil {
		t.Fatalf("SaveRecipe: %v", err)
	}
	if err := store.WithLock(func(state State) (State, error) {
		entry := NewEntry(KindRecipe, "kept", "c", "kept", "general", ScopeProject, "agent", now)
		entry.Recipe = &Recipe{Name: "kept", Commands: []RecipeCommand{{Tool: "read_file"}}}
		state.Entries = append(state.Entries, entry)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store.PruneStale(0, 0, now)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("pre-existing orphan manifest should be swept, stat err = %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("in-use manifest must survive: %v", err)
	}
}
