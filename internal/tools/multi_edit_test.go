package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiEditAppliesAllAndWritesOnce(t *testing.T) {
	root := t.TempDir()
	path := "config.txt"
	if err := os.WriteFile(filepath.Join(root, path), []byte("apple\nbeta\ncarrot\napple\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMultiEditTool(root)
	result := tool.Run(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"old_string": "beta", "new_string": "BETA"},
			map[string]any{"old_string": "carrot", "new_string": "CARROT"},
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("multi_edit failed: %s", result.Output)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != path {
		t.Fatalf("changed files = %v", result.ChangedFiles)
	}
	got, _ := os.ReadFile(filepath.Join(root, path))
	content := string(got)
	if content != "apple\nBETA\nCARROT\napple\n" {
		t.Fatalf("content = %q, want %q", content, "apple\nBETA\nCARROT\napple\n")
	}
}

func TestMultiEditAtomicOnFailure(t *testing.T) {
	root := t.TempDir()
	path := "data.txt"
	if err := os.WriteFile(filepath.Join(root, path), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMultiEditTool(root)
	result := tool.Run(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"old_string": "one", "new_string": "ONE"},
			map[string]any{"old_string": "does-not-exist", "new_string": "X"},
		},
	})
	if result.Status != StatusError {
		t.Fatalf("expected error for failing edit, got %s: %s", result.Status, result.Output)
	}
	// First edit must NOT have been written (atomicity).
	got, _ := os.ReadFile(filepath.Join(root, path))
	if string(got) != "one\ntwo\n" {
		t.Fatalf("partial write occurred, content = %q", string(got))
	}
}

func TestMultiEditReplaceAllAndAmbiguity(t *testing.T) {
	root := t.TempDir()
	path := "list.txt"
	if err := os.WriteFile(filepath.Join(root, path), []byte("x\ny\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMultiEditTool(root)

	// Ambiguous (two "x") without replace_all => error, no write.
	res := tool.Run(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"old_string": "x", "new_string": "z"},
		},
	})
	if res.Status != StatusError || !strings.Contains(res.Output, "matches 2 locations") {
		t.Fatalf("expected ambiguity error, got %s: %s", res.Status, res.Output)
	}

	// With replace_all => both replaced.
	res = tool.Run(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"old_string": "x", "new_string": "z", "replace_all": true},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("replace_all failed: %s", res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, path))
	if string(got) != "z\ny\nz\n" {
		t.Fatalf("replace_all content = %q", string(got))
	}
}

func TestMultiEditFuzzyFallback(t *testing.T) {
	root := t.TempDir()
	path := "code.go"
	content := "func foo() {\n\treturn bar\n}\n"
	if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewMultiEditTool(root)
	// Slight indentation drift in old_string should still match via fuzzy editing.
	res := tool.Run(context.Background(), map[string]any{
		"path": path,
		"edits": []any{
			map[string]any{"old_string": "func foo() {\nreturn bar\n}", "new_string": "func foo() { return bar }"},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("fuzzy multi_edit failed: %s", res.Output)
	}
	got, _ := os.ReadFile(filepath.Join(root, path))
	if !strings.Contains(string(got), "func foo() { return bar }") {
		t.Fatalf("fuzzy replacement not applied: %q", string(got))
	}
}

func TestMultiEditMissingEditsRejected(t *testing.T) {
	tool := NewMultiEditTool(t.TempDir())
	res := tool.Run(context.Background(), map[string]any{"path": "x"})
	if res.Status != StatusError {
		t.Fatalf("expected error for missing edits, got %s", res.Status)
	}
}
