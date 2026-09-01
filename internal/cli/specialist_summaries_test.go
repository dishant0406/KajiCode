package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dishant0406/KajiCode/internal/specialist"
)

// TestSpecialistSummariesIncludesBuiltins confirms the orchestrator delegation
// prompt is fed the built-in specialists (the wiring behind auto-delegation).
func TestSpecialistSummariesIncludesBuiltins(t *testing.T) {
	paths, err := specialist.DefaultPaths(t.TempDir())
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	descByName := map[string]string{}
	for _, info := range specialistSummaries(paths) {
		descByName[info.Name] = info.WhenToUse
	}
	for _, name := range []string{"worker", "explorer", "code-review"} {
		if descByName[name] == "" {
			t.Fatalf("specialistSummaries missing built-in %q (or empty description); got %v", name, descByName)
		}
	}
}

func TestSpecialistSummariesFiltersHiddenAndPrimary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, frontmatter string) {
		t.Helper()
		content := "---\n" + frontmatter + "name: " + name + "\ndescription: " + name + " desc\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("visible", "")
	write("secret", "hidden: true\n")
	write("chief", "mode: primary\n")

	summaries := specialistSummaries(specialist.Paths{UserDir: dir})
	got := map[string]bool{}
	for _, info := range summaries {
		got[info.Name] = true
	}
	if !got["visible"] {
		t.Error("visible specialist missing from delegation summaries")
	}
	if got["secret"] {
		t.Error("hidden specialist must not appear in delegation summaries")
	}
	if got["chief"] {
		t.Error("primary-mode specialist must not appear in delegation summaries")
	}
}
