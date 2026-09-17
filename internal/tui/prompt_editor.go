package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/usercommands"
)

type promptEditorStep int

const (
	promptEditorSlug promptEditorStep = iota
	promptEditorBody
	promptEditorOverwrite
)

type promptEditorState struct {
	step promptEditorStep
	slug string
	body composerState
	err  string
}

func (m model) openPromptEditor() model {
	m.promptEditor = &promptEditorState{}
	m.clearSuggestions()
	return m
}

func (m model) handlePromptEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor := m.promptEditor
	if editor == nil {
		return m, nil
	}
	editor.err = ""
	if keyIs(msg, tea.KeyEsc) {
		if editor.step == promptEditorSlug {
			m.promptEditor = nil
		} else {
			editor.step = promptEditorSlug
		}
		return m, nil
	}
	if editor.step == promptEditorOverwrite {
		switch strings.ToLower(keyText(msg)) {
		case "y":
			return m.savePromptEditor(true)
		case "n":
			editor.step = promptEditorBody
		}
		return m, nil
	}
	if editor.step == promptEditorSlug {
		switch {
		case keyIs(msg, tea.KeyEnter):
			if err := m.validatePromptSlug(editor.slug); err != nil {
				editor.err = err.Error()
				return m, nil
			}
			editor.step = promptEditorBody
		case keyBackspace(msg):
			runes := []rune(editor.slug)
			if len(runes) > 0 {
				editor.slug = string(runes[:len(runes)-1])
			}
		case keyPrintable(msg):
			editor.slug += strings.ToLower(keyText(msg))
		}
		return m, nil
	}
	if keyCtrl(msg, 's') {
		return m.savePromptEditor(false)
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

func (m model) handlePromptEditorPaste(content string) model {
	if m.promptEditor == nil {
		return m
	}
	content = sanitizeComposerPaste(content)
	if m.promptEditor.step == promptEditorSlug {
		m.promptEditor.slug += strings.ToLower(strings.ReplaceAll(content, "\n", ""))
		return m
	}
	if m.promptEditor.step == promptEditorBody {
		m.promptEditor.body = insertComposerText(m.promptEditor.body, content)
	}
	return m
}

func (m model) validatePromptSlug(slug string) error {
	if err := usercommands.ValidateName(strings.TrimSpace(slug)); err != nil {
		return err
	}
	if _, ok := resolveCommand("/" + strings.TrimSpace(slug)); ok {
		return fmt.Errorf("/%s is reserved by a built-in command", strings.TrimSpace(slug))
	}
	return nil
}

func (m model) savePromptEditor(overwrite bool) (tea.Model, tea.Cmd) {
	editor := m.promptEditor
	if editor == nil {
		return m, nil
	}
	if err := m.validatePromptSlug(editor.slug); err != nil {
		editor.step = promptEditorSlug
		editor.err = err.Error()
		return m, nil
	}
	if strings.TrimSpace(editor.body.text) == "" {
		editor.err = "prompt is required"
		return m, nil
	}
	cmd, err := usercommands.Save(m.userCommandPaths.UserDir, editor.slug, editor.body.text, overwrite)
	if errors.Is(err, os.ErrExist) {
		editor.step = promptEditorOverwrite
		return m, nil
	}
	if err != nil {
		editor.err = err.Error()
		return m, nil
	}
	m.userCommands = usercommands.Load(m.userCommandPaths)
	m.promptEditor = nil
	m.homeNotice = "Saved prompt /" + cmd.Name + ". Type it to insert the prompt into the composer."
	return m, nil
}

func (m model) promptEditorOverlay(width int, maxHeight int) string {
	if m.promptEditor == nil {
		return ""
	}
	overlayWidth := minInt(maxInt(36, width-8), 76)
	if width < 40 {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	editor := m.promptEditor

	prefix := []string{
		kajicodeTheme.faint.Render("Create a reusable personal prompt snippet."),
		"",
		kajicodeTheme.faint.Render("Slug"),
		fitStyledLine(kajicodeTheme.userPrompt.Render("/ ")+kajicodeTheme.ink.Render(editor.slug+promptEditorCursor(editor.step == promptEditorSlug)), innerWidth),
	}
	var bodyLines []string
	cursorLine := 0
	if editor.step != promptEditorSlug {
		prefix = append(prefix, "", kajicodeTheme.faint.Render("Prompt"))
		body := editor.body.text
		if body == "" {
			bodyLines = []string{promptEditorCursor(editor.step == promptEditorBody)}
		} else {
			if editor.step == promptEditorBody {
				body = insertPromptEditorCursor(editor.body)
			}
			bodyLines = strings.Split(body, "\n")
		}
		cursorLine = composerCursorLine(editor.body)
	}
	suffix := []string{}
	if editor.step == promptEditorOverwrite {
		suffix = append(suffix, "", kajicodeTheme.amber.Render("This personal prompt already exists. Overwrite it? (y/n)"))
	}
	if editor.err != "" {
		suffix = append(suffix, "", kajicodeTheme.red.Render(editor.err))
	}
	footer := "Enter continue  •  Esc back/cancel"
	if editor.step == promptEditorBody {
		footer = "Enter newline  •  Ctrl+S save  •  Esc back"
	}
	suffix = append(suffix, "", kajicodeTheme.line.Render(strings.Repeat("─", innerWidth)), kajicodeTheme.faint.Render(footer))

	prefix, bodyLines, suffix = fitEditorOverlay(prefix, bodyLines, suffix, cursorLine, maxHeight)
	lines := make([]string, 0, len(prefix)+len(bodyLines)+len(suffix))
	lines = append(lines, prefix...)
	for _, line := range bodyLines {
		lines = append(lines, kajicodeTheme.ink.Render(truncateRunes(line, innerWidth)))
	}
	lines = append(lines, suffix...)
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Save Prompt", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle()), width)
}

// fitEditorOverlay fits a bordered modal's sections into maxHeight rows, always
// keeping the footer (the last suffix lines, ending in the save hint). It drops
// prefix lines first (the intro is least important), then windows the body
// around cursorLine, and, if the overlay still overflows on a very short
// terminal, trims the leading suffix rows (blank line + separator) so the save
// hint and bottom border survive. maxHeight <= 0 means the overlay is not
// clipped (the non-alt-screen path) so every line is rendered unchanged.
func fitEditorOverlay(prefix []string, body []string, suffix []string, cursorLine int, maxHeight int) ([]string, []string, []string) {
	if maxHeight <= 0 {
		return prefix, body, suffix
	}
	// Two rows are the block's top and bottom border lines.
	avail := maxInt(0, maxHeight-2)
	// Reserve the suffix and at least one body row, then fit the prefix into the
	// remainder — the intro/slug chrome is the least important content.
	prefixRoom := maxInt(0, avail-len(suffix)-1)
	if len(prefix) > prefixRoom {
		prefix = prefix[len(prefix)-prefixRoom:]
	}
	bodyBudget := avail - len(prefix) - len(suffix)
	// Trim the suffix's leading padding/separator rows so its final footer line
	// survives even when the terminal is too short for the full chrome.
	for bodyBudget < 0 && len(suffix) > 1 {
		suffix = suffix[1:]
		bodyBudget++
	}
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	return prefix, windowEditorLines(body, cursorLine, bodyBudget), suffix
}

// windowEditorLines returns the slice of body lines to render so the cursor's
// line stays visible and at most avail lines are shown. A body that already
// fits is returned unchanged; when no rows are available the body is dropped.
func windowEditorLines(body []string, cursorLine int, avail int) []string {
	if len(body) <= avail {
		return body
	}
	if avail <= 0 {
		return nil
	}
	cursorLine = clampInt(cursorLine, 0, len(body)-1)
	start := clampInt(cursorLine-avail+1, 0, len(body)-avail)
	return body[start : start+avail]
}

func promptEditorCursor(active bool) string {
	if active {
		return "│"
	}
	return ""
}

func insertPromptEditorCursor(state composerState) string {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	return string(runes[:state.cursor]) + "│" + string(runes[state.cursor:])
}
