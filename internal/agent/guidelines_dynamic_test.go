package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeDir creates a directory (and parents).
func makeDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// stringContains reports whether s contains substring.
func stringContains(s, sub string) bool { return strings.Contains(s, sub) }

// stringSliceContains reports whether ss contains v.
func stringSliceContains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// skillInfoWriter records the project roots and boot baseline each dynamic render
// received, so tests can assert the loader saw the right inputs.
type skillInfoWriter struct {
	lastProject []string
	lastBoot    []SkillInfo
}

func (w *skillInfoWriter) render(projectRoots []string, boot []SkillInfo) ([]SkillInfo, string) {
	w.lastProject = projectRoots
	w.lastBoot = boot
	return append([]SkillInfo{}, boot...), "<available_skills>\ncatalog-rendered\n</available_skills>"
}

func TestDrainSkillsCatalogNoopWhenNoCatalog(t *testing.T) {
	tracker := newGuidelineTracker(t.TempDir())
	// No setCatalog: dynamic catalog is inactive; discovery must not emit one.
	sub := filepath.Join(t.TempDir(), "sub")
	if root := projectSkillRootForDir(sub); root != "" {
		tracker.ObservePath(root)
	}
	if block := tracker.drainSkillsCatalog(); block != "" {
		t.Fatalf("expected no catalog block without a wired catalog, got: %q", block)
	}
}

func TestDynamicCatalogEmitsOncePerDiscoveredRootAndSupersedes(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	// A project skill root in a subtree the run touches but which was NOT part of
	// the boot chain (startup cwd is root).
	deep := filepath.Join(root, "svc", "api")
	subSkillRoot := filepath.Join(deep, ".skills")
	makeDir(t, subSkillRoot)

	tracker := newGuidelineTracker(root)
	tracker.seedStartup(root) // boots only the startup chain (root-level)
	writer := &skillInfoWriter{}
	tracker.setCatalog([]SkillInfo{{Name: "boot-a", Description: "d"}}, writer.render)

	// Discover the deep subtree: marks the new root, but not yet drained.
	tracker.ObservePath(deep)
	if !tracker.catalogDirty {
		t.Fatal("expected catalog to be dirty after discovering a subtree skill root")
	}

	first := tracker.drainSkillsCatalog()
	if first == "" {
		t.Fatal("expected a catalog update after discovering a subtree root")
	}
	if !stringContains(first, "# Updated skill catalog") {
		t.Errorf("block must carry the supersede heading, got:\n%s", first)
	}
	if !stringContains(first, "catalog-rendered") {
		t.Errorf("block missing rendered catalog:\n%s", first)
	}
	// Loader saw the newly-discovered subtree root plus the boot baseline.
	if !stringSliceContains(writer.lastProject, subSkillRoot) {
		t.Errorf("loader project roots missing subtree root %q: %v", subSkillRoot, writer.lastProject)
	}
	if len(writer.lastBoot) != 1 || writer.lastBoot[0].Name != "boot-a" {
		t.Errorf("loader boot baseline = %+v, want boot-a", writer.lastBoot)
	}

	// The root is now cataloged: steady state drains emit nothing.
	if again := tracker.drainSkillsCatalog(); again != "" {
		t.Fatalf("catalog update re-emitted after the root was cataloged: %q", again)
	}
}

func TestBootSeededRootsAreNotRecataloged(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	startupSkillRoot := filepath.Join(root, ".skills")
	makeDir(t, startupSkillRoot)

	tracker := newGuidelineTracker(root)
	// seedStartup records the startup chain INCLUDING the root .skills, so the
	// boot catalog already covers it; setCatalog must treat it as cataloged.
	tracker.seedStartup(root)
	writer := &skillInfoWriter{}
	tracker.setCatalog([]SkillInfo{{Name: "boot-skill", Description: "d"}}, writer.render)

	tracker.ObservePath(root)
	if tracker.catalogDirty {
		t.Fatal("boot-seeded skill root must not mark the catalog dirty")
	}
	if block := tracker.drainSkillsCatalog(); block != "" {
		t.Fatalf("unexpected catalog update for boot-seeded root: %q", block)
	}
}

// additionOnlyLoader returns a fixed full set the first time it is called and
// records each render's inputs, so tests can exercise reconcile diff logic.
type fixedCatalogLoader struct {
	full     []SkillInfo
	rendered string
	calls    int
}

func (f *fixedCatalogLoader) render(projectRoots []string, boot []SkillInfo) ([]SkillInfo, string) {
	f.calls++
	return append([]SkillInfo{}, f.full...), f.rendered
}

func TestDrainSkillsCatalogSignalsRemovedName(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	deep := filepath.Join(root, "svc")
	subSkillRoot := filepath.Join(deep, ".skills")
	makeDir(t, subSkillRoot)

	tracker := newGuidelineTracker(root)
	tracker.seedStartup(root)
	loader := &fixedCatalogLoader{
		full:     []SkillInfo{{Name: "boot-a", Description: "d"}},
		rendered: "<available_skills>\nboot-a: d\n</available_skills>",
	}
	tracker.setCatalog([]SkillInfo{{Name: "boot-a", Description: "d"}, {Name: "gone-skill", Description: "g"}}, loader.render)

	// Observe the subtree to dirty the catalog; the next render's authoritative
	// set drops "gone-skill" (simulating the skill leaving the discovery surface).
	tracker.ObservePath(deep)
	block := tracker.drainSkillsCatalog()
	if block == "" {
		t.Fatal("expected a catalog reconcile update")
	}
	if !stringContains(block, "# Updated skill catalog") {
		t.Errorf("block must carry the supersede heading:\n%s", block)
	}
	if !stringContains(block, "No longer available: gone-skill") {
		t.Errorf("block must signal the removed name:\n%s", block)
	}
	// The removed name is no longer tracked as cataloged, so later drains stay
	// quiet on the same state.
	tracker.mu.Lock()
	still := tracker.catalogedNames["gone-skill"]
	tracker.mu.Unlock()
	if still {
		t.Errorf("removed name still tracked as cataloged")
	}
}

func TestDrainSkillsCatalogSignalsAddedName(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	deep := filepath.Join(root, "svc")
	subSkillRoot := filepath.Join(deep, ".skills")
	makeDir(t, subSkillRoot)

	tracker := newGuidelineTracker(root)
	tracker.seedStartup(root)
	loader := &fixedCatalogLoader{
		full:     []SkillInfo{{Name: "boot-a", Description: "d"}, {Name: "new-skill", Description: "n"}},
		rendered: "<available_skills>\nboot-a: d\nnew-skill: n\n</available_skills>",
	}
	tracker.setCatalog([]SkillInfo{{Name: "boot-a", Description: "d"}}, loader.render)

	tracker.ObservePath(deep)
	block := tracker.drainSkillsCatalog()
	if block == "" {
		t.Fatal("expected a catalog reconcile update")
	}
	if !stringContains(block, "Newly available skills: new-skill") {
		t.Errorf("block must signal the added name:\n%s", block)
	}
}
