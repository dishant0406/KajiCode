package tui

import (
	"runtime"

	tea "charm.land/bubbletea/v2"
)

// platform.go centralizes the chords whose *native* modifier differs on macOS,
// so labels shown to the user match what they'd press in a normal macOS app
// while raw key matching stays permissive enough to accept whatever the
// terminal actually forwards.
//
// Terminal caveat that shapes this design: a terminal generally does NOT
// forward the Cmd (Super) chord to the application at all. Ghostty, iTerm2, and
// Terminal.app intercept Cmd+V themselves and, for a paste, either inject a
// bracketed paste (text only) or — for an image-only clipboard — emit nothing.
// So `Super+V` can never be the sole paste path. The app therefore accepts every
// chord a terminal *can* forward, and docs/TERMINALS.md tells the user how to
// bind Cmd+V → Ctrl+V in their terminal for true native behavior.

// primaryModLabel is the modifier name a user on this platform expects for
// editing chords: "Cmd" on macOS, "Ctrl" elsewhere. Used to render help/hints.
func primaryModLabel() string {
	if runtime.GOOS == "darwin" {
		return "Cmd"
	}
	return "Ctrl"
}

// pasteChordLabel returns the label for the "paste from clipboard" chord shown
// in help/hints. On macOS this is Cmd+V (the chord the user presses; their
// terminal forwards it as Ctrl+V — see docs/TERMINALS.md); elsewhere Ctrl+V.
func pasteChordLabel() string { return primaryModLabel() + "+V" }

// copyChordLabel returns the label for copying transcript text. Copy is
// selection-driven (drag-select auto-copies) on every platform; the label names
// the modifier the user holds to extend a selection where the terminal supports
// it.
func copyChordLabel() string { return primaryModLabel() + "+C" }

// pasteGestureKey reports whether msg is an explicit request to paste from the
// OS clipboard. It matches every chord a terminal can forward:
//
//   - Ctrl+V (0x16) — the POSIX/readline paste chord; what Ghostty, kitty, and
//     most terminals send when Cmd+V is configured to forward, and what
//     Windows/Linux users press natively.
//   - Ctrl+Shift+V — the "paste as plain text" chord many Linux terminals use.
//   - Super+V — what the Kitty keyboard protocol reports for Cmd+V when the
//     terminal does forward the Super modifier through.
//
// Terminals that swallow the chord and instead emit a bracketed tea.PasteMsg
// never reach here; that path is handled separately. For an image-only
// clipboard neither path fires on most terminals, which is why the terminal
// binding docs exist.
func pasteGestureKey(msg tea.KeyMsg) bool {
	if keyIs(msg, 'v') && keyHasMod(msg, tea.ModSuper) {
		return true
	}
	if !keyCtrl(msg, 'v') {
		return false
	}
	// Ctrl+V with or without Shift (Shift = paste-as-plain-text). Ctrl+Alt+V is
	// left to whatever else claims Alt+V.
	return !keyHasMod(msg, tea.ModAlt)
}
