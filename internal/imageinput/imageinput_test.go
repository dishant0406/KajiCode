package imageinput

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileReadsAndNormalizes(t *testing.T) {
	root := t.TempDir()
	// 1x1 PNG (real PNG signature so http.DetectContentType returns image/png).
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	// Relative path resolves against workspaceRoot.
	block, err := LoadFile("pic.png", root, DefaultLimits())
	if err != nil {
		t.Fatalf("LoadFile relative: %v", err)
	}
	if block.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", block.MediaType)
	}
	if len(block.Data) != len(png) {
		t.Fatalf("Data length = %d, want %d", len(block.Data), len(png))
	}

	// Absolute path is used as-is.
	if _, err := LoadFile(filepath.Join(root, "pic.png"), root, DefaultLimits()); err != nil {
		t.Fatalf("LoadFile absolute: %v", err)
	}
}

func TestLoadFileMissing(t *testing.T) {
	root := t.TempDir()
	_, err := LoadFile("nope.png", root, DefaultLimits())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "nope.png") {
		t.Fatalf("error %q should name the path", err.Error())
	}
}

func TestLoadFileUnsupportedType(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just some plain text, not an image at all"), 0o644); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	_, err := LoadFile("notes.txt", root, DefaultLimits())
	if err == nil {
		t.Fatal("expected error for non-image content")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error %q should mention unsupported", err.Error())
	}
}

func TestLoadFileRejectsNonRegular(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "adir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A directory is non-regular like a FIFO/device; the guard must reject it
	// before os.Open (a writerless FIFO would otherwise block the read forever).
	_, err := LoadFile("adir", root, DefaultLimits())
	if err == nil {
		t.Fatal("expected error for a non-regular file")
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("error %q should mention regular file", err.Error())
	}
}

func TestLoadFileOversizeSourceRejected(t *testing.T) {
	root := t.TempDir()
	// A source file above the read bound is refused before it is buffered.
	big := make([]byte, (40<<20)+1)
	copy(big, []byte("GIF89a"))
	if err := os.WriteFile(filepath.Join(root, "big.gif"), big, 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	_, err := LoadFile("big.gif", root, DefaultLimits())
	if err == nil {
		t.Fatal("expected error for oversize source")
	}
	if !strings.Contains(err.Error(), "40 MiB") {
		t.Fatalf("error %q should mention the source limit", err.Error())
	}
}

func TestLoadFileNormalizesOverLimitDimensions(t *testing.T) {
	root := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 128, 64))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "wide.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	limits := DefaultLimits()
	limits.MaxWidth, limits.MaxHeight = 32, 32
	block, err := LoadFile("wide.png", root, limits)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width > limits.MaxWidth || cfg.Height > limits.MaxHeight {
		t.Fatalf("normalized %dx%d exceeds %dx%d", cfg.Width, cfg.Height, limits.MaxWidth, limits.MaxHeight)
	}
}
