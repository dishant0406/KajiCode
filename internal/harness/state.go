// Package harness owns KajiCode's self-learning (perpetual memory) system.
// It is a port of prime-agent's continual-harness + /refine architecture:
//
//   - A durable harness_state.json store of four editable kinds (prompt, memory,
//     recipe, subagent) in two scopes (global, per-session local).
//   - A learning pipeline (review gate -> plan -> apply -> record) whose apply
//     step is a guarded critical section with real conflict detection.
//   - Go-native "recipe" entries (command chains over already-registered tools)
//     that replace prime-agent's Python callables, so they run on any platform
//     with no additional interpreter.
//   - Rollback + full refinement history.
//
// The learning loop is opt-in from the agent's perspective: a nil controller in
// agent.Options leaves the loop byte-identical (same convention as SelfCorrect,
// Profile, and Trace).
package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/fsutil"
)

// Kind enumerates the editable harness entry types. It mirrors prime-agent's
// four harness kinds, with Skill replaced by Recipe (Go-native command chains).
type Kind string

const (
	KindPrompt   Kind = "prompt"
	KindMemory   Kind = "memory"
	KindRecipe   Kind = "recipe"
	KindSubagent Kind = "subagent"

	// BasePromptID is a reserved id that can never be created/updated/deleted by
	// learning: it is the immutable base system prompt. Learning only edits the
	// supplemental layer.
	BasePromptID   = "base_system_prompt"
	basePromptKind = KindPrompt
)

var allKinds = []Kind{KindPrompt, KindMemory, KindRecipe, KindSubagent}

// Scope identifies whether an entry lives in the global or per-session store.
type Scope string

const (
	ScopeLocal  Scope = "local"
	ScopeGlobal Scope = "global"
)

func (scope Scope) valid() bool {
	return scope == ScopeLocal || scope == ScopeGlobal
}

// RecipeCommand describes one step of a recipe as a call to an already-registered
// KajiCode tool. There is no new interpreter: "tool" is the registry name and
// "args" is the literal argument map passed to that tool's RunWithOptions.
type RecipeCommand struct {
	ID   string         `json:"id,omitempty"`
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// Recipe is a reusable Go-native procedure (the replacement for prime-agent's
// Python skill callables). Run via recipe_run, which dispatches each command to
// the referenced tool through the registry.
type Recipe struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Commands    []RecipeCommand `json:"commands"`
}

// Entry is a single durable harness record.
type Entry struct {
	ID        string         `json:"id"`
	Kind      Kind           `json:"kind"`
	Title     string         `json:"title"`
	Content   string         `json:"content"`
	Path      string         `json:"path,omitempty"`
	Scope     Scope          `json:"scope"`
	Recipe    *Recipe        `json:"recipe,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Source    string         `json:"source,omitempty"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
	// LastUsedAt is stamped by TouchEntry when the model actually applies the
	// lesson in a turn. It drives recall (freshest-first) so re-used lessons are
	// pinned at the top of the prompted budget while stale ones age out.
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	Version    int    `json:"version"`
}

// NewEntry builds an entry with defaults applied. ID is derived from title when
// empty. CreatedAt/UpdatedAt are stamped from now unless already set.
func NewEntry(kind Kind, title, content string, id string, path string, scope Scope, source string, now time.Time) Entry {
	if id == "" {
		id = Slug(title, string(kind))
	}
	if path == "" {
		path = "general"
	}
	if scope == "" {
		scope = ScopeLocal
	}
	if source == "" {
		source = "agent"
	}
	created := ""
	if !now.IsZero() {
		created = now.UTC().Format(time.RFC3339)
	}
	return Entry{
		ID:        id,
		Kind:      kind,
		Title:     title,
		Content:   content,
		Path:      path,
		Scope:     scope,
		Source:    source,
		CreatedAt: created,
		UpdatedAt: created,
		Version:   1,
	}
}

// RefinementEvent records a single applied (or attempted) learning pass.
type RefinementEvent struct {
	ID        string   `json:"id"`
	Trigger   string   `json:"trigger"`
	Changes   []string `json:"changes"`
	Evidence  string   `json:"evidence,omitempty"`
	Outcome   string   `json:"outcome,omitempty"`
	CreatedAt string   `json:"createdAt"`
}

// State is the in-memory mutable harness state for one scope. It is persisted to
// a single harness_state.json file. All mutations go through the owning
// Controller (or the exported Store helpers) so concurrent processes sharing the
// file serialize on an OS file lock, exactly like sessions.
type State struct {
	Scope       Scope             `json:"scope"`
	Entries     []Entry           `json:"entries"`
	Refinements []RefinementEvent `json:"refinements,omitempty"`
}

// StateFile is the canonical on-disk file name inside a learning directory.
const StateFile = "harness_state.json"

// RecipesDir is the subdirectory holding learned recipe manifests. It is kept
// separate from skills (per requirement): learned recipes are discovered only
// from this dedicated root, never merged into internal/skills.
const RecipesDir = "recipes"

// DefaultName is used to derive the global learning data directory from the
// shared data home, mirroring sessions.DefaultRoot (".../kajicode/learning").
const homeRelDir = "learning"

// Store persists a State to a harness_state.json under a learning directory.
// It is safe for concurrent use within a process (mutex) and across processes
// (OS file lock on the lock file, mirroring sessions).
type Store struct {
	// Dir is the learning root for this scope (e.g. <dataHome>/kajicode/learning
	// for global, <sessionDir>/learning for local).
	Dir string
	// Scope identifies whether this store is the global or per-session one.
	Scope Scope
	now   func() time.Time
	mu    sync.Mutex
}

// StoreOptions configures a Store.
type StoreOptions struct {
	Dir   string
	Scope Scope
	Now   func() time.Time
}

// NewStore builds a Store. Dir defaults to GlobalDir(env) when empty. now
// defaults to time.Now.
func NewStore(options StoreOptions) *Store {
	scope := options.Scope
	if !scope.valid() {
		scope = ScopeGlobal
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{Dir: options.Dir, Scope: scope, now: now}
}

// GlobalDir resolves the global learning directory, mirroring
// sessions.DefaultRoot. Env keys: XDG_DATA_HOME, HOME.
func GlobalDir(env map[string]string) string {
	dataHome := envValue(env, "XDG_DATA_HOME")
	home := envValue(env, "HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "kajicode", homeRelDir)
}

// LocalDir derives a session-local learning root from a session directory.
// It is "<sessionDir>/learning".
func LocalDir(sessionDir string) string {
	return filepath.Join(sessionDir, homeRelDir)
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

// Load reads the state file and returns the parsed State. A missing or corrupt
// file degrades to an empty state (never an unreadable store); the underlying
// parse error is surfaced via the returned error only for the corrupt case so
// callers can decide whether to log/replace it. Callers that need to preserve a
// corrupt file should inspect err.
func (store *Store) Load() (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadLocked()
}

func (store *Store) loadLocked() (State, error) {
	path := filepath.Join(store.Dir, StateFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Scope: store.Scope}, nil
		}
		return State{Scope: store.Scope}, fmt.Errorf("read learning state %s: %w", path, err)
	}
	state, parseErr := decodeState(data, store.Scope)
	if parseErr != nil {
		// Degrade to empty but report the corrupt file so callers can log/back up.
		state.Scope = store.Scope
		return state, fmt.Errorf("corrupt learning state %s: %w", path, parseErr)
	}
	return state, nil
}

// decodeState parses JSON into a State, normalizing scope and kinds, and
// dropping malformed entries instead of failing the whole load when individual
// records are bad.
func decodeState(data []byte, fallbackScope Scope) (State, error) {
	var raw struct {
		Scope       string            `json:"scope"`
		Entries     []json.RawMessage `json:"entries"`
		Refinements []RefinementEvent `json:"refinements"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return State{}, err
	}
	state := State{Scope: Scope(raw.Scope)}
	if !state.Scope.valid() {
		state.Scope = fallbackScope
	}
	for _, rawEntry := range raw.Entries {
		entry, err := decodeEntry(rawEntry, state.Scope)
		if err != nil {
			continue
		}
		state.Entries = append(state.Entries, entry)
	}
	sortEntries(state.Entries)
	state.Refinements = raw.Refinements
	return state, nil
}

func decodeEntry(raw json.RawMessage, fallbackScope Scope) (Entry, error) {
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, err
	}
	if !entry.Kind.valid() {
		return Entry{}, errors.New("invalid kind")
	}
	if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Title) == "" {
		return Entry{}, errors.New("missing id or title")
	}
	if !entry.Scope.valid() {
		entry.Scope = fallbackScope
	}
	return entry, nil
}

func (kind Kind) valid() bool {
	switch kind {
	case KindPrompt, KindMemory, KindRecipe, KindSubagent:
		return true
	default:
		return false
	}
}

// Save atomically writes the state to disk: temp file in the same directory,
// fsync, then rename with a Windows-sensitive retry. This guarantees a reader
// never observes a partial write and no data is lost to a crash mid-write.
func (store *Store) Save(state State) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.saveLocked(state)
}

// WithLock runs fn while holding the store's in-process mutex AND the exclusive
// OS file lock, guaranteeing cross-process serialization of load→mutate→save.
// fn receives the current state and returns the state to persist plus an error;
// save is skipped when fn errors. This is the only mutation path the harness
// apply/rollback pipeline uses, so concurrent processes (learn tool, agent loop,
// CLI rewind) never corrupt the shared file.
func (store *Store) WithLock(fn func(State) (State, error)) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	release, err := store.acquireFileLock()
	if err != nil {
		return err
	}
	defer release()
	state, _ := store.loadLocked()
	next, err := fn(state)
	if err != nil {
		return err
	}
	return store.saveLocked(next)
}

// TouchEntry stamps LastUsedAt on an existing entry (under the OS file lock) to
// record that the model actually applied that lesson in a run. bumpVersion
// controls whether the entry's Version advances: bumping keeps touch traffic
// conflict-visible for concurrent writers, while false isolates recall metadata
// from real content edits. Touch on a missing entry or a nil store is a no-op.
// The returned bool reports whether the touch landed.
func (store *Store) TouchEntry(kind Kind, id string, now time.Time, bumpVersion bool) bool {
	if store == nil || kind == "" || id == "" {
		return false
	}
	stamp := now.UTC().Format(time.RFC3339)
	var landed bool
	err := store.WithLock(func(state State) (State, error) {
		idx := idxEntry(state.Entries, kind, id)
		if idx < 0 {
			return state, nil
		}
		state.Entries[idx].LastUsedAt = stamp
		if bumpVersion {
			state.Entries[idx].Version++
		}
		landed = true
		return state, nil
	})
	if err != nil {
		return false
	}
	return landed
}

func (store *Store) saveLocked(state State) error {
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		return fmt.Errorf("create learning dir: %w", err)
	}
	state.Scope = store.Scope
	sortEntries(state.Entries)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode learning state: %w", err)
	}
	path := filepath.Join(store.Dir, StateFile)
	temp, err := os.CreateTemp(store.Dir, StateFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("create learning temp file: %w", err)
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(data); err != nil {
		cleanup()
		_ = temp.Close()
		return fmt.Errorf("write learning temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		_ = temp.Close()
		return fmt.Errorf("fsync learning temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close learning temp file: %w", err)
	}
	if err := fsutil.RenameWithRetry(tempName, path, nil); err != nil {
		cleanup()
		return fmt.Errorf("rename learning state into place: %w", err)
	}
	return nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		if entries[i].ID != entries[j].ID {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Title < entries[j].Title
	})
}

// OrderByRecency sorts a merged set so the most-recently-reused entries surface
// first (LastUsedAt desc), then most-recently-updated, then by kind/id/title for a
// deterministic tiebreak. It mirrors compaction's "keep the freshest within a
// budget" principle: recall prefers lessons the model has actually applied, so a
// bounded prompt shows the highest-value memory rather than an alphabetical slab.
func OrderByRecency(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if cmp := compareRecency(a, b); cmp != 0 {
			return cmp < 0
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Title < b.Title
	})
}

// compareRecency orders higher on LastUsedAt, falling back to UpdatedAt. Returns
// negative when a is more recent than b, zero on a tie. timestamps that do not
// parse as RFC3339 are treated as never-used (oldest).
func compareRecency(a, b Entry) int {
	at, errA := time.Parse(time.RFC3339, a.LastUsedAt)
	bt, errB := time.Parse(time.RFC3339, b.LastUsedAt)
	if errA != nil {
		at = time.Time{}
	}
	if errB != nil {
		bt = time.Time{}
	}
	if at.After(bt) {
		return -1
	}
	if bt.After(at) {
		return 1
	}
	ua, errA := time.Parse(time.RFC3339, a.UpdatedAt)
	ub, errB := time.Parse(time.RFC3339, b.UpdatedAt)
	if errA != nil {
		ua = time.Time{}
	}
	if errB != nil {
		ub = time.Time{}
	}
	switch {
	case ua.After(ub):
		return -1
	case ub.After(ua):
		return 1
	default:
		return 0
	}
}

// MergeHarnessStates unions a global and a local state into one view for
// planning/prompt display. Local entries win on id+kind conflict (a local entry
// shadows the global one) and are surfaced with a "local:" scope prefix on the
// id so editorial intent is unambiguous.
func MergeHarnessStates(global, local State) []Entry {
	byKey := map[string]Entry{}
	var order []string
	add := func(entry Entry) {
		key := entryKey(entry)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = entry
	}
	for _, e := range mergeListsForKind(global.Entries, local.Entries) {
		add(e)
	}
	out := make([]Entry, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	OrderByRecency(out)
	return out
}

func mergeListsForKind(global, local []Entry) []Entry {
	byKey := map[string]Entry{}
	for _, e := range global {
		byKey[entryKey(e)] = e
	}
	for _, e := range local {
		byKey[entryKey(e)] = e // local wins
	}
	out := make([]Entry, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, e)
	}
	sortEntries(out)
	return out
}

func entryKey(entry Entry) string {
	return string(entry.Kind) + ":" + entry.ID
}

// Slug derives an id from an arbitrary string: lowercase alphanumerics and
// underscores, collapsed, capped at 80 runes. fallback is used when raw slugs to
// empty.
func Slug(raw, fallback string) string {
	var b strings.Builder
	underscored := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			underscored = false
		case r == '_' || r == '-' || r == ' ':
			if !underscored {
				b.WriteByte('_')
				underscored = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = Slug(fallback, "entry")
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// FormatHarnessStateForPrompt renders a compact, bounded overview of merged
// entries for injection into the system prompt. It never includes full entry
// content beyond a per-entry truncation, and returns "" for an empty set so an
// untouched run adds nothing (byte-identical prompt).
func FormatHarnessStateForPrompt(scope Scope, entries []Entry, maxEntriesPerKind int) string {
	if len(entries) == 0 {
		return ""
	}
	if maxEntriesPerKind <= 0 {
		maxEntriesPerKind = 20
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<learning_state scope=%q>\n", scope)
	for _, kind := range allKinds {
		var kindEntries []Entry
		for _, e := range entries {
			if e.Kind == kind {
				kindEntries = append(kindEntries, e)
			}
		}
		fmt.Fprintf(&b, "%s: %d\n", kind, len(kindEntries))
		shown := 0
		for _, e := range kindEntries {
			if shown >= maxEntriesPerKind {
				break
			}
			summary := truncate(e.Content, 120)
			fmt.Fprintf(&b, "  - [%s:%s/%s] %s (%s, v%d): %s\n",
				e.Scope, e.Kind, e.ID, heading(e.Title), e.Path, e.Version, summary)
			if e.Recipe != nil {
				fmt.Fprintf(&b, "      recipe commands=%d\n", len(e.Recipe.Commands))
			}
			shown++
		}
		if overflow := len(kindEntries) - shown; overflow > 0 {
			fmt.Fprintf(&b, "  - +%d more\n", overflow)
		}
	}
	b.WriteString("</learning_state>")
	return strings.TrimSpace(b.String())
}

func heading(s string) string {
	if s == "" {
		return "(untitled)"
	}
	return s
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
