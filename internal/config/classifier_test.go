package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestClassifierToolResultFeatureRoundTrip guards the config 4-place rule for
// the tool-result gate: the feature block must survive save → load unchanged,
// exactly like the compaction feature beside it.
func TestClassifierToolResultFeatureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
		"classifier": {
			"enabled": true,
			"active": "jev",
			"profiles": [{"name": "jev", "endpoint": "https://example.test/v1/systemone", "model": "jev-1.13"}],
			"features": {
				"toolResult": {"enabled": true, "dropThreshold": 0.15, "keepThreshold": 0.6, "minPruneRatio": 0.3, "minBytes": 2048, "shadowMode": true}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	feature := cfg.Classifier.Features.ToolResult
	if !feature.Enabled || !feature.ShadowMode {
		t.Fatalf("feature flags lost: %+v", feature)
	}
	if feature.EffectiveDropThreshold() != 0.15 || feature.EffectiveKeepThreshold() != 0.6 {
		t.Fatalf("thresholds lost: %+v", feature)
	}
	if feature.EffectiveMinPruneRatio() != 0.3 || feature.EffectiveMinBytes() != 2048 {
		t.Fatalf("gate knobs lost: %+v", feature)
	}

	// Save → load again: the block must round-trip through MarshalJSON.
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round FileConfig
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if round.Classifier.Features.ToolResult != feature {
		t.Fatalf("round trip changed the feature: %+v != %+v", round.Classifier.Features.ToolResult, feature)
	}
}

// TestClassifierFeatureMergeOrsEnabled proves a later config layer can turn the
// tool-result feature on but a false value never turns it back off, matching
// every other bool feature switch.
func TestClassifierFeatureMergeOrsEnabled(t *testing.T) {
	dst := FileConfig{}
	dst.Classifier.Features.ToolResult.Enabled = true
	mergeClassifierConfig(&dst.Classifier, mustParseClassifier(t, `{"classifier":{"features":{"toolResult":{"enabled":false}}}}`).Classifier)
	if !dst.Classifier.Features.ToolResult.Enabled {
		t.Fatal("a false layer must not turn the feature off")
	}
	empty := FileConfig{}
	mergeClassifierConfig(&empty.Classifier, mustParseClassifier(t, `{"classifier":{"features":{"toolResult":{"enabled":true,"minBytes":4096}}}}`).Classifier)
	if !empty.Classifier.Features.ToolResult.Enabled {
		t.Fatal("a true layer must turn the feature on")
	}
	if empty.Classifier.Features.ToolResult.MinBytes != 4096 {
		t.Fatalf("minBytes not merged: %+v", empty.Classifier.Features.ToolResult)
	}
}

func mustParseClassifier(t *testing.T, body string) (cfg FileConfig) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("parse %s: %v", body, err)
	}
	return cfg
}
