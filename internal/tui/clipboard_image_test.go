package tui

import (
	"context"
	"testing"
)

func TestClipboardImageMsgAttachesImage(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4o"})
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG header
	// gpt-4o is vision-capable in the catalog, so the gate passes.
	updated := m.attachClipboardImage(pngData, "image/png")
	m2 := updated
	if len(m2.turnImages()) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(m2.turnImages()))
	}
	if m2.pendingAttachments[0].Image.MediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", m2.pendingAttachments[0].Image.MediaType)
	}
	if len(m2.pendingAttachments) != 1 || m2.pendingAttachments[0].Label != "clipboard" {
		t.Errorf("expected label 'clipboard', got %#v", m2.pendingAttachments)
	}
}

func TestClipboardImageVisionGateRefuses(t *testing.T) {
	// claude-haiku-3.5 is in the catalog and lacks ModelCapabilityVision.
	m := newModel(context.Background(), Options{ModelName: "claude-haiku-3.5"})
	pngData := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	updated := m.attachClipboardImage(pngData, "image/png")
	m2 := updated
	if len(m2.turnImages()) != 0 {
		t.Fatalf("expected 0 pending images on non-vision model, got %d", len(m2.turnImages()))
	}
}
