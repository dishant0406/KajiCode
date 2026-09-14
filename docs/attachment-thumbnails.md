# Attachment thumbnails (type-aware)

How the composer attachment area renders staged images, PDFs, and files. The
plain `[Image #1]` / `[Doc #1]` chip became a type-aware row: images get a real
quadrant-block thumbnail, PDFs a document icon, other files a generic icon.

## 1. What the terminal can do (this decides the design)

The interactive surface must work in **Kaji's bundled terminal ("termy")**, the
`libtermy_ffi.dylib` terminal compiled into `Kaji.app` (see `docs/TERMINALS.md`).
A stray `TERM_PROGRAM=ghostty` in the environment is **misleading** — it is
inherited from the outer shell, not termy (termy identifies itself as
`TERMY_TERM_PROGRAM=termy`).

Probed every Mach-O binary in `Kaji.app` for graphics-protocol markers. The only
sixel code in the whole bundle is `icy_sixel` inside an unrelated Node addon
(`Contents/Resources/native/pi_natives.*.node`); termy itself has none:

| Capability | Evidence | Result |
| --- | --- | --- |
| Kitty **graphics** protocol | `0` matches for `sixel`, `iTerm`, `1337;File`, `10EEEE`, `graphics protocol` | ❌ |
| Sixel | `0` matches in `libtermy_ffi.dylib` | ❌ |
| iTerm2 inline images | `0` matches | ❌ |
| Kitty **keyboard** protocol | `KITTY_KEYBOARD_PROTOCOL`, `DISAMBIGUATE_ESC_CODES` | ✅ |
| Truecolor | `truecolor`, `COLORTERM` | ✅ |
| Nerd Font glyphs | bundled `JetBrainsMonoNerdFontMono-*.ttf` | ✅ |

termy's FFI is a cell/damage model (`termy_frame_update`,
`termy_terminal_feed_output`): it parses text into cells and exposes no image
entry point. **Conclusion:** termy is a **text-cell terminal**. It cannot display
bitmaps, so a preview must be **ANSI cell art**. That is also the only mechanism
that renders identically in every other plain terminal, so it needs no capability
detection and no graphics-protocol tier.

Which cell art matters. The bundled font's `cmap` was read directly:

| Glyph set | Coverage |
| --- | --- |
| Half-blocks U+2580–259F | 32/32 |
| **Quadrants U+2596–259F** | **10/10** |
| Sextants U+1FB00–1FB3B | 0/60 |
| Octants U+1CD00–1CDE5 | 0/230 |
| Braille U+2800–28FF | 0/256 |

So the densest structure available is **quadrant blocks**: one cell = 2x2
sub-samples, four colours per cell instead of the two a lone `▀` carries. Sextant,
octant, and Braille renderers would be tofu, so they are not used.

## 2. Why no library and no new dependency

Terminal-image Go libraries (`blacktop/go-termimg`, `BourgeoisBear/rasterm`,
`mattn/go-sixel`, `eliukblau/pixterm`, `srlehn/termimg`, `ploMP4/chafa-go`) are
all centered on the graphics protocols termy lacks; only their cell-art fallback
would be used, and the quadrant renderer here is both simpler and denser than
what they fall back to. Bubble Tea v2 has no image view type.

`golang.org/x/image` is **not** in this repo's module graph or offline cache, so
`x/image/draw` is unavailable. The downscale is therefore a dozen lines of
stdlib `image` box filter (`boxDownscale`) — an area average in linear light,
which is exactly what a large downscale to a small preview needs — and it adds
nothing to `go.mod`.

## 3. Model: one typed attachment

`internal/tui/attachment.go` holds a single ordered model, replacing the old
parallel `pendingImages` / `pendingImageLabels` / `pendingDocuments` slices (and
their `last*` mirrors):

```go
type stagedAttachment struct {
    Label   string           // basename, or "clipboard"
    Image   *kajicoderuntime.ImageBlock
    Thumb   *attachmentThumb // decoded preview; nil when undecodable
    DocText string           // extracted PDF text layer
}
```

One value carries the label, the image bytes, and the PDF text layer, so the chip
row, the preview, the submit expansion, and the `/retry` snapshot cannot disagree
about what is staged. A PDF with rasterized pages contributes one image
attachment per page plus one document attachment, all in one ordered slice.

`m.pendingAttachments` is the staging queue; `m.lastAttachments` is the
`/retry` + `/edit` snapshot.

## 4. Rendering

`internal/tui/attachment_preview.go`:

- **Decode once, at attach time.** `buildAttachmentThumb` runs
  `image.DecodeConfig` first to reject ever-huge dimensions
  (`maxDecodeDim`, a decompression-bomb guard), then `image.Decode`, then
  `boxDownscale` into a grid capped at `96 x 64` samples (`maxPreviewCols*2` by
  `previewTargetRows*4`) — several samples per terminal cell, so the preview is
  a supersampled area average rather than one chunky sample per cell. Nothing
  decodes or resizes in the render loop, so no preview cache is needed.
- **Linear-light area averaging.** `boxDownscale` averages source pixels in
  linear light (squared values, via `linear`/`encodeSRGB`) rather than on
  gamma-encoded bytes, so a large downscale does not darken or muddy fine detail.
  `renderPreview` area-averages in the same style when the grid is finer than
  the preview.
- **Undecodable images degrade gracefully.** webp has no stdlib decoder, so its
  `Thumb` is nil; the chip still shows without a preview rather than refusing
  the attachment.
- **Quadrant blocks.** `renderPreview` (`internal/tui/attachment_quadrant.go`)
  paints each terminal cell from a 2x2 grid of samples as a single block glyph:
  the cell's two most distinct samples become the SGR foreground and background,
  each sample is assigned to whichever is nearer, and the resulting 4-bit mask
  picks the matching glyph from U+2580–259F. `previewDims` is the single source
  of the preview size: it fixes the height at `previewTargetRows` and derives the
  width from the source aspect. See §5 for the aspect math.
- **NO_COLOR.** A preview is 24-bit SGR, which the TUI strips under
  `NO_COLOR`; `attachmentPreviewEnabled` suppresses the preview there so raw
  escape codes never reach the screen (the icon chip is still shown).

Chip row (`renderAttachmentChips`): `▣ [Image #1] photo.png  ▤ [Doc #1] spec.pdf`.
Images and documents are numbered independently (`[Image #n]` / `[Doc #n]`), and
opaque files use `[File #n]`. Icons are plain geometric glyphs (`▣ ▤ ▢`), not
Nerd Font private-use codepoints, whose cell width is not guaranteed.

## 5. Geometry: one source of truth

**Aspect correction.** A preview is a bounded miniature whose proportions match
the source. This is the subtle part: a cell is about twice as tall as it is wide
and paints two sample rows, so one sample is roughly square on screen and the
on-screen height of `cols` columns is `cols * thumbH / (2 * thumbW)` rows. That
`/2` matches chafa's `font_ratio = 0.5` geometry (`chafa_calc_canvas_geometry`,
where `dest_aspect = (w/h) * font_ratio`). Omitting it renders every preview
twice as tall as the source — the bug this feature started with.

**Sizing for a small, detailed thumbnail.** The preview is deliberately *not* a
copy of the image at the composer width: its size is anchored to a fixed
**on-screen height** of `previewTargetRows` (8) rows — about **150 px**, the size
of a small profile picture — and the width follows the source aspect ratio. The
terminal here reports `font_size 15.26` / `line_height 1.2` in its own `termy.conf`
(`line_height` is a multiplier, so a cell is ~18 px tall), which puts 8 rows at
~150 px. The derived width is still capped at `maxPreviewCols` (48) so an
ultra-wide panorama cannot crowd out the input.

**Every staged image previews, in a horizontal queue.** `m.previewThumbs()`
returns one decoded grid per staged image, so pasting several images shows
several thumbnails. They are laid out **side by side in staged order** (a queue
growing to the right, joined by `previewGap` blank columns), never stacked: every
preview shares `previewTargetRows`, so the band has one constant height no matter
how many images are staged, and `layoutPreviews` splits the available width
evenly between them. When even minimum-width previews cannot all fit across the
composer, the first images that fit are drawn.

The composer previously hardcoded "chips = 1 line" in four places. All four now
call `m.attachmentBlockLines(innerWidth)` (chip row + preview rows):

| Site | File |
| --- | --- |
| box render | `internal/tui/model.go` (`composerBox`) |
| height calc | `internal/tui/composer_hit_rect.go` (`composerBoxLineCount`) |
| mouse hit-test | `internal/tui/composer.go` |
| wheel-target key | `internal/tui/mouse_wheel_target.go` (`pendingCount`) |

`attachmentBlockLines` and `attachmentBlock` both derive their preview size from
the single helper `m.attachmentPreviews(innerWidth)`, so the height calculation
and the render can never diverge. They are pinned together by
`TestAttachmentBlockGeometryMatchesRender`, with dedicated cases for the
`NO_COLOR` suppression, an undecodable image, a document-only attachment, several
staged images, and the many-image budget cap.

## 6. Testing

- `internal/tui/attachment_preview_test.go` — decode/downscale (solid colour,
  aspect ratio, never upscales, rejects garbage and empty bytes), quadrant cell
  structure and 2x2 pattern resolution, the height-anchored sizing and width cap,
  icons/labels, the geometry-matches-render pins (`NO_COLOR`, undecodable image,
  doc-only, multiple images, budget cap), the thumb encode/decode round-trip, and
  the user-row image rendering + rehydration + export.
- `internal/tui/image_attach_test.go` and friends — chip emission, attachment
  expansion at submit, `/image clear`, backspace-remove, `/retry`, `/edit`,
  vision gate and routing, and the new-session reset, all against the unified
  model.

## 7. Transcript history

A sent image stays visible: `launchPrompt` copies the staged previews onto the
new `rowUser` (`transcriptRow.thumbs`), and `renderUserRow`
(`internal/tui/rendering.go`) paints them under the prompt gutter. For resume, the
previews are stored on the same `EventMessage` payload (`userMessageSessionPayload`
→ `thumbnails`), compactly — the grid bytes deflated and base64-encoded
(`encodeThumb` / `decodeThumb` in `internal/tui/attachment_transcript.go`, capped
at `thumbHistoryLimit`). `transcriptRowsFromSessionEvents`
(`internal/tui/session.go`) restores them onto the rehydrated row. `thumbFingerprint`
folds the grid into the render fingerprint so the render cache invalidates, and
`plainTranscriptText` notes `[image #n]` in `/export` (which is plain text).

## 8. Deliberately out of scope

A graphics-protocol tier (Kitty/iTerm2/Sixel pixel-perfect previews) is not
implemented: termy cannot use it, it would need a new dependency plus runtime
capability detection, and the quadrant renderer already covers every terminal
KajiCode targets. A protocol tier would only ever light up outside Kaji.
