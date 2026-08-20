package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFileAt(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// makeProjectGitRoot marks dir as a git root (a .git/HEAD is enough for
// FindProjectGitRoot), mirroring the agent's git-root walk.
func makeProjectGitRoot(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("git mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("git head: %v", err)
	}
}

func TestProjectDirectorySkillRootsWalk(t *testing.T) {
	root := t.TempDir()
	makeProjectGitRoot(t, root)
	agentsSkill := filepath.Join(root, ".agents", "skills")
	if err := os.MkdirAll(agentsSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	dotskills := filepath.Join(sub, ".skills")
	if err := os.MkdirAll(dotskills, 0o755); err != nil {
		t.Fatal(err)
	}

	// Walking a deep cwd finds both roots in root-to-leaf order.
	roots := ProjectSkillRoots(sub, FindProjectGitRoot(sub))
	if len(roots) != 2 {
		t.Fatalf("expected 2 project roots, got %d: %v", len(roots), roots)
	}
	if roots[0] != agentsSkill {
		t.Errorf("roots[0] = %q, want the root-level .agents/skills %q", roots[0], agentsSkill)
	}
	if roots[1] != dotskills {
		t.Errorf("roots[1] = %q, want the deep .skills %q", roots[1], dotskills)
	}
}

func TestProjectSkillRootPrecedencePerDirSkillsOverAgents(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, ".agents", "skills")
	dotskills := filepath.Join(dir, ".skills")
	for _, d := range []string{agents, dotskills} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// .skills wins a name clash over .agents/skills in the same directory.
	if got := ProjectDirSkillRoot(dir); got != dotskills {
		t.Fatalf("ProjectDirSkillRoot = %q, want %q (.skills before .agents/skills)", got, dotskills)
	}
}

func TestProjectSkillRootsWithoutGitRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "x")
	if err := os.MkdirAll(filepath.Join(sub, ".skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No git metadata: only the cwd itself is walked (gitRoot "").
	roots := ProjectSkillRoots(sub, "")
	if len(roots) != 1 || roots[0] != filepath.Join(sub, ".skills") {
		t.Fatalf("expected only the cwd .skills, got %v", roots)
	}
}

func TestSkillOutputWrapsContentAndListsSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "demo", "SKILL.md")
	writeSkillFileAt(t, skillPath, "# Demo Skill\n\namp instructions")
	// Sibling resource files alongside SKILL.md.
	writeSkillFileAt(t, filepath.Join(dir, "demo", "scripts", "run.sh"), "#!/bin/sh\n")
	writeSkillFileAt(t, filepath.Join(dir, "demo", "reference.md"), "see reference")

	loaded, _, err := LoadFromRoots([]string{dir})
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load failed: %v, loaded=%d", err, len(loaded))
	}
	output := SkillOutput(loaded[0])

	for _, want := range []string{
		`<skill_content name="demo">`,
		"# Skill: demo",
		"amp instructions",
		"Base directory for this skill:",
		"<skill_files>",
		"<file>reference.md</file>",
		"<file>scripts</file>", // a directory sibling (immediate) is listed
		"</skill_files>",
		"</skill_content>",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("skill output missing %q:\n%s", want, output)
		}
	}
	// SKILL.md itself must never be listed.
	if strings.Contains(output, "<file>SKILL.md</file>") || strings.Contains(output, "<file>skill.md</file>") {
		t.Errorf("skill output must not list SKILL.md:\n%s", output)
	}
}

func TestSkillSiblingFilesSkipsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "demo", "SKILL.md")
	writeSkillFileAt(t, skillPath, "body")
	// A symlink inside the skill dir pointing outside it must not be listed.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeSkillFileAt(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(dir, "demo", "leak")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	files := skillSiblingFiles(filepath.Dir(skillPath))
	for _, name := range files {
		if name == "leak" {
			t.Errorf("escaping symlink leaked into sibling listing: %v", files)
		}
	}
}

func TestSkillSiblingFilesCapped(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "demo", "SKILL.md")
	writeSkillFileAt(t, skillPath, "body")
	for i := 0; i < 20; i++ {
		writeSkillFileAt(t, filepath.Join(dir, "demo", "f"+string(rune('a'+i))), "x")
	}
	files := skillSiblingFiles(filepath.Dir(skillPath))
	if len(files) > skillFilesLimit {
		t.Fatalf("sibling files capped: got %d, want <= %d", len(files), skillFilesLimit)
	}
}
