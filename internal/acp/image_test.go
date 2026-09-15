package acp

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

func acpPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// promptImages must carry the client's media type through so Normalize can
// validate it against the actual bytes.
func TestPromptImagesPreservesMediaType(t *testing.T) {
	src := acpPNG(t, 4, 4)
	blocks := []ContentBlock{{Type: "image", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(src)}}
	got, err := promptImages(blocks)
	if err != nil {
		t.Fatalf("promptImages: %v", err)
	}
	if len(got) != 1 || got[0].MediaType != "image/png" || !bytes.Equal(got[0].Data, src) {
		t.Fatalf("promptImages = %+v, want one image/png block with the decoded bytes", got)
	}
}

// Malformed base64 must fail the request loudly, not be dropped into a silent
// text-only turn.
func TestPromptImagesRejectsInvalidBase64(t *testing.T) {
	blocks := []ContentBlock{{Type: "image", MimeType: "image/png", Data: "!!!not base64!!!"}}
	if _, err := promptImages(blocks); err == nil {
		t.Fatal("expected an error for malformed base64")
	}
}

// An image whose encoded length exceeds the cap must be refused before it is
// decoded, so a client cannot force an unbounded allocation.
func TestPromptImagesRejectsOversizeEncodedLength(t *testing.T) {
	blocks := []ContentBlock{{Type: "image", MimeType: "image/png", Data: strings.Repeat("A", (maxPromptImageBytes/3+1)*4+4)}}
	if _, err := promptImages(blocks); err == nil {
		t.Fatal("expected an error for an oversize encoded image")
	}
}

// An oversize image is resized into the configured envelope rather than passed
// through, so the ACP surface matches exec and the TUI.
func TestNormalizeSessionImagesDownscalesOversize(t *testing.T) {
	src := acpPNG(t, 256, 128)
	cfg := config.ImagesConfig{MaxWidth: 64, MaxHeight: 64}
	got, err := normalizeSessionImages([]kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: src}}, cfg)
	if err != nil {
		t.Fatalf("normalizeSessionImages: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(got[0].Data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() > 64 || b.Dy() > 64 {
		t.Fatalf("result %dx%d exceeds 64x64", b.Dx(), b.Dy())
	}
}

// A client-supplied media type that disagrees with the bytes must fail the turn
// instead of being forwarded to the provider.
func TestNormalizeSessionImagesRejectsMediaTypeMismatch(t *testing.T) {
	cfg := config.ImagesConfig{}
	if _, err := normalizeSessionImages([]kajicoderuntime.ImageBlock{{MediaType: "image/png", Data: []byte("nope")}}, cfg); err == nil {
		t.Fatal("expected error for media type that does not match the bytes")
	}
}
