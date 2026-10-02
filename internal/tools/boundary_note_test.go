package tools

import (
	"strings"
	"testing"
)

// TestPathToolsMentionPermissionModeBoundary pins the description contract that
// keeps the model from reaching for bash when it is handed a path outside the
// workspace. The boundary is decided by the active permission mode, not by the
// tool, so every path-taking tool must say so; a bare "inside the workspace"
// description is what drove the shell-first behavior this note fixes.
func TestPathToolsMentionPermissionModeBoundary(t *testing.T) {
	root := t.TempDir()
	pathTools := map[string]Tool{
		"read_file":          NewReadFileTool(root),
		"read_minified_file": NewReadMinifiedFileTool(root),
		"write_file":         NewWriteFileTool(root),
		"edit_file":          NewEditFileTool(root),
		"multi_edit":         NewMultiEditTool(root),
		"ls":                 NewLsTool(root),
		"list_directory":     NewListDirectoryTool(root),
		"grep":               NewGrepTool(root),
		"glob":               NewGlobTool(root),
		"apply_patch":        NewApplyPatchTool(root),
	}
	for name, tool := range pathTools {
		if !strings.Contains(tool.Description(), "permission mode") {
			t.Errorf("%s description does not mention the permission-mode boundary:\n%s", name, tool.Description())
		}
	}
}

// TestMutationTargetsStaysWorkspaceOnly pins the deliberate limitation: rewind
// targets are resolved workspace-only, and an absolute outside path yields no
// target. The session checkpoint layer joins relative paths onto the workspace
// root (filepath.Join(root, rel)), so returning an absolute path here would record
// a phantom in-workspace file; an outside write is intentionally not snapshotted.
func TestMutationTargetsStaysWorkspaceOnly(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := outside + "/outside.txt"

	for _, name := range []string{"write_file", "edit_file"} {
		if got := MutationTargets(root, name, map[string]any{"path": outsideFile, "content": "x"}); len(got) != 0 {
			t.Errorf("%s with an outside path: got %v, want no targets", name, got)
		}
	}

	patch := "--- a/" + strings.TrimPrefix(outsideFile, "/") + "\n+++ b/" + strings.TrimPrefix(outsideFile, "/") + "\n@@ -1 +1 @@\n-x\n+y\n"
	if got := MutationTargets(root, "apply_patch", map[string]any{"patch": patch, "cwd": outside}); len(got) != 0 {
		t.Errorf("apply_patch with an outside cwd: got %v, want no targets", got)
	}
}
