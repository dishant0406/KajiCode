package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/zeroline"
)

// zerolineTickMsg advances the Zeroline animation frame (spinner) independently of
// agent events so "working…" spins smoothly between row updates.
type zerolineTickMsg time.Time

// bootFrames is how many ~120ms ticks the boot splash plays before the home
// page reveals (~1.9s), matching the mockup's splash timing.
const bootFrames = 16

func zerolineTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return zerolineTickMsg(t) })
}

// handleZerolineKeys intercepts theme controls when the zeroline skin is active.
// Digits 1-5 pick a color theme (only when the input is empty so they can still
// be typed); ctrl+t cycles; ctrl+l toggles light/dark.
func (m model) handleZerolineKeys(msg tea.KeyMsg) (model, bool) {
	switch msg.String() {
	case "ctrl+l":
		m.themeDark = !m.themeDark
		return m, true
	case "ctrl+t":
		m.themeVariant = (m.themeVariant + 1) % len(zeroline.Themes)
		return m, true
	case "ctrl+j":
		m.jsonMode = !m.jsonMode // TEXT/JSON view toggle
		return m, true
	case "ctrl+s":
		if m.showSplash {
			return m, true // the drawer only renders on the chat page; ignore on home
		}
		m.drawerOpen = true // sessions drawer (Esc/ctrl+s closes; handled in Update)
		m.drawerSessions = m.loadDrawerSessions()
		m.drawerIndex = 0
		return m, true
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		if k := msg.String(); len(k) == 1 && k >= "1" && k <= "5" {
			m.themeVariant = int(k[0] - '1')
			return m, true
		}
	}
	return m, false
}

// drawerData returns the open sessions drawer (nil when closed).
func (m model) drawerData() *zeroline.Drawer {
	if !m.drawerOpen {
		return nil
	}
	return &zeroline.Drawer{Sessions: m.drawerSessions, Selected: m.drawerIndex}
}

// loadDrawerSessions lists real sessions from the store (most-recent first),
// falling back to the sample list when the store is empty or unavailable.
func (m model) loadDrawerSessions() []zeroline.Session {
	if m.sessionStore == nil {
		return zeroline.DefaultSessions()
	}
	items, err := m.sessionStore.List()
	if err != nil || len(items) == 0 {
		return zeroline.DefaultSessions()
	}
	out := make([]zeroline.Session, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			title = "(untitled)"
		}
		id := it.SessionID
		if len(id) > 6 {
			id = id[:6]
		}
		out = append(out, zeroline.Session{
			ID:    id,
			When:  m.relativeWhen(it.UpdatedAt),
			Title: title,
			Model: it.ModelID,
			Turns: it.EventCount,
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// relativeWhen formats an RFC3339 timestamp as a short relative age (falls back
// to the raw string if it can't be parsed).
func (m model) relativeWhen(s string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return s
	}
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// zerolineHeader builds the zeroline header, including the live cost and
// cumulative token total from the usage tracker (with the unpriced fallback) so
// the top/bottom bars actually reflect consumption instead of always showing $0.
func (m model) zerolineHeader() zeroline.Header {
	h := zeroline.Header{
		Cwd:      m.cwd,
		Branch:   m.gitBranch,
		Model:    m.modelName,
		Provider: m.providerName,
	}
	if m.usageTracker != nil {
		summary := m.usageTracker.Summary()
		h.Cost = summary.TotalCost
		h.TotalTokens = summary.TotalTokens
	}
	if h.TotalTokens == 0 && m.unpricedTokens > 0 {
		h.TotalTokens = m.unpricedTokens
	}
	return h
}

func (m model) zerolineView() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	// Boot splash reveals on launch, then the home page.
	if !m.booted && m.showSplash {
		return zeroline.RenderBoot(m.themeVariant, m.themeDark, m.frame, width, height)
	}

	header := m.zerolineHeader()

	// Home until the first turn is submitted.
	if m.showSplash {
		return zeroline.RenderHome(zeroline.HomeData{
			Variant:     m.themeVariant,
			Dark:        m.themeDark,
			Width:       width,
			Height:      height,
			Header:      header,
			Input:       m.input.View(),
			Chips:       zeroline.DefaultChips(),
			ChipIndex:   -1, // resting state: chips are suggestions, none pre-selected
			Suggestions: m.zerolineSuggestions(),
			SelectedIdx: m.suggestionIdx,
			Picker:      m.zerolinePicker(),
		})
	}

	rows := m.zerolineRows()
	running := false
	for _, r := range rows {
		if r.Kind == "tool" && r.Running {
			running = true
		}
	}
	askUser := m.zerolineAskUser()
	// A pending permission prompt or ask_user questionnaire is the focus: suppress
	// the working/thinking spinner so the gate/question shows instead.
	blocked := m.pendingPermission != nil || askUser != nil
	thinking := m.pending && m.streamingText == "" && !running && !blocked

	return zeroline.RenderChat(zeroline.ChatData{
		Variant:     m.themeVariant,
		Dark:        m.themeDark,
		Width:       width,
		Height:      height,
		Header:      header,
		Rows:        rows,
		Working:     m.pending && !blocked,
		Thinking:    thinking,
		Stream:      m.streamingText,
		TokS:        m.streamTokS(),
		Spin:        m.frame,
		JSONMode:    m.jsonMode,
		Drawer:      m.drawerData(),
		Perm:        m.zerolinePerm(),
		AskUser:     askUser,
		Input:       m.input.View(),
		ImageChips:  renderImageChips(m.pendingImageLabels),
		Suggestions: m.zerolineSuggestions(),
		SelectedIdx: m.suggestionIdx,
		Picker:      m.zerolinePicker(),
	})
}

// zerolineAskUser maps a pending ask_user questionnaire into zeroline render data,
// or nil when none is active. The focused question is the one at the prompt's
// current index.
func (m model) zerolineAskUser() *zeroline.AskUser {
	if m.pendingAskUser == nil {
		return nil
	}
	prompt := m.pendingAskUser
	total := len(prompt.request.Questions)
	index := prompt.index
	if index >= total {
		index = total - 1
	}
	if index < 0 {
		index = 0
	}
	out := &zeroline.AskUser{
		Header: prompt.request.Header,
		Index:  index,
		Total:  total,
		Input:  m.input.Value(),
	}
	if total > 0 {
		question := prompt.request.Questions[index]
		out.Question = question.Question
		out.Options = question.Options
	}
	return out
}

// zerolineSuggestions maps the live autocomplete matches into zeroline render
// data, or nil when the overlay is inactive (a modal is up or there are no
// matches).
func (m model) zerolineSuggestions() []zeroline.Suggestion {
	if !m.suggestionsActive() {
		return nil
	}
	out := make([]zeroline.Suggestion, 0, len(m.suggestions))
	for _, s := range m.suggestions {
		out = append(out, zeroline.Suggestion{Name: s.Name, Desc: s.Desc})
	}
	return out
}

// zerolinePicker maps an open selector into zeroline render data, or nil when no
// picker is open.
func (m model) zerolinePicker() *zeroline.Picker {
	if m.picker == nil {
		return nil
	}
	labels := make([]string, 0, len(m.picker.items))
	for _, item := range m.picker.items {
		labels = append(labels, item.Label)
	}
	return &zeroline.Picker{Title: m.picker.title, Items: labels, Selected: m.picker.selected}
}

// streamTokS estimates tokens/sec for the current streaming segment (~4 chars/token).
func (m model) streamTokS() int {
	if m.streamingText == "" {
		return 0
	}
	frames := m.frame - m.streamStartFrame
	if frames <= 0 {
		return 0
	}
	secs := float64(frames) * 0.12
	toks := float64(len([]rune(m.streamingText))) / 4.0
	if secs <= 0 {
		return 0
	}
	return int(toks / secs)
}

func (m model) zerolineRows() []zeroline.Row {
	// Merge each tool call with its matching result (by id) into a single "tool"
	// card row: running until the result arrives, then carrying its status + body.
	results := make(map[string]transcriptRow)
	for _, r := range m.transcript {
		if r.kind == rowToolResult && r.id != "" {
			results[r.id] = r
		}
	}
	rows := make([]zeroline.Row, 0, len(m.transcript))
	for _, r := range m.transcript {
		switch r.kind {
		case rowUser:
			rows = append(rows, zeroline.Row{Kind: "user", Text: r.text})
		case rowAssistant:
			rows = append(rows, zeroline.Row{Kind: "assistant", Text: r.text})
		case rowToolCall:
			res, done := results[r.id]
			row := zeroline.Row{Kind: "tool", Tool: r.tool, Text: r.detail, Running: !(r.id != "" && done)}
			if done {
				row.Status = string(res.status)
				row.Detail = res.detail
			}
			rows = append(rows, row)
		case rowToolResult:
			if r.id != "" {
				continue // already merged into its tool call above
			}
			rows = append(rows, zeroline.Row{Kind: "tool", Tool: r.tool, Status: string(r.status), Detail: r.detail})
		case rowPermission:
			rows = append(rows, zeroline.Row{Kind: "permission", Text: r.text})
		case rowAskUser:
			rows = append(rows, zeroline.Row{Kind: "system", Text: r.text, Detail: r.detail})
		case rowSystem:
			rows = append(rows, zeroline.Row{Kind: "system", Text: r.text})
		case rowError:
			rows = append(rows, zeroline.Row{Kind: "error", Text: r.text})
		}
	}
	return rows
}

func (m model) zerolinePerm() *zeroline.Perm {
	if m.pendingPermission == nil {
		return nil
	}
	req := m.pendingPermission.request
	return &zeroline.Perm{
		Tool:    req.ToolName,
		Risk:    string(req.Risk.Level),
		Reason:  req.Reason,
		Summary: req.SideEffect,
	}
}
