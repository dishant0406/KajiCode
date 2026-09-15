package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Deleting an attachment token drops exactly that attachment and renumbers the
// survivors, so the token number always matches its position in the prompt.
func TestSyncAttachmentTokensDropsAndRenumbers(t *testing.T) {
	m := model{
		pendingAttachments: []stagedAttachment{
			newImageAttachment("a.png", "image/png", nil),
			newImageAttachment("b.png", "image/png", nil),
		},
		composer:       composerState{text: "see [Image #1] and [Image #2]", cursor: 0},
		composerActive: true,
	}
	m.composerPastePreviews = []composerPastePreview{
		{active: true, start: 4, end: 14, label: "[Image #1]", attachment: 1},
		{active: true, start: 19, end: 29, label: "[Image #2]", attachment: 2},
	}

	// Backspace with the cursor just past the first token deletes that token
	// whole, exactly as the composer key path does.
	state := composerState{text: "see [Image #1] and [Image #2]", cursor: 14}
	nextState, nextPreviews, ok := deleteComposerPastePreviewBefore(state, m.composerPastePreviews)
	if !ok {
		t.Fatal("expected the first token to be deletable as a whole")
	}
	m.setComposerState(nextState)
	m.composerPastePreviews = nextPreviews
	m.syncAttachmentTokens()

	if len(m.pendingAttachments) != 1 || m.pendingAttachments[0].Label != "b.png" {
		t.Fatalf("the deleted token should drop only a.png, got %#v", m.pendingAttachments)
	}
	if len(m.composerPastePreviews) != 1 || m.composerPastePreviews[0].attachment != 1 {
		t.Fatalf("surviving token should relink to index 1, got %#v", m.composerPastePreviews)
	}
	if got := m.currentComposerState().text; got != "see  and [Image #1]" {
		t.Fatalf("surviving token should renumber to [Image #1], got %q", got)
	}
}

func TestSyncAttachmentTokensNoTokensPrunesFromText(t *testing.T) {
	// No tracked previews (e.g. a queued submission): the text is the only
	// evidence, so only as many attachments as there are tokens survive.
	m := model{
		pendingAttachments: []stagedAttachment{
			newImageAttachment("a.png", "image/png", nil),
			newImageAttachment("b.png", "image/png", nil),
		},
		composer:       composerState{text: "only [Image #1] survives"},
		composerActive: true,
	}
	m.syncAttachmentTokens()
	if len(m.pendingAttachments) != 1 || m.pendingAttachments[0].Label != "a.png" {
		t.Fatalf("text with one token should keep one attachment, got %#v", m.pendingAttachments)
	}
}

func TestRebuildAttachmentTokensFromText(t *testing.T) {
	m := model{
		pendingAttachments: []stagedAttachment{
			newImageAttachment("a.png", "image/png", nil),
			{Label: "spec.pdf", DocText: "body"},
		},
		composer:       composerState{text: "see [Image #1] and [Doc #1]", cursor: 0},
		composerActive: true,
	}
	m.rebuildAttachmentTokensFromText()
	if len(m.composerPastePreviews) != 2 {
		t.Fatalf("both tokens should be rebuilt, got %#v", m.composerPastePreviews)
	}
	if m.composerPastePreviews[0].attachment != 1 || m.composerPastePreviews[1].attachment != 2 {
		t.Fatalf("tokens should relink in order, got %#v", m.composerPastePreviews)
	}
}

// Rebuilding from a token-less prompt must clear previews left over from the
// previous draft, otherwise their stale spans splice over the new text.
func TestRebuildAttachmentTokensFromTextClearsStalePreviews(t *testing.T) {
	m := model{
		pendingAttachments: []stagedAttachment{newImageAttachment("a.png", "image/png", nil)},
		composer:           composerState{text: "a recalled prompt with no tokens", cursor: 0},
		composerActive:     true,
		composerPastePreviews: []composerPastePreview{
			{active: true, start: 0, end: 5, label: "PASTE", attachment: 1},
		},
	}
	m.rebuildAttachmentTokensFromText()
	if len(m.composerPastePreviews) != 0 {
		t.Fatalf("token-less text must drop stale previews, got %#v", m.composerPastePreviews)
	}
	if len(m.pendingAttachments) != 0 {
		t.Fatalf("token-less text must drop unreferenced attachments, got %#v", m.pendingAttachments)
	}
}

// A prompt that references a non-first token by number must keep that attachment,
// not silently keep the first one by count.
func TestPruneAttachmentsToTextHonorsTokenNumber(t *testing.T) {
	pending := []stagedAttachment{
		newImageAttachment("a.png", "image/png", nil),
		newImageAttachment("b.png", "image/png", nil),
	}
	got := pruneAttachmentsToText("only [Image #2]", pending)
	if len(got) != 1 || got[0].Label != "b.png" {
		t.Fatalf("[Image #2] must keep the second image, got %#v", got)
	}
}

// A staged attachment with no live token must not ride along on a later submit:
// Esc/Ctrl-C clear the composer but keep pending attachments, so without pruning
// a subsequent attach would resurrect the orphan and silently send it.
func TestOrphanAttachmentDroppedOnNextAttach(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("a")))
	m.clearComposer() // Esc: attachments survive, token text is gone
	m = m.attachStaged(newImageAttachment("b.png", "image/png", []byte("b")))

	if len(m.pendingAttachments) != 1 || m.pendingAttachments[0].Label != "b.png" {
		t.Fatalf("only the newly-tokened attachment should survive, got %#v", m.pendingAttachments)
	}
	if got := m.currentComposerState().text; got != "[Image #1] " {
		t.Fatalf("the new attachment should be token #1, got %q", got)
	}
}

// Attaching identical content again must not stage a second copy: it would be
// sent twice and would give one attachment two tokens. The existing token moves
// to the cursor instead, and no new token is added.
func TestAttachStagedDeduplicatesIdenticalImage(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(newImageAttachment("clipboard", "image/png", []byte("same")))
	// Move the caret away, then re-attach the same bytes under a different label.
	m.setComposerState(composerState{text: m.currentComposerState().text, cursor: 0})
	m = m.attachStaged(newImageAttachment("Screenshot.png", "image/png", []byte("same")))

	if len(m.pendingAttachments) != 1 {
		t.Fatalf("identical content must stage one attachment, got %d", len(m.pendingAttachments))
	}
	if got := m.currentComposerState().text; got != "[Image #1] " {
		t.Fatalf("the single token should have moved to the cursor, got %q", got)
	}
	if len(m.composerPastePreviews) != 1 {
		t.Fatalf("exactly one token should remain, got %#v", m.composerPastePreviews)
	}
	// The original label survives (the first attach wins); a re-attach does not
	// rewrite the staged bytes or label.
	if m.pendingAttachments[0].Label != "clipboard" {
		t.Fatalf("re-attach must keep the existing attachment, got %q", m.pendingAttachments[0].Label)
	}
}

// Different bytes are different images and must both stage.
func TestAttachStagedKeepsDistinctImages(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("one")))
	m = m.attachStaged(newImageAttachment("b.png", "image/png", []byte("two")))
	if len(m.pendingAttachments) != 2 {
		t.Fatalf("distinct images must both stage, got %d", len(m.pendingAttachments))
	}
	if got := m.currentComposerState().text; got != "[Image #1] [Image #2] " {
		t.Fatalf("both tokens should be present, got %q", got)
	}
}

// The dedup applies to documents too, matching on extracted text.
func TestAttachStagedDeduplicatesIdenticalDocument(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(stagedAttachment{Label: "spec.pdf", DocText: "body"})
	m = m.attachStaged(stagedAttachment{Label: "copy.pdf", DocText: "body"})
	if len(m.pendingAttachments) != 1 {
		t.Fatalf("identical document text must stage one attachment, got %d", len(m.pendingAttachments))
	}
	if got := m.currentComposerState().text; got != "[Doc #1] " {
		t.Fatalf("the single doc token should be present, got %q", got)
	}
}

// A re-attached image that sits before another token must renumber so #N still
// matches text order after the move.
func TestAttachStagedDedupRenumbersAfterMove(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("a")))
	m = m.attachStaged(newImageAttachment("b.png", "image/png", []byte("b")))
	// Cursor back to the very start, then re-attach a.png: its token moves to the
	// front and b.png renumbers to #2.
	m.setComposerState(composerState{text: m.currentComposerState().text, cursor: 0})
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("a")))

	if len(m.pendingAttachments) != 2 {
		t.Fatalf("dedup must keep both distinct images, got %d", len(m.pendingAttachments))
	}
	if got := m.currentComposerState().text; got != "[Image #1] [Image #2] " {
		t.Fatalf("tokens should stay dense in text order, got %q", got)
	}
	// Token #1 must name the attachment whose bytes are "a".
	live := attachmentsForPrompt(m.currentComposerState().text, m.pendingAttachments)
	if len(live) != 2 || string(live[0].Image.Data) != "a" {
		t.Fatalf("token #1 should reference a.png after the move, got %#v", live)
	}
}

// Re-attaching identical content while the caret is elsewhere must move the token
// to the caret, not leave it where it was: the point of re-attaching is the token
// following the user. Regression: the delete parked the cursor at the old token's
// start, so the token was reinserted in place and nothing visible happened.
func TestAttachStagedDedupMovesTokenToCaret(t *testing.T) {
	m := model{composerActive: true}
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("a")))
	base := m.currentComposerState().text // "[Image #1] "
	m.setComposerState(composerState{text: base + "note", cursor: len([]rune(base + "note"))})
	m = m.attachStaged(newImageAttachment("a.png", "image/png", []byte("a")))

	if got := m.currentComposerState().text; got != "note[Image #1] " {
		t.Fatalf("token must relocate to the caret, got %q", got)
	}
	if len(m.pendingAttachments) != 1 {
		t.Fatalf("dedup must keep one attachment, got %d", len(m.pendingAttachments))
	}
	if len(m.composerPastePreviews) != 1 || m.composerPastePreviews[0].start != 4 {
		t.Fatalf("token preview must sit after 'note', got %#v", m.composerPastePreviews)
	}
}

// Rebuilding from text that references a later attachment (e.g. /edit of a prompt
// where an earlier attachment's token was deleted) must keep the referenced one.
// Regression: the rebuild pruned pendingAttachments before sync, so the preview
// index (into the pre-prune list) no longer matched and the attachment was dropped
// while its token stayed visible.
func TestRebuildAttachmentTokensKeepsReferencedLaterAttachment(t *testing.T) {
	m := model{
		pendingAttachments: []stagedAttachment{
			{Label: "spec.pdf", DocText: "body"},
			newImageAttachment("a.png", "image/png", []byte("a")),
		},
		composer:       composerState{text: "[Image #1] ", cursor: 0},
		composerActive: true,
	}
	m.rebuildAttachmentTokensFromText()

	if len(m.pendingAttachments) != 1 || m.pendingAttachments[0].Label != "a.png" {
		t.Fatalf("the referenced image must survive rebuild, got %#v", m.pendingAttachments)
	}
	if len(m.composerPastePreviews) != 1 || m.composerPastePreviews[0].attachment != 1 {
		t.Fatalf("preview must relink to the survivor, got %#v", m.composerPastePreviews)
	}
}

// A token-less prompt carries no attachments, so a leftover staged attachment is
// never attached to a prompt the user did not reference it in.
func TestAttachmentsForPromptRequiresToken(t *testing.T) {
	pending := []stagedAttachment{newImageAttachment("a.png", "image/png", nil)}
	if got := attachmentsForPrompt("just text", pending); len(got) != 0 {
		t.Fatalf("token-less prompt must carry no attachments, got %#v", got)
	}
	if got := attachmentsForPrompt("look [Image #1]", pending); len(got) != 1 {
		t.Fatalf("referenced attachment must be carried, got %#v", got)
	}
}

// Renumbering across the 9→10 digit boundary must shift later token offsets and
// the cursor, not assume every label keeps its width.
func TestRenumberAcrossDigitBoundary(t *testing.T) {
	var pending []stagedAttachment
	for i := 0; i < 11; i++ {
		pending = append(pending, newImageAttachment("img.png", "image/png", nil))
	}
	// Text holds tokens #1..#11 in order; previews point at concatenated spans.
	text := ""
	var previews []composerPastePreview
	for i := 1; i <= 11; i++ {
		label := fmt.Sprintf("[Image #%d]", i)
		start := len([]rune(text))
		text += label + " "
		previews = append(previews, composerPastePreview{
			active: true, start: start, end: start + len([]rune(label)),
			label: label, attachment: i,
		})
	}
	m := model{
		pendingAttachments:    pending,
		composer:              composerState{text: text, cursor: len([]rune(text))},
		composerActive:        true,
		composerPastePreviews: previews,
	}
	// Delete token #1: every survivor renumbers, and #10 → #9 shrinks by a rune.
	state := composerState{text: text, cursor: len([]rune("[Image #1]"))}
	nextState, nextPreviews, ok := deleteComposerPastePreviewBefore(state, previews)
	if !ok {
		t.Fatal("expected the first token to be deletable")
	}
	m.setComposerState(nextState)
	m.composerPastePreviews = nextPreviews
	m.syncAttachmentTokens()

	if len(m.pendingAttachments) != 10 {
		t.Fatalf("deleting one of eleven tokens should keep ten attachments, got %d", len(m.pendingAttachments))
	}
	// Every surviving token span must exactly cover its label in the final text.
	runes := []rune(m.currentComposerState().text)
	for _, p := range m.composerPastePreviews {
		got := string(runes[p.start:p.end])
		if got != p.label {
			t.Fatalf("token span %d:%d = %q, want label %q", p.start, p.end, got, p.label)
		}
	}
	if got := strings.TrimSpace(m.currentComposerState().text); !strings.HasPrefix(got, "[Image #1] [Image #2] ") || !strings.HasSuffix(got, "[Image #9] [Image #10]") {
		t.Fatalf("survivors should renumber densely, got %q", got)
	}
}
