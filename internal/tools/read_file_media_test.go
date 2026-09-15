package tools

import (
	"bytes"
	"context"
	"image"
	"image/png"
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

func TestReadFileImageReturnsImageBlock(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewScopedReadFileTool(root, nil)
	res := tool.Run(context.Background(), map[string]any{"path": "a.png"})
	if res.Status != StatusOK {
		t.Fatalf("expected ok for png, got %s: %s", res.Status, res.Output)
	}
	if len(res.Images) != 1 || res.Images[0].MediaType != "image/png" {
		t.Fatalf("expected one image/png block, got %+v", res.Images)
	}
	if strings.Contains(res.Output, "base64") {
		t.Fatalf("output must not embed base64, got: %.80s", res.Output)
	}
}

func TestReadFileImageForTextOnlyModelGivesNotice(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewScopedReadFileTool(root, nil).(readFileTool).RunWithOptions(context.Background(),
		map[string]any{"path": "a.png"}, RunOptions{ModelSupportsVision: func(string) bool { return false }})
	if res.Status != StatusOK {
		t.Fatalf("expected ok, got %s", res.Status)
	}
	if len(res.Images) != 0 {
		t.Fatalf("text-only model must get no image block, got %+v", res.Images)
	}
	if !strings.Contains(res.Output, "does not support image input") {
		t.Fatalf("expected a vision notice, got: %s", res.Output)
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

func TestRenderReadMediaMissingPDF(t *testing.T) {
	res := renderReadMedia("/nonexistent.pdf", "x.pdf", "application/pdf")
	// Reading a nonexistent file should still yield an error result rather than panic.
	if res.Status != StatusError {
		t.Fatalf("expected error for missing pdf, got %s", res.Status)
	}
}
