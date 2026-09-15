package tui

import (
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// stagedAttachment is one pending attachment for the next user turn. It keeps
// the pieces the composer and the submit path need in ONE value — the display
// label, the image bytes (with their pre-decoded preview), and a PDF text layer
// — so the inline [Image #N] / [Doc #N] token, the live preview, the submit
// expansion, and the /retry snapshot can never disagree about which attachments
// exist.
//
// Exactly one of Image or DocText is set: a raster image carries Image, a PDF
// carries DocText (and optionally rasterized page Images, each its own
// attachment). An attachment is referenced from the prompt by an inline token
// (see attachment_token.go); deleting that token drops the attachment.
type stagedAttachment struct {
	Label   string // short display name: basename, or "clipboard"
	Image   *kajicoderuntime.ImageBlock
	Thumb   *attachmentThumb // decoded preview for Image; nil when undecodable
	DocText string           // extracted PDF text layer
}

func (a stagedAttachment) isImage() bool { return a.Image != nil }
func (a stagedAttachment) isDoc() bool   { return a.DocText != "" }

// newImageAttachment wraps image bytes into a staged attachment and builds its
// preview thumb. A decode failure (an image that exceeds the decode bounds, or a
// type the registered decoders cannot read) leaves Thumb nil; the token still
// shows, just without a live preview, so a screenshot is never silently refused.
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

// attachmentPreviewRows is the number of preview lines the attachment strip adds
// at the given inner width, or 0 when nothing is previewed. All previews share
// one row height (they are laid out side by side), so this is that height.
func (m model) attachmentPreviewRows(innerWidth int) int {
	previews := m.attachmentPreviews(innerWidth)
	if len(previews) == 0 {
		return 0
	}
	return previews[0].rows
}

// attachmentBlockLines is the number of composer lines the attachment preview
// strip occupies at the given inner width. The composer box render, the height
// calculation, and the mouse hit-test all call this so they can never drift.
func (m model) attachmentBlockLines(innerWidth int) int {
	return m.attachmentPreviewRows(innerWidth)
}

// attachmentBlock renders the attachment previews as composer-ready lines: a
// horizontal queue (left to right, in staged order). An empty result means
// nothing is previewed. The attachment names are no longer drawn here — each is
// carried inline in the prompt by its [Image #N] / [Doc #N] token. Previews are
// 24-bit SGR quadrant blocks; under NO_COLOR (which strips color)
// attachmentPreviews reports no preview, so raw escape codes never reach the
// screen.
func (m model) attachmentBlock(innerWidth int) []string {
	return renderPreviewRow(m.attachmentPreviews(innerWidth))
}
