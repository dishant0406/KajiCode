package cli

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
)

// testPNG returns a small, fully valid PNG so it passes the real decode step in
// the shared loader (a bare signature is no longer enough now that the loader
// requires decodable content).
func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestResolveExecImagesValidSingle(t *testing.T) {
	root := t.TempDir()
	pngData := testPNG(t)
	if err := os.WriteFile(filepath.Join(root, "shot.png"), pngData, 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := resolveExecImages([]string{"shot.png"}, root, config.ImagesConfig{})
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("len(images) = %d, want 1", len(images))
	}
	if images[0].MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", images[0].MediaType)
	}
	if !bytes.Equal(images[0].Data, pngData) {
		t.Fatalf("Data = %v, want raw png bytes", images[0].Data)
	}
}

func TestResolveExecImagesRepeatedAndRelativeToRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "media")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.png"), testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.png"), testPNG(t), 0o600); err != nil {
		t.Fatal(err)
	}

	images, err := resolveExecImages([]string{"a.png", "media/b.png"}, root, config.ImagesConfig{})
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("len(images) = %d, want 2", len(images))
	}
}

func TestResolveExecImagesMissingFileIsUsageError(t *testing.T) {
	root := t.TempDir()
	_, err := resolveExecImages([]string{"nope.png"}, root, config.ImagesConfig{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "image file not found") {
		t.Fatalf("error = %q, want image-file-not-found", err.Error())
	}
}

func TestResolveExecImagesUnsupportedTypeIsUsageError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just some text, not an image"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveExecImages([]string{"notes.txt"}, root, config.ImagesConfig{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "unsupported image type") {
		t.Fatalf("error = %q, want unsupported-image-type", err.Error())
	}
}

func TestResolveExecImagesOversizeSourceIsUsageError(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, (40<<20)+1)
	copy(big, testPNG(t))
	if err := os.WriteFile(filepath.Join(root, "huge.png"), big, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveExecImages([]string{"huge.png"}, root, config.ImagesConfig{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(execUsageError); !ok {
		t.Fatalf("error type = %T, want execUsageError", err)
	}
	if !strings.Contains(err.Error(), "MiB") {
		t.Fatalf("error = %q, want size-cap message", err.Error())
	}
}

func TestResolveExecImagesEmptyReturnsNil(t *testing.T) {
	images, err := resolveExecImages(nil, t.TempDir(), config.ImagesConfig{})
	if err != nil {
		t.Fatalf("resolveExecImages error: %v", err)
	}
	if images != nil {
		t.Fatalf("images = %#v, want nil", images)
	}
}
