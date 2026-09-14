package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// stagedAttachment is one pending attachment for the next user turn. It keeps
// the three pieces the composer and the submit path need in ONE value — the
// display label, the image bytes (with their pre-decoded preview), and a PDF
// text layer — so the chip row, the live preview, the submit expansion, and the
// /retry snapshot can never disagree about which attachments exist.
//
// Exactly one of Image or DocText is set: a raster image carries Image, a PDF
// carries DocText (and optionally rasterized page Images, each its own
// attachment). A file the user could not otherwise attach still renders as a
// File chip via its Label.
type stagedAttachment struct {
	Label   string // short display name: basename, or "clipboard"
	Image   *kajicoderuntime.ImageBlock
	Thumb   *attachmentThumb // decoded preview for Image; nil when undecodable
	DocText string           // extracted PDF text layer
}

func (a stagedAttachment) isImage() bool { return a.Image != nil }
func (a stagedAttachment) isDoc() bool   { return a.DocText != "" }

// newImageAttachment wraps image bytes into a staged attachment and builds its
// preview thumb. A decode failure (e.g. webp, which the standard library cannot
// decode) leaves Thumb nil; the chip still shows, just without a live preview,
// so a webp screenshot is never silently refused.
func newImageAttachment(label string, mediaType string, data []byte) stagedAttachment {
	block := kajicoderuntime.ImageBlock{MediaType: mediaType, Data: data}
	att := stagedAttachment{Label: label, Image: &block}
	if thumb, ok := buildAttachmentThumb(block); ok {
		att.Thumb = thumb
	}
	return att
}

// turnImages returns the raw image blocks for the next provider request, in
// staged order. Documents and opaque files contribute none.
func (m model) turnImages() []kajicoderuntime.ImageBlock {
	var out []kajicoderuntime.ImageBlock
	for _, a := range m.pendingAttachments {
		if a.Image != nil {
			out = append(out, *a.Image)
		}
	}
	return out
}

// previewThumbs returns the decoded preview for every staged image, in staged
// order, skipping undecodable ones. Each staged image gets its own thumbnail, so
// pasting several images shows several previews rather than only the first.
func (m model) previewThumbs() []*attachmentThumb {
	var out []*attachmentThumb
	for _, a := range m.pendingAttachments {
		if a.Thumb != nil {
			out = append(out, a.Thumb)
		}
	}
	return out
}

// thumbHistoryLimit bounds how many previews a transcript row (and its persisted
// payload) carries, so a turn with many images cannot grow the history without
// bound. The composer still previews every staged image; history keeps the first
// few. It also bounds the payload the session writer serializes.
const thumbHistoryLimit = 4

// historyThumbs returns the staged previews to keep on the user's transcript row.
func (m model) historyThumbs() []*attachmentThumb {
	thumbs := m.previewThumbs()
	if len(thumbs) > thumbHistoryLimit {
		thumbs = thumbs[:thumbHistoryLimit]
	}
	return thumbs
}

// attachmentPreviews is the preview layout for the staged images at a given inner
// width: one sized preview per image, side by side in staged order. Nil when
// nothing should be drawn (nothing staged, nothing decodable, or previews
// disabled under NO_COLOR). The height calculation and the render both call it,
// so they can never disagree.
func (m model) attachmentPreviews(innerWidth int) []attachmentPreview {
	if !attachmentPreviewEnabled() {
		return nil
	}
	return layoutPreviews(m.previewThumbs(), innerWidth)
}

// attachmentPreviewRows is the number of preview lines the attachment block adds
// at the given inner width, or 0 when nothing is previewed. All previews share
// one row height (they are laid out side by side), so this is that height.
func (m model) attachmentPreviewRows(innerWidth int) int {
	previews := m.attachmentPreviews(innerWidth)
	if len(previews) == 0 {
		return 0
	}
	return previews[0].rows
}

// attachmentBlockLines is the number of composer lines the attachment block
// occupies at the given inner width: one chip row plus the preview rows. Zero
// when nothing is staged. The composer box render, the height calculation, and
// the mouse hit-test all call this so they can never drift.
func (m model) attachmentBlockLines(innerWidth int) int {
	if len(m.pendingAttachments) == 0 {
		return 0
	}
	return 1 + m.attachmentPreviewRows(innerWidth) // chip row + preview row
}

// attachmentBlock renders the attachment block as composer-ready lines: the chip
// row, then the previews as a horizontal queue (left to right, in staged order).
// An empty result means nothing is staged. Previews are 24-bit SGR quadrant
// blocks; under NO_COLOR (which strips color) attachmentPreviews reports no
// preview, so raw escape codes never reach the screen.
func (m model) attachmentBlock(innerWidth int) []string {
	if len(m.pendingAttachments) == 0 {
		return nil
	}
	lines := []string{kajicodeTheme.muted.Render(renderAttachmentChips(m.pendingAttachments))}
	return append(lines, renderPreviewRow(m.attachmentPreviews(innerWidth))...)
}

// renderAttachmentChips builds the pending-attachment row, one chip per staged
// attachment: "▣ [Image #1] photo.png  ▤ [Doc #1] spec.pdf". Images and
// documents are numbered independently, preserving the historical [Image #n] /
// [Doc #n] vocabulary. Returns "" when nothing is staged.
func renderAttachmentChips(items []stagedAttachment) string {
	if len(items) == 0 {
		return ""
	}
	chips := make([]string, 0, len(items))
	imgN, docN, fileN := 0, 0, 0
	for _, a := range items {
		var tag string
		switch {
		case a.isImage():
			imgN++
			tag = fmt.Sprintf("[Image #%d]", imgN)
		case a.isDoc():
			docN++
			tag = fmt.Sprintf("[Doc #%d]", docN)
		default:
			fileN++
			tag = fmt.Sprintf("[File #%d]", fileN)
		}
		chips = append(chips, attachmentIcon(a)+" "+tag+" "+shortAttachmentLabel(a.Label))
	}
	return strings.Join(chips, "  ")
}

// attachmentIcon returns the single-cell glyph for an attachment's chip, drawn
// from the plain geometric set the rest of the TUI already uses. No Nerd Font
// private-use codepoints are relied on, since their cell width is not guaranteed
// across terminals.
func attachmentIcon(a stagedAttachment) string {
	switch {
	case a.isImage():
		return "▣"
	case strings.EqualFold(filepath.Ext(a.Label), ".pdf"):
		return "▤"
	default:
		return "▢"
	}
}

// shortAttachmentLabel trims a label so a long screenshot name cannot push the
// chip row past the composer width.
func shortAttachmentLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "attachment"
	}
	const maxLabelRunes = 24
	if r := []rune(label); len(r) > maxLabelRunes {
		return string(r[:maxLabelRunes-1]) + "…"
	}
	return label
}
