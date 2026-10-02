package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/sandbox"
)

func TestModeLiftsBoundary(t *testing.T) {
	cases := []struct {
		mode  string
		write bool
		want  bool
	}{
		{"bypass-all", false, true},
		{"bypass-all", true, true},
		{"read-only", false, true},
		{"read-only", true, false},
		{"read-write", false, true},
		{"read-write", true, true},
		{"ask-all", false, false},
		{"ask-all", true, false},
		{"", false, false},
		{"unsafe", false, false},
		{"auto", false, false},
		{"  BYPASS-ALL  ", false, true},
	}
	for _, tc := range cases {
		if got := modeLiftsBoundary(tc.mode, tc.write); got != tc.want {
			t.Errorf("modeLiftsBoundary(%q, write=%v) = %v, want %v", tc.mode, tc.write, got, tc.want)
		}
	}
}

func scopedTestSetup(t *testing.T) (workspace string, scope *sandbox.Scope, outside string) {
	t.Helper()
	workspace = t.TempDir()
	var err error
	scope, err = sandbox.NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	outside = tempDirOutsideDefaultTemp(t)
	return workspace, scope, outside
}

// TestScopedReadPathModeTable pins the resolver's per-mode decision: an absolute
// path outside every scope root resolves only when the mode lifts the boundary.
func TestScopedReadPathModeTable(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"bypass-all", "read-only", "read-write"} {
		abs, _, err := resolveScopedReadPath(workspace, scope, mode, target)
		if err != nil {
			t.Errorf("mode %s: resolveScopedReadPath(%q) failed: %v", mode, target, err)
			continue
		}
		if !filepath.IsAbs(abs) {
			t.Errorf("mode %s: expected absolute path, got %q", mode, abs)
		}
	}

	for _, mode := range []string{"ask-all", ""} {
		_, _, err := resolveScopedReadPath(workspace, scope, mode, target)
		if err == nil || !strings.Contains(err.Error(), "must stay inside the workspace") {
			t.Errorf("mode %q: want unchanged containment error, got %v", mode, err)
		}
	}
}

// TestScopedWritePathModeTable pins that writes are only lifted by the modes
// that grant them; read-only reads outside but must not write outside.
func TestScopedWritePathModeTable(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)

	for _, mode := range []string{"bypass-all", "read-write"} {
		target := filepath.Join(outside, "new-"+mode+".txt")
		if _, _, err := resolveScopedTargetPath(workspace, scope, mode, target); err != nil {
			t.Errorf("mode %s: resolveScopedTargetPath(%q) failed: %v", mode, target, err)
		}
		if err := recheckScopedWriteTarget(workspace, scope, mode, target); err != nil {
			t.Errorf("mode %s: recheckScopedWriteTarget(%q) failed: %v", mode, target, err)
		}
	}

	for _, mode := range []string{"read-only", "ask-all", ""} {
		target := filepath.Join(outside, "blocked.txt")
		if _, _, err := resolveScopedTargetPath(workspace, scope, mode, target); err == nil {
			t.Errorf("mode %q: resolveScopedTargetPath(%q) should have failed", mode, target)
		}
		if err := recheckScopedWriteTarget(workspace, scope, mode, target); err == nil {
			t.Errorf("mode %q: recheckScopedWriteTarget(%q) should have failed", mode, target)
		}
	}
}

// TestReadFileToolHonorsPermissionMode is the end-to-end tool-level regression
// for the reported defect: under bypass-all (and the read-widening modes) the
// native read tools must reach a file outside the workspace, exactly as bash
// already does.
func TestReadFileToolHonorsPermissionMode(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("SECRET_HOME_OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"bypass-all", "read-only", "read-write"} {
		tool := NewScopedReadFileTool(workspace, scope).(optionsAwareTool)
		res := tool.RunWithOptions(context.Background(), map[string]any{"path": target}, RunOptions{PermissionMode: mode})
		if res.Status != StatusOK || !strings.Contains(res.Output, "SECRET_HOME_OUTSIDE") {
			t.Errorf("mode %s: read_file status=%s output=%q", mode, res.Status, res.Output)
		}
	}

	tool := NewScopedReadFileTool(workspace, scope).(optionsAwareTool)
	res := tool.RunWithOptions(context.Background(), map[string]any{"path": target}, RunOptions{PermissionMode: "ask-all"})
	if res.Status != StatusError || !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Errorf("ask-all: want containment error, got status=%s output=%q", res.Status, res.Output)
	}
}

func TestGrepToolHonorsPermissionMode(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	if err := os.WriteFile(filepath.Join(outside, "target.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewScopedGrepTool(workspace, scope).(optionsAwareTool)
	res := tool.RunWithOptions(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    outside,
	}, RunOptions{PermissionMode: "bypass-all"})
	if res.Status != StatusOK || !strings.Contains(res.Output, "needle") {
		t.Fatalf("bypass-all grep status=%s output=%q", res.Status, res.Output)
	}

	res = tool.RunWithOptions(context.Background(), map[string]any{
		"pattern": "needle",
		"path":    outside,
	}, RunOptions{PermissionMode: "ask-all"})
	if res.Status != StatusError || !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Fatalf("ask-all grep want containment error, got status=%s output=%q", res.Status, res.Output)
	}
}

func TestWriteFileToolHonorsPermissionMode(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)

	allowed := NewScopedWriteFileTool(workspace, scope).(optionsAwareTool)
	res := allowed.RunWithOptions(context.Background(), map[string]any{
		"path":    filepath.Join(outside, "written.txt"),
		"content": "hi",
	}, RunOptions{PermissionMode: "bypass-all"})
	if res.Status != StatusOK {
		t.Fatalf("bypass-all write_file status=%s output=%q", res.Status, res.Output)
	}
	if _, err := os.Stat(filepath.Join(outside, "written.txt")); err != nil {
		t.Fatalf("expected file written outside workspace: %v", err)
	}

	blocked := NewScopedWriteFileTool(workspace, scope).(optionsAwareTool)
	res = blocked.RunWithOptions(context.Background(), map[string]any{
		"path":    filepath.Join(outside, "blocked.txt"),
		"content": "hi",
	}, RunOptions{PermissionMode: "ask-all"})
	if res.Status != StatusError || !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Fatalf("ask-all write_file want containment error, got status=%s output=%q", res.Status, res.Output)
	}
}

// TestScopedResolverModeNoopKeepsRelativePaths guards that a mode which lifts
// the boundary does not change relative-path behavior (still workspace-rooted).
func TestScopedResolverModeNoopKeepsRelativePaths(t *testing.T) {
	workspace, scope, _ := scopedTestSetup(t)

	if err := os.MkdirAll(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "sub", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, relative, err := resolveScopedReadPath(workspace, scope, "bypass-all", "sub/file.txt")
	if err != nil {
		t.Fatalf("resolveScopedReadPath relative: %v", err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(abs, resolvedWorkspace) || relative != filepath.ToSlash(filepath.Join("sub", "file.txt")) {
		t.Fatalf("relative path resolved to abs=%q relative=%q", abs, relative)
	}
}

// TestResolveOutsidePathDoesNotUseProcessCwd pins the review finding that a
// relative path must never resolve against the process CWD when the boundary is
// lifted; only absolute outside paths are meaningful.
func TestResolveOutsidePathDoesNotUseProcessCwd(t *testing.T) {
	if _, _, err := resolveOutsidePath("relative.txt"); err == nil {
		t.Fatal("resolveOutsidePath accepted a relative path; it must refuse one")
	}
	if _, _, err := resolveOutsideTargetPath("relative.txt"); err == nil {
		t.Fatal("resolveOutsideTargetPath accepted a relative path; it must refuse one")
	}
}

// TestScopedReadPathOutsideMissingPathFailsClosed pins that a nonexistent path
// outside every root does not resolve under a lifting mode (EvalSymlinks fails).
func TestScopedReadPathOutsideMissingPathFailsClosed(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	missing := filepath.Join(outside, "does-not-exist", "nope.txt")
	if _, _, err := resolveScopedReadPath(workspace, scope, "bypass-all", missing); err == nil {
		t.Fatalf("expected error for a nonexistent outside path, got nil")
	}
}

// TestScopedReadPathFollowsSymlinkOutside pins that a symlink resolving outside
// the workspace is followed under a lifting mode (resolved to the canonical
// target) and refused under ask-all.
func TestScopedReadPathFollowsSymlinkOutside(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	realTarget := filepath.Join(outside, "real.txt")
	if err := os.WriteFile(realTarget, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "outside-link.txt")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	abs, _, err := resolveScopedReadPath(workspace, scope, "bypass-all", link)
	if err != nil {
		t.Fatalf("bypass-all symlink read failed: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(realTarget)
	if err != nil {
		t.Fatal(err)
	}
	if abs != resolved {
		t.Fatalf("symlink resolved to %q, want %q", abs, resolved)
	}
	if _, _, err := resolveScopedReadPath(workspace, scope, "ask-all", link); err == nil {
		t.Fatal("ask-all must refuse the escaping symlink")
	}
}

// TestScopedResolverLiftingModeWithExtraRoot guards that an --add-dir extra root
// keeps its absolute-path semantics when a lifting mode is also active.
func TestScopedResolverLiftingModeWithExtraRoot(t *testing.T) {
	workspace := t.TempDir()
	extra := tempDirOutsideDefaultTemp(t)
	scope, err := sandbox.NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	target := filepath.Join(extra, "in-extra.txt")
	if err := os.WriteFile(target, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	abs, relative, err := resolveScopedReadPath(workspace, scope, "bypass-all", target)
	if err != nil {
		t.Fatalf("resolveScopedReadPath: %v", err)
	}
	if abs != relative {
		t.Fatalf("extra-root match should report the absolute path for both values, got abs=%q relative=%q", abs, relative)
	}
}

// TestEditFileToolRefusesOutsideWriteUnderReadOnly pins the tool-level write
// denial the resolver table asserts, through the real tool.
func TestEditFileToolRefusesOutsideWriteUnderReadOnly(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)
	target := filepath.Join(outside, "edit-me.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	edit := NewScopedEditFileTool(workspace, scope).(optionsAwareTool)
	res := edit.RunWithOptions(context.Background(), map[string]any{
		"path":       target,
		"old_string": "old",
		"new_string": "new",
	}, RunOptions{PermissionMode: "read-only"})
	if res.Status != StatusError || !strings.Contains(res.Output, "must stay inside the workspace") {
		t.Fatalf("read-only edit_file want containment error, got status=%s output=%q", res.Status, res.Output)
	}
	if content, _ := os.ReadFile(target); string(content) != "old" {
		t.Fatalf("read-only edit_file modified the file: %q", content)
	}
}

// TestBashCwdResolverHonorsPermissionMode pins the shell-cwd case from the
// review: an outside cwd resolves under bypass-all and is refused under ask-all
// (the engine also gates it independently via requestPaths).
func TestBashCwdResolverHonorsPermissionMode(t *testing.T) {
	workspace, scope, outside := scopedTestSetup(t)

	if abs, _, err := resolveScopedPath(workspace, scope, "bypass-all", false, outside); err != nil || abs == "" {
		t.Fatalf("bypass-all outside cwd resolve failed: abs=%q err=%v", abs, err)
	}
	if _, _, err := resolveScopedPath(workspace, scope, "ask-all", false, outside); err == nil {
		t.Fatal("ask-all must refuse an outside cwd")
	}
}
