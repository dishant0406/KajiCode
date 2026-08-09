package tools

import (
	"context"
	"strings"
	"testing"
)

// optionsRunner is the subset of the concrete todo tool structs that accepts
// per-run options (the Tool interface only exposes Run).
type optionsRunner interface {
	RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result
}

func runWithOptions(ctx context.Context, tool Tool, args map[string]any, options RunOptions, t *testing.T) Result {
	t.Helper()
	cast, ok := tool.(optionsRunner)
	if !ok {
		t.Fatalf("tool %T does not support RunWithOptions", tool)
	}
	return cast.RunWithOptions(ctx, args, options)
}

func TestNormalizeTodoStatus(t *testing.T) {
	cases := map[string]string{
		"completed":   TodoStatusCompleted,
		"done":        TodoStatusCompleted,
		"DONE":        TodoStatusCompleted,
		"in_progress": TodoStatusInProgress,
		"wip":         TodoStatusInProgress,
		"cancelled":   TodoStatusCancelled,
		"skipped":     TodoStatusCancelled,
		"garbage":     TodoStatusPending,
		"":            TodoStatusPending,
	}
	for in, want := range cases {
		if got := NormalizeTodoStatus(in); got != want {
			t.Errorf("NormalizeTodoStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTodoEnforceSingleInProgress(t *testing.T) {
	items := []TodoItem{
		{Content: "a", Status: TodoStatusInProgress},
		{Content: "b", Status: TodoStatusInProgress},
		{Content: "c", Status: TodoStatusPending},
	}
	out := EnforceSingleInProgress(items)
	inProgress := 0
	for _, item := range out {
		if item.Status == TodoStatusInProgress {
			inProgress++
		}
	}
	if inProgress != 1 {
		t.Fatalf("expected 1 in_progress, got %d: %+v", inProgress, out)
	}
	// Last in_progress wins; earlier demoted to completed.
	if out[0].Status != TodoStatusCompleted {
		t.Fatalf("item 0 = %q, want completed", out[0].Status)
	}
}

func TestTodoPersistenceRoundTrip(t *testing.T) {
	store := newInProcessSessionStore()
	options := RunOptions{SessionID: "sess-1", SessionStore: store}

	err := writeTodos(options, "sess-1", []TodoItem{
		{Content: "one", Status: TodoStatusInProgress},
		{Content: "two", Status: TodoStatusPending},
	})
	if err != nil {
		t.Fatalf("writeTodos: %v", err)
	}
	got, err := readTodos(options, "sess-1")
	if err != nil {
		t.Fatalf("readTodos: %v", err)
	}
	if len(got) != 2 || got[0].Content != "one" || got[0].Status != TodoStatusInProgress {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestTodoToolsEndToEnd(t *testing.T) {
	store := newInProcessSessionStore()
	options := RunOptions{SessionID: "sess-2", SessionStore: store}

	write := NewTodoWriteTool()
	writeRes := runWithOptions(context.Background(), write, map[string]any{
		"todos": []any{
			map[string]any{"content": "step one", "status": "in_progress"},
			map[string]any{"content": "step two", "status": "pending"},
		},
	}, options, t)
	if writeRes.Status != StatusOK {
		t.Fatalf("todo_write failed: %s", writeRes.Output)
	}
	if !strings.Contains(writeRes.Output, "step one") || !strings.Contains(writeRes.Output, "step two") {
		t.Fatalf("todo_write output wrong: %s", writeRes.Output)
	}

	read := NewTodoReadTool()
	readRes := runWithOptions(context.Background(), read, nil, options, t)
	if readRes.Status != StatusOK || !strings.Contains(readRes.Output, "step one") {
		t.Fatalf("todo_read failed: %s", readRes.Output)
	}

	// Separate session stays empty.
	other := runWithOptions(context.Background(), NewTodoReadTool(), nil, RunOptions{SessionID: "sess-other", SessionStore: store}, t)
	if other.Status != StatusOK || !strings.Contains(other.Output, "empty") {
		t.Fatalf("other session should be empty: %s", other.Output)
	}
}

func TestTodoMissingSessionErrors(t *testing.T) {
	read := runWithOptions(context.Background(), NewTodoReadTool(), nil, RunOptions{}, t)
	if read.Status != StatusError || !strings.Contains(read.Output, "session") {
		t.Fatalf("expected session error, got: %s", read.Output)
	}
}
