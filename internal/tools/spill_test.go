package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTruncateExecOutputSpillWritesFullOutput(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	long := strings.Repeat("line of build output\n", 5000) // ~100KB > 40KB budget

	truncated, wasTruncated := truncateExecOutputSpill(long, defaultMaxOutputTokens, "bash")
	if !wasTruncated {
		t.Fatal("output over budget must truncate")
	}
	if !strings.Contains(truncated, "full output saved to ") {
		t.Fatalf("truncation notice missing spill path: %q", truncated[:200])
	}
	// Extract the path and verify the file holds the complete output.
	start := strings.Index(truncated, "full output saved to ") + len("full output saved to ")
	end := strings.Index(truncated[start:], " (grep")
	spillPath := truncated[start : start+end]
	content, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if string(content) != long {
		t.Fatalf("spill file must hold the full output: got %d bytes, want %d", len(content), len(long))
	}
}

func TestTruncateExecOutputUnderBudgetUnchanged(t *testing.T) {
	output, wasTruncated := truncateExecOutputSpill("short output", defaultMaxOutputTokens, "bash")
	if wasTruncated || output != "short output" {
		t.Fatalf("under-budget output must pass through untouched: %q", output)
	}
}

func TestSweepSpillDirRemovesOnlyOldFiles(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "bash-old.txt")
	newFile := filepath.Join(dir, "bash-new.txt")
	for _, path := range []string{oldFile, newFile} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-spillRetention - time.Hour)
	if err := os.Chtimes(oldFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	sweepSpillDir(dir)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("stale spill file must be removed")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatal("fresh spill file must survive the sweep")
	}
}

// A spill happens BEFORE bash's model-facing formatter runs its pattern-based
// secret scan, so the spill itself must scrub pattern-matched credentials —
// otherwise the file would hold in cleartext exactly what the transcript hides.
func TestSpillTruncatedOutputScrubsPatternSecrets(t *testing.T) {
	setTestTempDir(t)
	githubToken := "ghp_" + strings.Repeat("a", 36)
	body := "before\nAKIAIOSFODNN7EXAMPLE\n" + githubToken + "\nafter"

	path := SpillOutput("bash", body)
	if path == "" {
		t.Fatal("spill must return a file path")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("AWS access key reached the spill file in cleartext")
	}
	if strings.Contains(string(content), githubToken) {
		t.Fatal("GitHub token reached the spill file in cleartext")
	}
	if !strings.Contains(string(content), "before") || !strings.Contains(string(content), "after") {
		t.Fatalf("non-secret content must survive the scrub: %q", content)
	}
}

func TestSpillTruncatedOutputWritesFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path := SpillOutput("exec_command", "some output body")
	if path == "" {
		t.Fatal("spill must return a file path")
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "exec_command-") {
		t.Fatalf("spill file name must carry the tool prefix: %s", base)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "some output body" {
		t.Fatalf("unexpected spill content: %q", content)
	}
}

// TestSpillOutputIsAcceptedByTheReadPath proves the spill produced for a pruned
// compaction body is one the scoped read tools will open, so the recovery net is
// actually usable (not just a named path).
func TestSpillOutputIsAcceptedByTheReadPath(t *testing.T) {
	setTestTempDir(t)
	path := SpillOutput("grep", "recoverable pruned body")
	if path == "" {
		t.Fatal("SpillOutput must produce a path")
	}
	defer os.Remove(path)
	if _, ok := resolveSpillReadPath(path); !ok {
		t.Fatalf("the scoped read path must accept a spill file: %s", path)
	}
}

// TestResolveSpillReadPathExportedMatchesScopedCheck proves the exported helper
// the tool gate reuses to validate an existing spill pointer agrees with the
// scoped read tools: a real spill is accepted, a file outside the spill root is
// not.
func TestResolveSpillReadPathExportedMatchesScopedCheck(t *testing.T) {
	setTestTempDir(t)
	path := SpillOutput("bash", "a body")
	if path == "" {
		t.Fatal("SpillOutput must produce a path")
	}
	defer os.Remove(path)
	if _, ok := ResolveSpillReadPath(path); !ok {
		t.Fatalf("exported resolver must accept a real spill file: %s", path)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.txt")
	if err := os.WriteFile(foreign, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ResolveSpillReadPath(foreign); ok {
		t.Fatalf("exported resolver must reject a file outside the spill root: %s", foreign)
	}
}
