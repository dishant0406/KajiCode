package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for image.Decode
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"math"
	"os"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Preview geometry. The image is downscaled ONCE at attach time to at most
// thumbMaxW x thumbMaxH colour samples; every frame then renders cheap
// quadrant-block cells from that grid, so no decode or resize ever happens in
// the render loop.
//
// The grid covers the largest preview the composer can ever draw (maxPreviewCols
// columns by previewHeightBudget rows, at 2 samples per cell axis), so the preview
// is always a supersampled area average rather than one chunky sample per cell —
// that is what keeps a small thumbnail from looking pixelated. The renderer
// meanwhile draws a bounded miniature (at most maxPreviewCols columns) rather
// than scaling the image up to the composer width.
const (
	thumbMaxW = maxPreviewCols * 2
	thumbMaxH = previewTargetRows * 4
	// maxDecodeDim bounds the decoded dimensions before image.Decode runs, so a
	// malformed or deliberately huge image header cannot make the process
	// allocate an unbounded pixel buffer (a decompression bomb). 4096x4096 RGBA
	// is ~64 MiB — large but survivable, and far above any real screenshot.
	maxDecodeDim = 4096
)

type rgb struct{ r, g, b uint8 }

// attachmentThumb is a pre-downscaled image ready to paint as terminal cells.
type attachmentThumb struct {
	w, h int   // sample grid dimensions
	px   []rgb // row-major, len(w*h)
}

// buildAttachmentThumb decodes an image and downscales it for preview. It
// returns ok=false for anything the stdlib cannot decode (e.g. webp, which has
// no standard-library decoder) or that exceeds the decode bounds; callers then
// fall back to a plain icon chip rather than failing the attachment.
func buildAttachmentThumb(block kajicoderuntime.ImageBlock) (*attachmentThumb, bool) {
	if len(block.Data) == 0 {
		return nil, false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(block.Data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, false
	}
	if cfg.Width > maxDecodeDim || cfg.Height > maxDecodeDim {
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(block.Data))
	if err != nil {
		return nil, false
	}
	return boxDownscale(img), true
}

// boxDownscale averages source pixels into a grid proportional to the source,
// capped at the sample bounds and never upscaled. A box (area-average) filter is
// deliberately used instead of a higher-quality resampler: it is a dozen lines of
// stdlib code, has no dependency, and for a downscale this large it is both
// correct and stable. Samples are averaged in linear light (squared), not on
// gamma-encoded bytes, so fine detail does not darken or muddy.
func boxDownscale(src image.Image) *attachmentThumb {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil
	}

	scale := math.Min(float64(thumbMaxW)/float64(sw), float64(thumbMaxH)/float64(sh))
	if scale > 1 {
		scale = 1
	}
	dw := clampInt(int(math.Round(float64(sw)*scale)), 1, thumbMaxW)
	dh := clampInt(int(math.Round(float64(sh)*scale)), 1, thumbMaxH)

	thumb := &attachmentThumb{w: dw, h: dh, px: make([]rgb, dw*dh)}
	for dy := 0; dy < dh; dy++ {
		y0 := b.Min.Y + dy*sh/dh
		y1 := b.Min.Y + (dy+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0 := b.Min.X + dx*sw/dw
			x1 := b.Min.X + (dx+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs uint64
			var n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bl, _ := src.At(x, y).RGBA()
					rs += linear(uint8(r >> 8))
					gs += linear(uint8(g >> 8))
					bs += linear(uint8(bl >> 8))
					n++
				}
			}
			if n == 0 {
				continue
			}
			thumb.px[dy*dw+dx] = rgb{
				r: encodeSRGB(float64(rs) / float64(n)),
				g: encodeSRGB(float64(gs) / float64(n)),
				b: encodeSRGB(float64(bs) / float64(n)),
			}
		}
	}
	return thumb
}

// linear maps an sRGB byte to its squared (linear-light) 0..65535 value, so
// averaging happens in light space rather than on gamma-encoded bytes.
func linear(v uint8) uint64 {
	l := uint64(v) * uint64(v)
	return l
}

// encodeSRGB inverts linear() for an averaged value: it takes the mean squared
// value and returns the sRGB byte that would produce it.
func encodeSRGB(meanSq float64) uint8 {
	return uint8(clampInt(int(math.Sqrt(meanSq)+0.5), 0, 255))
}

// Preview sizing. The preview is a bounded miniature, not a copy of the image at
// the composer width. Its size is anchored to a fixed ON-SCREEN HEIGHT — about
// 150 px, the size of a small profile picture — so an image can never dominate
// the input or the transcript; the width follows the source aspect ratio. A cell
// is about twice as tall as it is wide and paints two sample rows, so
// previewTargetRows rows are roughly previewTargetRows*2*cellWidth pixels tall.
// The terminal here reports font_size 15.26 / line_height 1.2 (its own termy.conf;
// line_height is a multiplier, so a cell is ~18 px tall), which puts
// previewTargetRows rows at ~150 px.
const (
	// previewTargetRows is the height every preview aims for; the width then
	// follows from the source aspect ratio.
	previewTargetRows = 8
	// maxPreviewCols bounds the derived width so an ultra-wide (panoramic) image
	// cannot crowd out the input; minPreviewCols keeps a tiny source from
	// collapsing to noise.
	maxPreviewCols = 48
	minPreviewCols = 4
)

// previewGap is the number of blank columns between two previews laid side by
// side. Previews are a horizontal queue, not a vertical stack.
const previewGap = 2

// attachmentPreview is one image's preview at its computed size.
type attachmentPreview struct {
	thumb *attachmentThumb
	cols  int
	rows  int
}

// layoutPreviews is the single source of the attachment preview layout at a given
// width: one sized preview per staged image, side by side in staged order (a queue
// growing to the right), or nil when nothing should be drawn (nothing staged,
// nothing decodable, or previews disabled under NO_COLOR). Every preview shares
// previewTargetRows, so the row height is constant no matter how many images are
// staged; the available width is split evenly between them so the row never runs
// past the composer. When even minimum-width previews cannot all fit, only the
// first images that fit are drawn.
func layoutPreviews(thumbs []*attachmentThumb, innerWidth int) []attachmentPreview {
	if len(thumbs) == 0 || innerWidth <= 0 {
		return nil
	}
	// How many minimum-width previews fit side by side, gaps included.
	maxFit := (innerWidth + previewGap) / (minPreviewCols + previewGap)
	if maxFit < 1 {
		maxFit = 1
	}
	if len(thumbs) > maxFit {
		thumbs = thumbs[:maxFit]
	}
	perImage := (innerWidth - previewGap*(len(thumbs)-1)) / len(thumbs)
	out := make([]attachmentPreview, 0, len(thumbs))
	for _, t := range thumbs {
		cols, rows, ok := previewDims(t, perImage, previewTargetRows)
		if !ok {
			continue
		}
		out = append(out, attachmentPreview{thumb: t, cols: cols, rows: rows})
	}
	return out
}

// previewDims is the single source of the preview geometry: width, height, and
// whether anything should be drawn. The height is fixed at rows; the width comes
// from the source aspect so a wide image is wider than it is tall and a tall
// image is narrower, never stretched. A cell is about twice as tall as it is wide
// and paints two sample rows, so the drawn aspect is (cols)/(rows*2); matching the
// source aspect (thumbW/thumbH, which boxDownscale preserves) gives
// cols = rows*2*thumbW/thumbH — chafa's font_ratio=0.5 geometry. The width is
// capped at maxPreviewCols and by the sample grid (so the thumb is never
// upscaled) and floored at minPreviewCols. renderPreview and the height
// calculation both go through it, so they can never disagree.
func previewDims(thumb *attachmentThumb, availCols, rows int) (cols, outRows int, ok bool) {
	if thumb == nil || thumb.w == 0 || thumb.h == 0 || rows < 1 {
		return 0, 0, false
	}
	cols = int(math.Round(float64(rows) * 2 * float64(thumb.w) / float64(thumb.h)))
	cols = clampInt(min(cols, min(availCols, maxPreviewCols)), minPreviewCols, thumb.w)
	if cols <= 0 {
		return 0, 0, false
	}
	return cols, rows, true
}

// average returns the mean colour of the sample rectangle [x0,x1) x [y0,y1),
// clamped to the grid.
func (t *attachmentThumb) average(x0, x1, y0, y1 int) rgb {
	x0 = clampInt(x0, 0, t.w)
	x1 = clampInt(x1, x0+1, t.w)
	y0 = clampInt(y0, 0, t.h)
	y1 = clampInt(y1, y0+1, t.h)
	var rs, gs, bs, n uint64
	for y := y0; y < y1; y++ {
		row := y * t.w
		for x := x0; x < x1; x++ {
			p := t.px[row+x]
			rs += uint64(p.r)
			gs += uint64(p.g)
			bs += uint64(p.b)
			n++
		}
	}
	if n == 0 {
		return rgb{}
	}
	return rgb{uint8(rs / n), uint8(gs / n), uint8(bs / n)}
}

// renderPreview paints the thumb as quadrant-block cells: each terminal cell is
// a 2x2 grid of colour samples drawn as one block glyph whose foreground and
// background are the cell's two most distinct samples. The caller pads each
// returned line to the composer width.
func renderPreview(thumb *attachmentThumb, cols, rows int) []string {
	if thumb == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	lines := make([]string, 0, rows)
	for y := 0; y < rows; y++ {
		var b strings.Builder
		b.Grow(cols * 20)
		for x := 0; x < cols; x++ {
			fg, bg, glyph := thumb.quadrantCell(x, y, cols, rows)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm%c",
				fg.r, fg.g, fg.b, bg.r, bg.g, bg.b, glyph)
		}
		b.WriteString("\x1b[0m")
		lines = append(lines, b.String())
	}
	return lines
}

// renderPreviewRow paints a horizontal queue of previews: each is rendered to its
// own lines, then the lines are joined side by side with previewGap blank columns
// between them. Every preview shares a row count, so the result is that many
// lines and the images sit in one row growing to the right.
func renderPreviewRow(previews []attachmentPreview) []string {
	if len(previews) == 0 {
		return nil
	}
	rows := previews[0].rows
	gap := strings.Repeat(" ", previewGap)
	lines := make([]string, rows)
	for _, p := range previews {
		cellLines := renderPreview(p.thumb, p.cols, p.rows)
		for y := 0; y < rows; y++ {
			line := ""
			if y < len(cellLines) {
				line = cellLines[y]
			}
			if lines[y] == "" {
				lines[y] = line
			} else {
				lines[y] += gap + line
			}
		}
	}
	return lines
}

// attachmentPreviewEnabled reports whether a live image preview may be painted.
// Previews are 24-bit SGR quadrant blocks, so under NO_COLOR (which forces the
// TUI to strip color) they would spew raw escape codes instead of an image; the
// plain icon chip is shown in that case.
func attachmentPreviewEnabled() bool {
	return !noColorRequested(os.Getenv)
}
