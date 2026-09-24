package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// updateCollector records every session/update variant a client receives.
type updateCollector struct {
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
	c.variants = append(c.variants, probe.Update.SessionUpdate)
	c.raw = append(c.raw, append(json.RawMessage(nil), params...))
}

func (c *updateCollector) has(variant string) bool {
	for _, v := range c.variants {
		if v == variant {
			return true
		}
	}
	return false
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
	deps.Commands = []AvailableCommand{{Name: "config", Description: "Show config"}}
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
	if !updates.has(UpdateAvailableCommands) {
		t.Fatalf("expected available_commands_update, got %v", updates.variants)
	}
	if !updates.has(UpdateConfigOption) {
		t.Fatalf("expected config_option_update, got %v", updates.variants)
	}
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
	if !updates.has(UpdateUserMessageChunk) || !updates.has(UpdateAgentMessageChunk) {
		t.Fatalf("load did not replay history: %v", updates.variants)
	}
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
	var sawOutput bool
	for _, raw := range updates.raw {
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
	deps.RunAgent = func(_ context.Context, _ string, _ kajicoderuntime.Provider, opts agent.Options) (agent.Result, error) {
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
	for _, raw := range updates.raw {
		var probe struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Used          int    `json:"used"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Update.SessionUpdate == UpdateUsage && probe.Update.Used == 10 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected usage_update used=10, got %v", updates.variants)
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

func TestACPRejectsUnconfinedPermissionModes(t *testing.T) {
	h := newHarness(t, testDeps(t))
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	// Both `unsafe` and `bypass-all` disable the sandbox, so neither may be set
	// over the wire — on the config-option path or the legacy modes path.
	for _, mode := range []string{"unsafe", "bypass-all"} {
		if err := h.client.Call(ctx, MethodSessionSetConfigOption, SetSessionConfigOptionParams{SessionID: newRes.SessionID, ConfigID: configIDMode, Value: mode}, &SetSessionConfigOptionResult{}); err == nil {
			t.Errorf("set_config_option must reject %q", mode)
		}
		if err := h.client.Call(ctx, MethodSessionSetMode, SetSessionModeParams{SessionID: newRes.SessionID, ModeID: mode}, &SetSessionModeResult{}); err == nil {
			t.Errorf("set_mode must reject %q", mode)
		}
	}
	// The advertised mode list must not offer them either.
	for _, m := range newRes.Modes.AvailableModes {
		if m.ID == "unsafe" || m.ID == "bypass-all" {
			t.Errorf("unconfined mode %q must not be advertised", m.ID)
		}
	}
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
	if !updates.has(UpdateSessionInfo) {
		t.Fatalf("expected session_info_update, got %v", updates.variants)
	}
	var title string
	for _, raw := range updates.raw {
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
	var model *SessionConfigOption
	for i := range res.ConfigOptions {
		if res.ConfigOptions[i].ID == configIDModel {
			model = &res.ConfigOptions[i]
		}
	}
	if model == nil {
		t.Fatal("no model option")
	}
	// Every discovered model must be offered, not just the resolved one.
	have := map[string]bool{}
	for _, v := range model.Options {
		have[v.Value] = true
	}
	for _, want := range []string{"gpt-4.1", "gpt-4.1-mini", "o3"} {
		if !have[want] {
			t.Fatalf("model %q missing from selector: %+v", want, model.Options)
		}
	}
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
	calls := 0
	deps.DiscoverModels = func(_ context.Context, _ config.ProviderProfile) ([]SessionConfigOptionValue, error) {
		calls++
		return []SessionConfigOptionValue{{Value: "m1"}, {Value: "m2"}}, nil
	}
	h, updates := newCollectorHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &res); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	before := calls
	if err := h.client.Call(ctx, MethodKajiCodeRefreshModels, KajiCodeRefreshModelsParams{SessionID: res.SessionID}, &KajiCodeRefreshModelsResult{}); err != nil {
		t.Fatalf("refresh_models: %v", err)
	}
	if calls <= before {
		t.Fatal("refresh_models did not re-run discovery")
	}
	if !updates.has(UpdateConfigOption) {
		t.Fatal("refresh_models did not emit config_option_update")
	}
}
