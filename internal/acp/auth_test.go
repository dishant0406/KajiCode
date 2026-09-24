package acp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestACPInitializeAdvertisesAuthMethodsAndLogout(t *testing.T) {
	deps := testDeps(t)
	deps.AuthMethods = func(terminal bool) []AuthMethod {
		if !terminal {
			return nil
		}
		return []AuthMethod{{ID: "login-x", Name: "Sign in with X", Type: "terminal", Args: []string{"auth", "login", "x"}}}
	}
	deps.Logout = func(context.Context) error { return nil }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res InitializeResult
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{Auth: ClientAuthCapabilities{Terminal: true}},
	}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.AgentCapabilities.Auth.Logout == nil {
		t.Fatal("auth.logout must be advertised when a logout handler is wired")
	}
	if len(res.AuthMethods) != 1 || res.AuthMethods[0].Type != "terminal" || res.AuthMethods[0].Args[0] != "auth" {
		t.Fatalf("authMethods = %+v", res.AuthMethods)
	}
}

func TestACPInitializeOmitsTerminalMethodsWithoutCapability(t *testing.T) {
	deps := testDeps(t)
	deps.AuthMethods = func(terminal bool) []AuthMethod {
		if !terminal {
			return nil
		}
		return []AuthMethod{{ID: "login-x", Type: "terminal"}}
	}
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res InitializeResult
	// No client auth.terminal capability -> no terminal methods advertised.
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{ProtocolVersion: ProtocolVersion}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if len(res.AuthMethods) != 0 {
		t.Fatalf("terminal methods must not be advertised without the capability: %+v", res.AuthMethods)
	}
}

func TestACPAuthenticateAndLogout(t *testing.T) {
	deps := testDeps(t)
	var authenticated, loggedOut bool
	deps.Authenticate = func(_ context.Context, methodID string) error {
		if methodID != "m1" {
			t.Errorf("methodID = %q", methodID)
		}
		authenticated = true
		return nil
	}
	deps.Logout = func(context.Context) error { loggedOut = true; return nil }
	h := newHarness(t, deps)
	defer h.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.Call(ctx, MethodAuthenticate, AuthenticateParams{MethodID: "m1"}, &AuthenticateResult{}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if !authenticated {
		t.Fatal("authenticate handler was not invoked")
	}
	if err := h.client.Call(ctx, MethodLogout, LogoutParams{}, &LogoutResult{}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !loggedOut {
		t.Fatal("logout handler was not invoked")
	}
	// An empty methodId is rejected.
	if err := h.client.Call(ctx, MethodAuthenticate, AuthenticateParams{}, &AuthenticateResult{}); err == nil {
		t.Fatal("empty methodId must be rejected")
	}
}

func TestACPAddProviderViaElicitation(t *testing.T) {
	deps := testDeps(t)
	var got map[string]string
	deps.ProviderAdd = func(_ context.Context, fields map[string]string) (string, error) {
		got = fields
		return "Added provider demo", nil
	}
	h := newHarness(t, deps)
	defer h.stop()
	// The client renders the elicitation form and submits. The form MUST NOT ask
	// for secrets (ACP forbids API keys in form mode), so the schema carries none.
	h.client.Handle(MethodElicitationCreate, func(_ context.Context, params json.RawMessage) (any, error) {
		var p CreateElicitationParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.SessionID == "" {
			t.Error("add-provider elicitation must be session-scoped")
		}
		if strings.Contains(string(p.RequestedSchema), "apiKey") || strings.Contains(string(p.RequestedSchema), "key") {
			t.Errorf("form must not request secrets: %s", p.RequestedSchema)
		}
		return CreateElicitationResult{
			Action:  "accept",
			Content: map[string]json.RawMessage{"name": json.RawMessage(`"demo"`)},
		}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.client.Call(ctx, MethodInitialize, InitializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: ClientCapabilities{Elicitation: &ElicitationCapabilities{Form: &struct{}{}}},
	}, &InitializeResult{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var newRes NewSessionResult
	if err := h.client.Call(ctx, MethodSessionNew, NewSessionParams{Cwd: t.TempDir(), McpServers: []McpServer{}}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if err := h.client.Call(ctx, MethodSessionPrompt, PromptParams{SessionID: newRes.SessionID, Prompt: []ContentBlock{TextBlock("/add-provider")}}, &PromptResult{}); err != nil {
		t.Fatalf("session/prompt /add-provider: %v", err)
	}
	if got["name"] != "demo" {
		t.Fatalf("ProviderAdd got %#v", got)
	}
	if _, ok := got["apiKey"]; ok {
		t.Fatal("apiKey must never be collected over the wire")
	}
}
