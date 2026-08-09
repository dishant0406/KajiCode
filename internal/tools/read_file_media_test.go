package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileDidYouMean(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(dir, name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("pkg", "database.go", "package pkg\n")
	mustWrite("pkg", "util.go", "package pkg\n")
	mustWrite("pkg", "README.md", "hi\n")

	tool := NewScopedReadFileTool(root, nil)

	// Missing path with near-miss sibling: should suggest database.go.
	res := tool.Run(context.Background(), map[string]any{"path": "pkg/databse.go"})
	if res.Status != StatusError {
		t.Fatalf("expected error for missing file, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "database.go") {
		t.Fatalf("expected did-you-mean suggestion for database.go, got: %s", res.Output)
	}

	// Missing path with no sibling matches: plain not-found error.
	res = tool.Run(context.Background(), map[string]any{"path": "pkg/nope.go"})
	if res.Status != StatusError {
		t.Fatalf("expected error, got %s", res.Status)
	}
	if strings.Contains(res.Output, "Did you mean") {
		t.Fatalf("expected no suggestions, got: %s", res.Output)
	}
}

func TestReadFileBinaryRejected(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "blob.bin")
	if err := os.WriteFile(bin, []byte{0x00, 0x7f, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadFileTool(root).Run(context.Background(), map[string]any{"path": "blob.bin"})
	if res.Status != StatusError {
		t.Fatalf("expected binary rejection error, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "binary") {
		t.Fatalf("expected binary message, got: %s", res.Output)
	}
}

func TestReadFileImageMedia(t *testing.T) {
	root := t.TempDir()
	// Minimal valid PNG signature + trailing bytes.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	if err := os.WriteFile(filepath.Join(root, "a.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadFileTool(root).Run(context.Background(), map[string]any{"path": "a.png"})
	if res.Status != StatusOK {
		t.Fatalf("expected ok for png, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "data:image/png;base64,") {
		t.Fatalf("expected base64 data URI, got: %.80s", res.Output)
	}
	if res.Meta["mime"] != "image/png" {
		t.Fatalf("meta mime = %q, want image/png", res.Meta["mime"])
	}
}

func TestReadFileDirectoryRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := NewReadFileTool(root).Run(context.Background(), map[string]any{"path": "subdir"})
	if res.Status != StatusError {
		t.Fatalf("expected error reading directory, got %s", res.Status)
	}
	if !strings.Contains(res.Output, "regular file") {
		t.Fatalf("expected regular-file message, got: %s", res.Output)
	}
}

func TestRenderReadMediaPDF(t *testing.T) {
	res := renderReadMedia("/nonexistent.pdf", "x.pdf", "application/pdf")
	// Reading a nonexistent file should still yield an error result rather than panic.
	if res.Status != StatusError {
		t.Fatalf("expected error for missing pdf, got %s", res.Status)
	}
}
