package imageinput

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register GIF decoder for image.Decode/DecodeConfig
	"image/jpeg"
	"image/png"
	"math"
	"net/http"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	_ "golang.org/x/image/webp" // register WebP decoder for image.Decode/DecodeConfig
)

// Limits is the provider-safe envelope an image is normalized into: the largest
// pixel dimensions and the largest encoded byte size sent to a provider.
// Resizing keeps screenshots readable while staying under the per-image limits
// providers enforce (a 4K screenshot is commonly several MB and thousands of
// pixels wide, which the provider rejects outright otherwise).
type Limits struct {
	MaxWidth  int
	MaxHeight int
	// MaxBytes caps the BASE64-ENCODED image bytes — the payload the request
	// actually carries. Providers limit the encoded body, not the raw bytes:
	// base64 inflates by 4/3, so a limit expressed on raw bytes lets through an
	// image ~33% larger than the provider allows (Anthropic's "5 MB per image" is
	// measured on the base64 payload). Bounding the encoded form is what keeps a
	// screenshot sendable instead of rejected.
	MaxBytes   int
	AutoResize bool // when false an over-limit image is rejected instead of resized
}

// DefaultLimits matches the envelope other coding agents use: 2000x2000 and
// 5 MiB of base64 (i.e. ~3.75 MiB raw), auto-resizing by default. 5 MiB encoded
// sits exactly at the per-image ceiling Anthropic and most providers enforce.
func DefaultLimits() Limits {
	return Limits{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 5 << 20, AutoResize: true}
}

// LimitsFrom returns DefaultLimits with each non-zero/non-nil override applied.
// It is the one place a configured envelope becomes a loader Limits, so every
// surface that has resolved config (cli exec, the TUI, ACP) stays consistent.
func LimitsFrom(maxWidth, maxHeight, maxBytes int, autoResize *bool) Limits {
	out := DefaultLimits()
	if maxWidth > 0 {
		out.MaxWidth = maxWidth
	}
	if maxHeight > 0 {
		out.MaxHeight = maxHeight
	}
	if maxBytes > 0 {
		out.MaxBytes = maxBytes
	}
	if autoResize != nil {
		out.AutoResize = *autoResize
	}
	return out
}

// LimitsOrDefault returns limits unchanged, or DefaultLimits when it is unset
// (MaxBytes <= 0). A loader can then normalize without a nil/zero check.
func LimitsOrDefault(limits Limits) Limits {
	if limits.MaxBytes <= 0 {
		return DefaultLimits()
	}
	return limits
}

const (
	// maxSourceBytes bounds how much of a source file is read before deciding
	// whether to keep or resize it. It is generous (a 4K screenshot is a few MB)
	// while still refusing to buffer a multi-gigabyte file just to discard it.
	maxSourceBytes = 40 << 20
	// maxDecodePixels bounds the decoded pixel count before image.Decode runs, so
	// a decompression bomb header cannot make the process allocate an unbounded
	// pixel buffer. 64 megapixels is ~256 MiB as RGBA, far above any screenshot.
	maxDecodePixels = 64 << 20
	// maxResizeRounds caps the shrink-and-reencode loop so a pathological image
	// cannot spin forever.
	maxResizeRounds = 32
)

// jpegQualities is the quality ladder tried (after PNG) to land under MaxBytes,
// highest first so the best quality that fits wins.
var jpegQualities = []int{85, 75, 65, 55, 45}

// base64Len is the number of bytes rawLen encodes to under standard base64.
// It is the measure MaxBytes bounds (providers limit the encoded payload), and
// it lives here so the encode sites and the fit checks never disagree.
func base64Len(rawLen int) int {
	return (rawLen + 2) / 3 * 4
}

// normalizeImage validates the media type, then returns the image unchanged when
// it already fits the envelope, or downscaled and re-encoded to fit when it does
// not. It is the shared step behind LoadFile, ReadClipboardImage and PDF page
// rendering, so every input surface sends a provider-safe image.
func normalizeImage(data []byte, limits Limits) (kajicoderuntime.ImageBlock, error) {
	mediaType := sniffImageMediaType(data)
	if mediaType == "" {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("unsupported image type (allowed: png, jpeg, gif, webp)")
	}
	return normalizeKnown(mediaType, data, limits)
}

// Normalize normalizes an image whose media type arrived alongside the bytes
// (stream-json and ACP input) rather than a file the sniffer reads. The caller's
// media type is only a claim, so the content is sniffed and the SNIFFED type is
// authoritative: an unsupported or mislabelled payload is rejected (nothing
// reaches a provider mislabelled), while a supported image whose label is merely
// imprecise (a jpg called image/jpg) is corrected rather than refused.
func Normalize(mediaType string, data []byte, limits Limits) (kajicoderuntime.ImageBlock, error) {
	if kajicoderuntime.NormalizeImageMediaType(mediaType) == "" {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("unsupported image type (allowed: png, jpeg, gif, webp)")
	}
	sniffed := sniffImageMediaType(data)
	if sniffed == "" {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image data does not match a supported image type")
	}
	return normalizeKnown(sniffed, data, limits)
}

// normalizeKnown normalizes already-validated image bytes into limits: the bytes
// are returned unchanged when they fit, or downscaled and re-encoded (PNG, then
// JPEG at descending quality) until they do.
func normalizeKnown(mediaType string, data []byte, limits Limits) (kajicoderuntime.ImageBlock, error) {
	cfg, _, configErr := image.DecodeConfig(bytes.NewReader(data))
	if configErr != nil {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image could not be decoded")
	}
	if uint64(cfg.Width)*uint64(cfg.Height) > maxDecodePixels {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image %dx%d is too large to process", cfg.Width, cfg.Height)
	}
	if cfg.Width <= limits.MaxWidth && cfg.Height <= limits.MaxHeight && base64Len(len(data)) <= limits.MaxBytes {
		return kajicoderuntime.ImageBlock{MediaType: mediaType, Data: data}, nil
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image could not be decoded")
	}

	if !limits.AutoResize {
		return kajicoderuntime.ImageBlock{}, fmt.Errorf("image exceeds the %dx%d / %d encoded-byte limit", limits.MaxWidth, limits.MaxHeight, limits.MaxBytes)
	}

	current := downscale(decoded, limits.MaxWidth, limits.MaxHeight)
	preferJPEG := mediaType == "image/jpeg"
	for round := 0; round < maxResizeRounds; round++ {
		if mime, encoded, ok := encodeUnderLimit(current, limits.MaxBytes, preferJPEG); ok {
			return kajicoderuntime.ImageBlock{MediaType: mime, Data: encoded}, nil
		}
		w, h := current.Bounds().Dx(), current.Bounds().Dy()
		if w <= 1 && h <= 1 {
			break
		}
		current = downscale(current, maxInt(1, w*3/4), maxInt(1, h*3/4))
	}
	return kajicoderuntime.ImageBlock{}, fmt.Errorf("image could not be resized below the %d encoded-byte limit", limits.MaxBytes)
}

// sniffImageMediaType maps the leading bytes of an image to the allow-listed
// MIME string, or "" when it is not one of the supported types. Content sniffing
// is used rather than the caller's filename or the clipboard's own claim.
func sniffImageMediaType(data []byte) string {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return kajicoderuntime.NormalizeImageMediaType(http.DetectContentType(data[:n]))
}

// downscale area-averages src into a new image no larger than maxW x maxH,
// preserving aspect ratio and never upscaling.
//
// The box filter is deliberate, chosen over a sharpening kernel (Lanczos/
// CatmullRom) on measured evidence rather than preference. Across 1.92x-5.12x
// downscales of screenshot-like content, a Lanczos3 kernel gained at most ~6%
// edge contrast, lost ~10% at 2.56x, and enlarged the encoded result by
// 145-266% because it leaves high-frequency detail that PNG cannot compress.
// Under a tight byte budget that inflation forces extra shrink rounds, so the
// kernel path can end up at lower resolution than the box path for the same
// limit. Sharper pixels are not worth a larger or smaller final image.
func downscale(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	scale := math.Min(float64(maxW)/float64(sw), float64(maxH)/float64(sh))
	if scale > 1 {
		scale = 1
	}
	dw := maxInt(1, int(math.Round(float64(sw)*scale)))
	dh := maxInt(1, int(math.Round(float64(sh)*scale)))
	if dw >= sw && dh >= sh {
		return src
	}

	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	// Source indices are computed in 64-bit: a very tall/wide source (up to the
	// pixel cap, so one dimension can reach ~64M) times a destination index would
	// overflow a 32-bit int, so the products are widened before dividing.
	for dy := 0; dy < dh; dy++ {
		y0 := b.Min.Y + int(int64(dy)*int64(sh)/int64(dh))
		y1 := b.Min.Y + int(int64(dy+1)*int64(sh)/int64(dh))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0 := b.Min.X + int(int64(dx)*int64(sw)/int64(dw))
			x1 := b.Min.X + int(int64(dx+1)*int64(sw)/int64(dw))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					// RGBA() returns alpha-premultiplied channels; average them
					// premultiplied, then un-premultiply below so the straight-alpha
					// RGBA buffer is written with the correct (not darkened) color.
					cr, cg, cb, ca := src.At(x, y).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			avgR, avgG, avgB, avgA := r/n, g/n, bl/n, a/n
			if avgA > 0 && avgA < 0xFFFF {
				avgR = avgR * 0xFFFF / avgA
				avgG = avgG * 0xFFFF / avgA
				avgB = avgB * 0xFFFF / avgA
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i+0] = clamp8(avgR)
			dst.Pix[i+1] = clamp8(avgG)
			dst.Pix[i+2] = clamp8(avgB)
			dst.Pix[i+3] = clamp8(avgA)
		}
	}
	return dst
}

// encodeUnderLimit encodes img at or below maxBytes of base64, returning the MIME
// type and bytes of the first encoding that fits. A JPEG source is tried as JPEG
// first (re-encoding an already-compressed image to PNG only inflates it);
// everything else is tried as PNG first (lossless). JPEG has no alpha channel, so
// any transparency is flattened onto white first — otherwise a transparent
// screenshot would encode as a black rectangle.
func encodeUnderLimit(img image.Image, maxBytes int, preferJPEG bool) (string, []byte, bool) {
	tryPNG := func() (string, []byte, bool) {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err == nil && base64Len(buf.Len()) <= maxBytes {
			return "image/png", buf.Bytes(), true
		}
		return "", nil, false
	}
	var flat image.Image
	tryJPEG := func() (string, []byte, bool) {
		// JPEG has no alpha, so flatten onto white lazily — only when a JPEG
		// attempt is actually made, not on the PNG-first fast path that fits.
		if flat == nil {
			flat = flattenOntoWhite(img)
		}
		for _, quality := range jpegQualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: quality}); err == nil && base64Len(buf.Len()) <= maxBytes {
				return "image/jpeg", buf.Bytes(), true
			}
		}
		return "", nil, false
	}
	if preferJPEG {
		if mime, data, ok := tryJPEG(); ok {
			return mime, data, ok
		}
		return tryPNG()
	}
	if mime, data, ok := tryPNG(); ok {
		return mime, data, ok
	}
	return tryJPEG()
}

// flattenOntoWhite composites img over an opaque white background, so an image
// with alpha can be JPEG-encoded without its transparent areas turning black.
func flattenOntoWhite(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			// RGBA() is alpha-premultiplied; unscaled over white yields the
			// correct opaque color without a separate premultiply step.
			dst.SetRGBA(x, y, color.RGBA{
				R: clamp8(uint64(r) + 0xFFFF - uint64(a)),
				G: clamp8(uint64(g) + 0xFFFF - uint64(a)),
				B: clamp8(uint64(bl) + 0xFFFF - uint64(a)),
				A: 0xFF,
			})
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clamp8 converts a 16-bit (0..0xFFFF) channel value into an 8-bit byte.
func clamp8(v uint64) uint8 {
	if v > 0xFFFF {
		v = 0xFFFF
	}
	return uint8(v >> 8)
}
