package tui

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// Images and documents are referenced in the prompt by an inline token inserted
// at the cursor when they are attached, e.g. "compare [Image #1] with [Image #2]".
// The token is a literal part of the composer text, so its position is exactly
// where the user put it and it survives submit into the model-facing prompt.
//
// A token is tracked as a composerPastePreview whose attachment field links it to
// a staged attachment, so arrow keys step over it as one unit and Backspace
// deletes it whole. Because the token span text equals its own label, deleting a
// token deletes the visible token and the attachment together.

// attachmentTokenPattern matches a token this feature inserts, capturing the
// kind (Image/Doc) and the number. It is also used to recover tokens when the
// composer previews are unavailable (e.g. after /edit restores a previous prompt
// into the composer).
var attachmentTokenPattern = regexp.MustCompile(`\[(Image|Doc) #(\d+)\]`)

// attachStaged appends a staged attachment and inserts its inline token at the
// cursor, so attaching always leaves a visible, deletable reference in the
// prompt. Staged attachments with no live token (e.g. left over from a composer
// clear) are reconciled away first: they are unreferenced, so keeping them would
// both silently send them and skew the new token's number.
//
// Attaching content that is already staged (re-pasting the same screenshot) does
// not add a second copy — it would be sent twice and would give one attachment
// two tokens. The existing token is moved to the cursor instead.
func (m model) attachStaged(a stagedAttachment) model {
	// Reconcile first (single source of truth for the pending/preview invariant):
	// this drops staged attachments with no live token — e.g. left over from a
	// composer clear — and keeps the previews linked to the pending slice. A raw
	// prune here would shift the slice under previews that still index it.
	m.syncAttachmentTokens()
	if attachmentIndexFor(m.pendingAttachments, a) >= 0 {
		return m.moveAttachmentTokenToCursor(a)
	}
	index := len(m.pendingAttachments)
	m.pendingAttachments = append(m.pendingAttachments, a)
	m.insertAttachmentToken(attachmentLabelAt(m.pendingAttachments, index), index)
	return m
}

// moveAttachmentTokenToCursor relocates the inline token of an already-staged
// attachment to the composer cursor, leaving exactly one token for it. Used when
// identical content is attached again: the token (the visible confirmation of the
// attach) follows the user instead of a duplicate copy being staged.
func (m model) moveAttachmentTokenToCursor(a stagedAttachment) model {
	index := attachmentIndexFor(m.pendingAttachments, a)
	if index < 0 {
		return m
	}
	state := normalizeComposerState(m.currentComposerState())
	runes := []rune(state.text)
	cursor := state.cursor
	for _, p := range validComposerPastePreviews(state, m.composerPastePreviews) {
		if p.attachment != index+1 {
			continue
		}
		// Absorb the separator space the token was inserted with, so moving it does
		// not leave a stray gap where the old token was.
		end := p.end
		if end < len(runes) && runes[end] == ' ' {
			end++
		}
		next, previews := deleteComposerRangeWithPastePreviews(state, m.composerPastePreviews, p.start, end)
		// deleteComposerRange parks the cursor at the deleted span's start; restore
		// the user's caret (shifted by the removed span) so the reinserted token
		// lands where the user is, not where the old token was.
		if cursor >= end {
			cursor -= end - p.start
		} else if cursor > p.start {
			cursor = p.start
		}
		next.cursor = clamp(cursor, 0, len([]rune(next.text)))
		m.setComposerState(next)
		m.composerPastePreviews = previews
		break
	}
	m.insertAttachmentToken(attachmentLabelAt(m.pendingAttachments, index), index)
	// Moving a token can reorder it among the others, so renumber so the visible
	// #N always matches text order.
	m.syncAttachmentTokens()
	return m
}

// attachmentIndexFor returns the index of the staged attachment holding the same
// content as a, or -1. Images match on media type and bytes; documents on text.
// Labels are intentionally ignored: the same screenshot attached as "clipboard"
// and as "Screenshot 2026.png" is one image, not two.
func attachmentIndexFor(pending []stagedAttachment, a stagedAttachment) int {
	for i, existing := range pending {
		switch {
		case a.isImage() && existing.isImage():
			if existing.Image.MediaType == a.Image.MediaType && bytes.Equal(existing.Image.Data, a.Image.Data) {
				return i
			}
		case a.isDoc() && existing.isDoc():
			if existing.DocText == a.DocText {
				return i
			}
		}
	}
	return -1
}

// attachmentLabelAt is the inline token for the attachment at index: the n-th
// image/document among the staged attachments, in order.
func attachmentLabelAt(items []stagedAttachment, index int) string {
	if index < 0 || index >= len(items) {
		return ""
	}
	a := items[index]
	n := 0
	for i := 0; i <= index; i++ {
		if items[i].isImage() == a.isImage() && items[i].isDoc() == a.isDoc() {
			n++
		}
	}
	switch {
	case a.isImage():
		return fmt.Sprintf("[Image #%d]", n)
	case a.isDoc():
		return fmt.Sprintf("[Doc #%d]", n)
	default:
		return ""
	}
}

// insertAttachmentToken inserts label at the cursor as an atomic token linked to
// the staged attachment at index (0-based). A trailing space keeps the caret
// clear of the token so the user can keep typing. No-op when label is empty.
func (m *model) insertAttachmentToken(label string, index int) {
	if label == "" {
		return
	}
	state := normalizeComposerState(m.currentComposerState())
	insert := label + " "
	start := state.cursor
	labelLen := len([]rune(label))
	next := composerPastePreviewsAfterInsert(m.composerPastePreviews, start, len([]rune(insert)))
	m.setComposerState(insertComposerText(state, insert))
	next = append(next, composerPastePreview{
		active:     true,
		start:      start,
		end:        start + labelLen,
		label:      label,
		attachment: index + 1,
	})
	sortComposerPastePreviews(next)
	m.composerPastePreviews = next
}

// stagedAttachmentFor returns the staged attachment a preview links to, or false
// for a plain text paste / a stale link.
func stagedAttachmentFor(pending []stagedAttachment, preview composerPastePreview) (stagedAttachment, bool) {
	if preview.attachment <= 0 || preview.attachment > len(pending) {
		return stagedAttachment{}, false
	}
	return pending[preview.attachment-1], true
}

// composerPastePreviewsWithoutAttachments drops the attachment tokens (leaving
// plain text-paste previews) when the staged attachments are cleared, e.g.
// /image clear. The visible token text stays in the composer, orphaned, so the
// user can delete it as ordinary text.
func composerPastePreviewsWithoutAttachments(previews []composerPastePreview) []composerPastePreview {
	kept := make([]composerPastePreview, 0, len(previews))
	for _, p := range previews {
		if p.attachment > 0 {
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// documentPreamble formats document text as a model-facing preamble, naming each
// so the model can attribute the text. Empty when there are no documents.
func documentPreamble(docs []stagedAttachment) string {
	var b strings.Builder
	for _, a := range docs {
		b.WriteString("Attached document: ")
		b.WriteString(a.Label)
		b.WriteString("\n")
		b.WriteString(a.DocText)
		b.WriteString("\n\n")
	}
	return b.String()
}

// historyThumbsFor returns the previews to keep on a transcript row for the
// given attachments, honoring the history preview cap.
func historyThumbsFor(attachments []stagedAttachment) []*attachmentThumb {
	var thumbs []*attachmentThumb
	for _, a := range attachments {
		if a.Thumb != nil {
			thumbs = append(thumbs, a.Thumb)
		}
	}
	if len(thumbs) > thumbHistoryLimit {
		thumbs = thumbs[:thumbHistoryLimit]
	}
	return thumbs
}

// syncAttachmentTokens reconciles the staged attachments with the tokens still
// present in the composer after an edit: an attachment whose token was deleted is
// dropped, the survivors are renumbered so the user always sees #1, #2, … in text
// order, and each preview is relinked to its renumbered position. A token can
// change width across the digit boundary (a former "#10" becomes "#9"), so the
// surviving spans and the cursor are shifted to match. When the composer holds no
// attachment tokens the text is the only evidence, so attachments whose token the
// user deleted are pruned from it.
func (m *model) syncAttachmentTokens() {
	state := m.currentComposerState()
	previews := validComposerPastePreviews(state, m.composerPastePreviews)
	hasToken := false
	for _, p := range previews {
		if p.attachment > 0 {
			hasToken = true
			break
		}
	}
	if !hasToken {
		// No tracked tokens (e.g. a prompt restored by /edit, or text left after a
		// delete that removed the last token): prune from the text itself.
		m.pendingAttachments = pruneAttachmentsToText(state.text, m.pendingAttachments)
		return
	}

	kept := make([]composerPastePreview, 0, len(previews))
	live := make([]stagedAttachment, 0, len(previews))
	imageN, docN := 0, 0
	for _, p := range previews {
		attachment, ok := stagedAttachmentFor(m.pendingAttachments, p)
		if !ok {
			kept = append(kept, p)
			continue
		}
		switch {
		case attachment.isImage():
			imageN++
			p.label = fmt.Sprintf("[Image #%d]", imageN)
		case attachment.isDoc():
			docN++
			p.label = fmt.Sprintf("[Doc #%d]", docN)
		default:
			continue
		}
		p.attachment = len(live) + 1
		live = append(live, attachment)
		kept = append(kept, p)
	}
	m.pendingAttachments = live
	text, adjusted, cursor := renumberAttachmentTokens(state.text, kept, state.cursor)
	m.composerPastePreviews = adjusted
	m.setComposerState(composerState{text: text, cursor: cursor})
}

// renumberAttachmentTokens rewrites each attachment token span to its current
// label and returns the new text with every preview's offsets and the cursor
// shifted to match. A label can change length across the digit boundary (a token
// that was "#10" renumbers to "#9"), so later offsets and the cursor move by the
// accumulated delta rather than staying put. Plain text-paste spans keep their
// text but still shift with the tokens before them.
func renumberAttachmentTokens(text string, previews []composerPastePreview, cursor int) (string, []composerPastePreview, int) {
	if len(previews) == 0 {
		return text, previews, cursor
	}
	runes := []rune(text)
	var b strings.Builder
	origPos := 0
	shift := 0
	cursorShift := 0
	out := make([]composerPastePreview, 0, len(previews))
	for _, p := range previews {
		if p.start < origPos || p.end > len(runes) || p.start >= p.end {
			out = append(out, p)
			continue
		}
		origStart, origEnd := p.start, p.end
		b.WriteString(string(runes[origPos:origStart]))
		label := p.label
		if p.attachment == 0 {
			label = string(runes[origStart:origEnd])
		}
		delta := len([]rune(label)) - (origEnd - origStart)
		p.start = origStart + shift
		p.end = p.start + len([]rune(label))
		b.WriteString(label)
		if origEnd <= cursor {
			cursorShift += delta
		}
		shift += delta
		origPos = origEnd
		out = append(out, p)
	}
	if origPos < len(runes) {
		b.WriteString(string(runes[origPos:]))
	}
	newText := b.String()
	return newText, out, clamp(cursor+cursorShift, 0, len([]rune(newText)))
}

// rebuildAttachmentTokensFromText re-derives the attachment token previews from
// the composer text and links them, in order, to the staged attachments. Used
// when a prompt carrying tokens is restored into the composer (e.g. /edit or a
// popped queued message), so the tokens become atomic and backspace-deletable
// again and later edits keep the attachments in lockstep. Tokens are matched to
// staged attachments of the same kind, in order; extra attachments with no token
// are dropped. Stale text-paste previews are replaced, since the restored text is
// not the text they were tracking.
func (m *model) rebuildAttachmentTokensFromText() {
	text := m.currentComposerState().text
	matches := attachmentTokenPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		// The recalled text carries no tokens, so any previews still held belong to
		// the previous draft and would splice stale spans over the new text. Drop
		// them and prune the attachments from the text alone.
		m.composerPastePreviews = nil
		m.pendingAttachments = pruneAttachmentsToText(text, m.pendingAttachments)
		return
	}
	var images, docs []int
	for i, a := range m.pendingAttachments {
		switch {
		case a.isImage():
			images = append(images, i)
		case a.isDoc():
			docs = append(docs, i)
		}
	}
	previews := make([]composerPastePreview, 0, len(matches))
	imageN, docN := 0, 0
	for _, match := range matches {
		kind := text[match[2]:match[3]]
		var attachIndex int
		switch {
		case kind == "Image" && imageN < len(images):
			attachIndex, imageN = images[imageN], imageN+1
		case kind == "Doc" && docN < len(docs):
			attachIndex, docN = docs[docN], docN+1
		default:
			continue
		}
		start := len([]rune(text[:match[0]]))
		end := len([]rune(text[:match[1]]))
		previews = append(previews, composerPastePreview{
			active:     true,
			start:      start,
			end:        end,
			label:      string([]rune(text)[start:end]),
			attachment: attachIndex + 1,
		})
	}
	if len(previews) == 0 {
		return
	}
	m.composerPastePreviews = previews
	// syncAttachmentTokens relinks and renumbers, and rebuilds pendingAttachments
	// from the previews that actually resolved, so an attachment whose token the
	// text lacks is dropped. Pruning here first would invalidate the preview
	// indices (they point into the pre-sync list) and drop a referenced attachment.
	m.syncAttachmentTokens()
}

// pruneAttachmentsToText drops staged attachments whose token is missing from
// text. Tokens are numbered densely by kind, so `[Image #N]` names the N-th
// staged image and `[Doc #N]` the N-th staged document; an attachment with no
// matching token is dropped rather than silently sent. Used when the composer
// previews are gone and the text is the only evidence of which attachments
// survive (submit of a queued/`/retry` prompt, or a restored `/edit` draft).
func pruneAttachmentsToText(text string, pending []stagedAttachment) []stagedAttachment {
	if len(pending) == 0 {
		return pending
	}
	images, docs := referencedAttachmentNumbers(text)
	kept := make([]stagedAttachment, 0, len(pending))
	imageN, docN := 0, 0
	for _, a := range pending {
		switch {
		case a.isImage():
			imageN++
			if images[imageN] {
				kept = append(kept, a)
			}
		case a.isDoc():
			docN++
			if docs[docN] {
				kept = append(kept, a)
			}
		default:
			kept = append(kept, a)
		}
	}
	return kept
}

// referencedAttachmentNumbers returns the set of [Image #N] and [Doc #N] token
// numbers that appear in text, so pruning can keep exactly the attachments those
// tokens name rather than assuming the survivors are the first ones staged.
func referencedAttachmentNumbers(text string) (images, docs map[int]bool) {
	images, docs = map[int]bool{}, map[int]bool{}
	for _, match := range attachmentTokenPattern.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		if match[1] == "Image" {
			images[n] = true
		} else {
			docs[n] = true
		}
	}
	return images, docs
}

// attachmentContext is the attachment state a single turn needs: the images to
// send and the documents whose text becomes the prompt preamble.
type attachmentContext struct {
	images []kajicoderuntime.ImageBlock
	docs   []stagedAttachment
}

// attachmentContextFor resolves the attachments referenced by a turn.
func attachmentContextFor(attachments []stagedAttachment) attachmentContext {
	var ctx attachmentContext
	for _, a := range attachments {
		switch {
		case a.isImage():
			ctx.images = append(ctx.images, *a.Image)
		case a.isDoc():
			ctx.docs = append(ctx.docs, a)
		}
	}
	return ctx
}

// attachmentsForPrompt resolves the attachments a submission carries from the
// prompt text about to be sent. An attachment is referenced by its inline token,
// so only the attachments whose token survives in the text are sent — a staged
// attachment with no token (e.g. orphaned by a composer clear) is dropped rather
// than silently attached to a prompt the user never associated it with.
func attachmentsForPrompt(text string, pending []stagedAttachment) []stagedAttachment {
	return pruneAttachmentsToText(text, pending)
}
