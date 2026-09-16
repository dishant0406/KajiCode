package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateHome points HOME/USERPROFILE at an empty temp home so the convention
// skill roots (~/.agents/skills, ~/.claude/skills) do not leak the developer's
// real skills into a test.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func writeMergedSkill(t *testing.T, dir string, name string, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// TestMergeRootsOrdersGlobalProjectPlugin proves the single ordering authority:
// global roots (primary, then ~/.agents/skills), then project roots, then plugin
// roots. Earlier roots win a name clash in LoadFromRoots.
func TestMergeRootsOrdersGlobalProjectPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}

	primary := t.TempDir()
	project := t.TempDir()
	plugin := t.TempDir()
	// A ~/.claude/skills root sits after ~/.agents/skills in the global group.
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}

	roots := MergeRoots(primary, []string{plugin}, []string{project})
	want := []string{primary, agents, claude, project, plugin}
	if len(roots) != len(want) {
		t.Fatalf("roots = %#v, want %#v", roots, want)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("roots[%d] = %q, want %q (full: %#v)", i, roots[i], want[i], roots)
		}
	}
}

// TestLoadMergedPrecedence proves global > project > plugin on a name clash and
// that each tier is reachable by name.
func TestLoadMergedPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	primary := t.TempDir()
	project := t.TempDir()
	plugin := t.TempDir()
	writeMergedSkill(t, primary, "shared", "primary body")
	writeMergedSkill(t, project, "shared", "project body")
	writeMergedSkill(t, project, "project-only", "project only body")
	writeMergedSkill(t, plugin, "shared", "plugin body")
	writeMergedSkill(t, plugin, "plugin-only", "plugin only body")

	loaded, dups, err := LoadMerged(primary, []string{plugin}, []string{project})
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	byName := map[string]Skill{}
	for _, skill := range loaded {
		byName[skill.Name] = skill
	}
	if byName["shared"].Content != "primary body" {
		t.Fatalf("global should win 'shared', got content %q", byName["shared"].Content)
	}
	if byName["project-only"].Content != "project only body" {
		t.Fatalf("project skill missing, got %#v", byName["project-only"])
	}
	if byName["plugin-only"].Content != "plugin only body" {
		t.Fatalf("plugin skill missing, got %#v", byName["plugin-only"])
	}
	if len(dups) != 2 {
		t.Fatalf("expected 2 collisions for 'shared', got %#v", dups)
	}
}

// TestLoadMergedDiscoversAgentsWithoutPlugins proves the shared ~/.agents/skills
// root surfaces even with no plugin roots.
func TestLoadMergedDiscoversAgentsWithoutPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMergedSkill(t, agents, "agents-skill", "from agents")

	loaded, _, err := LoadMerged(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "agents-skill" {
		t.Fatalf("agents skill should surface with no plugins, got %#v", loaded)
	}
}

// TestListMergedStripsContent proves the listing variant drops bodies (the
// catalog never carries skill content).
func TestListMergedStripsContent(t *testing.T) {
	isolateHome(t)
	primary := t.TempDir()
	writeMergedSkill(t, primary, "alpha", "secret body")

	listed, _, err := ListMerged(primary, nil, nil)
	if err != nil {
		t.Fatalf("ListMerged: %v", err)
	}
	if len(listed) != 1 || listed[0].Content != "" {
		t.Fatalf("ListMerged must strip content, got %#v", listed)
	}
}
