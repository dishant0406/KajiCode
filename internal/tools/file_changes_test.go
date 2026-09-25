package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileFileChangesCreateAndOverwrite checks the structured before/after
// content the ACP layer turns into a diff: a create has no old content, an
// overwrite keeps the prior content.
func TestWriteFileFileChangesCreateAndOverwrite(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	create := tool.Run(context.Background(), map[string]any{"path": "a.txt", "content": "hello\n"})
	if create.Status != StatusOK {
		t.Fatalf("create failed: %s", create.Output)
	}
	if len(create.FileChanges) != 1 {
		t.Fatalf("expected one file change, got %+v", create.FileChanges)
	}
	if got := create.FileChanges[0]; got.Path != "a.txt" || got.OldContent != "" || got.NewContent != "hello\n" {
		t.Fatalf("create change = %+v", got)
	}

	overwrite := tool.Run(context.Background(), map[string]any{"path": "a.txt", "content": "world\n", "overwrite": true})
	if overwrite.Status != StatusOK {
		t.Fatalf("overwrite failed: %s", overwrite.Output)
	}
	if got := overwrite.FileChanges[0]; got.OldContent != "hello\n" || got.NewContent != "world\n" {
		t.Fatalf("overwrite change = %+v", got)
	}
}

// TestEditFileFileChangesCaptureBeforeAndAfter checks that edit_file reports the
// full old/new content, not the bounded preview.
func TestEditFileFileChangesCaptureBeforeAndAfter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := NewEditFileTool(root).Run(context.Background(), map[string]any{
		"path": "a.txt", "old_string": "beta", "new_string": "BETA",
	})
	if result.Status != StatusOK {
		t.Fatalf("edit failed: %s", result.Output)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected one file change, got %+v", result.FileChanges)
	}
	if got := result.FileChanges[0]; got.OldContent != "alpha\nbeta\n" || got.NewContent != "alpha\nBETA\n" {
		t.Fatalf("edit change = %+v", got)
	}
}

// TestMultiEditFileChangesCaptureBeforeAndAfter checks the same for multi_edit.
func TestMultiEditFileChangesCaptureBeforeAndAfter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := NewMultiEditTool(root).Run(context.Background(), map[string]any{
		"path": "a.txt",
		"edits": []any{
			map[string]any{"old_string": "one", "new_string": "ONE"},
			map[string]any{"old_string": "two", "new_string": "TWO"},
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("multi_edit failed: %s", result.Output)
	}
	if got := result.FileChanges[0]; got.OldContent != "one\ntwo\n" || got.NewContent != "ONE\nTWO\n" {
		t.Fatalf("multi_edit change = %+v", got)
	}
}

// TestApplyPatchFileChanges captures pre/post content for a patch that creates
// and modifies files, including a null old content for the create.
func TestApplyPatchFileChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"diff --git a/scratch.txt b/scratch.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/scratch.txt",
		"@@ -0,0 +1 @@",
		"+hello",
		"diff --git a/existing.txt b/existing.txt",
		"--- a/existing.txt",
		"+++ b/existing.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch})
	if result.Status != StatusOK {
		if gitApplyUnavailable(result.Output) {
			t.Skipf("git binary unavailable: %s", result.Output)
		}
		t.Fatalf("apply_patch failed: %s", result.Output)
	}
	byPath := map[string]FileChange{}
	for _, change := range result.FileChanges {
		byPath[change.Path] = change
	}
	if got := byPath["scratch.txt"]; got.OldContent != "" || got.NewContent != "hello\n" {
		t.Fatalf("create change = %+v", got)
	}
	if got := byPath["existing.txt"]; got.OldContent != "old\n" || got.NewContent != "new\n" {
		t.Fatalf("modify change = %+v", got)
	}
}

// TestApplyPatchFileChangesWithSubdirCwd locks the path labels for a patch
// applied from a subdirectory (cwd), where the reported path must be joined with
// the cwd relative to the workspace, not double-resolved.
func TestApplyPatchFileChangesWithSubdirCwd(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := strings.Join([]string{
		"diff --git a/a.txt b/a.txt",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"",
	}, "\n")

	result := NewApplyPatchTool(root).Run(context.Background(), map[string]any{"patch": patch, "cwd": "sub"})
	if result.Status != StatusOK {
		if gitApplyUnavailable(result.Output) {
			t.Skipf("git binary unavailable: %s", result.Output)
		}
		t.Fatalf("apply_patch failed: %s", result.Output)
	}
	if len(result.FileChanges) != 1 {
		t.Fatalf("expected one file change, got %+v", result.FileChanges)
	}
	if got := result.FileChanges[0]; got.Path != "sub/a.txt" || got.OldContent != "old\n" || got.NewContent != "new\n" {
		t.Fatalf("subdir change = %+v", got)
	}
}

// TestFileChangesAreRedactedAtRegistryBoundary proves a secret written to a file
// is scrubbed from the structured diff before it can reach a client.
func TestFileChangesAreRedactedAtRegistryBoundary(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewWriteFileTool(root))
	const secret = "sk-proj-abcdefghijklmnop1234"

	result := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path": "a.txt", "content": "token = " + secret + "\n",
	}, RunOptions{PermissionGranted: true})
	if result.Status != StatusOK {
		t.Fatalf("write failed: %s", result.Output)
	}
	if !result.Redacted {
		t.Fatalf("expected Redacted to be set")
	}
	for _, change := range result.FileChanges {
		if strings.Contains(change.NewContent, secret) || strings.Contains(change.OldContent, secret) {
			t.Fatalf("secret leaked in file change: %+v", change)
		}
	}
}
