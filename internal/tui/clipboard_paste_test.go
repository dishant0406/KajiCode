package tui

import (
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestPasteGestureKey pins the chords treated as an explicit clipboard paste.
// Ctrl+V (the POSIX/kitty-standard paste chord) and Cmd+V (ModSuper, what macOS
// terminals that speak the Kitty keyboard protocol report) must both match, so a
// screenshot paste works regardless of how the terminal forwards the key.
func TestPasteGestureKey(t *testing.T) {
	ctrlV := testKeyCtrl('v')
	if !pasteGestureKey(ctrlV) {
		t.Error("Ctrl+V should be a paste gesture")
	}
	superV := testKeyPressMod('v', tea.ModSuper)
	if !pasteGestureKey(superV) {
		t.Error("Cmd+V (ModSuper) should be a paste gesture")
	}
	// Ctrl+Shift+V is paste-as-plain-text on Linux terminals.
	ctrlShiftV := testKeyCtrl('v')
	{
		k := ctrlShiftV.Key()
		k.Mod = tea.ModCtrl | tea.ModShift
		ctrlShiftV = tea.KeyPressMsg(k)
	}
	if !pasteGestureKey(ctrlShiftV) {
		t.Error("Ctrl+Shift+V should be a paste gesture")
	}
	// Ctrl+Alt+V is not a paste chord (Alt+V may be claimed elsewhere).
	altV := testKeyCtrl('v')
	{
		k := altV.Key()
		k.Mod = tea.ModCtrl | tea.ModAlt
		altV = tea.KeyPressMsg(k)
	}
	if pasteGestureKey(altV) {
		t.Error("Ctrl+Alt+V must not be a paste gesture")
	}
	// A plain "v" is ordinary typing, not a paste.
	if pasteGestureKey(testKey('v')) {
		t.Error("plain v must not be a paste gesture")
	}
	// Ctrl+C and Cmd+C are not paste.
	if pasteGestureKey(testKeyCtrl('c')) || pasteGestureKey(testKeyPressMod('c', tea.ModSuper)) {
		t.Error("Ctrl/Cmd+C must not be a paste gesture")
	}
}

// TestPlatformChordLabels pins the platform-aware labels users see: macOS shows
// Cmd, every other platform shows Ctrl.
func TestPlatformChordLabels(t *testing.T) {
	wantMod := "Ctrl"
	if runtime.GOOS == "darwin" {
		wantMod = "Cmd"
	}
	if got := primaryModLabel(); got != wantMod {
		t.Errorf("primaryModLabel() = %q, want %q", got, wantMod)
	}
	if got := pasteChordLabel(); got != wantMod+"+V" {
		t.Errorf("pasteChordLabel() = %q, want %q", got, wantMod+"+V")
	}
	if got := copyChordLabel(); got != wantMod+"+C" {
		t.Errorf("copyChordLabel() = %q, want %q", got, wantMod+"+C")
	}
}

// TestCtrlVDispatchesClipboardPaste pins that pressing the paste chord on the
// idle composer requests a clipboard read rather than typing a literal "v".
// This is the path that recovers image paste when the terminal emits no
// bracketed-paste event for a screenshot.
func TestCtrlVDispatchesClipboardPaste(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.terminalFocused = true

	_, cmd := m.Update(testKeyCtrl('v'))
	if cmd == nil {
		t.Fatal("Ctrl+V should dispatch a clipboard-read command")
	}
	// The command must be the paste reader, not a composer insert.
	if _, ok := cmd().(clipboardReadMsg); !ok {
		t.Fatalf("Ctrl+V produced %T, want clipboardReadMsg", cmd())
	}
}

// TestCtrlVPasteDoesNotTypeLiteralV guards the regression the gesture fixes:
// before wiring, Ctrl+V fell through to the composer and inserted a stray "v".
func TestCtrlVPasteDoesNotTypeLiteralV(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.terminalFocused = true

	updated, _ := m.Update(testKeyCtrl('v'))
	next := updated.(model)
	if got := next.composerValue(); got != "" {
		t.Fatalf("composer = %q, want empty (Ctrl+V must not type a literal v)", got)
	}
}

// TestCtrlVSwallowedByBlockingModal pins that the paste chord respects a modal:
// while a picker owns input, Ctrl+V must not hijack a clipboard read.
func TestCtrlVSwallowedByBlockingModal(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.terminalFocused = true
	m.picker = &commandPicker{kind: pickerModel}

	_, cmd := m.Update(testKeyCtrl('v'))
	if cmd != nil {
		if _, ok := cmd().(clipboardReadMsg); ok {
			t.Fatal("Ctrl+V must not paste while a modal picker is open")
		}
	}
}
