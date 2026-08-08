package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultLearningConfigIsEnabled(t *testing.T) {
	def := DefaultLearningConfig()
	if !def.IsEnabled() {
		t.Fatal("auto-learning should default to enabled")
	}
	if def.TurnInterval != AutoLearnTurnIntervalDefault {
		t.Fatalf("turn interval = %d, want default %d", def.TurnInterval, AutoLearnTurnIntervalDefault)
	}
	if !def.IsCompactEnabled() {
		t.Fatal("compact-triggered learning should default to enabled")
	}
	if def.CooldownMs != AutoLearnCooldownMsDefault {
		t.Fatalf("cooldown = %d, want default %d", def.CooldownMs, AutoLearnCooldownMsDefault)
	}
}

func TestLearningConfigUnmarshalDistinguishesExplicitFalse(t *testing.T) {
	var cfg LearningConfig
	if err := json.Unmarshal([]byte(`{"enabled": false, "compact": false, "turnInterval": 3, "cooldownMs": 1000}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.IsEnabled() {
		t.Fatal("enabled should be false")
	}
	if cfg.IsCompactEnabled() {
		t.Fatal("compact should be false")
	}
	if cfg.TurnInterval != 3 {
		t.Fatalf("turnInterval = %d, want 3", cfg.TurnInterval)
	}
	if cfg.CooldownMs != 1000 {
		t.Fatalf("cooldownMs = %d, want 1000", cfg.CooldownMs)
	}
	// Explicit false must not be considered "empty" (so it is persisted & merged).
	if cfg.Empty() {
		t.Fatal("explicit-false config must not be empty")
	}
}

func TestLearningConfigEmptyWhenAbsent(t *testing.T) {
	var cfg LearningConfig
	if err := json.Unmarshal([]byte(`{}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.Empty() {
		t.Fatal("absent learning config should be empty")
	}
}

func TestLearningConfigMarshalOmittedWhenEmpty(t *testing.T) {
	data, err := json.Marshal(LearningConfig{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("empty learning config should marshal to {}, got %s", data)
	}
}

func TestEffectiveAppliesDefaults(t *testing.T) {
	cfg := LearningConfig{TurnInterval: 0}
	got := cfg.Effective()
	if got.TurnInterval != AutoLearnTurnIntervalDefault {
		t.Fatalf("effective turnInterval = %d, want default %d", got.TurnInterval, AutoLearnTurnIntervalDefault)
	}
	if !got.IsEnabled() {
		t.Fatal("effective should be enabled by default")
	}
}

func TestValidateLearningConfigRejectsNegatives(t *testing.T) {
	cfg := LearningConfig{TurnInterval: -1}
	issues := validateLearningConfig(cfg)
	if len(issues) == 0 {
		t.Fatal("negative turnInterval should report an issue")
	}
	cfg = LearningConfig{CooldownMs: -5}
	issues = validateLearningConfig(cfg)
	if len(issues) == 0 {
		t.Fatal("negative cooldownMs should report an issue")
	}
}

func TestResolveAppliesLearningDefaults(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "[REDACTED]", "model": "m"}]
	}`)
	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolved.Learning.IsEnabled() {
		t.Fatal("resolved learning should be enabled by default")
	}
	if resolved.Learning.TurnInterval != AutoLearnTurnIntervalDefault {
		t.Fatalf("resolved turnInterval = %d, want default %d", resolved.Learning.TurnInterval, AutoLearnTurnIntervalDefault)
	}
}

func TestResolveMergesLearningUserConfig(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "[REDACTED]", "model": "m"}],
		"learning": {"enabled": true, "turnInterval": 5, "compact": false, "cooldownMs": 60000}
	}`)
	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Learning.TurnInterval != 5 {
		t.Fatalf("turnInterval = %d, want 5", resolved.Learning.TurnInterval)
	}
	if resolved.Learning.IsCompactEnabled() {
		t.Fatal("compact should be disabled")
	}
}

func TestProjectConfigCannotDisableLearning(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "[REDACTED]", "model": "m"}],
		"learning": {"enabled": true, "turnInterval": 5}
	}`)
	projectPath := writeConfig(t, `{"learning": {"enabled": false, "turnInterval": 1}}`)
	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// A cloned project must not silently weaken autonomous learning: project
	// learning config is ignored, so user values win.
	if resolved.Learning.TurnInterval != 5 {
		t.Fatalf("project config leaked turnInterval, got %d want 5", resolved.Learning.TurnInterval)
	}
	if !resolved.Learning.IsEnabled() {
		t.Fatal("project config leaked enabled=false")
	}
}

func TestValidateBytesReportsLearningIssues(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"learning": {"turnInterval": -3, "cooldownMs": -1}}`))
	if !hasIssuePath(issues, "learning.turnInterval") {
		t.Fatalf("issues = %#v, missing learning.turnInterval", issues)
	}
	if !hasIssuePath(issues, "learning.cooldownMs") {
		t.Fatalf("issues = %#v, missing learning.cooldownMs", issues)
	}
}

func TestSetLearningConfigPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := SetLearningConfig(path, "turnInterval", "7"); err != nil {
		t.Fatalf("SetLearningConfig: %v", err)
	}
	if _, err := SetLearningConfig(path, "compact", "off"); err != nil {
		t.Fatalf("SetLearningConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Learning.TurnInterval != 7 {
		t.Fatalf("turnInterval = %d, want 7", cfg.Learning.TurnInterval)
	}
	if cfg.Learning.IsCompactEnabled() {
		t.Fatal("compact should be off")
	}
}

func TestSetLearningConfigRejectsBadValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := SetLearningConfig(path, "enabled", "maybe"); err == nil {
		t.Fatal("enabled=maybe should error")
	}
	if _, err := SetLearningConfig(path, "turnInterval", "-5"); err == nil {
		t.Fatal("turnInterval=-5 should error")
	}
	if _, err := SetLearningConfig(path, "cooldownMs", "-1"); err == nil {
		t.Fatal("cooldownMs=-1 should error")
	}
	if _, err := SetLearningConfig(path, "bogus", "1"); err == nil {
		t.Fatal("unknown key should error")
	}
}
