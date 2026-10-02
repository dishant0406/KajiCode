package classifier

import "testing"

func TestToolResultFeatureDefaults(t *testing.T) {
	var f ToolResultFeature
	if got := f.EffectiveKeepThreshold(); got != DefaultGateKeepThreshold {
		t.Fatalf("keep default = %v, want %v", got, DefaultGateKeepThreshold)
	}
	if got := f.EffectiveDropThreshold(); got != DefaultGateDropThreshold {
		t.Fatalf("drop default = %v, want %v", got, DefaultGateDropThreshold)
	}
	if got := f.EffectiveMinPruneRatio(); got != DefaultGateMinPruneRatio {
		t.Fatalf("ratio default = %v, want %v", got, DefaultGateMinPruneRatio)
	}
	if got := f.EffectiveMinBytes(); got != DefaultGateMinBytes {
		t.Fatalf("bytes default = %v, want %v", got, DefaultGateMinBytes)
	}
}

func TestToolResultFeatureClampsInvertedBand(t *testing.T) {
	// A drop threshold above the keep threshold would invert the band (drop the
	// confident-yes) — it must clamp to keep. This is the same guard the shipped
	// compaction band uses.
	f := ToolResultFeature{KeepThreshold: 0.4, DropThreshold: 0.9}
	if got := f.EffectiveDropThreshold(); got != 0.4 {
		t.Fatalf("inverted band must clamp to keep, got %v", got)
	}
}

func TestToolResultFeatureOverrides(t *testing.T) {
	f := ToolResultFeature{KeepThreshold: 0.7, DropThreshold: 0.2, MinPruneRatio: 0.5, MinBytes: 4096}
	if got := f.EffectiveKeepThreshold(); got != 0.7 {
		t.Fatalf("keep = %v", got)
	}
	if got := f.EffectiveDropThreshold(); got != 0.2 {
		t.Fatalf("drop = %v", got)
	}
	if got := f.EffectiveMinPruneRatio(); got != 0.5 {
		t.Fatalf("ratio = %v", got)
	}
	if got := f.EffectiveMinBytes(); got != 4096 {
		t.Fatalf("bytes = %v", got)
	}
}
