package cli

import (
	"strings"
	"testing"
)

func TestACPAuthMethodsGatedOnTerminalCapability(t *testing.T) {
	if got := acpAuthMethods(false); got != nil {
		t.Fatalf("no terminal capability must advertise no methods, got %+v", got)
	}
	methods := acpAuthMethods(true)
	if len(methods) == 0 {
		t.Fatal("expected terminal auth methods for OAuth providers")
	}
	for _, m := range methods {
		if m.Type != "terminal" {
			t.Errorf("%s: type = %q, want terminal", m.ID, m.Type)
		}
		if !strings.HasPrefix(m.ID, "login-") {
			t.Errorf("unexpected method id %q", m.ID)
		}
		if len(m.Args) != 3 || m.Args[0] != "auth" || m.Args[1] != "login" {
			t.Errorf("%s: args = %v, want auth login <provider>", m.ID, m.Args)
		}
	}
}

func TestACPResolveProviderCatalogID(t *testing.T) {
	// A known catalog name resolves to that descriptor.
	id, err := resolveProviderCatalogID("openrouter", "")
	if err != nil || id != "openrouter" {
		t.Fatalf("openrouter -> %q, %v", id, err)
	}
	// An unknown name uses the default custom entry...
	id, err = resolveProviderCatalogID("my-endpoint", "")
	if err != nil || id != "custom-openai-compatible" {
		t.Fatalf("unknown -> %q, %v", id, err)
	}
	// ...or the explicit custom entry the caller chose.
	id, err = resolveProviderCatalogID("my-anthropic", "custom-anthropic-compatible")
	if err != nil || id != "custom-anthropic-compatible" {
		t.Fatalf("anthropic custom -> %q, %v", id, err)
	}
	// An unknown custom entry is an error, not a silent fallback.
	if _, err := resolveProviderCatalogID("x", "not-a-real-entry"); err == nil {
		t.Fatal("unknown custom entry must error")
	}
}

func TestACPErrFromWriterStripsPrefix(t *testing.T) {
	if got := errFromWriter("[kajicode] provider catalog id is required\n").Error(); got != "provider catalog id is required" {
		t.Fatalf("errFromWriter = %q", got)
	}
	if got := errFromWriter("   ").Error(); got != "command failed" {
		t.Fatalf("empty output -> %q", got)
	}
}
