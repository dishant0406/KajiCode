package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/fsutil"
)

// styleEditorState is the single-step modal behind "/style" (no argument): an
// editor for the operator's freeform, globally-persisted speaking style. Unlike
// the /prompt editor there is no slug step — the style is a fixed concept, so
// only a body is edited. On save, the body is written to the per-user
// RESPONSE_STYLE.md so it persists across sessions and projects and is injected
// into the system prompt every run (see agent.readUserStyle).
type styleEditorState struct {
	body composerState
	err  string
}

// styleEditorOpen asserts the TUI has a persisted-style target directory. It is
// the parent of the per-user config.json (i.e. ~/.config/kajicode), which is
// where agent.readUserStyle expects RESPONSE_STYLE.md.
func (m model) userStyleDir() string {
	if m.userConfigPath == "" {
		return ""
	}
	return filepath.Dir(m.userConfigPath)
}

// userStylePath returns the on-disk path of the persisted speaking style.
func (m model) userStylePath() string {
	dir := m.userStyleDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "RESPONSE_STYLE.md")
}

// openStyleEditor opens the style editor seeded from any persisted /style text,
// so the operator edits their existing style rather than retyping it.
func (m model) openStyleEditor() model {
	body := composerState{}
	if existing, err := os.ReadFile(m.userStylePath()); err == nil {
		body = composerState{text: strings.TrimSpace(string(existing)), cursor: len([]rune(strings.TrimSpace(string(existing))))}
	}
	m.styleEditor = &styleEditorState{body: body}
	m.clearSuggestions()
	return m
}

// handleStyleEditorKey routes keystrokes while the style editor is modal. Esc
// cancels (keeping any in-memory draft for the session only), Ctrl+S saves.
func (m model) handleStyleEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor := m.styleEditor
	if editor == nil {
		return m, nil
	}
	editor.err = ""
	if keyIs(msg, tea.KeyEsc) {
		m.styleEditor = nil
		return m, nil
	}
	if keyCtrl(msg, 's') {
		return m.saveStyleEditor()
	}
	state := editor.body
	switch {
	case keyIs(msg, tea.KeyEnter):
		state = insertComposerText(state, "\n")
	case keyBackspace(msg):
		state = deleteComposerRange(state, state.cursor-1, state.cursor)
	case keyIs(msg, tea.KeyLeft):
		state.cursor--
	case keyIs(msg, tea.KeyRight):
		state.cursor++
	case keyIs(msg, tea.KeyUp):
		state.cursor = composerLineStart(state)
	case keyIs(msg, tea.KeyDown):
		state.cursor = composerLineEnd(state)
	case keyIs(msg, tea.KeyHome) || keyCtrl(msg, 'a'):
		state.cursor = composerLineStart(state)
	case keyIs(msg, tea.KeyEnd) || keyCtrl(msg, 'e'):
		state.cursor = composerLineEnd(state)
	case keyCtrl(msg, 'w'):
		state = deleteComposerWordBefore(state)
	case keyPrintable(msg):
		state = insertComposerText(state, keyText(msg))
	}
	editor.body = normalizeComposerState(state)
	return m, nil
}

// handleStyleEditorPaste inserts clipboard content at the cursor, modal to the
// style editor (never leaking into the underlying composer).
func (m model) handleStyleEditorPaste(content string) model {
	if m.styleEditor == nil {
		return m
	}
	m.styleEditor.body = insertComposerText(m.styleEditor.body, sanitizeComposerPaste(content))
	return m
}

// saveStyleEditor writes the edited body to the per-user RESPONSE_STYLE.md. An
// empty body clears the persisted style (the equivalent of /style clear).
func (m model) saveStyleEditor() (tea.Model, tea.Cmd) {
	editor := m.styleEditor
	if editor == nil {
		return m, nil
	}
	dir := m.userStyleDir()
	if dir == "" {
		editor.err = "user config directory is not configured"
		return m, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		editor.err = err.Error()
		return m, nil
	}
	text := strings.TrimSpace(editor.body.text)
	if text == "" {
		// Clearing the persisted style is legitimate (a blank response style).
		if err := os.Remove(m.userStylePath()); err != nil && !os.IsNotExist(err) {
			editor.err = err.Error()
			return m, nil
		}
	} else {
		tmp, err := os.CreateTemp(dir, ".response-style-*.tmp")
		if err != nil {
			editor.err = err.Error()
			return m, nil
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			editor.err = err.Error()
			return m, nil
		}
		if _, err := tmp.WriteString(text + "\n"); err != nil {
			_ = tmp.Close()
			editor.err = err.Error()
			return m, nil
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			editor.err = err.Error()
			return m, nil
		}
		if err := tmp.Close(); err != nil {
			editor.err = err.Error()
			return m, nil
		}
		if err := fsutil.RenameWithRetry(tmpPath, m.userStylePath(), nil); err != nil {
			editor.err = err.Error()
			return m, nil
		}
	}
	m.styleEditor = nil
	m.homeNotice = "Saved speaking style. It now applies to every reply."
	return m, nil
}

// readPersistedStyle returns the raw contents of a persisted style file, or ""
// when the file is missing/empty. Used by /style show to display the current
// on-disk global style.
func readPersistedStyle(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// styleEditorOverlay renders the style editor as a modal overlay.
func (m model) styleEditorOverlay(width int) string {
	if m.styleEditor == nil {
		return ""
	}
	overlayWidth := minInt(maxInt(36, width-8), 76)
	if width < 40 {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	editor := m.styleEditor
	lines := []string{
		kajicodeTheme.faint.Render("Your speaking style, applied to every reply and persisted globally."),
		"",
		kajicodeTheme.faint.Render("Style"),
	}
	body := editor.body.text
	if body == "" {
		body = promptEditorCursor(true)
	} else {
		body = insertPromptEditorCursor(editor.body)
	}
	for _, line := range strings.Split(body, "\n") {
		lines = append(lines, kajicodeTheme.ink.Render(truncateRunes(line, innerWidth)))
	}
	if editor.err != "" {
		lines = append(lines, "", kajicodeTheme.red.Render(editor.err))
	}
	lines = append(lines, "", kajicodeTheme.line.Render(strings.Repeat("─", innerWidth)), kajicodeTheme.faint.Render("Enter newline  •  Ctrl+S save  •  Esc cancel"))
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Speaking Style", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle()), width)
}

// clearStyleEditor removes the persisted global speaking style.
func (m model) clearStyleEditor() (model, string) {
	path := m.userStylePath()
	if path == "" {
		return m, "Style\nUser config directory is not configured."
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return m, "Style\n" + err.Error()
	}
	m.responseStyle = ""
	return m, "Style\nCleared the global speaking style. Replies now use the built-in default."
}
