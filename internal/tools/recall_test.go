package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/harness"
)

func testTime() time.Time { return time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC) }

// seedProjectLesson writes one entry into the project learning store at root.
func seedProjectLesson(t *testing.T, root string, entry harness.Entry) {
	t.Helper()
	store := harness.NewStore(harness.StoreOptions{Dir: root, Scope: harness.ScopeProject})
	if err := store.WithLock(func(state harness.State) (harness.State, error) {
		state.Entries = append(state.Entries, entry)
		return state, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestRecallToolFindsByQuery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	seedProjectLesson(t, root, harness.NewEntry(harness.KindMemory, "Use cmake", "Always build with cmake --build .", "cmake", "general", harness.ScopeProject, "agent", testTime()))
	seedProjectLesson(t, root, harness.NewEntry(harness.KindMemory, "Lint", "Run gofmt before commit", "lint", "general", harness.ScopeProject, "agent", testTime()))

	tool := NewRecallTool(root, "")
	result := tool.Run(context.Background(), map[string]any{"query": "cmake"})
	if result.Status != StatusOK {
		t.Fatalf("recall = %#v", result)
	}
	if !strings.Contains(result.Output, "cmake") || strings.Contains(result.Output, "gofmt") {
		t.Fatalf("recall output = %q", result.Output)
	}
}

func TestRecallToolEmptyStore(t *testing.T) {
	tool := NewRecallTool(filepath.Join(t.TempDir(), "learning"), "")
	result := tool.Run(context.Background(), map[string]any{"query": "anything"})
	if result.Status != StatusOK || !strings.Contains(result.Output, "No stored lessons") {
		t.Fatalf("empty recall = %#v", result)
	}
}

func TestRecallToolKindFilter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "learning")
	seedProjectLesson(t, root, harness.NewEntry(harness.KindMemory, "Fact", "a memory", "fact", "general", harness.ScopeProject, "agent", testTime()))
	seedProjectLesson(t, root, harness.NewEntry(harness.KindPrompt, "Note", "a prompt note", "note", "general", harness.ScopeProject, "agent", testTime()))

	tool := NewRecallTool(root, "")
	result := tool.Run(context.Background(), map[string]any{"kind": "prompt"})
	if !strings.Contains(result.Output, "prompt") || strings.Contains(result.Output, "a memory") {
		t.Fatalf("kind filter output = %q", result.Output)
	}
}

func TestRecallToolSearchesProjectAndGlobal(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	global := filepath.Join(base, "global")
	seedProjectLesson(t, project, harness.NewEntry(harness.KindMemory, "Project fact", "project-only-lesson", "p", "general", harness.ScopeProject, "agent", testTime()))
	seedProjectLesson(t, global, harness.NewEntry(harness.KindMemory, "Global fact", "global-only-lesson", "g", "general", harness.ScopeGlobal, "agent", testTime()))

	tool := NewRecallTool(project, global)
	result := tool.Run(context.Background(), map[string]any{})
	if result.Status != StatusOK {
		t.Fatalf("recall = %#v", result)
	}
	if !strings.Contains(result.Output, "project-only-lesson") || !strings.Contains(result.Output, "global-only-lesson") {
		t.Fatalf("recall should merge project+global, got %q", result.Output)
	}
	// The scope is labeled so the model knows where a lesson lives.
	if !strings.Contains(result.Output, string(harness.ScopeGlobal)) || !strings.Contains(result.Output, string(harness.ScopeProject)) {
		t.Fatalf("recall should report entry scope, got %q", result.Output)
	}
}
