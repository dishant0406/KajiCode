package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func seedNotes(t *testing.T, store *harness.Store, entries ...harness.Entry) {
	t.Helper()
	if err := store.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, entries...)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestNotesMatchRequestAcrossScopes(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps, harness.NewEntry(harness.KindMemory, "Vision-gate orphan token", "The TUI drops images for a non-vision model but leaves the [Image #1] token in the composer.", "vision", "general", harness.ScopeProject, "agent", testNow()))
	seedNotes(t, gs, harness.NewEntry(harness.KindMemory, "Mongo dotted keys", "Keys with dots break $unset.", "mongo", "general", harness.ScopeGlobal, "agent", testNow()))
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)

	notes := eng.Notes("vision drop leaves the image token in the composer")
	if !strings.Contains(notes, "Vision-gate orphan token") || strings.Contains(notes, "Mongo") {
		t.Fatalf("notes should hold only the matching project note: %q", notes)
	}
	if !strings.Contains(notes, "Call recall") {
		t.Fatalf("notes should say how to read more: %q", notes)
	}
	if global := eng.Notes("mongo dotted keys break $unset"); !strings.Contains(global, "Mongo dotted keys") {
		t.Fatalf("a matching global note should be attached: %q", global)
	}
	for _, request := range []string{"hello there", "do the thing", "composer"} {
		if notes := eng.Notes(request); notes != "" {
			t.Fatalf("%q is too vague for notes, got %q", request, notes)
		}
	}
	if (*LearningEngine)(nil).Notes("anything") != "" {
		t.Fatal("nil engine should give no notes")
	}
}

func TestNotesSkipStandingNotesAndStayShort(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps,
		harness.NewEntry(harness.KindPrompt, "Release rule", "Always tag the release after the smoke test.", "release_rule", "general", harness.ScopeProject, "agent", testNow()),
		harness.NewEntry(harness.KindMemory, "Release smoke test", strings.Repeat("smoke test release details ", 100), "release_smoke", "general", harness.ScopeProject, "agent", testNow()),
	)
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)

	notes := eng.Notes("release smoke test details")
	if strings.Contains(notes, "Release rule") {
		t.Fatalf("a standing note already in the system prompt was repeated: %q", notes)
	}
	if !strings.Contains(notes, "Release smoke test") {
		t.Fatalf("matching note missing: %q", notes)
	}
	for _, line := range strings.Split(notes, "\n") {
		if len([]rune(line)) > requestNoteContentLen+60 {
			t.Fatalf("note line not shortened (%d): %q", len(line), line)
		}
	}
}

// The notes must reach the model as part of the user's request, and a request
// with no matching note must be sent exactly as typed.
func TestRunAttachesMatchingNotesToRequest(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps, harness.NewEntry(harness.KindMemory, "KajiCode tests on this machine", "Run make test; go test has two known failures in internal/tui.", "tests", "general", harness.ScopeProject, "agent", testNow()))
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)

	lastUser := func(prompt string) string {
		t.Helper()
		provider := &learnScriptedProvider{}
		if _, err := Run(context.Background(), prompt, provider, Options{Cwd: t.TempDir(), Learning: eng}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		messages := provider.requests[0].Messages
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == kajicoderuntime.MessageRoleUser {
				return messages[i].Content
			}
		}
		t.Fatal("no user message in request")
		return ""
	}

	withNotes := lastUser("how do I run the tests?")
	if !strings.HasPrefix(withNotes, "how do I run the tests?") || !strings.Contains(withNotes, "<notes_from_earlier_sessions>") || !strings.Contains(withNotes, "KajiCode tests on this machine") {
		t.Fatalf("matching note not attached to the request: %q", withNotes)
	}
	if plain := lastUser("hello"); plain != "hello" {
		t.Fatalf("request with no matching note changed: %q", plain)
	}
}

func TestReinforceKeepsStandingAndMatchedNotesAlive(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps,
		harness.NewEntry(harness.KindPrompt, "Use cmake", "Always build with cmake", "cmake", "general", harness.ScopeProject, "agent", testNow()),
		harness.NewEntry(harness.KindMemory, "Mongo dotted keys", "keys with dots break $unset", "mongo", "general", harness.ScopeProject, "agent", testNow()),
		harness.NewEntry(harness.KindMemory, "Unrelated", "release tagging steps", "unrelated", "general", harness.ScopeProject, "agent", testNow()),
	)
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	eng.Context() // rendering the prompt alone must record nothing
	eng.Reinforce()
	if state, _ := ps.Load(); state.Entries[0].Reinforcements+state.Entries[1].Reinforcements+state.Entries[2].Reinforcements != 0 {
		t.Fatalf("rendering the prompt reinforced notes: %#v", state.Entries)
	}

	if eng.BeginRun("mongo dotted keys break $unset") == "" {
		t.Fatal("the mongo note should match")
	}
	eng.Reinforce()

	state, _ := ps.Load()
	got := map[string]int{}
	for _, entry := range state.Entries {
		got[entry.ID] = entry.Reinforcements
	}
	if got["cmake"] != 1 || got["mongo"] != 1 || got["unrelated"] != 0 {
		t.Fatalf("reinforcements = %v, want the standing and matched notes only", got)
	}
}

// Standing notes are chosen by last edit, so a note reinforced on every run
// cannot crowd out a newer project rule (the old lock-in bug).
func TestStandingNotesPreferNewestEditOverReinforcement(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	var rules []harness.Entry
	for i := 0; i < learnedPromptMaxEntries; i++ {
		rule := harness.NewEntry(harness.KindPrompt, "Old rule "+string(rune('a'+i)), "old", "old_"+string(rune('a'+i)), "general", harness.ScopeProject, "agent", testNow())
		rule.Reinforcements = 5000
		rule.LastUsedAt = "2030-01-01T00:00:00Z"
		rules = append(rules, rule)
	}
	fresh := harness.NewEntry(harness.KindPrompt, "Fresh rule", "new convention", "fresh", "general", harness.ScopeProject, "agent", testNow().Add(time.Hour))
	seedNotes(t, ps, append(rules, fresh)...)

	ctx := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss).Context()
	if !strings.HasPrefix(ctx, "- Fresh rule:") {
		t.Fatalf("newest rule should lead the standing notes:\n%s", ctx)
	}
	if got := strings.Count(ctx, "\n") + 1; got != learnedPromptMaxEntries {
		t.Fatalf("standing notes = %d, want %d", got, learnedPromptMaxEntries)
	}
}

func TestStandingNotesStayShort(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps, harness.NewEntry(harness.KindPrompt, strings.Repeat("long title ", 50), strings.Repeat("very long content ", 4000), "big", "general", harness.ScopeProject, "agent", testNow()))
	ctx := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss).Context()
	if limit := learnedPromptMaxTitleLen + learnedPromptMaxContentLen + 4; len([]rune(ctx)) > limit {
		t.Fatalf("standing note not shortened: %d runes", len([]rune(ctx)))
	}
}

// The TUI and ACP reuse one engine for many runs; every run must reinforce what
// it used, not only the first.
func TestReinforcementWorksOnEveryRunOfOneEngine(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps, harness.NewEntry(harness.KindMemory, "KajiCode tests on this machine", "Run make test; go test has two known failures in internal/tui.", "tests", "general", harness.ScopeProject, "agent", testNow()))
	eng := NewLearningEngine(config.LearningConfig{}, nil, gs, ps, ss)
	for run := 0; run < 2; run++ {
		if _, err := Run(context.Background(), "how do I run the tests?", &learnScriptedProvider{}, Options{Cwd: t.TempDir(), Learning: eng}); err != nil {
			t.Fatalf("Run %d: %v", run, err)
		}
	}
	state, _ := ps.Load()
	if state.Entries[0].Reinforcements != 2 {
		t.Fatalf("reinforcements after two runs = %d, want 2", state.Entries[0].Reinforcements)
	}
}

// Notes go only to the provider. The learning pass, the run result, and so
// compaction and saved sessions must see the request exactly as typed, or the
// planner would re-save notes about the notes.
func TestRequestNotesStayOutOfHistoryAndLearningPass(t *testing.T) {
	gs, ps, ss := newEngineStores(t)
	seedNotes(t, ps, harness.NewEntry(harness.KindMemory, "Cmake build flags", "Build with cmake --build . --parallel", "cmake_flags", "general", harness.ScopeProject, "agent", testNow()))
	provider := &learnScriptedProvider{}
	eng := NewLearningEngine(config.LearningConfig{}, provider, gs, ps, ss)
	result, err := Run(context.Background(), "no, build cmake with parallel flags", provider, Options{Cwd: t.TempDir(), Learning: eng})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sawNotes, sawPass := false, false
	for _, request := range provider.requests {
		joined := ""
		for _, message := range request.Messages {
			joined += message.Content
		}
		isPass := strings.Contains(joined, "auto-learning review gate") || strings.Contains(joined, "optimizer for KajiCode's self-learning memory")
		if isPass {
			sawPass = true
			if strings.Contains(joined, requestNotesOpen) {
				t.Fatalf("learning pass saw the attached notes:\n%.400s", joined)
			}
		} else if strings.Contains(joined, requestNotesOpen) {
			sawNotes = true
		}
	}
	if !sawNotes || !sawPass {
		t.Fatalf("test did not exercise both paths: notes=%v pass=%v", sawNotes, sawPass)
	}
	for _, message := range result.Messages {
		if strings.Contains(message.Content, requestNotesOpen) {
			t.Fatalf("run result carries the notes: %q", message.Content)
		}
	}
}
