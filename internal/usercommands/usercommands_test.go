package usercommands

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadParsesFrontmatterAndBody(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kajicode", "commands")
	writeFile(t, dir, "release.md", "---\ndescription: Cut a release\nmodel: claude-sonnet-4.5\n---\nCut release $1 from the current branch.")

	cmds := Load(DefaultPaths(root, ""))
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	c := cmds[0]
	if c.Name != "release" {
		t.Fatalf("name = %q, want release", c.Name)
	}
	if c.Description != "Cut a release" {
		t.Fatalf("description = %q", c.Description)
	}
	if c.Model != "claude-sonnet-4.5" {
		t.Fatalf("model = %q", c.Model)
	}
	if c.Template != "Cut release $1 from the current branch." {
		t.Fatalf("template = %q", c.Template)
	}
	if !c.Project {
		t.Fatal("expected Project=true for a .kajicode/commands file")
	}
}

func TestLoadProjectOverridesUser(t *testing.T) {
	root := t.TempDir()
	userCfg := t.TempDir()
	writeFile(t, filepath.Join(userCfg, "kajicode", "commands"), "review.md", "USER version")
	writeFile(t, filepath.Join(root, ".kajicode", "commands"), "review.md", "PROJECT version")

	cmds := Load(DefaultPaths(root, userCfg))
	if len(cmds) != 1 {
		t.Fatalf("expected 1 deduped command, got %d", len(cmds))
	}
	if cmds[0].Template != "PROJECT version" {
		t.Fatalf("project should override user, got %q", cmds[0].Template)
	}
	if !cmds[0].Project {
		t.Fatal("winning command should be the project one")
	}
}

func TestLoadSkipsNonMarkdownAndInvalidNames(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kajicode", "commands")
	writeFile(t, dir, "ok.md", "fine")
	writeFile(t, dir, "notes.txt", "ignored")   // not .md
	writeFile(t, dir, "Bad Name.md", "ignored") // space → invalid name

	cmds := Load(DefaultPaths(root, ""))
	if len(cmds) != 1 || cmds[0].Name != "ok" {
		t.Fatalf("expected only the valid 'ok' command, got %#v", cmds)
	}
}

func TestLoadMissingDirsAreEmpty(t *testing.T) {
	if cmds := Load(DefaultPaths(t.TempDir(), t.TempDir())); len(cmds) != 0 {
		t.Fatalf("expected no commands for empty dirs, got %d", len(cmds))
	}
	if cmds := Load(Paths{}); len(cmds) != 0 {
		t.Fatalf("expected no commands for blank paths, got %d", len(cmds))
	}
}

func TestValidateName(t *testing.T) {
	for _, name := range []string{"review", "pr-2", "0"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "Review", "two words", "../escape", "ümlaut"} {
		if err := ValidateName(name); err == nil {
			t.Fatalf("ValidateName(%q) unexpectedly succeeded", name)
		}
	}
}

func TestSaveCreatesSecurePersonalCommandAndProtectsOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "commands")
	cmd, err := Save(dir, "review", "Review this carefully.\r\nThen summarize.", false)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cmd.Name != "review" || cmd.Project || cmd.Template != "Review this carefully.\nThen summarize." {
		t.Fatalf("saved command = %#v", cmd)
	}
	info, err := os.Stat(filepath.Join(dir, "review.md"))
	if err != nil {
		t.Fatalf("stat saved command: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("saved mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := Save(dir, "review", "replacement", false); !os.IsExist(err) {
		t.Fatalf("non-overwrite Save error = %v, want os.ErrExist", err)
	}
	cmd, err = Save(dir, "review", "replacement", true)
	if err != nil {
		t.Fatalf("overwrite Save: %v", err)
	}
	if cmd.Template != "replacement" {
		t.Fatalf("overwritten template = %q", cmd.Template)
	}
}

func TestSaveRejectsInvalidOrEmptyInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := Save(dir, "Bad Name", "body", false); err == nil {
		t.Fatal("invalid slug should fail")
	}
	if _, err := Save(dir, "valid", "  \n", false); err == nil {
		t.Fatal("empty body should fail")
	}
	if _, err := Save("", "valid", "body", false); err == nil {
		t.Fatal("empty directory should fail")
	}
}

func TestExpandPlaceholders(t *testing.T) {
	cases := []struct {
		name     string
		template string
		args     string
		want     string
	}{
		{"arguments", "Fix: $ARGUMENTS", "the flaky test in pkg", "Fix: the flaky test in pkg"},
		{"positional", "Release $1 ($2)", "v1.2 patch", "Release v1.2 (patch)"},
		{"missing positional is empty", "Tag $1 then $2", "only", "Tag only then "},
		{"literal dollar", "cost is $$5", "x", "cost is $5"},
		{"no placeholders no args", "Just do it.", "", "Just do it."},
		{"no placeholders with args appends", "Do it.", "now please", "Do it.\n\nnow please"},
		{"mixed", "PR $1: $ARGUMENTS", "title body words here", "PR title: title body words here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Expand(tc.template, tc.args); got != tc.want {
				t.Fatalf("Expand(%q,%q) = %q, want %q", tc.template, tc.args, got, tc.want)
			}
		})
	}
}
