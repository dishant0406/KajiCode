package tui

import "testing"

func TestRemoveLastAttachment(t *testing.T) {
	m := model{
		pendingAttachments: []stagedAttachment{
			newImageAttachment("a.png", "image/png", nil),
			newImageAttachment("b.png", "image/png", nil),
			{Label: "spec.pdf", DocText: "body"},
		},
	}

	// Removal pops the rightmost chip, so the document staged last goes first.
	m, ok := m.removeLastAttachment()
	if !ok || len(m.pendingAttachments) != 2 {
		t.Fatalf("doc should be removed first: ok=%v n=%d", ok, len(m.pendingAttachments))
	}
	// Then the last image.
	m, ok = m.removeLastAttachment()
	if !ok || len(m.pendingAttachments) != 1 || m.pendingAttachments[0].Label != "a.png" {
		t.Fatalf("last image should be removed: ok=%v attachments=%#v", ok, m.pendingAttachments)
	}
	// Remove the final image.
	m, ok = m.removeLastAttachment()
	if !ok || len(m.pendingAttachments) != 0 {
		t.Fatalf("all attachments should be removed: ok=%v n=%d", ok, len(m.pendingAttachments))
	}
	// Nothing left.
	if _, ok := m.removeLastAttachment(); ok {
		t.Fatal("removeLastAttachment on an empty set must report false")
	}
}
