package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLsLaysOutTreeDirsFirst(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "README.md"), "# hi")
	writeTestFile(t, filepath.Join(root, "src", "main.go"), "package main")
	writeTestFile(t, filepath.Join(root, "src", "util", "x.go"), "package util")

	res := NewLsTool(root).Run(context.Background(), map[string]any{"path": "."})
	if res.Status != StatusOK {
		t.Fatalf("ls failed: %s", res.Output)
	}
	output := res.Output
	if !strings.Contains(output, "README.md") {
		t.Fatalf("expected README.md, got:\n%s", output)
	}
	if !strings.Contains(output, "src/") {
		t.Fatalf("expected src/ dir line, got:\n%s", output)
	}
	if !strings.Contains(output, "main.go") {
		t.Fatalf("expected main.go, got:\n%s", output)
	}
	// Directory line must appear before its contents (dirs-first tree order).
	dirIdx := strings.Index(output, "src/")
	fileIdx := strings.Index(output, "main.go")
	if dirIdx == -1 || fileIdx == -1 || dirIdx > fileIdx {
		t.Fatalf("expected src/ dir before main.go, got:\n%s", output)
	}
}

func TestLsHonorsDepthLimit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a", "b", "c", "deep.txt"), "x")
	writeTestFile(t, filepath.Join(root, "a", "b", "top.txt"), "x")

	res := NewLsTool(root).Run(context.Background(), map[string]any{"path": ".", "depth": 2})
	if res.Status != StatusOK {
		t.Fatalf("ls failed: %s", res.Output)
	}
	output := res.Output
	if strings.Contains(output, "deep.txt") {
		t.Fatalf("depth=2 leaked depth-3 child, got:\n%s", output)
	}
	if !strings.Contains(output, "top.txt") {
		t.Fatalf("expected depth-2 child top.txt, got:\n%s", output)
	}
}

func TestLsSkipsDefaultAndCustomIgnores(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "x")
	writeTestFile(t, filepath.Join(root, "keep.txt"), "x")
	writeTestFile(t, filepath.Join(root, "secret", "key.pem"), "x")

	res := NewLsTool(root).Run(context.Background(), map[string]any{"path": ".", "ignore": []string{"secret"}})
	if res.Status != StatusOK {
		t.Fatalf("ls failed: %s", res.Output)
	}
	output := res.Output
	if strings.Contains(output, "node_modules") || strings.Contains(output, "index.js") {
		t.Fatalf("default ignore leaked node_modules, got:\n%s", output)
	}
	if strings.Contains(output, "secret") || strings.Contains(output, "key.pem") {
		t.Fatalf("custom ignore leaked secret, got:\n%s", output)
	}
	if !strings.Contains(output, "keep.txt") {
		t.Fatalf("keep.txt should be present, got:\n%s", output)
	}
}

func TestLsTruncatesAboveLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		writeTestFile(t, filepath.Join(root, "f"+string(rune('a'+i))+".txt"), "x")
	}
	res := NewLsTool(root).Run(context.Background(), map[string]any{"path": ".", "limit": 5})
	if res.Status != StatusOK || !res.Truncated {
		t.Fatalf("expected truncated ls, got status=%s truncated=%v", res.Status, res.Truncated)
	}
	if !strings.Contains(res.Output, "[truncated: showing 5 of 20") {
		t.Fatalf("expected truncation notice, got:\n%s", res.Output)
	}
	if res.Meta["truncation_reason"] != "limit" {
		t.Fatalf("expected limit truncation reason, got %v", res.Meta)
	}
}

func TestGlobSortsByMtimeDescending(t *testing.T) {
	root := t.TempDir()
	older := filepath.Join(root, "older.txt")
	newer := filepath.Join(root, "newer.txt")
	mid := filepath.Join(root, "mid.txt")
	for _, p := range []string{older, mid, newer} {
		writeTestFile(t, p, "x")
	}
	// Stagger mtimes deterministically.
	base := time.Now().Add(-time.Minute)
	_ = os.Chtimes(older, base, base)
	_ = os.Chtimes(mid, base.Add(time.Minute), base.Add(time.Minute))
	_ = os.Chtimes(newer, base.Add(2*time.Minute), base.Add(2*time.Minute))

	res := NewGlobTool(root).Run(context.Background(), map[string]any{"pattern": "*.txt"})
	if res.Status != StatusOK {
		t.Fatalf("glob failed: %s", res.Output)
	}
	lines := strings.Fields(res.Output)
	if len(lines) != 3 {
		t.Fatalf("expected 3 matches, got %v", lines)
	}
	if lines[0] != "newer.txt" {
		t.Fatalf("expected newest first, got %v", lines)
	}
	if lines[2] != "older.txt" {
		t.Fatalf("expected oldest last, got %v", lines)
	}
}

func TestGrepFilesAndContentSortByMtime(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	writeTestFile(t, a, "needle\n")
	writeTestFile(t, b, "needle\n")
	// Make b newer than a.
	base := time.Now().Add(-time.Minute)
	_ = os.Chtimes(a, base, base)
	_ = os.Chtimes(b, base.Add(2*time.Minute), base.Add(2*time.Minute))

	files := NewGrepTool(root).Run(context.Background(), map[string]any{"pattern": "needle", "output_mode": "files_with_matches"})
	if files.Status != StatusOK {
		t.Fatalf("grep files failed: %s", files.Output)
	}
	names := strings.Fields(files.Output)
	if len(names) != 2 {
		t.Fatalf("expected 2 files, got %v", names)
	}
	if names[0] != "b.txt" {
		t.Fatalf("expected newest-file-first in file list, got %v", names)
	}

	content := NewGrepTool(root).Run(context.Background(), map[string]any{"pattern": "needle"})
	if content.Status != StatusOK {
		t.Fatalf("grep content failed: %s", content.Output)
	}
	if !strings.HasPrefix(content.Output, "b.txt:1:") {
		t.Fatalf("expected content results newest-file-first, got:\n%s", content.Output)
	}
}
