package tui

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// quadrantGlyphs is every block glyph the quadrant renderer can emit; a preview
// cell must draw exactly one of these.
const quadrantGlyphs = "▘▝▀▖▌▞▛▗▚▐▜▄▙▟█ "

// encodeTestPNG builds an in-memory PNG of the given solid colour and size.
func encodeTestPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestBuildAttachmentThumbDownscalesToSampleGrid(t *testing.T) {
	block := kajicoderuntime.ImageBlock{
		MediaType: "image/png",
		Data:      encodeTestPNG(t, 600, 300, color.RGBA{R: 200, G: 40, B: 40, A: 255}),
	}
	thumb, ok := buildAttachmentThumb(block)
	if !ok {
		t.Fatal("a valid PNG should decode into a thumb")
	}
	if thumb.w > thumbMaxW || thumb.h > thumbMaxH {
		t.Fatalf("thumb %dx%d exceeds the sample bound %dx%d", thumb.w, thumb.h, thumbMaxW, thumbMaxH)
	}
	if thumb.w <= 0 || thumb.h <= 0 {
		t.Fatalf("thumb must have positive dimensions, got %dx%d", thumb.w, thumb.h)
	}
	// A 2:1 source must stay wide, not square.
	if thumb.w <= thumb.h {
		t.Fatalf("aspect ratio not preserved: %dx%d", thumb.w, thumb.h)
	}
	// A wide source fills the height budget (the binding axis for a 2:1 source,
	// since the height cap is half the width cap), keeping readable resolution.
	if thumb.h != thumbMaxH {
		t.Fatalf("wide source should fill the height budget %d, got %d", thumbMaxH, thumb.h)
	}
	// A solid-colour image must downsample to that same colour (within 1/255
	// from the linear-light round-trip).
	got := thumb.px[0]
	if absDiff(got.r, 200) > 1 || absDiff(got.g, 40) > 1 || absDiff(got.b, 40) > 1 {
		t.Fatalf("solid colour averaged to %#v, want ~{200 40 40}", got)
	}
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func TestBuildAttachmentThumbNeverUpscales(t *testing.T) {
	block := kajicoderuntime.ImageBlock{
		MediaType: "image/png",
		Data:      encodeTestPNG(t, 10, 8, color.RGBA{R: 10, G: 20, B: 30, A: 255}),
	}
	thumb, ok := buildAttachmentThumb(block)
	if !ok {
		t.Fatal("expected a thumb")
	}
	if thumb.w != 10 || thumb.h != 8 {
		t.Fatalf("small image should keep its size, got %dx%d", thumb.w, thumb.h)
	}
}

func TestBuildAttachmentThumbRejectsUndecodable(t *testing.T) {
	if _, ok := buildAttachmentThumb(kajicoderuntime.ImageBlock{MediaType: "image/png", Data: []byte("not an image")}); ok {
		t.Fatal("garbage bytes must not decode into a thumb")
	}
	if _, ok := buildAttachmentThumb(kajicoderuntime.ImageBlock{MediaType: "image/png"}); ok {
		t.Fatal("empty data must not decode into a thumb")
	}
}

func TestRenderPreviewEmitsQuadrantCells(t *testing.T) {
	thumb := &attachmentThumb{
		w:  8,
		h:  8,
		px: make([]rgb, 64),
	}
	lines := renderPreview(thumb, 8, 4)
	if len(lines) != 4 {
		t.Fatalf("expected 4 preview rows, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.ContainsAny(line, quadrantGlyphs) {
			t.Fatalf("row %d should use quadrant block glyphs: %q", i, line)
		}
		if !strings.Contains(line, "\x1b[38;2;") || !strings.Contains(line, "\x1b[48;2;") {
			t.Fatalf("row %d should set both fg and bg colours: %q", i, line)
		}
	}
	if got := renderPreview(nil, 8, 4); got != nil {
		t.Fatalf("a nil thumb must render nothing, got %q", got)
	}
}

func TestPreviewDimsIsHeightAnchored(t *testing.T) {
	// The preview targets a fixed on-screen height; the width follows the source
	// aspect ratio. A wide source must therefore be wider than it is tall, and the
	// width must never exceed maxPreviewCols.
	wide := &attachmentThumb{w: 240, h: 120, px: make([]rgb, 240*120)}
	cols, rows, ok := previewDims(wide, 200, previewTargetRows)
	if !ok {
		t.Fatal("a wide thumb should preview")
	}
	if rows != previewTargetRows {
		t.Fatalf("rows %d should equal the target %d", rows, previewTargetRows)
	}
	// 2:1 source at 8 rows → 8*2*2 = 32 cols, under the cap.
	if cols != previewTargetRows*4 {
		t.Fatalf("2:1 source at %d rows should be %d cols, got %d", previewTargetRows, previewTargetRows*4, cols)
	}

	// An ultra-wide source is capped so a panorama cannot crowd out the input.
	panorama := &attachmentThumb{w: 4000, h: 100, px: make([]rgb, 4000*100)}
	if c, _, _ := previewDims(panorama, 400, previewTargetRows); c != maxPreviewCols {
		t.Fatalf("panorama width %d should be capped at %d", c, maxPreviewCols)
	}

	// A tall source is narrower than it is tall, never stretched.
	tall := &attachmentThumb{w: 60, h: 240, px: make([]rgb, 60*240)}
	if c, r, _ := previewDims(tall, 400, previewTargetRows); c >= r {
		t.Fatalf("tall source cols %d should be < rows %d", c, r)
	}

	// The sample grid, not the cap, bounds a small thumb: a preview never upscales.
	small := &attachmentThumb{w: 6, h: 3, px: make([]rgb, 18)}
	if c, _, _ := previewDims(small, 400, previewTargetRows); c != 6 {
		t.Fatalf("preview width %d should clamp to the thumb width 6", c)
	}
	// A narrow availability shrinks the preview within the floor.
	if c, _, _ := previewDims(wide, 11, previewTargetRows); c != 11 {
		t.Fatalf("preview width %d should honour the available width 11", c)
	}
	// An extremely narrow composer still yields a drawable preview.
	if c, _, ok := previewDims(wide, 1, previewTargetRows); !ok || c != minPreviewCols {
		t.Fatalf("narrow composer should clamp to %d, got %d ok=%v", minPreviewCols, c, ok)
	}
	// A square source stays square on screen: a cell is about twice as tall as it
	// is wide, so cols must be twice the row count for the drawn aspect to be 1.
	square := &attachmentThumb{w: 200, h: 200, px: make([]rgb, 200*200)}
	if c, r, _ := previewDims(square, 400, previewTargetRows); c != r*2 {
		t.Fatalf("square source should be %d cols for %d rows, got %d", r*2, r, c)
	}
}

func TestPreviewDimsHonoursRowBudget(t *testing.T) {
	// A row budget below the target is honoured exactly, and every row count is
	// accepted as long as it is drawable.
	square := &attachmentThumb{w: 200, h: 200, px: make([]rgb, 200*200)}
	if _, rows, ok := previewDims(square, 400, 3); !ok || rows != 3 {
		t.Fatalf("explicit row budget should be respected, got %d ok=%v", rows, ok)
	}
	if _, _, ok := previewDims(square, 400, 0); ok {
		t.Fatal("a zero row budget must not preview")
	}
}

func TestBoxDownscalePreservesSpatialPattern(t *testing.T) {
	// A 4-quadrant image: each quadrant a distinct colour. After a large
	// downscale the quadrant structure must survive so the preview is readable.
	const w, h = 400, 400
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c color.RGBA
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{220, 40, 40, 255} // TL red
			case x >= w/2 && y < h/2:
				c = color.RGBA{40, 160, 60, 255} // TR green
			case x < w/2 && y >= h/2:
				c = color.RGBA{50, 90, 220, 255} // BL blue
			default:
				c = color.RGBA{230, 220, 60, 255} // BR yellow
			}
			img.Set(x, y, c)
		}
	}
	thumb := boxDownscale(img)
	if thumb == nil || thumb.w < 32 || thumb.h < 32 {
		t.Fatalf("preview grid too coarse to resolve detail: %+v", thumb)
	}
	tl := thumb.px[0]
	tr := thumb.px[(thumb.w - 1)]
	bl := thumb.px[(thumb.h-1)*thumb.w]
	br := thumb.px[len(thumb.px)-1]
	if !sameQuadrant(tl, 220, 40, 40) {
		t.Fatalf("top-left quadrant colour lost: %#v", tl)
	}
	if !sameQuadrant(tr, 40, 160, 60) {
		t.Fatalf("top-right quadrant colour lost: %#v", tr)
	}
	if !sameQuadrant(bl, 50, 90, 220) {
		t.Fatalf("bottom-left quadrant colour lost: %#v", bl)
	}
	if !sameQuadrant(br, 230, 220, 60) {
		t.Fatalf("bottom-right quadrant colour lost: %#v", br)
	}
}

func sameQuadrant(got rgb, r, g, b uint8) bool {
	return absDiff(got.r, r) <= 2 && absDiff(got.g, g) <= 2 && absDiff(got.b, b) <= 2
}

// The quadrant cell resolves 2x2 structure that a single averaged sample would
// flatten: a cell whose top half is red and bottom half is blue must pick the
// matching vertical-fill glyphs, not one blended colour.
func TestQuadrantCellResolvesVerticalPattern(t *testing.T) {
	// 2-wide x 4-tall samples: the top half red, the bottom half blue. A single
	// averaged sample would flatten this to one purple; the quadrant cell must
	// keep both colours.
	thumb := &attachmentThumb{
		w: 2,
		h: 4,
		px: []rgb{
			{200, 0, 0}, {200, 0, 0},
			{0, 0, 200}, {0, 0, 200},
			{200, 0, 0}, {200, 0, 0},
			{0, 0, 200}, {0, 0, 200},
		},
	}
	fg, bg, glyph := thumb.quadrantCell(0, 0, 1, 1)
	// The two colours must be the red and the blue, not a purple average.
	if !isColor(fg, 200, 0, 0) && !isColor(bg, 200, 0, 0) {
		t.Fatalf("red sample lost: fg=%#v bg=%#v", fg, bg)
	}
	if !isColor(fg, 0, 0, 200) && !isColor(bg, 0, 0, 200) {
		t.Fatalf("blue sample lost: fg=%#v bg=%#v", fg, bg)
	}
	// A solid top half over a solid bottom half is a half-fill glyph.
	if glyph != '▀' && glyph != '▄' {
		t.Fatalf("top/bottom split should fill half the cell, got %q", glyph)
	}
}

func isColor(c rgb, r, g, b uint8) bool {
	return c.r == r && c.g == g && c.b == b
}

func TestAttachmentIconAndLabel(t *testing.T) {
	if got := attachmentIcon(newImageAttachment("x.png", "image/png", nil)); got != "▣" {
		t.Fatalf("image icon = %q, want ▣", got)
	}
	if got := attachmentIcon(stagedAttachment{Label: "spec.pdf"}); got != "▤" {
		t.Fatalf("pdf icon = %q, want ▤", got)
	}
	if got := attachmentIcon(stagedAttachment{Label: "notes.txt"}); got != "▢" {
		t.Fatalf("generic icon = %q, want ▢", got)
	}
	long := strings.Repeat("a", 40)
	if got := shortAttachmentLabel(long); len([]rune(got)) > 24 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long label should be truncated with an ellipsis, got %q", got)
	}
	if got := shortAttachmentLabel("   "); got != "attachment" {
		t.Fatalf("blank label = %q, want attachment", got)
	}
}

// attachmentBlockLines must count the chip row plus one preview per staged image,
// and the rendered block must have exactly that many lines — the composer box
// height, the hit-test, and the render all rely on this single source of truth.
func TestAttachmentBlockGeometryMatchesRender(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{
		newImageAttachment("photo.png", "image/png", encodeTestPNG(t, 80, 40, color.RGBA{R: 1, G: 2, B: 3, A: 255})),
	}

	const innerWidth = 60
	lines := m.attachmentBlock(innerWidth)
	if len(lines) == 0 {
		t.Fatal("expected a non-empty attachment block")
	}
	if want := m.attachmentBlockLines(innerWidth); want != len(lines) {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", want, len(lines))
	}
	if !strings.Contains(lines[0], "[Image #1]") {
		t.Fatalf("first line should be the chip row, got %q", lines[0])
	}
	if !strings.ContainsAny(lines[1], quadrantGlyphs) {
		t.Fatalf("second line should be the image preview, got %q", lines[1])
	}

	// No attachments: zero lines, and the empty block renders nothing.
	empty := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	if empty.attachmentBlockLines(innerWidth) != 0 || len(empty.attachmentBlock(innerWidth)) != 0 {
		t.Fatal("an empty attachment set must occupy no composer lines")
	}
}

// Every staged image gets its own preview, laid out side by side in staged order
// (a queue growing right), so pasting several images shows several thumbnails in
// one constant-height band rather than only the first.
func TestAttachmentBlockPreviewsEveryStagedImage(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{
		newImageAttachment("a.png", "image/png", encodeTestPNG(t, 80, 40, color.RGBA{R: 200, G: 0, B: 0, A: 255})),
		newImageAttachment("b.png", "image/png", encodeTestPNG(t, 40, 40, color.RGBA{G: 200, A: 255})),
		// An undecodable image still gets a chip but contributes no preview.
		newImageAttachment("c.webp", "image/webp", []byte("not an image")),
	}

	const innerWidth = 60
	previews := m.attachmentPreviews(innerWidth)
	if len(previews) != 2 {
		t.Fatalf("two decodable images should yield two previews, got %d", len(previews))
	}
	lines := m.attachmentBlock(innerWidth)
	if got := m.attachmentBlockLines(innerWidth); got != len(lines) {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", got, len(lines))
	}
	// Side by side: one constant-height band, not a stack of previews.
	if len(lines) != 1+previewTargetRows {
		t.Fatalf("two images should render one %d-row band, got %d lines", previewTargetRows, len(lines)-1)
	}
	if !strings.ContainsAny(lines[1], quadrantGlyphs) {
		t.Fatalf("line after the chip should hold the preview band, got %q", lines[1])
	}
}

// Many images are laid out side by side and share the composer width: the band
// keeps one constant height instead of growing with the image count.
func TestAttachmentBlockBoundsManyPreviews(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	for i := 0; i < 6; i++ {
		m.pendingAttachments = append(m.pendingAttachments,
			newImageAttachment("x.png", "image/png", encodeTestPNG(t, 80, 40, color.RGBA{R: 10, A: 255})))
	}
	const innerWidth = 60
	// The band height never exceeds the target, however many images are staged.
	if rows := m.attachmentPreviewRows(innerWidth); rows > previewTargetRows {
		t.Fatalf("preview band occupied %d rows, over the target %d", rows, previewTargetRows)
	}
	// The side-by-side previews together must fit the composer width, gaps included.
	total := 0
	for _, p := range m.attachmentPreviews(innerWidth) {
		total += p.cols
	}
	total += previewGap * (len(m.attachmentPreviews(innerWidth)) - 1)
	if total > innerWidth {
		t.Fatalf("previews span %d columns, over the %d available", total, innerWidth)
	}
	if len(m.attachmentPreviews(innerWidth)) < 2 {
		t.Fatalf("expected several previews side by side, got %d", len(m.attachmentPreviews(innerWidth)))
	}
	if got, want := m.attachmentBlockLines(innerWidth), len(m.attachmentBlock(innerWidth)); got != want {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", got, want)
	}
}

// Under NO_COLOR the preview is suppressed, so the height calculation and the
// render must BOTH drop to the single chip row. This guards the regression where
// attachmentBlockLines still counted preview rows while attachmentBlock painted
// only the chip — a geometry divergence that mis-sized the composer box.
func TestAttachmentBlockGeometryUnderNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{
		newImageAttachment("photo.png", "image/png", encodeTestPNG(t, 80, 40, color.RGBA{R: 1, G: 2, B: 3, A: 255})),
	}

	const innerWidth = 60
	lines := m.attachmentBlock(innerWidth)
	if len(lines) != 1 {
		t.Fatalf("NO_COLOR block should be the single chip row, got %d lines", len(lines))
	}
	if got := m.attachmentBlockLines(innerWidth); got != len(lines) {
		t.Fatalf("NO_COLOR attachmentBlockLines=%d but block rendered %d lines", got, len(lines))
	}
	if strings.ContainsAny(lines[0], "▘▝▀▖▌▞▛▗▚▐▜▄▙▟█") {
		t.Fatalf("NO_COLOR must not paint the preview, got %q", lines[0])
	}
}

// A staged image whose bytes cannot be decoded (e.g. webp) has no thumb, so it
// must contribute no preview row: the block is exactly the chip row and the
// height calc agrees. This is the same divergence class as the NO_COLOR bug.
func TestAttachmentBlockGeometryUndecodableImage(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{
		newImageAttachment("shot.webp", "image/webp", []byte("definitely not an image")),
	}

	const innerWidth = 60
	lines := m.attachmentBlock(innerWidth)
	if len(lines) != 1 {
		t.Fatalf("undecodable image should be the single chip row, got %d lines", len(lines))
	}
	if got := m.attachmentBlockLines(innerWidth); got != len(lines) {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", got, len(lines))
	}
}

// A document-only attachment (no image) carries no preview, so the block is one
// line — the [Doc #n] chip.
func TestAttachmentBlockGeometryDocOnly(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{{Label: "spec.pdf", DocText: "body"}}

	const innerWidth = 60
	lines := m.attachmentBlock(innerWidth)
	if len(lines) != 1 || !strings.Contains(lines[0], "[Doc #1]") {
		t.Fatalf("doc-only block should be the single [Doc #1] chip row, got %#v", lines)
	}
	if got := m.attachmentBlockLines(innerWidth); got != len(lines) {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", got, len(lines))
	}
}

// Only decodable images preview: an undecodable attachment ahead of a decodable
// one must not suppress the latter.
func TestAttachmentBlockPreviewsDecodableImageAfterUndecodable(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.pendingAttachments = []stagedAttachment{
		newImageAttachment("shot.webp", "image/webp", []byte("not an image")),
		newImageAttachment("photo.png", "image/png", encodeTestPNG(t, 80, 40, color.RGBA{R: 1, G: 2, B: 3, A: 255})),
	}

	const innerWidth = 60
	lines := m.attachmentBlock(innerWidth)
	if len(lines) != 1+previewTargetRows {
		t.Fatalf("the decodable image should still preview, got %d lines", len(lines))
	}
	if !strings.ContainsAny(lines[1], quadrantGlyphs) {
		t.Fatalf("second line should be the preview, got %q", lines[1])
	}
	if got := m.attachmentBlockLines(innerWidth); got != len(lines) {
		t.Fatalf("attachmentBlockLines=%d but block rendered %d lines", got, len(lines))
	}
}

// A user transcript row keeps its image previews, so a pasted image stays visible
// in history after it is sent.
func TestUserRowRendersStoredThumbs(t *testing.T) {
	thumb := &attachmentThumb{w: 8, h: 8, px: make([]rgb, 64)}
	row := transcriptRow{kind: rowUser, text: "look at this", thumbs: []*attachmentThumb{thumb}}
	out := renderUserRow(row, 80)
	// Previews are the only user-row content that sets 24-bit fg/bg SGR colours.
	if !strings.Contains(out, "\x1b[38;2;") || !strings.Contains(out, "\x1b[48;2;") {
		t.Fatalf("user row should render stored image previews, got %q", out)
	}
	if !strings.Contains(out, "look at this") {
		t.Fatalf("user row should still render its prompt text, got %q", out)
	}
	// A row without images renders no preview colour codes.
	plain := renderUserRow(transcriptRow{kind: rowUser, text: "just text"}, 80)
	if strings.Contains(plain, "\x1b[38;2;") {
		t.Fatalf("an image-less user row must not paint a preview, got %q", plain)
	}
}

// Encode/decode must round-trip a preview grid exactly, so a resumed session
// restores the same image, and a corrupt payload degrades to no preview.
func TestThumbEncodingRoundTrip(t *testing.T) {
	src := &attachmentThumb{w: 3, h: 2, px: []rgb{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 11, 12}, {13, 14, 15}, {16, 17, 18}}}
	encoded, ok := encodeThumb(src)
	if !ok {
		t.Fatal("a valid grid should encode")
	}
	got, ok := decodeThumb(encoded)
	if !ok {
		t.Fatal("an encoded grid should decode")
	}
	if got.w != src.w || got.h != src.h {
		t.Fatalf("dims %dx%d round-tripped to %dx%d", src.w, src.h, got.w, got.h)
	}
	for i := range src.px {
		if got.px[i] != src.px[i] {
			t.Fatalf("pixel %d: %#v != %#v", i, got.px[i], src.px[i])
		}
	}
	if _, ok := decodeThumb("not base64!!"); ok {
		t.Fatal("garbage must not decode into a thumb")
	}
	if _, ok := encodeThumb(nil); ok {
		t.Fatal("a nil grid must not encode")
	}
}

// A user message's persisted thumbnails restore onto the rehydrated row, so a
// resumed session shows the same images the live turn did.
func TestUserThumbRehydrationFromSessionEvent(t *testing.T) {
	thumb := &attachmentThumb{w: 2, h: 2, px: []rgb{{10, 20, 30}, {40, 50, 60}, {70, 80, 90}, {100, 110, 120}}}
	encoded, ok := encodeThumb(thumb)
	if !ok {
		t.Fatal("expected the grid to encode")
	}
	payload, err := json.Marshal(userMessageSessionPayload("see this", []string{encoded}))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	rows := transcriptRowsFromSessionEvents([]sessions.Event{{
		Type:    sessions.EventMessage,
		Payload: payload,
	}})
	if len(rows) != 1 {
		t.Fatalf("expected 1 rehydrated row, got %d", len(rows))
	}
	if len(rows[0].thumbs) != 1 {
		t.Fatalf("expected 1 restored preview, got %d", len(rows[0].thumbs))
	}
	got := rows[0].thumbs[0]
	if got.w != thumb.w || got.h != thumb.h || !reflect.DeepEqual(got.px, thumb.px) {
		t.Fatalf("restored grid %+v != original %+v", got, thumb)
	}
	// A malformed entry is skipped rather than failing the row.
	badPayload, _ := json.Marshal(userMessageSessionPayload("text only", []string{"not-base64!!"}))
	bad := transcriptRowsFromSessionEvents([]sessions.Event{{Type: sessions.EventMessage, Payload: badPayload}})
	if len(bad) != 1 || len(bad[0].thumbs) != 0 {
		t.Fatalf("a corrupt thumbnail should be skipped, got %#v", bad)
	}
}

// /export stays plain text but records how many images a user row carried,
// instead of dropping them silently.
func TestExportNotesUserRowImages(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.transcript = []transcriptRow{{
		kind:   rowUser,
		text:   "here",
		thumbs: []*attachmentThumb{{w: 2, h: 2, px: make([]rgb, 4)}},
	}}
	out := m.plainTranscriptText()
	if !strings.Contains(out, "you: here") || !strings.Contains(out, "[image #1]") {
		t.Fatalf("export should note the image, got %q", out)
	}
}

// The render fingerprint must distinguish two same-sized images with different
// pixels, or the render cache would serve a stale preview after an image change.
func TestThumbFingerprintDistinguishesPixels(t *testing.T) {
	a := &attachmentThumb{w: 2, h: 2, px: []rgb{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 11, 12}}}
	b := &attachmentThumb{w: 2, h: 2, px: []rgb{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}, {10, 11, 99}}}
	if thumbFingerprint(a) == thumbFingerprint(b) {
		t.Fatal("same-sized grids with different pixels must have distinct fingerprints")
	}
	if thumbFingerprint(a) != thumbFingerprint(a) {
		t.Fatal("fingerprint must be stable")
	}
	if thumbFingerprint(nil) != "" {
		t.Fatal("a nil grid must have an empty fingerprint")
	}
}

// layoutPreviews is the single source of the side-by-side geometry: previews keep
// staged order, share one row height, and never together exceed the width.
func TestLayoutPreviewsKeepsOrderAndFitsWidth(t *testing.T) {
	thumbs := []*attachmentThumb{
		{w: 80, h: 40, px: make([]rgb, 80*40)},
		{w: 40, h: 40, px: make([]rgb, 40*40)},
	}
	const innerWidth = 40
	out := layoutPreviews(thumbs, innerWidth)
	if len(out) != 2 {
		t.Fatalf("expected both previews, got %d", len(out))
	}
	// Staged order preserved: the wider 2:1 source first, the square second.
	if out[0].cols <= out[1].cols {
		t.Fatalf("first (wider) preview cols %d should exceed %d", out[0].cols, out[1].cols)
	}
	if out[0].rows != out[1].rows {
		t.Fatalf("side-by-side previews must share a row height: %d != %d", out[0].rows, out[1].rows)
	}
	if out[0].cols+previewGap+out[1].cols > innerWidth {
		t.Fatalf("previews overflow the width: %d > %d", out[0].cols+previewGap+out[1].cols, innerWidth)
	}
	// A wider composer gives each preview more room, so the band grows horizontally.
	wide := layoutPreviews(thumbs, 120)
	if wide[0].cols <= out[0].cols {
		t.Fatalf("a wider composer should widen the previews: %d <= %d", wide[0].cols, out[0].cols)
	}
	// Nothing to lay out → nothing drawn.
	if layoutPreviews(nil, innerWidth) != nil || layoutPreviews(thumbs, 0) != nil {
		t.Fatal("no thumbs or no width must lay out nothing")
	}
}
