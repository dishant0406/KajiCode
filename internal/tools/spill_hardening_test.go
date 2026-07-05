package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func spillDirName() string {
	if uid := os.Getuid(); uid >= 0 {
		return fmt.Sprintf("zero-tool-output-%d", uid)
	}
	return "zero-tool-output"
}

func TestSpillDirRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	elsewhere := filepath.Join(tmp, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	// An attacker pre-creates the spill path as a symlink to redirect spills.
	if err := os.Symlink(elsewhere, filepath.Join(tmp, spillDirName())); err != nil {
		t.Fatal(err)
	}
	if path := spillTruncatedOutput("bash", "sensitive output"); path != "" {
		t.Fatalf("spill must refuse a symlinked directory, wrote %s", path)
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Fatalf("nothing may land behind the symlink: %v", entries)
	}
}

func TestSpillDirAcceptsOwnedDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if path := spillTruncatedOutput("bash", "ok"); path == "" {
		t.Fatal("spill must work in a clean per-user temp dir")
	}
}
