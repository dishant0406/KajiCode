package modelregistry

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelsource"
)

func mkEntry(id, alias string) ModelEntry {
	return ModelEntry{
		ID: id, DisplayName: id, APIModel: id, Provider: ProviderAnthropic,
		ContextLimits: ContextLimits{ContextWindow: 200000, MaxOutputTokens: 64000},
		Capabilities:  ModelCapabilities{ModelCapabilityChat},
		Status:        ModelStatusActive, Aliases: []string{alias},
		Cost: ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 1, OutputPerMillion: 2,
			Source: "test", SourceLastVerified: "2026-06-06",
		},
	}
}

func resolveTestRegistry(t *testing.T) Registry {
	t.Helper()
	sonnet := mkEntry("claude-sonnet-4-5", "sonnet-4.5")
	sonnet.MatchPatterns = []string{`(?i)sonnet[^a-z0-9]*4[.\s]?5`}
	sonnet.ReasoningEfforts = []ReasoningEffort{ReasoningEffortNone, ReasoningEffortLow, ReasoningEffortHigh}
	sonnet.DefaultReasoningEffort = ReasoningEffortLow

	old := mkEntry("claude-sonnet-4-0", "sonnet-4.0")
	old.Status = ModelStatusDeprecated
	old.Deprecation = &DeprecationRule{FallbackID: "claude-sonnet-4-5", WarningMsg: "sonnet-4-0 retired; use 4.5"}

	reg, err := NewRegistry([]ModelEntry{sonnet, old})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestResolveRegexAlias(t *testing.T) {
	reg := resolveTestRegistry(t)
	for _, in := range []string{"claude-sonnet-4-5", "sonnet-4.5", "Sonnet 4.5", "sonnet4.5"} {
		m, ok := reg.Resolve(in)
		if !ok || m.ID != "claude-sonnet-4-5" {
			t.Errorf("Resolve(%q) = %q,%v; want claude-sonnet-4-5", in, m.ID, ok)
		}
	}
	if _, ok := reg.Resolve("totally-unknown"); ok {
		t.Error("unknown input should not resolve")
	}
}

func TestResolveWithFallbackRedirectsDeprecated(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("claude-sonnet-4-0")
	if !ok || m.ID != "claude-sonnet-4-5" {
		t.Fatalf("expected redirect to 4.5, got %q,%v", m.ID, ok)
	}
	if notice == "" {
		t.Error("expected a deprecation notice")
	}
}

func TestResolveWithFallbackActiveNoNotice(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("Sonnet 4.5")
	if !ok || m.ID != "claude-sonnet-4-5" || notice != "" {
		t.Fatalf("active model should resolve cleanly, got %q notice=%q", m.ID, notice)
	}
}

func TestEffectiveReasoningEffort(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, _ := reg.Get("claude-sonnet-4-5")
	if got := EffectiveReasoningEffort(m, ReasoningEffortHigh); got != ReasoningEffortHigh {
		t.Errorf("supported effort = %q; want high", got)
	}
	if got := EffectiveReasoningEffort(m, ReasoningEffortXHigh); got != ReasoningEffortLow {
		t.Errorf("unsupported effort should fall back to default low, got %q", got)
	}
	if got := EffectiveReasoningEffort(m, ""); got != ReasoningEffortLow {
		t.Errorf("empty effort should use default low, got %q", got)
	}
}

// TestEffectiveReasoningEffortFromModelsDev pins that the run-time resolver and
// the /effort picker share one models.dev-backed source and never disagree. A real
// reasoning model (o3-mini, synthesized from models.dev) has its requested tier
// honored; a non-reasoning model coerces to "none" with no name-based guess.
func TestEffectiveReasoningEffortFromModelsDev(t *testing.T) {
	usingSeed(t)
	modelsource.Bind("openai", "")
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	reasoning, ok := reg.Get("o3-mini")
	if !ok {
		t.Fatal("o3-mini should resolve from models.dev")
	}
	if got := EffectiveReasoningEffort(reasoning, ReasoningEffortHigh); got != ReasoningEffortHigh {
		t.Errorf("supported effort = %q; want high", got)
	}
	// xhigh is outside o3-mini's set and it declares no default, so it coerces to
	// the first supported tier rather than to "none".
	if got := EffectiveReasoningEffort(reasoning, ReasoningEffortXHigh); got != ReasoningEffortLow {
		t.Errorf("unsupported effort = %q; want low (first supported)", got)
	}
	// The picker and the resolver must agree on the supported set for the same id.
	for _, tier := range reg.ReasoningEfforts("o3-mini") {
		if got := EffectiveReasoningEffort(reasoning, tier); got != tier {
			t.Errorf("picker advertises %q but resolver returns %q", tier, got)
		}
	}

	// Non-reasoning model: no tiers, stays "none".
	plain, ok := reg.Get("gpt-4.1")
	if !ok {
		t.Fatal("gpt-4.1 should be in the curated registry")
	}
	if got := EffectiveReasoningEffort(plain, ReasoningEffortHigh); got != ReasoningEffortNone {
		t.Errorf("non-reasoning model = %q; want none", got)
	}
}
