# Attachment thumbnails (type-aware) — research + plan

Goal: replace the plain `[Image #1]` / `[Doc #1]` chip with a **type-aware
thumbnail row**:

- **images** → a real (live) preview of the image
- **PDF** → a PDF icon
- **other files** → a generic file icon
- everything stays inside the composer attachment area

Status: research done, plan only. No code changed yet.

---

## 1. What the terminal can actually do (this decides everything)

The user's terminal is **Kaji's bundled terminal ("termy")**, shipped as
`/Applications/Kaji.app/Contents/Frameworks/libtermy_ffi.dylib`.

Probed the dylib directly:

| Capability | Evidence | Result |
| --- | --- | --- |
| Kitty **graphics** protocol | `0` matches for `sixel`, `iTerm`, `a=T`, `f=100`, `transmit`, `image_id`, `z_index`, `placeholder` | ❌ not supported |
| Sixel | `0` matches | ❌ |
| iTerm2 inline images | `0` matches | ❌ |
| Kitty **keyboard** protocol | `KITTY_KEYBOARD_PROTOCOL`, `DISAMBIGUATE_ESC_CODES`, `REPORT_EVENT_TYPES` | ✅ supported |
| Truecolor | `truecolor`, `COLORTERM` | ✅ |
| Multiple fonts / glyphs | bundled `JetBrainsMonoNerdFontMono-*.ttf` | ✅ Nerd Font available |

**Conclusion:** termy is a **text-cell terminal**. It cannot display bitmap
graphics. A "live preview" of an image must therefore be rendered as **ANSI
cell art** (colored Unicode half-blocks). This is the only mechanism that
works in termy and in every other plain terminal.

## 2. Library research (Go, for terminal image previews)

| Library | Protocol / approach | Fit for KajiCode |
| --- | --- | --- |
| **`blacktop/go-termimg`** | Kitty / Sixel / iTerm2 / **Unicode half-block fallback**, terminal auto-detect | Best-in-class; but the graphics protocols are dead weight in termy. Only its half-block path is useful here. |
| **`BourgeoisBear/rasterm`** | Kitty / iTerm2 / Sixel encoders + `IsKittyCapable`/`IsItermCapable` | Same limitation; no ANSI fallback. |
| **`srlehn/termimg`** | cell-based placement across protocols | Experimental; API unstable. |
| **`mattn/go-sixel`** | Sixel only | termy has no Sixel. |
| **`eliukblau/pixterm`** | ANSI truecolor + half-block (CLI-first) | Approach matches, but it is a CLI, not a clean library API. |
| **Custom half-block** | ~120 LOC using stdlib `image` + `draw` | ✅ **Chosen.** Zero new deps, deterministic, works everywhere. |
| `charmbracelet/ultraviolet` `uv.Cell` | cell model with `Content` + style | Useful as the *lowering* primitive, already an indirect dep. |
| `charmbracelet/x/ansi` | SGR truecolor helpers | Already a direct dep. |

Charm's own Bubble Tea v2 announcement advertises "inline images", but the
v2 API surface has **no image view type**; the reusable primitive is the
`uv.Cell` model in `ultraviolet`. Neither exposes a ready-made
image→cells renderer, so a small local renderer is the right call.

**Decision:** implement a small, well-tested local renderer
(`internal/tui` or a new `internal/thumb`) that downscales the image with
`golang.org/x/image/draw` (CatmullRom) into a grid of cells and emits
half-blocks. Add `blacktop/go-termimg` **only if/when** we want a
graphics-protocol upgrade for Kitty/Ghostty/WezTerm users.

## 3. Proposed architecture

### 3.1 A typed attachment model (fixes a latent bug)

Today `pendingImageLabels []string` is a **parallel** slice to
`pendingImages`, and for a multi-page PDF the labels and images are **not
1:1** (`image_attach.go:214-217` appends one label per rasterized page). Keying
thumbnails on labels is therefore fragile.

Replace the parallel slices with one ordered model:

```go
// internal/tui/attachment.go (new)
type attachmentKind int
const (
    attachImage attachmentKind = iota // raster image
    attachDocument                    // PDF (text layer)
    attachFile                        // other file
)

type attachment struct {
    kind      attachmentKind
    label     string // basename / "clipboard"
    mediaType string // "image/png", ...
    data      []byte // image bytes (for the preview); nil for docs
    docText   string // extracted PDF text
}
```

`pendingAttachments []attachment` replaces `pendingImages`,
`pendingImageLabels`, and `pendingDocuments`. The submit path
(`launchPrompt`) expands `attachImage` → `Message.Images` and
`attachDocument` → prompt preamble, preserving current behavior. This removes
the label/image count skew as a class of bug.

### 3.2 Type classification

Reuse the existing single authority `internal/tools/file_media.go`
(`classifyFileKind`, `mediaMimeForPath`) rather than a new table. It already
maps `.png/.jpg/.jpeg/.webp/.gif/.bmp` → image and `.pdf` → PDF.

### 3.3 Rendering: three tiers

`renderAttachmentChips` (`image_attach.go:303`) is replaced by
`renderAttachments(...)` which emits a **chip row** plus, for images, a
**preview block**:

**Tier 1 — real image preview (ANSI half-blocks)**
- Decode with `image.Decode` (png/jpeg/gif/webp; webp needs
  `golang.org/x/image/webp`, one small new dep — or skip webp in v1).
- Downscale to the target cell box with `x/image/draw` (CatmullRom).
- Each output row = 2 image rows; emit `▀` with `fg` = top pixel,
  `bg` = bottom pixel (24-bit SGR). This doubles vertical resolution and is
  what pixterm/chafa use.
- Cell budget: e.g. `maxCols = min(innerWidth-6, 24)`, `maxRows = 6`;
  preserve aspect ratio (account for cells being ~2× taller than wide).

```
╭──────────────────────────────────╮
│ [Image #1] screenshot.png        │
│  ▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀    │   ← colored half-blocks = the image
│  ▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀▀▀    │
│ > _                              │
╰──────────────────────────────────╯
```

**Tier 2 — icons (when not an image)**
- PDF → `` (nf-fa-file_pdf / nf-md-file_pdf) — Nerd Font is bundled, so a
  Nerd Font glyph is safe; fall back to `PDF` text if the Nerd Font glyph
  width is unreliable.
- Generic file → `` / ``.
- Unknown → `•`.

**Tier 3 — graphics-protocol upgrade (optional / later)**
- Detect Kitty / iTerm2 / Sixel capability once at startup
  (`rasterm.IsKittyCapable()` etc., **not** Ghostty-specific).
- If supported, swap the half-block block for the real protocol
  (go-termimg). Keeps termy on tier 1 and gives Kitty/WezTerm/iTerm2 users
  pixel-perfect previews.
- Gate behind a config flag; default on only when detection is confident.

### 3.4 Placeholder fallback

When the terminal has **no truecolor** (`colorprofile` != TrueColor), a
half-block preview is meaningless. Fall back to a monochrome ASCII sketch or
straight to the icon chip. Detect via the existing
`tea.ColorProfileMsg` / `colorprofile` plumbing.

## 4. Layout / geometry changes (the fiddly part)

The composer hardcodes "chips = exactly 1 line" in three places. A variable
height preview block must update **all three together**:

| Site | File:line | Change |
| --- | --- | --- |
| box render | `internal/tui/model.go:4253` | render chip row + preview block |
| height calc | `internal/tui/composer_hit_rect.go:94` (`composerBoxLineCount`) | add `attachmentBlockLines(width)` instead of `+1` |
| mouse hit-test | `internal/tui/composer.go:280-283` | offset by the same block height |

Introduce one source of truth:

```go
func attachmentBlockHeight(width int) int // 0 if no attachments
```

and make all three call it. Cache the rendered preview per
`(mediaType,len,width)` so re-renders in the update loop stay cheap — the
Cursed Renderer diffs cells, but we should avoid re-decoding the image every
frame.

## 5. Where the code goes

| Concern | File |
| --- | --- |
| attachment model | `internal/tui/attachment.go` (new) |
| image → half-block cells | `internal/thumb/halfblock.go` (new) |
| icons / kind → glyph | `internal/tui/attachment_icon.go` (new) |
| render chip + preview | `renderAttachments` replacing `renderAttachmentChips` (`image_attach.go`) |
| geometry (3 call sites) | `model.go`, `composer_hit_rect.go`, `composer.go` |
| classification | reuse `internal/tools/file_media.go` |
| deps | `golang.org/x/image` (+ maybe `webp`); optional `blacktop/go-termimg` |

## 6. Testing

- `internal/thumb`: golden tests — decode a tiny PNG, assert the emitted
  half-block grid cell count and SGR structure; assert aspect ratio; assert
  1×1 and very wide/tall images don't panic.
- `internal/tui`: composer box line count matches `attachmentBlockHeight`;
  mouse hit-test lands on the input line with/without a preview; no-attachment
  and image-only and PDF-only and mixed cases; narrow-width clamps.
- Regression: submit still expands images into `Message.Images` and PDF text
  into the preamble; `/image clear`, backspace-remove, `/retry`, `/edit`
  still work against the unified model.
- Terminal gate: run under `TERM=xterm-256color` (no truecolor) to confirm
  the icon fallback.

## 7. Risks / open questions

1. **Cell aspect ratio** differs per font/terminal; a fixed 2:1 assumption is
   standard but previews will look slightly stretched. Consider reading
   `tea.PixelSize`/cell size if available (ultraviolet has `CellSizeEvent`).
2. **Nerd Font glyph width** — Nerd Fonts sometimes report width 1 while
   rendering 2 cells; must width-check with `x/ansi` and pad defensively.
3. **Decode cost** — a 10 MiB PNG decodes to a large `image.Image`; cap the
   decode with `image.DecodeConfig` first and downsample before
   `image.Decode` if possible.
4. **Security** — image bytes are untrusted. Decode via the stdlib only (no
   `unsafe`, no CGo), enforce `imageinput.MaxImageBytes`, and never shell out
   to a renderer. No new external process.
5. **Scope** — v1 (half-block + icons, termy-correct) is small and
   self-contained. The graphics-protocol tier is an optional follow-up and
   should ship behind a flag.

## 8. Recommended delivery slices

1. **Slice A** — unify attachments into `pendingAttachments` (no visual change,
   pure refactor + tests). Removes the label/image skew bug.
2. **Slice B** — icons for PDF / file / unknown + the 3-site geometry fix.
3. **Slice C** — half-block image preview + cache + fallback for no-truecolor.
4. **Slice D** (optional) — graphics-protocol upgrade tier behind a flag.

Slices A–C deliver exactly what was asked (real image preview, PDF icon, file
icon) and are fully correct in termy.
