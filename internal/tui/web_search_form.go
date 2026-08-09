package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// webSearchForm is the /web-search setup modal. Reusing the API-key prompt's
// modal idiom (masked input, Enter submit, Esc cancel), it walks the user
// through: picking a web-search provider, optionally adjusting its base URL,
// entering an API key, then persisting both to the shell rc and the env-fallback
// file (and making them live via os.Setenv). /web-search status and remove are
// handled by handleWebSearchCommand, not this form.

type webSearchFormStep int

const (
	webSearchStepProvider webSearchFormStep = iota
	webSearchStepKey
)

type webSearchFormState struct {
	step      webSearchFormStep
	providers []tools.WebSearchProvider
	selected  int // index into providers
	// Which field (base URL vs API key) owns typing on the key step.
	focusBase bool
	baseURL   string
	apiKey    string
	err       string
}

// webSearchConfigDir returns the directory that holds config.json (and thus the
// env-fallback file the /web-search form writes). Falls back to the default user
// config dir when the model has no resolved config path yet.
func (m model) webSearchConfigDir() string {
	if p := strings.TrimSpace(m.userConfigPath); p != "" {
		return filepath.Dir(p)
	}
	if dir, err := config.UserConfigDir(); err == nil {
		return filepath.Join(dir, "kajicode")
	}
	return ""
}

// openWebSearchForm starts the setup form at the provider step.
func (m model) openWebSearchForm() model {
	providers := tools.WebSearchProviders()
	if len(providers) == 0 {
		return m.appendSystemNotice("No web-search providers are registered.")
	}
	ws := &webSearchFormState{providers: providers}
	m.webSearchForm = ws
	m.clearSuggestions()
	return m
}

// handleWebSearchCommand dispatches /web-search subcommands:
//   - empty or "add": open the setup form
//   - "status": print what's configured (env + env file)
//   - "remove": clear configured keys from the shell rc + env file
func (m model) handleWebSearchCommand(text string) model {
	arg := strings.ToLower(strings.TrimSpace(text))
	switch arg {
	case "", "add", "setup":
		if m.pending {
			return m.appendSystemNotice("Cannot open /web-search while a run is active. Press Esc to cancel it first.")
		}
		return m.openWebSearchForm()
	case "status":
		return m.appendSystemNotice(m.webSearchStatusText())
	case "remove", "clear", "reset":
		return m.webSearchRemove()
	default:
		return m.appendSystemNotice("Usage: /web-search  |  /web-search status  |  /web-search remove")
	}
}

// webSearchCurrentProvider returns the provider the form is on. nil-safe.
func (m model) webSearchCurrentProvider() *tools.WebSearchProvider {
	f := m.webSearchForm
	if f == nil || f.selected < 0 || f.selected >= len(f.providers) {
		return nil
	}
	return &f.providers[f.selected]
}

// handleWebSearchKey captures keystrokes while the form is open.
func (m model) handleWebSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.webSearchForm
	if f == nil {
		return m, nil
	}
	switch {
	case keyIs(msg, tea.KeyEsc), keyCtrl(msg, 'c'):
		if f.step == webSearchStepKey {
			// Esc on the entry step returns to the provider list (not a full
			// cancel), so an accidental Enter doesn't wedge the user.
			return m.webSearchBack()
		}
		m.webSearchForm = nil
		return m.appendSystemNotice("Cancelled web-search setup — nothing was changed."), nil
	case f.step == webSearchStepProvider:
		// Provider list: ↑/↓ move the highlight, Enter (or →) selects.
		switch {
		case keyIs(msg, tea.KeyDown):
			f.moveSelection(1)
			return m, nil
		case keyIs(msg, tea.KeyUp):
			f.moveSelection(-1)
			return m, nil
		case keyIs(msg, tea.KeyEnter), keyIs(msg, tea.KeyRight):
			return m.webSearchAdvance()
		default:
			return m, nil
		}
	case f.step == webSearchStepKey:
		// Entry step: Enter submits, →/↓ advance, ←/↑ return to the list,
		// Tab switches the focused field, typing edits the field.
		switch {
		case keyIs(msg, tea.KeyEnter), keyIs(msg, tea.KeyRight), keyIs(msg, tea.KeyDown):
			return m.submitWebSearch()
		case keyIs(msg, tea.KeyLeft), keyIs(msg, tea.KeyUp):
			return m.webSearchBack()
		case keyIs(msg, tea.KeyTab):
			f.focusBase = !f.focusBase
			return m, nil
		case keyBackspace(msg):
			if f.focusBase {
				f.baseURL = delLastRune(f.baseURL)
			} else {
				f.apiKey = delLastRune(f.apiKey)
			}
			return m, nil
		case keyCtrl(msg, 'u'):
			if f.focusBase {
				f.baseURL = ""
			} else {
				f.apiKey = ""
			}
			return m, nil
		default:
			if t := keyText(msg); t != "" && !keyAlt(msg) && !keyHasMod(msg, tea.ModCtrl) {
				if f.focusBase {
					f.baseURL += t
				} else {
					f.apiKey += t
				}
			}
			return m, nil
		}
	}
	return m, nil
}

// moveSelection shifts the provider highlight by delta, wrapping around.
func (f *webSearchFormState) moveSelection(delta int) {
	n := len(f.providers)
	if n == 0 {
		return
	}
	f.selected = (f.selected + delta + n) % n
}

// delLastRune removes the final rune of a string.
func delLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[:len(r)-1])
}

// webSearchAdvance moves forward through the form's steps.
func (m model) webSearchAdvance() (tea.Model, tea.Cmd) {
	f := m.webSearchForm
	if f == nil {
		return m, nil
	}
	if f.step == webSearchStepProvider {
		f.step = webSearchStepKey
		// Prefill from what the provider already has set (default base URL, then any
		// live / stored value). Start focus on the API key unless the provider is
		// keyless (then the base URL is the only editable field worth fronting).
		prov := f.providers[f.selected]
		if db := tools.WebSearchProviderDefaultBaseURL(prov.ID); db != "" {
			f.baseURL = db
		}
		if live := strings.TrimSpace(os.Getenv(prov.BaseURLEnv)); live != "" {
			f.baseURL = live
		}
		f.focusBase = !prov.RequiresKey
		return m, nil
	}
	return m, nil
}

// webSearchBack returns from the entry step to the provider list (clearing any
// entry field error). It is a no-op on the list step, where Esc fully cancels.
func (m model) webSearchBack() (tea.Model, tea.Cmd) {
	f := m.webSearchForm
	if f == nil {
		return m, nil
	}
	if f.step == webSearchStepKey {
		f.step = webSearchStepProvider
		f.err = ""
		f.baseURL = ""
		f.apiKey = ""
		return m, nil
	}
	m.webSearchForm = nil
	return m.appendSystemNotice("Cancelled web-search setup — nothing was changed."), nil
}

// submitWebSearch persists the configured provider/key and reports the result.
func (m model) submitWebSearch() (tea.Model, tea.Cmd) {
	f := m.webSearchForm
	if f == nil {
		return m, nil
	}
	m.webSearchForm = nil
	prov := f.providers[f.selected]
	key := strings.TrimSpace(f.apiKey)
	if prov.RequiresKey && key == "" {
		m.webSearchForm = f // reopen so the user can type a key
		f.step = webSearchStepKey
		f.focusBase = false
		f.err = "A key is required for " + prov.Label + "."
		return m, nil
	}

	pairs := map[string]string{}
	if prov.EnvVar != "" && key != "" {
		pairs[prov.EnvVar] = key
	}
	baseURL := strings.TrimSpace(f.baseURL)
	if prov.BaseURLEnv != "" && baseURL != "" {
		pairs[prov.BaseURLEnv] = baseURL
	}
	// Always record the selected provider so the runtime uses this provider's chain.
	pairs["KAJICODE_WEBSEARCH_PROVIDER"] = prov.ID
	if prov.DefaultBaseURL != "" && strings.TrimSpace(baseURL) == "" {
		pairs["KAJICODE_WEBSEARCH_BASE_URL"] = prov.DefaultBaseURL
	}

	var lines []string

	// 1) Shell rc (export lines).
	rcPath := ""
	if rc, err := detectShellRC(); err == nil {
		rcPath = rc
		if werr := writeEnvToRC(rc, pairs); werr != nil {
			lines = append(lines, "⚠ couldn't write "+rc+": "+werr.Error())
		} else {
			lines = append(lines, "✓ wrote export lines to "+rc)
			sourceRC(rc) // best-effort so vars are live in this shell's children
		}
	} else {
		lines = append(lines, "ℹ no shell rc detected — key saved as a fallback env file instead ("+err.Error()+")")
	}

	// 2) Env-file fallback (covers startups that never source the rc).
	dir := m.webSearchConfigDir()
	if dir != "" {
		if _, werr := config.WriteEnvFile(dir, pairs); werr != nil {
			lines = append(lines, "⚠ couldn't write env fallback "+config.EnvFilePath(dir)+": "+werr.Error())
		} else {
			lines = append(lines, "✓ saved fallback "+config.EnvFilePath(dir))
		}
	}

	// 3) Make the vars live in this process immediately.
	for k, v := range pairs {
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	lines = append(lines, "✓ "+prov.Label+" is now configured. Run /web-search status to verify.")

	where := "shell profile"
	if rcPath == "" {
		where = "env fallback"
	}
	return m.appendSystemNotice("Web search configured (" + prov.Label + " → " + where + ").\n\n" + strings.Join(lines, "\n")), nil
}

// webSearchStatusText reports which providers and keys are currently visible.
func (m model) webSearchStatusText() string {
	var b strings.Builder
	b.WriteString("Web search configuration\n")
	b.WriteString("------------------------\n\n")
	providers := tools.WebSearchProviders()
	configured := false
	for _, p := range providers {
		key := "—"
		if p.EnvVar != "" {
			if v := strings.TrimSpace(os.Getenv(p.EnvVar)); v != "" {
				key = maskedProviderWizardKey(v) + " (env)"
				configured = true
			}
		}
		base := "—"
		if p.BaseURLEnv != "" {
			if v := strings.TrimSpace(os.Getenv(p.BaseURLEnv)); v != "" {
				base = v
			} else if db := tools.WebSearchProviderDefaultBaseURL(p.ID); db != "" {
				base = db + " (default)"
			}
		} else if db := tools.WebSearchProviderDefaultBaseURL(p.ID); db != "" {
			base = db
		}
		b.WriteString(fmt.Sprintf("%-24s key: %-12s base: %s\n", p.Label, key, base))
	}
	if p := strings.TrimSpace(os.Getenv("KAJICODE_WEBSEARCH_PROVIDER")); p != "" {
		b.WriteString("\nActive provider: " + p + "\n")
	} else {
		b.WriteString("\nNo active provider selected — the failover chain will be used.\n")
	}
	dir := m.webSearchConfigDir()
	if dir != "" {
		if fi, err := os.Stat(config.EnvFilePath(dir)); err == nil && fi.Mode().IsRegular() {
			b.WriteString("\nEnv fallback file present: " + config.EnvFilePath(dir) + "\n")
		}
	}
	if !configured {
		b.WriteString("\nNo API keys are set. Run /web-search to configure one.\n")
	}
	return b.String()
}

// webSearchRemove clears configured keys/base URLs from the shell rc and env
// fallback, and removes the provider override.
func (m model) webSearchRemove() model {
	var lines []string
	providers := tools.WebSearchProviders()

	keys := map[string]bool{}
	for _, p := range providers {
		if p.EnvVar != "" {
			keys[p.EnvVar] = true
		}
		if p.BaseURLEnv != "" {
			keys[p.BaseURLEnv] = true
		}
	}
	keys["KAJICODE_WEBSEARCH_PROVIDER"] = true

	// Empty the rc block (remove the guarded region).
	if rc, err := detectShellRC(); err == nil {
		if werr := writeEnvToRC(rc, map[string]string{}); werr != nil {
			lines = append(lines, "⚠ couldn't clear "+rc+": "+werr.Error())
		} else {
			lines = append(lines, "✓ cleared exports from "+rc)
		}
	} else {
		lines = append(lines, "ℹ "+err.Error())
	}

	// Remove keys from the env fallback.
	if dir := m.webSearchConfigDir(); dir != "" {
		var removed []string
		for k := range keys {
			removed = append(removed, k)
		}
		ok, err := config.RemoveEnvFileKeys(dir, removed)
		if err != nil {
			lines = append(lines, "⚠ couldn't clear env fallback: "+err.Error())
		} else if ok {
			lines = append(lines, "✓ cleared fallback env file")
		}
	}

	// Clear live process env vars (we set them via os.Setenv earlier).
	for k := range keys {
		_ = os.Unsetenv(k)
	}

	lines = append(lines, "Web search credentials removed. The tool falls back to the failover chain (keyless providers).")
	return m.appendSystemNotice("Web search configuration removed.\n\n" + strings.Join(lines, "\n"))
}

// webSearchFormOverlay renders the setup modal. Different layouts per step.
func (m model) webSearchFormOverlay(width int) string {
	f := m.webSearchForm
	if f == nil {
		return ""
	}
	overlayWidth := minInt(width, pickerOverlayMaxWidth)
	if overlayWidth < pickerOverlayMinWidth {
		overlayWidth = width
	}
	var lines []string
	switch f.step {
	case webSearchStepProvider:
		lines = m.webSearchProviderOverlayContent(overlayWidth)
	case webSearchStepKey:
		lines = m.webSearchKeyOverlayContent(overlayWidth)
	}
	if f.err != "" {
		lines = append(lines, "", kajicodeTheme.red.Render(f.err))
	}
	title := "Web search"
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, kajicodeTheme.lineStrong, lipgloss.NewStyle()), width)
}

// webSearchProviderOverlayContent renders the provider list.
func (m model) webSearchProviderOverlayContent(width int) []string {
	f := m.webSearchForm
	lines := []string{
		kajicodeTheme.ink.Render("Choose a web-search provider to configure:"),
		"",
	}
	for i, p := range f.providers {
		surface := transparentSurface
		marker := surface(kajicodeTheme.faintest).Render("  ")
		if i == f.selected {
			surface = kajicodeTheme.onSel
			marker = surface(kajicodeTheme.accent).Render("❯ ")
		}
		label := surface(kajicodeTheme.ink).Render(p.Label)
		lines = append(lines, fitStyledLine(marker+label, width))
	}
	lines = append(lines, "", kajicodeTheme.faint.Render("⏎ select · Esc cancel · ↑/↓ move"))
	return lines
}

// webSearchKeyOverlayContent renders the base-URL + API-key entry step.
func (m model) webSearchKeyOverlayContent(width int) []string {
	f := m.webSearchForm
	prov := m.webSearchCurrentProvider()
	if prov == nil {
		return []string{"internal error: no provider selected"}
	}
	lines := []string{
		kajicodeTheme.accent.Render("Configure " + prov.Label),
		kajicodeTheme.ink.Render("URL and API key for this provider. Stored plaintext in your shell profile."),
		"",
	}
	// Base URL field (only when the provider exposes a configurable base URL).
	if prov.BaseURLEnv != "" {
		lines = append(lines, m.webSearchInputLine(prov.BaseURLEnv, f.baseURL, "https://...", f.focusBase, width))
	}
	// API key field (only when required).
	if prov.RequiresKey {
		label := prov.EnvVar
		if label == "" {
			label = "API key"
		}
		lines = append(lines, m.webSearchInputLine(label+",", f.apiKey, "paste key here", !f.focusBase, width))
	}
	lines = append(lines, "",
		kajicodeTheme.faint.Render("⏎ save · Esc back · Tab switch field"),
		kajicodeTheme.faint.Render("Key is masked; the exact export line is written to your shell profile and the fallback env file."),
	)
	return lines
}

// webSearchInputLine is a single editable row for the form, cursor-prefixed when
// focused.
func (m model) webSearchInputLine(prompt string, value string, placeholder string, focused bool, width int) string {
	p := kajicodeTheme.userPrompt.Render(prompt + " ")
	marker := kajicodeTheme.accent.Render("▌")
	if !focused {
		marker = " "
	}
	if value == "" {
		return fitStyledLine(p+marker+kajicodeTheme.faint.Render(placeholder), width)
	}
	return fitStyledLine(p+kajicodeTheme.ink.Render(value)+marker, width)
}
