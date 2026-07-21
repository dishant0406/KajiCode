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

func (m model) promptEditorOverlay(width int) string {
	if m.promptEditor == nil {
		return ""
	}
	overlayWidth := minInt(maxInt(36, width-8), 76)
	if width < 40 {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	editor := m.promptEditor
	lines := []string{
		kajicodeTheme.faint.Render("Create a reusable personal prompt snippet."),
		"",
		kajicodeTheme.faint.Render("Slug"),
		fitStyledLine(kajicodeTheme.userPrompt.Render("/ ")+kajicodeTheme.ink.Render(editor.slug+promptEditorCursor(editor.step == promptEditorSlug)), innerWidth),
	}
	if editor.step != promptEditorSlug {
		lines = append(lines, "", kajicodeTheme.faint.Render("Prompt"))
		body := editor.body.text
		if body == "" {
			body = promptEditorCursor(editor.step == promptEditorBody)
		} else if editor.step == promptEditorBody {
			body = insertPromptEditorCursor(editor.body)
		}
		for _, line := range strings.Split(body, "\n") {
			lines = append(lines, kajicodeTheme.ink.Render(truncateRunes(line, innerWidth)))
		}
	}
	if editor.step == promptEditorOverwrite {
		lines = append(lines, "", kajicodeTheme.amber.Render("This personal prompt already exists. Overwrite it? (y/n)"))
	}
	if editor.err != "" {
		lines = append(lines, "", kajicodeTheme.red.Render(editor.err))
	}
	footer := "Enter continue  •  Esc back/cancel"
	if editor.step == promptEditorBody {
		footer = "Enter newline  •  Ctrl+S save  •  Esc back"
	}
	lines = append(lines, "", kajicodeTheme.line.Render(strings.Repeat("─", innerWidth)), kajicodeTheme.faint.Render(footer))
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Save Prompt", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle()), width)
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
