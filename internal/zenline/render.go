package zenline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Header is the live session context shown on both surfaces.
type Header struct {
	Cwd, Branch, Model, Provider string
	Dirty                        bool
	CtxPct                       int
	Cost                         float64
}

// Row is one rendered transcript entry, mapped from the TUI's live state.
type Row struct {
	Kind    string // user | assistant | toolcall | toolresult | permission | system | error
	Text    string
	Tool    string
	Status  string // ok | error (toolresult)
	Detail  string
	Running bool // toolcall: still in flight (no result yet)
}

// Perm is an in-flight permission prompt awaiting a decision.
type Perm struct {
	Tool, Risk, Reason, Summary string
}

// HomeData drives the Zen home page.
type HomeData struct {
	Variant       int
	Dark          bool
	Width, Height int
	Header        Header
	Recent        [][3]string
	Input         string
}

// ChatData drives the Statusline chat page.
type ChatData struct {
	Variant       int
	Dark          bool
	Width, Height int
	Header        Header
	Rows          []Row
	Working       bool
	Thinking      bool   // waiting for the model's first token
	Stream        string // live assistant text being streamed
	TokS          int    // streaming tokens/sec
	Spin          int
	Perm          *Perm
	Input         string
}

type styles struct {
	pal             Pal
	fg, dim, mute   lipgloss.Style
	acc, acc2       lipgloss.Style
	green, red, amb lipgloss.Style
}

// newStyles builds foreground-only text styles. These compose INSIDE the chat
// status bars (which set their own Panel backgrounds), so they must NOT bake in a
// background of their own.
func newStyles(p Pal) styles {
	f := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	return styles{p, f(p.Fg), f(p.Dim), f(p.Mute), f(p.Accent), f(p.Accent2), f(p.Green), f(p.Red), f(p.Amber)}
}

// newCanvasStyles is for full-bleed surfaces (the home + boot splash) where text
// sits directly on the themed background. Each style carries the theme background
// so content cells match the surrounding whitespace fill — otherwise the text
// shows the terminal's own background, producing a visible "card" against the
// themed margins.
func newCanvasStyles(p Pal) styles {
	f := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Background(p.Bg)
	}
	return styles{p, f(p.Fg), f(p.Dim), f(p.Mute), f(p.Accent), f(p.Accent2), f(p.Green), f(p.Red), f(p.Amber)}
}

// block is a solid caret cell used for the streaming cursor.
func (s styles) block() string {
	return lipgloss.NewStyle().Background(s.pal.Accent).Render(" ")
}

// RenderBoot renders the launch splash: the ZERO wordmark reveals line-by-line,
// then the tagline and a loading line, advancing by animation frame (~120ms).
func RenderBoot(variant int, dark bool, frame, w, h int) string {
	p := Resolve(variant, dark)
	s := newCanvasStyles(p)
	reveal := []int{1, 3, 5, 7, 9} // per-line reveal frames (~120ms each)
	var b strings.Builder
	for i, l := range wordmark {
		if i < len(reveal) && frame >= reveal[i] {
			b.WriteString(s.acc.Render(l) + "\n")
		} else {
			b.WriteString(strings.Repeat(" ", len([]rune(l))) + "\n")
		}
	}
	b.WriteString("\n")
	if frame >= 11 {
		b.WriteString(s.dim.Render("Own your agent. ") + s.acc2.Render("Any model.") + s.dim.Render(" Zero lock-in.") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if frame >= 8 {
		b.WriteString(s.mute.Render("initializing runtime · loading providers ") + s.amb.Render(spinFrames[frame%len(spinFrames)]))
	}
	content := lipgloss.NewStyle().Align(lipgloss.Center).Background(p.Bg).Render(b.String())
	return lipgloss.Place(maxi(w, 40), maxi(h, 8), lipgloss.Center, lipgloss.Center, content,
		lipgloss.WithWhitespaceBackground(p.Bg))
}

// ---------------------------------------------------------------- HOME (ZEN)

// RenderHome renders the centered Zen landing surface.
func RenderHome(d HomeData) string {
	p := Resolve(d.Variant, d.Dark)
	s := newCanvasStyles(p)
	w := maxi(d.Width, 40)

	var b strings.Builder
	for _, l := range wordmark {
		b.WriteString(s.acc.Render(l) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(s.dim.Render("Own your agent. ") + s.acc2.Render("Any model.") + s.dim.Render(" Zero lock-in.") + "\n\n")
	b.WriteString(headerStripe(s, d.Header) + "\n\n")

	if len(d.Recent) > 0 {
		b.WriteString(s.mute.Render("recent sessions") + "\n")
		for _, r := range d.Recent {
			title := r[0]
			pad := 26 - len(title)
			if pad < 1 {
				pad = 1
			}
			b.WriteString(s.mute.Render("› ") + s.fg.Render(title) + strings.Repeat(" ", pad) +
				s.dim.Render(r[1]) + "  " + s.mute.Render(r[2]) + "\n")
		}
		b.WriteString("\n")
	}

	box := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(p.Line).
		BorderBackground(p.Bg).Background(p.Bg).
		Padding(0, 1).Width(mini(58, w-4)).Render(d.Input)
	b.WriteString(box + "\n\n")
	b.WriteString(s.mute.Render("⏎ start · 1-5 theme · ^L light · / commands · @ files · ! bash · ^C quit"))

	content := lipgloss.NewStyle().Align(lipgloss.Center).Background(p.Bg).Render(b.String())
	return lipgloss.Place(w, maxi(d.Height, 8), lipgloss.Center, lipgloss.Center, content,
		lipgloss.WithWhitespaceBackground(p.Bg))
}

func headerStripe(s styles, h Header) string {
	dirty := ""
	if h.Dirty {
		dirty = s.amb.Render("✱")
	}
	parts := s.dim.Render(shortPath(h.Cwd)) + s.dim.Render(" · ⎇ ") + s.dim.Render(orDash(h.Branch)) + dirty +
		s.dim.Render(" · ") + s.fg.Render(orDash(h.Model)) + s.dim.Render(" · ") + s.dim.Render(orDash(h.Provider))
	return parts
}

// ------------------------------------------------------------- CHAT (STATUS)

// RenderChat renders the Statusline chat surface from live agent state.
func RenderChat(d ChatData) string {
	p := Resolve(d.Variant, d.Dark)
	s := newStyles(p)
	w := maxi(d.Width, 40)
	h := maxi(d.Height, 8)

	run := "normal"
	switch {
	case d.Perm != nil:
		run = "blocked"
	case d.Working:
		run = "work"
	case hasAssistant(d.Rows):
		run = "done"
	}

	top := s.topBar(run, d.Header, w)
	bottom := s.botBar(run, d.Header, d.Variant, d.TokS, w)
	cmd := s.cmdRegion(d, w)

	bodyH := h - 3
	if bodyH < 1 {
		bodyH = 1
	}
	var body string
	if d.Perm != nil {
		body = s.permModal(d.Perm, w, bodyH)
	} else {
		body = s.transcript(d, w, bodyH)
	}
	return top + "\n" + body + "\n" + cmd + "\n" + bottom
}

// Rect is a screen region in cell coordinates (0-based, y measured from the top
// of the whole frame including the top status bar).
type Rect struct{ X, Y, W, H int }

// PermGeometry holds the clickable button regions of the centered permission
// modal. It is computed purely from width/height so the renderer and the mouse
// hit-test always agree.
type PermGeometry struct {
	Active              bool
	Allow, Always, Deny Rect
}

// Hit returns "allow", "always", "deny" or "" for a click at (x, y).
func (g PermGeometry) Hit(x, y int) string {
	in := func(r Rect) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }
	switch {
	case in(g.Allow):
		return "allow"
	case in(g.Always):
		return "always"
	case in(g.Deny):
		return "deny"
	}
	return ""
}

func permBoxWidth(w int) int {
	bw := 52
	if bw > w-2 {
		bw = w - 2
	}
	if bw < 38 {
		bw = 38
	}
	return bw
}

// PermLayout computes the button hitboxes for the centered modal. Must stay in
// lockstep with permModal/permModalLines below.
func PermLayout(width, height int) PermGeometry {
	bw := permBoxWidth(width)
	bodyH := height - 3
	if bodyH < 8 {
		bodyH = 8
	}
	top := (bodyH - 8) / 2
	if top < 0 {
		top = 0
	}
	bx := (width - bw) / 2
	if bx < 0 {
		bx = 0
	}
	btnY := 1 + top + 5 // top bar row + modal top + buttons row index
	return PermGeometry{
		Active: true,
		Allow:  Rect{bx + 2, btnY, 13, 1},
		Always: Rect{bx + 17, btnY, 14, 1},
		Deny:   Rect{bx + 33, btnY, 12, 1},
	}
}

func (s styles) topBar(run string, h Header, w int) string {
	p := s.pal
	var modeTxt string
	var modeBg lipgloss.Color
	switch run {
	case "work":
		modeTxt, modeBg = "⟳ WORKING", p.Amber
	case "done":
		modeTxt, modeBg = "✓ DONE", p.Green
	case "blocked":
		modeTxt, modeBg = "⚠ BLOCKED", p.Red
	default:
		modeTxt, modeBg = "NORMAL", p.Accent
	}
	mode := lipgloss.NewStyle().Background(modeBg).Foreground(p.Bg).Bold(true).Padding(0, 1).Render(modeTxt)
	b1 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel2).Padding(0, 1).Render(in) }
	b2 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel).Padding(0, 1).Render(in) }

	dirty := ""
	if h.Dirty {
		dirty = s.amb.Render("✱")
	}
	branch := b1(s.fg.Render("⎇ " + orDash(h.Branch)) + dirty)
	cwd := b2(s.dim.Render(shortPath(h.Cwd)))
	model := b2(s.mute.Render("model ") + s.fg.Render(orDash(h.Model)))
	prov := b2(s.mute.Render("prov ") + s.dim.Render(orDash(h.Provider)))
	ctx := b2(s.mute.Render("ctx ") + s.fg.Render(strconv.Itoa(h.CtxPct)+"%"))
	cost := b1(s.mute.Render("$ ") + s.fg.Render(fmt.Sprintf("%.2f", h.Cost)))

	return bar(mode+branch+cwd+model, prov+ctx+cost, w, p.Panel)
}

func (s styles) botBar(run string, h Header, variant, tokS, w int) string {
	p := s.pal
	caution := run == "blocked"
	stTxt := map[string]string{"work": "WORKING", "done": "DONE", "blocked": "BLOCKED", "normal": "READY"}[run]
	dot := lipgloss.NewStyle().Background(p.Accent).Render(" ")
	stStyle := s.fg
	if caution {
		stStyle = s.red.Bold(true)
	}
	b1 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel2).Padding(0, 1).Render(in) }
	b2 := func(in string) string { return lipgloss.NewStyle().Background(p.Panel).Padding(0, 1).Render(in) }

	tps := s.green.Render("0")
	if tokS > 0 {
		tps = s.green.Render(strconv.Itoa(tokS))
	}
	left := b1(dot+" "+stStyle.Render(stTxt)) + b2(s.dim.Render("utf-8")) +
		b2(s.mute.Render("tok/s ")+tps)
	right := b2(s.mute.Render("ctx ")+s.gauge(float64(h.CtxPct)/100, 8)) +
		b2(s.mute.Render(ThemeName(variant))) +
		b1(s.mute.Render("1-5 theme · ^L light"))
	return bar(left, right, w, p.Panel)
}

func (s styles) gauge(v float64, w int) string {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	f := int(v*float64(w) + 0.5)
	if f > w {
		f = w
	}
	return s.mute.Render("▕") + s.acc.Render(strings.Repeat("█", f)) +
		lipgloss.NewStyle().Foreground(s.pal.Line2).Render(strings.Repeat("░", w-f)) + s.mute.Render("▏")
}

func bar(left, right string, w int, fill lipgloss.Color) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	spacer := lipgloss.NewStyle().Background(fill).Render(strings.Repeat(" ", gap))
	return left + spacer + right
}

func (s styles) cmdRegion(d ChatData, w int) string {
	p := s.pal
	if d.Perm != nil {
		hint := s.mute.Render("click a choice · keys ") +
			s.acc.Render("a") + s.mute.Render("/") + s.acc.Render("y") + s.mute.Render("/") + s.acc.Render("d") +
			s.mute.Render(" · Esc cancel")
		return padRight(hint, w, p.Bg)
	}
	line := s.acc.Bold(true).Render(":") + " " + d.Input
	return padRight(line, w, p.Bg)
}

// permModal renders the centered permission modal across the body region.
func (s styles) permModal(p *Perm, w, bodyH int) string {
	bw := permBoxWidth(w)
	modal := s.permModalLines(p, bw)
	top := (bodyH - len(modal)) / 2
	if top < 0 {
		top = 0
	}
	left := (w - bw) / 2
	if left < 0 {
		left = 0
	}
	bg := func(n int) string {
		if n < 0 {
			n = 0
		}
		return lipgloss.NewStyle().Background(s.pal.Bg).Render(strings.Repeat(" ", n))
	}
	blank := bg(w)
	out := make([]string, 0, bodyH)
	for i := 0; i < bodyH; i++ {
		mi := i - top
		if mi >= 0 && mi < len(modal) {
			out = append(out, bg(left)+modal[mi]+bg(w-left-bw))
		} else {
			out = append(out, blank)
		}
	}
	return strings.Join(out, "\n")
}

func (s styles) permModalLines(p *Perm, bw int) []string {
	amber := lipgloss.NewStyle().Foreground(s.pal.Amber)
	vb := amber.Render("│")
	contentW := bw - 4 // borders + one space of padding each side

	title := "permission required"
	dashN := bw - 3 - lipgloss.Width(title) - 2
	if dashN < 0 {
		dashN = 0
	}
	topLine := amber.Render("╭─ ") + s.amb.Bold(true).Render(title) + amber.Render(" "+strings.Repeat("─", dashN)+"╮")
	botLine := amber.Render("╰" + strings.Repeat("─", bw-2) + "╯")

	content := func(c string) string {
		pad := contentW - lipgloss.Width(c)
		if pad < 0 {
			pad = 0
		}
		return vb + " " + c + strings.Repeat(" ", pad) + " " + vb
	}

	toolLine := s.fg.Bold(true).Render(clip(p.Tool, contentW))
	risk := ""
	if p.Risk != "" {
		risk = "RISK " + strings.ToUpper(p.Risk)
	}
	reason := clip(orDash(p.Reason), contentW-lipgloss.Width(risk)-2)
	meta := s.dim.Render(reason)
	if risk != "" {
		meta += s.dim.Render("  ") + s.amb.Render(risk)
	}

	allowBtn := lipgloss.NewStyle().Background(s.pal.Accent).Foreground(s.pal.Bg).Bold(true).Render("[ a · allow ]")
	alwaysBtn := s.dim.Render("[ ") + s.acc.Render("y") + s.dim.Render(" · always ]")
	denyBtn := s.dim.Render("[ ") + s.acc.Render("d") + s.dim.Render(" · deny ]")
	buttons := allowBtn + "  " + alwaysBtn + "  " + denyBtn

	return []string{
		topLine,
		content(""),
		content(toolLine),
		content(meta),
		content(""),
		content(buttons),
		content(""),
		botLine,
	}
}

func (s styles) transcript(d ChatData, w, h int) string {
	tw := w - 4
	var lines []string
	add := func(ls ...string) { lines = append(lines, ls...) }
	blank := func() {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}

	for _, r := range d.Rows {
		switch r.Kind {
		case "user":
			blank()
			add(s.mute.Render("› ") + s.acc.Bold(true).Render("you ") + s.fg.Render(clip(r.Text, tw-6)))
		case "assistant":
			blank()
			add(s.acc2.Bold(true).Render("✦ zero"))
			lines = append(lines, s.renderAssistant(r.Text, tw)...)
		case "toolcall":
			blank()
			marker := s.mute.Render("▸")
			if r.Running {
				marker = s.amb.Render(spinFrames[d.Spin%len(spinFrames)])
			}
			line := marker + " " + toolIcon(s, r.Tool) + " " + s.acc2.Render(toolLabel(r.Tool))
			if a := clip(firstLine(r.Detail), tw-22); a != "" {
				line += "  " + s.dim.Render(a)
			}
			add(line)
		case "toolresult":
			summary, showBody, bodyMax := resultSummary(r.Tool, r.Status, r.Detail)
			if r.Status == "error" {
				add("  " + s.mute.Render("⎿ ") + s.red.Render(clip(firstLine(r.Detail), tw-8)))
			} else if summary != "" {
				add("  " + s.mute.Render("⎿ ") + s.dim.Render(clip(summary, tw-8)))
			}
			if showBody && r.Status != "error" {
				lines = append(lines, s.renderCodeBlock(r.Detail, tw, bodyMax)...)
			}
		case "permission":
			blank()
			add(s.amb.Render("⚠ ") + s.dim.Render(clip(r.Text, tw-4)))
		case "system":
			blank()
			for _, dl := range strings.Split(r.Text, "\n") {
				add(s.dim.Render(clip(dl, tw)))
			}
		case "error":
			blank()
			add(s.red.Render("✗ " + clip(r.Text, tw-4)))
		}
	}

	switch {
	case d.Stream != "":
		blank()
		add(s.acc2.Bold(true).Render("✦ zero"))
		slines := s.renderAssistant(d.Stream, tw)
		if len(slines) > 0 {
			slines[len(slines)-1] += s.block() // streaming caret
		}
		lines = append(lines, slines...)
	case d.Thinking:
		blank()
		add(s.acc2.Bold(true).Render("✦ zero") + "  " + s.amb.Render(spinFrames[d.Spin%len(spinFrames)]) +
			s.dim.Render(" thinking"+strings.Repeat(".", d.Spin%4)))
	}

	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}

	out := strings.Join(lines, "\n")
	if d.Perm != nil {
		out = lipgloss.NewStyle().Faint(true).Render(out)
	}
	return lipgloss.NewStyle().PaddingLeft(2).Render(out)
}

// renderAssistant lays out a model message: prose is word-wrapped, fenced code
// blocks are kept verbatim in an aligned, clipped block with a gutter so code
// never re-wraps or knocks the layout out of alignment.
func (s styles) renderAssistant(text string, tw int) []string {
	var out []string
	inCode := false
	for _, ln := range strings.Split(text, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inCode = !inCode
			out = append(out, "        "+s.mute.Render("┄┄┄┄┄┄"))
			continue
		}
		if inCode {
			out = append(out, "        "+s.mute.Render("│ ")+s.fg.Render(clip(detab(ln), tw-12)))
			continue
		}
		if t == "" {
			out = append(out, "")
			continue
		}
		for _, wl := range wrap(t, tw-9) {
			out = append(out, "        "+s.fg.Render(wl))
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// renderCodeBlock renders tool output (file contents, diffs, listings) as an
// aligned block with a left gutter, clipped to width and capped at max lines.
// Unified diffs get +/-/@@ coloring; everything else stays neutral.
func (s styles) renderCodeBlock(detail string, tw, max int) []string {
	detail = strings.TrimRight(detail, "\n")
	if detail == "" {
		return nil
	}
	isDiff := strings.Contains(detail, "@@ ") || strings.HasPrefix(strings.TrimSpace(detail), "diff ") ||
		strings.Contains(detail, "\n--- ") || strings.Contains(detail, "\n+++ ")
	lines := strings.Split(detail, "\n")
	gut := s.mute.Render("│ ")
	var out []string
	for i, ln := range lines {
		if i >= max {
			out = append(out, "      "+s.mute.Render(fmt.Sprintf("│ … +%d more lines", len(lines)-i)))
			break
		}
		out = append(out, "      "+gut+s.codeLine(detab(ln), tw-8, isDiff))
	}
	return out
}

func (s styles) codeLine(ln string, w int, isDiff bool) string {
	c := clip(ln, w)
	if isDiff {
		switch {
		case strings.HasPrefix(ln, "@@"):
			return s.acc.Render(c)
		case strings.HasPrefix(ln, "+"):
			return s.green.Render(c)
		case strings.HasPrefix(ln, "-"):
			return s.red.Render(c)
		}
	}
	return s.dim.Render(c)
}

func detab(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

// resultSummary collapses a tool's raw output into a one-line summary the way a
// proper coding agent does (file reads → line counts, listings → entry counts),
// and decides whether the full body (diffs, shell output, grep hits) is shown.
func resultSummary(tool, status, detail string) (summary string, showBody bool, bodyMax int) {
	if status == "error" {
		return "", true, 3
	}
	switch tool {
	case "read_file":
		fl := firstLine(detail)
		if i := strings.LastIndex(fl, "("); i >= 0 {
			return strings.TrimSuffix(fl[i+1:], ")"), false, 0 // "N lines" / "lines a-b of N"
		}
		return "read", false, 0
	case "list_directory":
		if strings.HasPrefix(firstLine(detail), "Directory is empty") {
			return "empty", false, 0
		}
		return plural(countBodyLines(detail), "entry", "entries"), false, 0
	case "glob":
		return plural(countNonEmpty(detail), "match", "matches"), false, 0
	case "grep":
		fl := firstLine(detail)
		if strings.HasPrefix(fl, "0 matches") || strings.HasPrefix(fl, "No matches") {
			return "0 matches", false, 0
		}
		return plural(countNonEmpty(detail), "match", "matches"), true, 4
	case "write_file", "edit_file", "apply_patch":
		return "", true, 8
	case "bash":
		return "", true, 10
	default:
		return clip(firstLine(detail), 56), false, 0
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func countNonEmpty(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// countBodyLines counts non-empty lines after the first blank line (skips a
// header like "Contents of .:").
func countBodyLines(s string) int {
	parts := strings.SplitN(s, "\n\n", 2)
	if len(parts) == 2 {
		return countNonEmpty(parts[1])
	}
	return countNonEmpty(s)
}

// --- tool glyph mapping ------------------------------------------------------

func toolKind(tool string) string {
	switch tool {
	case "write_file", "edit_file", "apply_patch":
		return "write"
	case "bash":
		return "shell"
	case "update_plan":
		return "plan"
	default:
		return "read"
	}
}

func toolIcon(s styles, tool string) string {
	switch toolKind(tool) {
	case "write":
		return s.amb.Render("✎")
	case "shell":
		return s.amb.Render("❯")
	case "plan":
		return s.acc.Render("◷")
	default:
		return s.acc2.Render("◇")
	}
}

func toolLabel(tool string) string {
	if tool == "" {
		return "tool"
	}
	return tool
}

// --- small helpers -----------------------------------------------------------

var wordmark = []string{
	" ███████  ███████  ██████   ██████ ",
	"      ██  ██       ██   ██  ██   ██",
	"    ███   █████    ██████   ██   ██",
	"  ███     ██       ██   ██  ██   ██",
	" ███████  ███████  ██   ██   ██████",
}

func hasAssistant(rows []Row) bool {
	for _, r := range rows {
		if r.Kind == "assistant" {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func detailLines(s string, max int) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	ls := strings.Split(s, "\n")
	if len(ls) > max {
		ls = ls[:max]
		ls = append(ls, "…")
	}
	return ls
}

func wrap(text string, w int) []string {
	if w < 8 {
		w = 8
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for _, word := range words {
		switch {
		case cur == "":
			cur = word
		case len(cur)+1+len(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func padRight(s string, w int, fill lipgloss.Color) string {
	gap := w - lipgloss.Width(s)
	if gap < 0 {
		gap = 0
	}
	return s + lipgloss.NewStyle().Background(fill).Render(strings.Repeat(" ", gap))
}

func shortPath(p string) string {
	if p == "" {
		return "~"
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}
