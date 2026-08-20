package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

func visionTestRegistry(t *testing.T) modelregistry.Registry {
	t.Helper()
	reg, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return reg
}

func TestFirstVisionCapableProvider(t *testing.T) {
	reg := visionTestRegistry(t)
	// gpt-4.1 and claude-sonnet are vision-capable; a text-only custom id is not
	// confirmed, so it is skipped.
	resolved := config.ResolvedConfig{
		Providers: []config.ProviderProfile{
			{Name: "textonly", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-3.5-turbo"},
			{Name: "vision", ProviderKind: config.ProviderKindAnthropic, Model: "claude-sonnet-4-5"},
		},
	}
	p, ok := firstVisionCapableProvider(resolved, reg)
	if !ok {
		t.Fatal("expected a vision-capable provider")
	}
	if p.Name != "vision" {
		t.Fatalf("expected vision provider, got %+v", p)
	}
}

func TestFirstVisionCapableProviderNoneAvailable(t *testing.T) {
	reg := visionTestRegistry(t)
	resolved := config.ResolvedConfig{
		Providers: []config.ProviderProfile{
			{Name: "a", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-3.5-turbo"},
			{Name: "b", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-3.5-turbo"},
		},
	}
	if _, ok := firstVisionCapableProvider(resolved, reg); ok {
		t.Fatal("expected no vision-capable provider when none configured")
	}
}

func TestFirstVisionCapableProviderEmpty(t *testing.T) {
	reg := visionTestRegistry(t)
	if _, ok := firstVisionCapableProvider(config.ResolvedConfig{}, reg); ok {
		t.Fatal("expected no vision provider with empty config")
	}
}

func TestInstallVisionProvider(t *testing.T) {
	var buf bytes.Buffer
	resolved := config.ResolvedConfig{
		Provider: config.ProviderProfile{Name: "openai", Model: "gpt-4.1"},
	}
	profile := config.ProviderProfile{Name: "anthropic", Model: "claude-sonnet-4-5"}
	if err := installVisionProvider(&resolved, profile, &buf); err != nil {
		t.Fatal(err)
	}
	if resolved.Provider.Name != "anthropic" || resolved.Provider.Model != "claude-sonnet-4-5" {
		t.Fatalf("provider not swapped to vision profile: %+v", resolved.Provider)
	}
	if !strings.Contains(buf.String(), "claude-sonnet-4-5") {
		t.Fatalf("expected vision-routing notice, got %q", buf.String())
	}
}
