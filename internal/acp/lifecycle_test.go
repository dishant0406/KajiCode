package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/hooks"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
	"github.com/dishant0406/KajiCode/internal/sessions"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// updateCollector records every session/update variant a client receives. The
// Conn dispatches each notification on its own goroutine (jsonrpc.go handleLine)
// while a request response returns independently, so a test that asserts a
// notification right after a Call can race the delivery goroutine. Every field
// is read/written only under mu, and tests use waitForVariant (not an immediate
// has) to await delivery.
type updateCollector struct {
	mu       sync.Mutex
	variants []string
	raw      []json.RawMessage
}

func (c *updateCollector) handle(_ context.Context, params json.RawMessage) {
	var probe struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &probe) != nil {
		return
	}
	c.mu.Lock()
	c.variants = append(c.variants, probe.Update.SessionUpdate)
	c.raw = append(c.raw, append(json.RawMessage(nil), params...))
	c.mu.Unlock()
}

func (c *updateCollector) has(variant string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.variants {
		if v == variant {
			return true
		}
	}
	return false
}

// snapshot returns copies of the recorded variants and raw updates.
func (c *updateCollector) snapshot() ([]string, []json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.variants...), append([]json.RawMessage(nil), c.raw...)
}

func (c *updateCollector) rawMessages() []json.RawMessage {
	_, raw := c.snapshot()
	return raw
}

func (c *updateCollector) seen() []string {
	variants, _ := c.snapshot()
	return variants
}

// waitForVariant blocks until variant arrives or the timeout elapses, so an
// assertion after a Call cannot race the notification's delivery goroutine.
func (c *updateCollector) waitForVariant(t *testing.T, variant string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.has(variant) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s, got %v", variant, c.seen())
}

// newCollectorHarness wires a client to an agent and captures every update.
func newCollectorHarness(t *testing.T, deps Deps) (*clientHarness, *updateCollector) {
	t.Helper()
	h := newHarness(t, deps)
	collector := &updateCollector{}
	h.client.HandleNotify(MethodSessionUpdate, collector.handle)
	return h, collector
}

func TestACPSessionNewAdvertisesConfigOptionsAndCommands(t *testing.T) {
	deps := testDeps(t)
	deps.Commands = func(string) []AvailableCommand {
		return []AvailableCommand{{Name: "config", Description: "Show config"}}
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if len(res.ConfigOptions) == 0 {
		t.Fatal("session/new returned no configOptions")
	}
	var model *SessionConfigOption
	for i := range res.ConfigOptions {
		if res.ConfigOptions[i].ID == configIDModel {
			model = &res.ConfigOptions[i]
		}
	}
	if model == nil || model.Category != configCategoryModel || model.CurrentValue == "" {
		t.Fatalf("model config option = %+v", model)
	}
	updates.waitForVariant(t, UpdateAvailableCommands)
	updates.waitForVariant(t, UpdateConfigOption)
}

func TestACPSessionLoadReplaysHistory(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	meta, err := deps.Store.Create(sessions.CreateInput{Title: "ACP session", Cwd: cwd})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := deps.Store.AppendEvents(meta.SessionID, []sessions.AppendEventInput{
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "user", "content": "first question"}},
		{Type: sessions.EventMessage, Payload: map[string]any{"role": "assistant", "content": "first answer"}},
	}); err != nil {
		t.Fatalf("append events: %v", err)
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: meta.SessionID, Cwd: cwd, McpServers: []McpServer{}}, &LoadSessionResult{}); err != nil {
		t.Fatalf("session/load: %v", err)
	}
	// The replay must include both user and assistant chunks.
	updates.waitForVariant(t, UpdateUserMessageChunk)
	updates.waitForVariant(t, UpdateAgentMessageChunk)
}

func TestACPSessionListAndDelete(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var list ListSessionsResult
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: cwd}, &list); err != nil {
		t.Fatalf("session/list: %v", err)
	}
	found := false
	for _, s := range list.Sessions {
		if s.SessionID == newRes.SessionID {
			found = true
		}
	}
	if !found {
		t.Fatalf("session/list did not include the created session: %+v", list.Sessions)
	}
	if err := h.client.Call(ctx, MethodSessionDelete, DeleteSessionParams{SessionID: newRes.SessionID}, &DeleteSessionResult{}); err != nil {
		t.Fatalf("session/delete: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionLoad, LoadSessionParams{SessionID: newRes.SessionID, Cwd: cwd, McpServers: []McpServer{}}, &LoadSessionResult{}); err == nil {
		t.Fatal("expected session/load to fail after delete")
	}
}

func TestACPClosedSessionCanBeReloaded(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionClose, CloseSessionParams{SessionID: newRes.SessionID}, &CloseSessionResult{}); err != nil {
		t.Fatalf("session/close: %v", err)
	}
	// Close detaches but keeps the session on disk, so it can be re-opened.
	if err := h.client.Call(ctx, MethodSessionResume, ResumeSessionParams{SessionID: newRes.SessionID, Cwd: cwd, McpServers: []McpServer{}}, &ResumeSessionResult{}); err != nil {
		t.Fatalf("session/resume after close: %v", err)
	}
}

func TestACPSetConfigOptionSwitchesAndRejectsUnknown(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var setRes SetSessionConfigOptionResult
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDMode, Value: string(agent.PermissionModeReadOnly)}, &setRes); err != nil {
		t.Fatalf("set config mode: %v", err)
	}
	if len(setRes.ConfigOptions) == 0 {
		t.Fatal("set_config_option must return the full option list")
	}
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDMode, Value: "unsafe"}, &SetSessionConfigOptionResult{}); err == nil {
		t.Fatal("unsafe mode must be rejected over ACP")
	}
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: "nope", Value: "x"}, &SetSessionConfigOptionResult{}); err == nil {
		t.Fatal("unknown config option must be rejected")
	}
}

func TestACPSlashCommandRoutesToDispatcher(t *testing.T) {
	deps := testDeps(t)
	var gotCommand, gotArgs string
	deps.RunCommand = func(_ context.Context, command, args, _, _ string) (string, bool, error) {
		gotCommand, gotArgs = command, args
		return "command output", true, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{
		SessionID: newRes.SessionID,
		Prompt:    []ContentBlock{TextBlock("/config extra args")},
	}, &promptRes); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if gotCommand != "config" || gotArgs != "extra args" {
		t.Fatalf("dispatcher got %q %q", gotCommand, gotArgs)
	}
	if promptRes.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q", promptRes.StopReason)
	}
	// The command output must reach the client as a message chunk.
	updates.waitForVariant(t, UpdateAgentMessageChunk)
	var sawOutput bool
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && strings.Contains(probe.Update.Content.Text, "command output") {
			sawOutput = true
		}
	}
	if !sawOutput {
		t.Fatal("command output was not streamed to the client")
	}
}

func TestACPUsageUpdateFromOnUsage(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveContextWindow = func(config.ProviderProfile) int { return 200_000 }
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		if opts.ContextWindow != 200_000 {
			t.Errorf("agent.Options.ContextWindow = %d, want the resolved window", opts.ContextWindow)
		}
		if opts.OnUsage != nil {
			opts.OnUsage(agent.Usage{PromptTokens: 7, CompletionTokens: 3})
		}
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	var found bool
	updates.waitForVariant(t, UpdateUsage)
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Used          int    `json:"used"`
				Size          int    `json:"size"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateUsage {
			if probe.Update.Used != 10 {
				t.Errorf("usage_update used = %d, want 10", probe.Update.Used)
			}
			if probe.Update.Size != 200_000 {
				t.Errorf("usage_update size = %d, want the resolved context window (200000)", probe.Update.Size)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected usage_update, got %v", updates.seen())
	}
}

// TestACPUnknownModelStillEnablesCompaction proves the compaction window falls
// back (modelregistry.AgentContextWindow) when the model's real window is
// unknown, while usage_update.size stays 0 (no misleading denominator).
func TestACPUnknownModelStillEnablesCompaction(t *testing.T) {
	deps := testDeps(t)
	deps.ResolveContextWindow = func(config.ProviderProfile) int { return 0 }
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		if opts.ContextWindow != modelregistry.FallbackContextWindow {
			t.Errorf("ContextWindow = %d, want fallback %d (compaction enabled)", opts.ContextWindow, modelregistry.FallbackContextWindow)
		}
		if opts.OnUsage != nil {
			opts.OnUsage(agent.Usage{PromptTokens: 1})
		}
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	updates.waitForVariant(t, UpdateUsage)
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Size          int    `json:"size"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateUsage && probe.Update.Size != 0 {
			t.Errorf("usage_update size = %d, want 0 for an unknown model (no denominator)", probe.Update.Size)
		}
	}
}

func TestACPInitializeAdvertisesSessionCapabilities(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res InitializeResult
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{ProtocolVersion: ProtocolVersion}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	caps := res.AgentCapabilities
	if caps.SessionCapabilities.List == nil || caps.SessionCapabilities.Resume == nil ||
		caps.SessionCapabilities.Close == nil || caps.SessionCapabilities.Delete == nil ||
		caps.SessionCapabilities.Fork == nil || caps.SessionCapabilities.AdditionalDirectories == nil {
		t.Fatalf("session capabilities not advertised: %+v", caps.SessionCapabilities)
	}
	if !caps.LoadSession || !caps.PromptCapabilities.Image || !caps.PromptCapabilities.EmbeddedContext {
		t.Fatalf("baseline capabilities regressed: %+v", caps)
	}
}

func TestACPSessionForkBranchesFromParent(t *testing.T) {
	deps := testDeps(t)
	cwd := t.TempDir()
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var parent NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: cwd, McpServers: []McpServer{}}, &parent); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// Give the parent one turn so the fork has history to inherit.
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: parent.SessionID, Prompt: []ContentBlock{TextBlock("hello")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	var fork ForkSessionResult
	if err := h.client.Call(ctx, MethodSessionFork, ForkSessionParams{SessionID: parent.SessionID, Cwd: cwd, McpServers: []McpServer{}}, &fork); err != nil {
		t.Fatalf("session/fork: %v", err)
	}
	if fork.SessionID == "" || fork.SessionID == parent.SessionID {
		t.Fatalf("fork must yield a distinct session id, got %q", fork.SessionID)
	}
	// Both the parent and the child are independently listable.
	var list ListSessionsResult
	if err := h.client.Call(ctx, MethodSessionList, ListSessionsParams{Cwd: cwd}, &list); err != nil {
		t.Fatalf("session/list: %v", err)
	}
	var haveParent, haveFork bool
	for _, s := range list.Sessions {
		if s.SessionID == parent.SessionID {
			haveParent = true
		}
		if s.SessionID == fork.SessionID {
			haveFork = true
		}
	}
	if !haveParent || !haveFork {
		t.Fatalf("list should contain parent and fork: parent=%v fork=%v", haveParent, haveFork)
	}
	// Forking an unknown session fails.
	if err := h.client.Call(ctx, MethodSessionFork, ForkSessionParams{SessionID: "nope", Cwd: cwd, McpServers: []McpServer{}}, &ForkSessionResult{}); err == nil {
		t.Fatal("forking an unknown session must fail")
	}
}

func TestACPRejectsUnsafePermissionModeAndAcceptsBypassAll(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// `unsafe` is a launch-time mode, not a session profile: it must never be
	// settable over the wire.
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDMode, Value: "unsafe"}, &SetSessionConfigOptionResult{}); err == nil {
		t.Error("set_config_option must reject unsafe")
	}
	if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: "unsafe"}, &SetSessionModeResult{}); err == nil {
		t.Error("set_mode must reject unsafe")
	}
	// `bypass-all` is an explicit opt-in a client may offer, like the TUI picker.
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDMode, Value: string(agent.PermissionModeBypassAll)}, &SetSessionConfigOptionResult{}); err != nil {
		t.Errorf("set_config_option must accept bypass-all: %v", err)
	}
	// The advertised option lists must offer bypass-all (and never unsafe).
	advertised := map[string]bool{}
	for _, m := range newRes.Modes.AvailableModes {
		advertised[m.ID] = true
	}
	modeOption := modeConfigOption(t, newRes.ConfigOptions)
	for _, value := range modelGroupsValues(t, modeOption) {
		advertised[value] = true
	}
	if advertised["unsafe"] {
		t.Error("unsafe must not be advertised")
	}
	if !advertised[string(agent.PermissionModeBypassAll)] {
		t.Error("bypass-all must be advertised")
	}
}

// modeConfigOption returns the permissions ("mode") config option.
func modeConfigOption(t *testing.T, options []SessionConfigOption) *SessionConfigOption {
	t.Helper()
	for i := range options {
		if options[i].ID == configIDMode {
			return &options[i]
		}
	}
	t.Fatal("no mode option")
	return nil
}

// modelGroupsValues flattens a select option's values, grouped or flat.
func modelGroupsValues(t *testing.T, option *SessionConfigOption) []string {
	t.Helper()
	var out []string
	for _, group := range modelGroupsOf(t, option) {
		for _, value := range group.Options {
			out = append(out, value.Value)
		}
	}
	return out
}

func TestACPSessionCloseCancelsInFlightTurn(t *testing.T) {
	deps := testDeps(t)
	started := make(chan struct{})
	deps.RunAgent = func(c context.Context, _ string, _ kajicoderuntime.Provider, _ agent.Options) (agent.Result, error) {
		close(started)
		<-c.Done() // block until the turn is cancelled
		return agent.Result{}, c.Err()
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	promptDone := make(chan error, 1)
	go func() {
		promptDone <- h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{})
	}()
	<-started
	if err := h.client.Call(ctx, MethodSessionClose, CloseSessionParams{SessionID: newRes.SessionID}, &CloseSessionResult{}); err != nil {
		t.Fatalf("session/close: %v", err)
	}
	select {
	case err := <-promptDone:
		if err != nil {
			t.Fatalf("cancelled prompt should return a result, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("session/close did not cancel the in-flight turn")
	}
}

func TestACPRejectsAdditionalDirectoryOutsideWorkspace(t *testing.T) {
	deps := testDeps(t)
	root := t.TempDir()
	deps.ResolveWorkspaceRoot = func(cwd string) (string, error) { return cwd, nil }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{
		Cwd:                   root,
		McpServers:            []McpServer{},
		AdditionalDirectories: []string{"/etc"},
	}, &NewSessionResult{}); err == nil {
		t.Fatal("an additional directory outside the workspace must be rejected")
	}
	// A subdirectory of the workspace is allowed.
	inside := filepath.Join(root, "sub")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{
		Cwd:                   root,
		McpServers:            []McpServer{},
		AdditionalDirectories: []string{inside},
	}, &NewSessionResult{}); err != nil {
		t.Fatalf("in-workspace additional directory rejected: %v", err)
	}
}

func TestACPElicitationRoutesAskUser(t *testing.T) {
	deps := testDeps(t)
	// The agent loop is replaced so the test drives OnAskUser directly through
	// the wiring (nil when the client offers no form, a form otherwise).
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		if opts.OnAskUser == nil {
			t.Error("expected OnAskUser to be wired when the client supports elicitation")
			return agent.Result{FinalAnswer: "none"}, nil
		}
		resp, err := opts.OnAskUser(context.Background(), agent.AskUserRequest{
			ToolCallID: "tc1",
			Header:     "Need input",
			Questions:  []agent.AskUserQuestion{{Question: "Which env?"}, {Question: "Which region?"}},
		})
		if err != nil {
			t.Errorf("ask user: %v", err)
		}
		if len(resp.Answers) != 2 || resp.Answers[0] != "staging" || resp.Answers[1] != "us-east" {
			t.Errorf("answers = %#v", resp.Answers)
		}
		return agent.Result{FinalAnswer: "ok"}, nil
	}

	h := newHarness(t, deps)
	defer h.stop()
	// The client implements elicitation/create and accepts.
	h.client.Handle(MethodElicitationCreate, func(_ context.Context, params json.RawMessage) (any, error) {
		var p CreateElicitationParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.Mode != "form" || p.SessionID == "" || len(p.RequestedSchema) == 0 {
			t.Errorf("bad elicitation params: %+v", p)
		}
		return CreateElicitationResult{
			Action: "accept",
			Content: map[string]json.RawMessage{
				"answer_0": json.RawMessage(`"staging"`),
				"answer_1": json.RawMessage(`"us-east"`),
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var initRes InitializeResult
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{Elicitation: &ElicitationCapabilities{Form: &struct{}{}}},
	}, &initRes); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
}

func TestACPRetitleEmitsSessionInfoUpdate(t *testing.T) {
	deps := testDeps(t)
	deps.Retitle = func(_ context.Context, sessionID, _ string) (string, error) {
		return "Fork And Compact", nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("/retitle")}}, &promptRes); err != nil {
		t.Fatalf("session/prompt /retitle: %v", err)
	}
	updates.waitForVariant(t, UpdateSessionInfo)
	var title string
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Title         string `json:"title"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateSessionInfo {
			title = probe.Update.Title
		}
	}
	if title != "Fork And Compact" {
		t.Fatalf("session_info_update title = %q", title)
	}
}

func TestACPModelSelectorUsesDiscoveredModels(t *testing.T) {
	deps := testDeps(t)
	deps.DiscoverModels = func(_ context.Context, _ config.ProviderProfile) ([]SessionConfigOptionValue, error) {
		return []SessionConfigOptionValue{
			{Value: "gpt-4.1"}, {Value: "gpt-4.1-mini"}, {Value: "o3"},
		}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	model := modelOption(t, res.ConfigOptions)
	// Every discovered model must be offered, not just the resolved one.
	have := map[string]bool{}
	for _, v := range modelOptionValues(t, model) {
		have[v.Value] = true
	}
	for _, want := range []string{"gpt-4.1", "gpt-4.1-mini", "o3"} {
		if !have[qualifyModel("fake", want)] {
			t.Fatalf("model %q missing from selector: %+v", want, model.Options)
		}
	}
}

// TestACPModelSelectorListsAllProviders proves the model selector offers every
// usable provider's models (grouped by provider), not just the active one, and
// that choosing a model from another provider switches the provider for the run.
func TestACPModelSelectorListsAllProviders(t *testing.T) {
	deps := testDeps(t)
	deps.Providers = func() []config.ProviderProfile {
		return []config.ProviderProfile{{Name: "fake"}, {Name: "other"}}
	}
	deps.DiscoverModels = func(_ context.Context, profile config.ProviderProfile) ([]SessionConfigOptionValue, error) {
		switch profile.Name {
		case "other":
			return []SessionConfigOptionValue{{Value: "other-1"}}, nil
		default:
			return []SessionConfigOptionValue{{Value: "fake-1"}}, nil
		}
	}
	var captured agent.Options
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		captured = opts
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	model := modelOption(t, res.ConfigOptions)
	groups := modelGroupsOf(t, model)
	providers := map[string]bool{}
	for _, g := range groups {
		providers[g.Group] = true
	}
	if !providers["fake"] || !providers["other"] {
		t.Fatalf("selector groups = %v, want both fake and other", providers)
	}
	// Switch to the other provider's model via its qualified value.
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{
		SessionID: res.SessionID, ConfigID: configIDModel, Value: qualifyModel("other", "other-1"),
	}, &SetSessionConfigOptionResult{}); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if captured.ProviderName != "other" {
		t.Fatalf("run provider = %q, want other (provider did not switch)", captured.ProviderName)
	}
	if captured.Model != "other-1" {
		t.Fatalf("run model = %q, want other-1", captured.Model)
	}
}

// modelOption returns the model session config option, failing if absent.
func modelOption(t *testing.T, options []SessionConfigOption) *SessionConfigOption {
	t.Helper()
	for i := range options {
		if options[i].ID == configIDModel {
			return &options[i]
		}
	}
	t.Fatal("no model option")
	return nil
}

// modelGroupsOf decodes a model option's options into groups, whether the wire
// form was grouped or flat. The result travels through JSON, so it is re-decoded
// rather than type-asserted.
func modelGroupsOf(t *testing.T, option *SessionConfigOption) []ConfigOptionGroup {
	t.Helper()
	raw, err := json.Marshal(option.Options)
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	var groups []ConfigOptionGroup
	if err := json.Unmarshal(raw, &groups); err == nil && len(groups) > 0 && groups[0].Group != "" {
		return groups
	}
	var flat []SessionConfigOptionValue
	if err := json.Unmarshal(raw, &flat); err == nil && len(flat) > 0 {
		return []ConfigOptionGroup{{Options: flat}}
	}
	return nil
}

// modelOptionValues flattens a model option's groups into their values.
func modelOptionValues(t *testing.T, option *SessionConfigOption) []SessionConfigOptionValue {
	t.Helper()
	var out []SessionConfigOptionValue
	for _, g := range modelGroupsOf(t, option) {
		out = append(out, g.Options...)
	}
	return out
}

func TestACPSelfCorrectAndProfileReachAgentOptions(t *testing.T) {
	deps := testDeps(t)
	var captured agent.Options
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		captured = opts
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// Default: self-correct off, no profile policy.
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if captured.SelfCorrect != nil || captured.Profile != nil {
		t.Fatalf("defaults changed the loop: SelfCorrect=%v Profile=%v", captured.SelfCorrect != nil, captured.Profile != nil)
	}
	// Enable both, then a second turn must carry them.
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDSelfCorrect, Value: "full"}, &SetSessionConfigOptionResult{}); err != nil {
		t.Fatalf("set selfcorrect: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDProfile, Value: "fast"}, &SetSessionConfigOptionResult{}); err != nil {
		t.Fatalf("set profile: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("again")}}, &PromptResult{}); err != nil {
		t.Fatalf("second prompt: %v", err)
	}
	if captured.SelfCorrect == nil {
		t.Fatal("selfcorrect=full did not reach agent.Options.SelfCorrect")
	}
	if captured.Profile == nil || captured.Profile.Name != "fast" {
		t.Fatalf("profile=fast did not reach agent.Options.Profile: %+v", captured.Profile)
	}
	// The profile's own knobs must be applied, not just its policy: fast sets a
	// 30-turn budget and low effort. The default testDeps MaxTurns is 4.
	if captured.MaxTurns != 30 {
		t.Fatalf("profile=fast did not displace the turn budget: MaxTurns=%d", captured.MaxTurns)
	}
	if captured.ReasoningEffort != "low" {
		t.Fatalf("profile=fast did not fill effort: %q", captured.ReasoningEffort)
	}
	// Unknown values are rejected.
	if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDProfile, Value: "nope"}, &SetSessionConfigOptionResult{}); err == nil {
		t.Fatal("unknown profile must be rejected")
	}
}

func TestACPRefreshModelsReemitsConfigOptions(t *testing.T) {
	deps := testDeps(t)
	deps.Providers = func() []config.ProviderProfile {
		return []config.ProviderProfile{{Name: "fake"}, {Name: "other"}}
	}
	calls := map[string]int{}
	deps.DiscoverModels = func(_ context.Context, profile config.ProviderProfile) ([]SessionConfigOptionValue, error) {
		calls[profile.Name]++
		return []SessionConfigOptionValue{{Value: profile.Name + "-1"}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	before := calls["other"]
	if err := h.client.Call(ctx, MethodKajiCodeRefreshModels, KajiCodeRefreshModelsParams{SessionID: res.SessionID}, &KajiCodeRefreshModelsResult{}); err != nil {
		t.Fatalf("refresh_models: %v", err)
	}
	if calls["other"] <= before {
		t.Fatal("refresh_models did not re-run discovery for every provider")
	}
	updates.waitForVariant(t, UpdateConfigOption)
}

// TestACPRefreshModelsSlashCommand proves the /refresh-models slash command — the
// client-reachable refresh — re-runs discovery across every provider, streams a
// summary, and re-emits config_option_update, matching the vendor method.
func TestACPRefreshModelsSlashCommand(t *testing.T) {
	deps := testDeps(t)
	deps.Providers = func() []config.ProviderProfile {
		return []config.ProviderProfile{{Name: "fake"}, {Name: "other"}}
	}
	calls := map[string]int{}
	deps.DiscoverModels = func(_ context.Context, profile config.ProviderProfile) ([]SessionConfigOptionValue, error) {
		calls[profile.Name]++
		return []SessionConfigOptionValue{{Value: profile.Name + "-1"}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	before := calls["other"]
	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/refresh-models")}}, &promptRes); err != nil {
		t.Fatalf("session/prompt /refresh-models: %v", err)
	}
	if promptRes.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q", promptRes.StopReason)
	}
	if calls["other"] <= before {
		t.Fatal("/refresh-models did not re-run discovery for every provider")
	}
	updates.waitForVariant(t, UpdateConfigOption)
	var summarySeen bool
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && strings.Contains(probe.Update.Content.Text, "Refreshed models") {
			summarySeen = true
		}
	}
	if !summarySeen {
		t.Fatal("/refresh-models did not stream a refresh summary")
	}
}

// TestACPSkillsListsWorkspaceSkills proves /skills lists the session's merged
// skill set (including project skills), matching the model's own catalog — not
// just the global-only `kajicode skills list`.
func TestACPSkillsListsWorkspaceSkills(t *testing.T) {
	deps := testDeps(t)
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		return Workspace{Skills: []agent.SkillInfo{
			{Name: "demo-project-skill", Description: "A project-local skill."},
			{Name: "global-skill", Description: "A global skill."},
		}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var promptRes PromptResult
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/skills")}}, &promptRes); err != nil {
		t.Fatalf("session/prompt /skills: %v", err)
	}
	if promptRes.StopReason != StopEndTurn {
		t.Fatalf("stopReason = %q", promptRes.StopReason)
	}
	var text string
	updates.waitForVariant(t, UpdateAgentMessageChunk)
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateAgentMessageChunk {
			text += probe.Update.Content.Text
		}
	}
	if !strings.Contains(text, "demo-project-skill") {
		t.Fatalf("/skills did not list the project skill: %q", text)
	}
	if !strings.Contains(text, "global-skill") {
		t.Fatalf("/skills did not list the global skill: %q", text)
	}
	if !strings.Contains(text, "Skills (2)") {
		t.Fatalf("/skills header = %q, want count 2", text)
	}
}

// TestACPSkillsEmptyState proves a skill-less workspace gets a clear message, not
// an empty response.
func TestACPSkillsEmptyState(t *testing.T) {
	deps := testDeps(t)
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		return Workspace{}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/skills")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt /skills: %v", err)
	}
	updates.waitForVariant(t, UpdateAgentMessageChunk)
	var text string
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateAgentMessageChunk {
			text += probe.Update.Content.Text
		}
	}
	if !strings.Contains(text, "No skills installed") {
		t.Fatalf("/skills empty state = %q", text)
	}
}

// TestACPSkillsListFormUsesMergedSet proves `/skills list` takes the same
// session-scoped path as bare `/skills` (not the global-only CLI fallback).
func TestACPSkillsListFormUsesMergedSet(t *testing.T) {
	deps := testDeps(t)
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		return Workspace{Skills: []agent.SkillInfo{{Name: "project-only-skill"}}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/skills list")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt /skills list: %v", err)
	}
	updates.waitForVariant(t, UpdateAgentMessageChunk)
	var text string
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateAgentMessageChunk {
			text += probe.Update.Content.Text
		}
	}
	if !strings.Contains(text, "project-only-skill") {
		t.Fatalf("/skills list did not use the merged set: %q", text)
	}
}

// TestACPSkillsPermissionMarkers proves the listing uses the same [deny]/[prompt]
// markers as the model catalog.
func TestACPSkillsPermissionMarkers(t *testing.T) {
	deps := testDeps(t)
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		return Workspace{Skills: []agent.SkillInfo{
			{Name: "denied-skill", Permission: "deny"},
			{Name: "prompt-skill", Permission: "prompt"},
			{Name: "allowed-skill", Permission: "allow"},
		}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/skills")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt /skills: %v", err)
	}
	updates.waitForVariant(t, UpdateAgentMessageChunk)
	var text string
	for _, raw := range updates.rawMessages() {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateAgentMessageChunk {
			text += probe.Update.Content.Text
		}
	}
	if !strings.Contains(text, "denied-skill [deny]") {
		t.Fatalf("missing [deny] marker: %q", text)
	}
	if !strings.Contains(text, "prompt-skill [prompt]") {
		t.Fatalf("missing [prompt] marker: %q", text)
	}
	if strings.Contains(text, "allowed-skill [") {
		t.Fatalf("allow skill should have no marker: %q", text)
	}
}

// TestACPSkillCommandExpandsBody proves /name args resolves an installed skill,
// running a turn whose prompt is the skill body plus the request (not raw prose),
// matching the TUI's skill dispatch.
func TestACPSkillCommandExpandsBody(t *testing.T) {
	deps := testDeps(t)
	var capturedPrompt string
	deps.RunAgent = func(_ context.Context, prompt string, _ kajicoderuntime.Provider, _ agent.Options) (agent.Result, error) {
		capturedPrompt = prompt
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	deps.ExpandSkill = func(workspaceRoot, name, args string) (string, bool) {
		if name != "code-review" {
			return "", false
		}
		return "SKILL BODY: review the diff.\n\n" + args, true
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/code-review on this diff")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if !strings.Contains(capturedPrompt, "SKILL BODY: review the diff.") {
		t.Fatalf("skill body was not inlined into the prompt: %q", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "on this diff") {
		t.Fatalf("request was not appended: %q", capturedPrompt)
	}
}

// TestACSPSkillCommandUnknownFallsThrough proves an unknown /name is not consumed
// by the skill hook — it runs as an ordinary prompt with the raw text.
func TestACPSkillCommandUnknownFallsThrough(t *testing.T) {
	deps := testDeps(t)
	var capturedPrompt string
	deps.RunAgent = func(_ context.Context, prompt string, _ kajicoderuntime.Provider, _ agent.Options) (agent.Result, error) {
		capturedPrompt = prompt
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	deps.ExpandSkill = func(string, string, string) (string, bool) { return "", false }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("/no-such-skill hello")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if !strings.Contains(capturedPrompt, "/no-such-skill hello") {
		t.Fatalf("unknown /name should fall through to the model verbatim: %q", capturedPrompt)
	}
}

// TestACPRunTurnWiresWorkspaceCollaborators proves every collaborator the
// workspace carries (harness, deferred tools, hooks, file tracker, session
// store, MCP instructions, sub-agents, learning) reaches agent.Options — i.e.
// ACP runs the same feature set as exec/TUI.
func TestACPRunTurnWiresWorkspaceCollaborators(t *testing.T) {
	deps := testDeps(t)
	hookStub := hooks.NewDispatcher(hooks.DispatcherOptions{})
	taskStub := stubTaskCompletions{}
	deps.BuildWorkspace = func(_, _ string, _ config.ResolvedConfig) (Workspace, error) {
		return Workspace{
			Registry:        tools.NewRegistry(),
			Harness:         config.HarnessConfig{},
			DeferThreshold:  42,
			Hooks:           hookStub,
			FileTracker:     tools.NewFileTracker(),
			SessionStore:    tools.SessionStore(nil),
			MCPInstructions: []agent.MCPInstructions{{Server: "s", Instructions: "i"}},
			Agents:          []agent.AgentInfo{{Name: "a", WhenToUse: "w"}},
			TaskCompletions: taskStub,
		}, nil
	}
	deps.BuildLearning = func(string, config.ResolvedConfig, kajicoderuntime.Provider, string) *agent.LearningEngine {
		return &agent.LearningEngine{}
	}
	var captured agent.Options
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
		captured = opts
		return agent.Result{FinalAnswer: "ok"}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt: %v", err)
	}
	if captured.DeferThreshold != 42 {
		t.Errorf("DeferThreshold = %d, want 42", captured.DeferThreshold)
	}
	if captured.Hooks == nil {
		t.Error("Hooks not wired")
	}
	if captured.FileTracker == nil {
		t.Error("FileTracker not wired")
	}
	if captured.Learning == nil {
		t.Error("Learning not wired")
	}
	if len(captured.MCPInstructions) != 1 || len(captured.Agents) != 1 {
		t.Errorf("MCP/Agents not wired: mcp=%v agents=%v", captured.MCPInstructions, captured.Agents)
	}
	if captured.TaskCompletions == nil {
		t.Error("TaskCompletions not wired")
	}
}

type stubTaskCompletions struct{}

func (stubTaskCompletions) DrainCompletedTasks() []string { return nil }

// TestACPWorkspaceBuiltOnceAndClosedOnce proves the workspace is built once per
// session and its Close runs exactly once when the session is dropped.
func TestACPWorkspaceBuiltOnceAndClosedOnce(t *testing.T) {
	deps := testDeps(t)
	builds, closes := 0, 0
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		builds++
		return Workspace{Registry: tools.NewRegistry(), Close: func() { closes++ }}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
			t.Fatalf("prompt %d: %v", i, err)
		}
	}
	if builds != 1 {
		t.Fatalf("workspace built %d times, want 1 (cached per session)", builds)
	}
	if err := h.client.Call(ctx, MethodSessionClose, CloseSessionParams{SessionID: res.SessionID}, &CloseSessionResult{}); err != nil {
		t.Fatalf("session/close: %v", err)
	}
	if closes != 1 {
		t.Fatalf("workspace Close called %d times, want 1", closes)
	}
}

// TestACPWorkspaceClosedOnDelete proves session/delete also releases the
// workspace (no MCP/sub-agent leak).
func TestACPWorkspaceClosedOnDelete(t *testing.T) {
	deps := testDeps(t)
	closes := 0
	deps.BuildWorkspace = func(string, string, config.ResolvedConfig) (Workspace, error) {
		return Workspace{Registry: tools.NewRegistry(), Close: func() { closes++ }}, nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: res.SessionID, Prompt: []ContentBlock{TextBlock("hi")}}, &PromptResult{}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionDelete, DeleteSessionParams{SessionID: res.SessionID}, &DeleteSessionResult{}); err != nil {
		t.Fatalf("session/delete: %v", err)
	}
	if closes != 1 {
		t.Fatalf("workspace Close called %d times, want 1", closes)
	}
}
