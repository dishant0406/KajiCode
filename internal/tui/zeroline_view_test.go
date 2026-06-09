package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroline"
)

func newZerolineModel() model {
	m := newModel(context.Background(), Options{
		Skin:         "zeroline",
		ThemeDark:    true,
		Cwd:          "/home/you/src/zero",
		ProviderName: "anthropic",
		ModelName:    "claude-sonnet-4.5",
	})
	m.width, m.height = 100, 30
	m.booted = true // skip the boot splash animation; these tests cover home/chat
	return m
}

func TestZerolineHomeThenChat(t *testing.T) {
	m := newZerolineModel()

	// Home is shown until the first turn (showSplash true).
	if !strings.Contains(m.View(), "std-lib-first") {
		t.Fatal("expected Zen home before first turn")
	}

	// Simulate an in-progress run with a live transcript.
	m.showSplash = false
	m.pending = true
	m.transcript = []transcriptRow{
		{kind: rowUser, text: "refactor internal/agent/loop.go"},
		{kind: rowToolCall, id: "t1", tool: "grep", text: "tool call: grep", detail: "pattern: case"},
		{kind: rowToolResult, id: "t1", tool: "grep", status: tools.StatusOK, detail: "3 matches"},
	}
	chat := m.View()
	for _, want := range []string{"WORKING", "❯", "grep", "claude-sonnet-4.5", "thinking"} {
		if !strings.Contains(chat, want) {
			t.Errorf("chat view missing %q", want)
		}
	}

	// once tokens stream, the live text shows instead of the thinking line
	m.streamingText = "here is the streamed answer"
	if s := m.View(); !strings.Contains(s, "here is the streamed answer") {
		t.Error("streaming text not rendered in chat view")
	}
}

func TestZerolineAskUserRender(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
		answer:  func([]string) {},
	})
	next := updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("expected ask_user prompt to be pending")
	}

	out := next.View()
	for _, want := range []string{"Which framework?", "React", "Vue", "question 1 of 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("zeroline ask_user view missing %q", want)
		}
	}
	// The misleading "working…"/"thinking" spinner must be suppressed while a
	// questionnaire is pending.
	if strings.Contains(out, "thinking") {
		t.Error("zeroline ask_user view should suppress the thinking spinner")
	}
}

func TestZerolinePermissionRender(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "edit_file", SideEffect: "write"},
	}
	out := m.View()
	for _, want := range []string{"BLOCKED", "PERMISSION", "edit_file", "allow", "deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("permission view missing %q", want)
		}
	}
}

func TestZerolinePermissionMouseClick(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	var got []permissionDecision
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "edit_file", SideEffect: "write"},
		decide:  func(d agent.PermissionDecision) { got = append(got, permissionDecision(d.Action)) },
	}
	geo := zeroline.PermLayout(m.width, m.height)
	if !geo.Active {
		t.Fatal("expected active permission geometry at 100x30")
	}
	click := tea.MouseMsg{X: geo.Allow.X + geo.Allow.W/2, Y: geo.Allow.Y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
	updated, _ := m.Update(click)
	next := updated.(model)
	if len(got) != 1 || got[0] != permissionDecisionAllow {
		t.Fatalf("mouse click on allow should resolve allow, got %#v", got)
	}
	if next.pendingPermission != nil {
		t.Fatal("permission should clear after a resolving click")
	}
}

func TestZerolineRunningToolSuppressesThinking(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	// A tool call with no matching result → a running card; the separate
	// "thinking…" line must be suppressed (not stacked under the running card).
	m.transcript = []transcriptRow{
		{kind: rowToolCall, id: "t1", tool: "bash", text: "tool call: bash", detail: "go test ./..."},
	}
	if out := m.View(); strings.Contains(out, "thinking") {
		t.Error("a running tool card should suppress the separate 'thinking' line")
	}
}

func TestZerolineDrawerOpenClose(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	// ctrl+s opens the drawer and renders the panel
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m2 := updated.(model)
	if !m2.drawerOpen {
		t.Fatal("ctrl+s should open the sessions drawer")
	}
	if !strings.Contains(m2.View(), "sessions") {
		t.Error("open drawer should render the panel")
	}
	// esc closes it
	updated2, _ := m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated2.(model).drawerOpen {
		t.Fatal("esc should close the sessions drawer")
	}
}

func TestZerolineDrawerCtrlCNotSwallowed(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m2 := upd.(model)
	if !m2.drawerOpen {
		t.Fatal("ctrl+s should open the drawer")
	}
	if _, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("Ctrl+C must not be swallowed by the drawer (it should quit)")
	}
}

func TestZerolineDrawerNotOpenedOnHome(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = true // home page
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if upd.(model).drawerOpen {
		t.Error("ctrl+s on the home page must not open the drawer (nothing renders it there)")
	}
}

func TestZerolineThemeKeys(t *testing.T) {
	m := newZerolineModel()
	// digit selects theme when input empty
	nm, handled := m.handleZerolineKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if !handled || nm.themeVariant != 2 {
		t.Fatalf("theme select failed handled=%v variant=%d", handled, nm.themeVariant)
	}
	// ctrl+l toggles light/dark
	nm2, handled2 := nm.handleZerolineKeys(tea.KeyMsg{Type: tea.KeyCtrlL})
	if !handled2 || nm2.themeDark == nm.themeDark {
		t.Fatal("ctrl+l did not toggle dark")
	}
}
