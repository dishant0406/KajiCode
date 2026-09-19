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
	if def.DebounceMs != AutoLearnDebounceDefaultMs {
		t.Fatalf("debounce = %d, want default %d", def.DebounceMs, AutoLearnDebounceDefaultMs)
	}
	if !def.IsCompactEnabled() {
		t.Fatal("compact-triggered learning should default to enabled")
	}
	if def.PruneAfterDays != AutoLearnPruneAfterDaysDefault {
		t.Fatalf("pruneAfterDays = %d, want default %d", def.PruneAfterDays, AutoLearnPruneAfterDaysDefault)
	}
	if def.MaxEntries != AutoLearnMaxEntriesDefault {
		t.Fatalf("maxEntries = %d, want default %d", def.MaxEntries, AutoLearnMaxEntriesDefault)
	}
}

func TestLearningConfigUnmarshalDistinguishesExplicitFalse(t *testing.T) {
	var cfg LearningConfig
	if err := json.Unmarshal([]byte(`{"enabled": false, "compact": false, "debounceMs": 1000, "pruneAfterDays": 7, "maxEntries": 50}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.IsEnabled() {
		t.Fatal("enabled should be false")
	}
	if cfg.IsCompactEnabled() {
		t.Fatal("compact should be false")
	}
	if cfg.DebounceMs != 1000 {
		t.Fatalf("debounceMs = %d, want 1000", cfg.DebounceMs)
	}
	if cfg.PruneAfterDays != 7 {
		t.Fatalf("pruneAfterDays = %d, want 7", cfg.PruneAfterDays)
	}
	if cfg.MaxEntries != 50 {
		t.Fatalf("maxEntries = %d, want 50", cfg.MaxEntries)
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
	cfg := LearningConfig{DebounceMs: 0}
	got := cfg.Effective()
	if got.DebounceMs != AutoLearnDebounceDefaultMs {
		t.Fatalf("effective debounceMs = %d, want default %d", got.DebounceMs, AutoLearnDebounceDefaultMs)
	}
	if !got.IsEnabled() {
		t.Fatal("effective should be enabled by default")
	}
	if got.Debounce().Milliseconds() != AutoLearnDebounceDefaultMs {
		t.Fatalf("effective debounce duration = %v", got.Debounce())
	}
}

func TestValidateLearningConfigRejectsNegatives(t *testing.T) {
	cfg := LearningConfig{DebounceMs: -1}
	issues := validateLearningConfig(cfg)
	if len(issues) == 0 {
		t.Fatal("negative debounceMs should report an issue")
	}
	cfg = LearningConfig{PruneAfterDays: -5}
	issues = validateLearningConfig(cfg)
	if len(issues) == 0 {
		t.Fatal("negative pruneAfterDays should report an issue")
	}
	cfg = LearningConfig{MaxEntries: -5}
	issues = validateLearningConfig(cfg)
	if len(issues) == 0 {
		t.Fatal("negative maxEntries should report an issue")
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
	if resolved.Learning.DebounceMs != AutoLearnDebounceDefaultMs {
		t.Fatalf("resolved debounceMs = %d, want default %d", resolved.Learning.DebounceMs, AutoLearnDebounceDefaultMs)
	}
}

func TestResolveMergesLearningUserConfig(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "[REDACTED]", "model": "m"}],
		"learning": {"enabled": true, "debounceMs": 60000, "compact": false, "pruneAfterDays": 30}
	}`)
	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Learning.DebounceMs != 60000 {
		t.Fatalf("debounceMs = %d, want 60000", resolved.Learning.DebounceMs)
	}
	if resolved.Learning.PruneAfterDays != 30 {
		t.Fatalf("pruneAfterDays = %d, want 30", resolved.Learning.PruneAfterDays)
	}
	if resolved.Learning.IsCompactEnabled() {
		t.Fatal("compact should be disabled")
	}
}

func TestProjectConfigCannotDisableLearning(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "[REDACTED]", "model": "m"}],
		"learning": {"enabled": true, "debounceMs": 5000}
	}`)
	projectPath := writeConfig(t, `{"learning": {"enabled": false, "debounceMs": 1}}`)
	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// A cloned project must not silently weaken autonomous learning: project
	// learning config is ignored, so user values win.
	if resolved.Learning.DebounceMs != 5000 {
		t.Fatalf("project config leaked debounceMs, got %d want 5000", resolved.Learning.DebounceMs)
	}
	if !resolved.Learning.IsEnabled() {
		t.Fatal("project config leaked enabled=false")
	}
}

func TestValidateBytesReportsLearningIssues(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{"learning": {"debounceMs": -3, "pruneAfterDays": -1, "maxEntries": -1}}`))
	if !hasIssuePath(issues, "learning.debounceMs") {
		t.Fatalf("issues = %#v, missing learning.debounceMs", issues)
	}
	if !hasIssuePath(issues, "learning.pruneAfterDays") {
		t.Fatalf("issues = %#v, missing learning.pruneAfterDays", issues)
	}
	if !hasIssuePath(issues, "learning.maxEntries") {
		t.Fatalf("issues = %#v, missing learning.maxEntries", issues)
	}
}

func TestSetLearningConfigPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := SetLearningConfig(path, "debounceMs", "7000"); err != nil {
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
	if cfg.Learning.DebounceMs != 7000 {
		t.Fatalf("debounceMs = %d, want 7000", cfg.Learning.DebounceMs)
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
	if _, err := SetLearningConfig(path, "debounceMs", "-5"); err == nil {
		t.Fatal("debounceMs=-5 should error")
	}
	if _, err := SetLearningConfig(path, "maxEntries", "-1"); err == nil {
		t.Fatal("maxEntries=-1 should error")
	}
	if _, err := SetLearningConfig(path, "bogus", "1"); err == nil {
		t.Fatal("unknown key should error")
	}
}
