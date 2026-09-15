package imageinput

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// encodePNG builds an in-memory PNG of the given size with a deterministic but
// non-uniform pattern, so a downscale has real detail to average.
func encodePNG(t *testing.T, w, h int) []byte {
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

// encodeJPEG builds an in-memory JPEG, used to exercise media-type sniffing.
func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestNormalizePassesThroughSmallImage(t *testing.T) {
	src := encodePNG(t, 32, 32)
	block, err := normalizeImage(src, DefaultLimits())
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	if block.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want image/png", block.MediaType)
	}
	if !bytes.Equal(block.Data, src) {
		t.Fatal("a within-limits image must be returned byte-identical (no re-encode)")
	}
}

func TestNormalizeDownscalesOversizeDimensions(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxWidth, limits.MaxHeight = 64, 64
	src := encodePNG(t, 256, 128)

	block, err := normalizeImage(src, limits)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width > limits.MaxWidth || cfg.Height > limits.MaxHeight {
		t.Fatalf("result %dx%d exceeds %dx%d", cfg.Width, cfg.Height, limits.MaxWidth, limits.MaxHeight)
	}
	// Aspect ratio preserved (256x128 -> 64x32).
	if cfg.Width != 64 || cfg.Height != 32 {
		t.Fatalf("result = %dx%d, want 64x32", cfg.Width, cfg.Height)
	}
}

func TestNormalizeShrinksToByteLimitViaJPEG(t *testing.T) {
	// A noisy full-size image that is well over a tiny byte cap; the encoder
	// ladder must fall through PNG to JPEG and still land under the cap.
	limits := Limits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4 << 10, AutoResize: true}
	src := encodePNG(t, 400, 400)

	block, err := normalizeImage(src, limits)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	if len(block.Data) > limits.MaxBytes {
		t.Fatalf("result %d bytes exceeds cap %d", len(block.Data), limits.MaxBytes)
	}
	if block.MediaType != "image/jpeg" && block.MediaType != "image/png" {
		t.Fatalf("MediaType = %q, want a supported type", block.MediaType)
	}
}

func TestNormalizeBoundsEncodedSizeNotRaw(t *testing.T) {
	// The cap is on the base64 payload the provider receives, not the raw bytes.
	// This image's raw size fits the cap while its encoded size does not, so a
	// raw-only check would pass it through and the provider would reject it.
	limits := Limits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 20000, AutoResize: true}
	src := encodePNG(t, 900, 900)
	if len(src) > limits.MaxBytes {
		t.Fatalf("test image must fit raw (%d) to exercise the encoded-only case", len(src))
	}
	if base64Len(len(src)) <= limits.MaxBytes {
		t.Fatalf("test image must EXCEED the cap once encoded: base64Len(%d) = %d", len(src), base64Len(len(src)))
	}

	block, err := normalizeImage(src, limits)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	if got := base64Len(len(block.Data)); got > limits.MaxBytes {
		t.Fatalf("encoded result %d bytes exceeds cap %d", got, limits.MaxBytes)
	}
}

func TestBase64Len(t *testing.T) {
	cases := []struct{ raw, want int }{
		{0, 0}, {1, 4}, {2, 4}, {3, 4}, {4, 8}, {5, 8}, {6, 8}, {7, 12},
	}
	for _, tc := range cases {
		if got := base64Len(tc.raw); got != tc.want {
			t.Errorf("base64Len(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestNormalizeRejectsWhenAutoResizeOff(t *testing.T) {
	limits := Limits{MaxWidth: 16, MaxHeight: 16, MaxBytes: 1 << 20, AutoResize: false}
	src := encodePNG(t, 64, 64)
	if _, err := normalizeImage(src, limits); err == nil {
		t.Fatal("expected rejection when auto-resize is off and image is over-limit")
	}
}

func TestNormalizeResizesWebP(t *testing.T) {
	// WebP now has a real decoder, so an over-limit webp must be downscaled and
	// re-encoded like any other type rather than rejected.
	limits := Limits{MaxWidth: 32, MaxHeight: 32, MaxBytes: 1 << 20, AutoResize: true}
	block, err := normalizeImage(readWebPFixture(t), limits)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if cfg.Width > limits.MaxWidth || cfg.Height > limits.MaxHeight {
		t.Fatalf("result %dx%d exceeds %dx%d", cfg.Width, cfg.Height, limits.MaxWidth, limits.MaxHeight)
	}
}

func TestNormalizeRejectsUnsupportedType(t *testing.T) {
	if _, err := normalizeImage([]byte("not an image at all"), DefaultLimits()); err == nil {
		t.Fatal("expected error for non-image bytes")
	}
}

// A transparent image forced down to JPEG must not become a black rectangle: its
// transparent areas are flattened onto white before JPEG encoding.
func TestNormalizeFlattensTransparencyOntoWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8)) // all zero = fully transparent
	// preferJPEG forces the JPEG branch directly (a transparent PNG can be smaller
	// than its JPEG, so normalizeImage's fit check would otherwise keep the PNG).
	mime, data, ok := encodeUnderLimit(img, 1<<20, true)
	if !ok || mime != "image/jpeg" {
		t.Fatalf("expected a JPEG encoding, got mime=%q ok=%v", mime, ok)
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	r, g, b, _ := decoded.At(4, 4).RGBA()
	if r < 0xF000 || g < 0xF000 || b < 0xF000 {
		t.Fatalf("transparent area should flatten to white, got rgb=%d,%d,%d", r>>8, g>>8, b>>8)
	}
}

func TestNormalizePassesThroughWebPWithinLimits(t *testing.T) {
	// A webp that already fits the envelope is returned unchanged (no re-encode).
	webp := readWebPFixture(t)
	block, err := normalizeImage(webp, DefaultLimits())
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	if block.MediaType != "image/webp" {
		t.Fatalf("MediaType = %q, want image/webp", block.MediaType)
	}
	if !bytes.Equal(block.Data, webp) {
		t.Fatal("a within-limits webp must be returned byte-identical")
	}
}

func TestNormalizeRejectsMediaTypeMismatch(t *testing.T) {
	// A caller-supplied media type over bytes that are not an image must be
	// rejected, so a client cannot smuggle arbitrary data as image/png.
	if _, err := Normalize("image/png", []byte("not an image at all"), DefaultLimits()); err == nil {
		t.Fatal("expected error for media type that does not match the bytes")
	}
	// A supported image whose label is merely imprecise is CORRECTED to the
	// sniffed type rather than refused, so a jpg sent as image/png reaches the
	// provider labelled correctly.
	block, err := Normalize("image/png", encodeJPEG(t), DefaultLimits())
	if err != nil {
		t.Fatalf("Normalize should correct a mislabelled but valid image: %v", err)
	}
	if block.MediaType != "image/jpeg" {
		t.Fatalf("MediaType = %q, want the sniffed image/jpeg", block.MediaType)
	}
}

func TestNormalizePreservesColorOfTranslucentImage(t *testing.T) {
	// A uniformly translucent solid color must survive a downscale with the same
	// straight RGB (the premultiplied-average pitfall darkens these).
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	want := color.NRGBA{R: 200, G: 40, B: 90, A: 128}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, want)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	limits := DefaultLimits()
	limits.MaxWidth, limits.MaxHeight = 16, 16
	src := buf.Bytes()
	block, err := normalizeImage(src, limits)
	if err != nil {
		t.Fatalf("normalizeImage: %v", err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(block.Data))
	if err != nil {
		t.Fatalf("decode result: %v", err)
	}
	got := color.NRGBAModel.Convert(decoded.At(8, 8)).(color.NRGBA)
	// Allow a couple units of rounding slack per channel.
	if absDiff(int(got.R), int(want.R)) > 3 || absDiff(int(got.G), int(want.G)) > 3 ||
		absDiff(int(got.B), int(want.B)) > 3 || absDiff(int(got.A), int(want.A)) > 3 {
		t.Fatalf("color shifted: got %+v, want ~%+v", got, want)
	}
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// readWebPFixture loads the checked-in 128x128 webp. The Go toolchain has no
// webp encoder, so a fixture is the only deterministic way to exercise the real
// decode path (a fabricated RIFF header has no bitstream and cannot be decoded).
func readWebPFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sample.webp"))
	if err != nil {
		t.Fatalf("read webp fixture: %v", err)
	}
	return data
}

func TestSniffImageMediaType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", encodePNG(t, 2, 2), "image/png"},
		{"jpeg", func() []byte {
			var buf bytes.Buffer
			_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
			return buf.Bytes()
		}(), "image/jpeg"},
		{"webp", readWebPFixture(t), "image/webp"},
		{"text", []byte("hello"), ""},
	}
	for _, tc := range cases {
		if got := sniffImageMediaType(tc.data); got != tc.want {
			t.Errorf("%s: sniff = %q, want %q", tc.name, got, tc.want)
		}
	}
}
