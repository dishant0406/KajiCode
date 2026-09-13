package modelregistry

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelsource"
)

// TestReasoningEffortsFromModelsDev pins that effort tiers come from models.dev
// (the embedded seed here — no network, no disk cache) rather than a hardcoded
// name table. The provider row is authoritative: tiers are read for the session's
// bound provider slug, which is how the CLI binds the selected provider at startup.
func TestReasoningEffortsFromModelsDev(t *testing.T) {
	usingSeed(t)
	modelsource.Bind("openai", "")
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want []ReasoningEffort
	}{
		{"gpt-5", []ReasoningEffort{ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"gpt-5.5", []ReasoningEffort{ReasoningEffortNone, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh}},
		{"gpt-5.3-codex-spark", []ReasoningEffort{ReasoningEffortNone, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh}},
		{"o3-mini", []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		// Non-reasoning models: no tiers, and no name-based inference to invent any.
		{"gpt-4.1", nil},
		{"gpt-4o", nil},
		{"totally-made-up-model-zzz", nil},
	}
	for _, c := range cases {
		got := reg.ReasoningEfforts(c.name)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}

// TestReasoningEffortsNoNameInference pins the deliberate removal of the name
// heuristic: with the snapshot off, a model the registry cannot resolve reports no
// tiers, never a guess derived from its name.
func TestReasoningEffortsNoNameInference(t *testing.T) {
	modelsource.Disable()
	ResetSynthesizedCacheForTest()
	t.Cleanup(modelsource.Disable)
	reg, err := NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-5", "o3-mini", "hunyuan-t1", "hy3"} {
		if got := reg.ReasoningEfforts(name); len(got) != 0 {
			t.Errorf("%s: unregistered model guessed tiers %v", name, got)
		}
	}
}

// usingSeed enables the embedded models.dev seed for a test and restores state.
func usingSeed(t *testing.T) {
	t.Helper()
	modelsource.Enable()
	ResetSynthesizedCacheForTest()
	t.Cleanup(func() {
		ResetSynthesizedCacheForTest()
		modelsource.Bind("", "")
		modelsource.Disable()
	})
}
