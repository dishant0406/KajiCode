package modelregistry

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/modelsource"
)

// modelsDevFixture is a minimal catalog.json carrying one model the curated
// catalog does NOT know, so Get must synthesize it from models.dev.
const modelsDevFixture = `{
  "models": {
    "acme/vision-flash-9": {
      "id": "acme/vision-flash-9", "name": "Vision Flash 9",
      "tool_call": true,
      "modalities": {"input": ["text", "image"], "output": ["text"]},
      "limit": {"context": 250000, "output": 32000},
      "cost": {"input": 1.5, "output": 6.0, "cache_read": 0.15}
    },
    "acme/text-only-9": {
      "id": "acme/text-only-9", "name": "Text Only 9",
      "reasoning": true,
      "reasoning_options": [{"type": "effort", "values": ["low", "medium", "high"]}],
      "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 128000, "output": 16000},
      "cost": {"input": 0.5, "output": 1.5}
    }
  },
  "providers": {}
}`

func loadModelsDevFixture(t *testing.T) {
	t.Helper()
	modelsource.Enable()
	if err := modelsource.LoadDocument([]byte(modelsDevFixture)); err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	ResetSynthesizedCacheForTest()
	t.Cleanup(func() {
		ResetSynthesizedCacheForTest()
		modelsource.Disable()
	})
}

func TestGetSynthesizesUnknownModelFromModelsDev(t *testing.T) {
	loadModelsDevFixture(t)
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := registry.Get("acme/vision-flash-9")
	if !ok {
		t.Fatal("expected models.dev-synthesized entry")
	}
	if !entry.Supports(ModelCapabilityVision) {
		t.Error("vision-flash-9 should support vision (modalities.input has image)")
	}
	if entry.ContextLimits.ContextWindow != 250000 {
		t.Errorf("context = %d, want 250000", entry.ContextLimits.ContextWindow)
	}
	if entry.Cost.InputPerMillion != 1.5 {
		t.Errorf("input cost = %v, want 1.5", entry.Cost.InputPerMillion)
	}
}

func TestGetSynthesizedEntryHasReasoningEfforts(t *testing.T) {
	loadModelsDevFixture(t)
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	efforts := registry.ReasoningEfforts("acme/text-only-9")
	if len(efforts) == 0 {
		t.Fatal("expected reasoning efforts from models.dev")
	}
	want := map[ReasoningEffort]bool{ReasoningEffortLow: true, ReasoningEffortMedium: true, ReasoningEffortHigh: true}
	for _, effort := range efforts {
		if !want[effort] {
			t.Errorf("unexpected effort %q", effort)
		}
	}
}

func TestSupportsVisionUsesModelsDevForUnknownModel(t *testing.T) {
	loadModelsDevFixture(t)
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if !SupportsVision(registry, "acme/vision-flash-9") {
		t.Error("models.dev says vision-flash-9 accepts images")
	}
	// text-only-9 is in models.dev as text-only, so the name heuristic must not
	// override models.dev's authoritative "no".
	if SupportsVision(registry, "acme/text-only-9") {
		t.Error("models.dev says text-only-9 is text-only")
	}
}

func TestCuratedCatalogUnchangedWhenSnapshotDisabled(t *testing.T) {
	// No modelsource.Enable(): synthesis must be off.
	modelsource.Disable()
	ResetSynthesizedCacheForTest()
	t.Cleanup(func() {
		ResetSynthesizedCacheForTest()
		modelsource.Disable()
	})
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get("acme/vision-flash-9"); ok {
		t.Error("without the snapshot, unknown models must stay unknown")
	}
}
