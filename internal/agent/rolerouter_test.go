package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

func testRoleRouter(t *testing.T) RoleRouter {
	t.Helper()
	registry, err := modelregistry.NewRegistry(modelregistry.DefaultModelEntries())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return RoleRouter{
		Resolved: config.ResolvedConfig{
			ActiveProvider: "openai",
			Provider: config.ProviderProfile{
				Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1",
			},
			Providers: []config.ProviderProfile{
				{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
				{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-sonnet-4-5"},
			},
			ModelRoles: map[string]string{
				"design":    "anthropic:claude-sonnet-4-5",
				"implement": "gpt-4.1-mini",
				"alias":     "@design",
			},
			DefaultModel: "gpt-4.1",
		},
		Registry:     registry,
		NewProvider:  testProviderBuilder,
		DefaultModel: "gpt-4.1",
	}
}

// testProviderBuilder returns a trivial provider whose identity is the profile name.
var _ ProviderBuilder = testProviderBuilder

func testProviderBuilder(_ context.Context, profile config.ProviderProfile) (kajicoderuntime.Provider, error) {
	return fakeProvider{name: profile.Name + "/" + profile.Model}, nil
}

type fakeProvider struct {
	name string
}

func (fakeProvider) StreamCompletion(context.Context, kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	return nil, nil
}

func TestRoleRouterProfileForActiveWhenEmpty(t *testing.T) {
	r := testRoleRouter(t)
	p, ok := r.ProfileFor("")
	if !ok || p.Name != "openai" {
		t.Fatalf("expected active openai for empty role, got %+v ok=%v", p, ok)
	}
	p, ok = r.ProfileFor("default")
	if !ok || p.Name != "openai" {
		t.Fatalf("expected active openai for default role, got %+v ok=%v", p, ok)
	}
}

func TestRoleRouterProfileForNoOverride(t *testing.T) {
	r := testRoleRouter(t)
	// "commit" is not a built-in default role and not in modelRoles -> active.
	p, ok := r.ProfileFor("commit")
	if !ok || p.Name != "openai" {
		t.Fatalf("expected active openai for unmapped custom role, got %+v ok=%v", p, ok)
	}
}

func TestRoleRouterProfileForUnsetBuiltInFallsBackWhenNoCapableProfile(t *testing.T) {
	r := testRoleRouter(t)
	// "review" is a built-in default role but not configured. Both saved providers
	// here are active-model-vicinity models WITHOUT reasoning capability, so no
	// distinct reasoning profile exists and the router must fall back to active.
	r.Resolved.Providers = []config.ProviderProfile{
		{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
		{Name: "openai-mini", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4o-mini"}, // no reasoning
	}
	r.Resolved.Provider.Model = "gpt-4.1"
	p, ok := r.ProfileFor("review")
	if !ok || p.Name != "openai" {
		t.Fatalf("expected active fallback when no distinct reasoning profile, got %+v ok=%v", p, ok)
	}
}

func TestRoleRouterProfileForUnsetBuiltInResolvesCapability(t *testing.T) {
	r := testRoleRouter(t)
	// "plan" is a built-in default role, unconfigured. claude-opus-4.1 (anthropic) is
	// reasoning-capable and differs from the active gpt-4.1, so the capability fallback
	// should route to it.
	r.Resolved.Providers = []config.ProviderProfile{
		{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
		{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-opus-4.1"},
	}
	r.Resolved.Provider.Model = "gpt-4.1"
	p, ok := r.ProfileFor("plan")
	if !ok {
		t.Fatalf("expected plan role to resolve, got ok=%v", ok)
	}
	if p.Name != "anthropic" || p.Model != "claude-opus-4.1" {
		t.Fatalf("expected capability fallback to anthropic/claude-opus-4.1, got %+v", p)
	}
}

func TestRoleRouterProfileForUnsetVisionResolvesCapability(t *testing.T) {
	r := testRoleRouter(t)
	// "vision" built-in, unconfigured. gpt-4.1 is the active model (vision-capable),
	// so it's excluded; claude-opus-4.1 is also vision-capable and differs.
	r.Resolved.Providers = []config.ProviderProfile{
		{Name: "openai", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"},
		{Name: "anthropic", ProviderKind: config.ProviderKindAnthropic, Model: "claude-opus-4.1"},
	}
	r.Resolved.Provider.Model = "gpt-4.1"
	p, ok := r.ProfileFor("vision")
	if !ok || p.Name != "anthropic" {
		t.Fatalf("expected vision fallback to anthropic profile, got %+v ok=%v", p, ok)
	}
}

func TestRoleRouterProfileForDefaultRoleIgnoresCapability(t *testing.T) {
	r := testRoleRouter(t)
	// "default" must always return the active profile regardless of capability.
	p, ok := r.ProfileFor("default")
	if !ok || p.Name != "openai" {
		t.Fatalf("expected active openai for default role, got %+v ok=%v", p, ok)
	}
}

func TestRoleRouterProfileForProviderPrefixed(t *testing.T) {
	r := testRoleRouter(t)
	p, ok := r.ProfileFor("design")
	if !ok {
		t.Fatal("expected design role to resolve")
	}
	if p.Name != "anthropic" || p.Model != "claude-sonnet-4-5" {
		t.Fatalf("expected anthropic/claude-sonnet-4-5, got %+v", p)
	}
}

func TestRoleRouterProfileForBareModel(t *testing.T) {
	r := testRoleRouter(t)
	// "implement" -> "gpt-4.1-mini": resolves in the registry but no configured
	// profile has that Model, so the override cannot be honored. The router reports
	// ok=false and falls back to the active profile (non-fatal).
	p, ok := r.ProfileFor("implement")
	if ok {
		t.Fatalf("expected ok=false for an override with no matching profile, got ok=%v p=%+v", ok, p)
	}
	if p.Name != "openai" {
		t.Fatalf("expected active fallback for unmatchable bare model, got %+v", p)
	}
}

func TestRoleRouterProfileForDiscoveryIDOwnerSlash(t *testing.T) {
	r := testRoleRouter(t)
	r.Resolved.Providers = append(r.Resolved.Providers, config.ProviderProfile{
		Name: "zerocarbon-codex", ProviderKind: config.ProviderKindAzureOpenAI, Model: "gpt-5.5",
	})
	// A models.dev discovery id "azure/gpt-5.6-luna" names the azureOpenAI owner
	// via its kind slug "azure" and overrides the model. This repairs role
	// bindings that persisted a discovery id as-is (no ":" provider prefix).
	r.Resolved.ModelRoles["vision"] = "azure/gpt-5.6-luna"
	p, ok := r.ProfileFor("vision")
	if !ok {
		t.Fatalf("expected ok=true for a discovery-id owner/model binding, got ok=%v p=%+v", ok, p)
	}
	if p.Name != "zerocarbon-codex" || p.Model != "gpt-5.6-luna" {
		t.Fatalf("expected zerocarbon-codex / gpt-5.6-luna, got %+v", p)
	}
}

func TestRoleRouterProfileForDiscoveryIDKnownProviderName(t *testing.T) {
	r := testRoleRouter(t)
	// Owner matches a saved provider by Name (not kind): "anthropic/gpt-5.6-luna".
	r.Resolved.ModelRoles["vision"] = "anthropic/gpt-5.6-luna"
	p, ok := r.ProfileFor("vision")
	if !ok {
		t.Fatalf("expected ok=true for owner-name slash binding, got ok=%v", ok)
	}
	if p.Name != "anthropic" || p.Model != "gpt-5.6-luna" {
		t.Fatalf("expected anthropic / gpt-5.6-luna, got %+v", p)
	}
}

func TestRoleRouterProfileForDiscoveryIDUnknownOwnerFallsBack(t *testing.T) {
	r := testRoleRouter(t)
	r.Resolved.ModelRoles["vision"] = "some-unknown-provider/gpt-5.6-luna"
	p, ok := r.ProfileFor("vision")
	if ok {
		t.Fatalf("expected ok=false for unknown slash owner, got ok=%v p=%+v", ok, p)
	}
	if p.Name != "openai" {
		t.Fatalf("expected active fallback for unknown slash owner, got %+v", p)
	}
}

func TestRoleRouterProfileForAlias(t *testing.T) {
	r := testRoleRouter(t)
	p, ok := r.ProfileFor("alias")
	if !ok {
		t.Fatal("expected alias role to resolve")
	}
	if p.Name != "anthropic" || p.Model != "claude-sonnet-4-5" {
		t.Fatalf("expected alias->design->anthropic, got %+v", p)
	}
}

func TestRoleRouterProfileForUnknownProviderFallsBack(t *testing.T) {
	r := testRoleRouter(t)
	r.Resolved.ModelRoles["missing"] = "nope:does-not-exist"
	p, ok := r.ProfileFor("missing")
	if ok {
		t.Fatalf("expected ok=false for a known-but-unresolvable role, got ok=%v p=%+v", ok, p)
	}
	if p.Name != "openai" {
		t.Fatalf("expected active fallback, got %+v", p)
	}
}

func TestRoleRouterAliasCycle(t *testing.T) {
	r := testRoleRouter(t)
	r.Resolved.ModelRoles["a"] = "@b"
	r.Resolved.ModelRoles["b"] = "@a"
	p, ok := r.ProfileFor("a")
	if ok {
		t.Fatalf("expected cycle to fail resolution, got ok=%v p=%+v", ok, p)
	}
	if p.Name != "openai" {
		t.Fatalf("expected active fallback after cycle, got %+v", p)
	}
}

func TestRoleRouterEffectiveModel(t *testing.T) {
	r := testRoleRouter(t)
	if got := r.EffectiveModel(); got != "gpt-4.1" {
		t.Fatalf("EffectiveModel = %q, want gpt-4.1", got)
	}
	r.DefaultModel = ""
	if got := r.EffectiveModel(); got != "gpt-4.1" {
		t.Fatalf("EffectiveModel without default = %q, want active gpt-4.1", got)
	}
}

func TestRoleRouterProviderFor(t *testing.T) {
	r := testRoleRouter(t)
	p, profile, err := r.ProviderFor(context.Background(), "design")
	if err != nil {
		t.Fatalf("ProviderFor error: %v", err)
	}
	if p == nil {
		t.Fatal("expected a non-nil provider")
	}
	if profile.Name != "anthropic" {
		t.Fatalf("expected anthropic profile, got %+v", profile)
	}
}

func TestRoleRouterProviderForNoBuilder(t *testing.T) {
	r := testRoleRouter(t)
	r.NewProvider = nil
	_, _, err := r.ProviderFor(context.Background(), "design")
	if err == nil {
		t.Fatal("expected error when NewProvider is nil")
	}
}

func TestRoleRouterBuilderErrorFallback(t *testing.T) {
	r := testRoleRouter(t)
	r.NewProvider = func(_ context.Context, _ config.ProviderProfile) (kajicoderuntime.Provider, error) {
		return nil, errors.New("boom")
	}
	_, _, err := r.ProviderFor(context.Background(), "design")
	if err == nil {
		t.Fatal("expected builder error to propagate")
	}
}

func TestRoleRouterFirstVisionCapableProvider(t *testing.T) {
	r := testRoleRouter(t)
	p, ok := r.FirstVisionCapableProvider("claude-sonnet-4-5")
	if !ok {
		t.Fatal("expected a vision-capable provider")
	}
	if p.Name != "openai" { // claude-sonnet-4-5 excluded, so gpt-4.1 (vision) wins
		t.Fatalf("expected openai gpt-4.1, got %+v", p)
	}
	// Remove every profile -> none left.
	r.Resolved.Providers = r.Resolved.Providers[:0]
	if _, ok := r.FirstVisionCapableProvider(""); ok {
		t.Fatal("expected no vision provider when none configured")
	}
}

func TestRoleRouterConcurrency(t *testing.T) {
	r := testRoleRouter(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := r.ProfileFor("design"); !ok {
				t.Error("profile resolution failed under concurrency")
			}
		}()
	}
	wg.Wait()
}
