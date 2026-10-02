package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// classifierConfigureDeps returns deps whose user config path points at a temp
// config.json, so a configure run writes only inside the test.
func classifierConfigureDeps(t *testing.T, body string) (appDeps, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	deps := appDeps{userConfigPath: func() (string, error) { return path, nil }}
	return deps, path
}

func readClassifierConfigFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return doc
}

// TestClassifierConfigureUpsertsAndEnablesGuards the configure path: it registers
// a new profile, enables the capability when a feature turns on, writes the
// thresholds, and preserves keys the writer does not model.
func TestClassifierConfigureUpsertsAndEnablesGuards(t *testing.T) {
	body := `{
		"classifier": {
			"enabled": true,
			"active": "jev",
			"profiles": [{"name":"jev","endpoint":"https://old.test/v1","apiKeyStored":true,"customFuture":7}],
			"features": {"compaction": {"enabled": true, "keepResultThreshold": 0.35, "futureKnob": true}}
		},
		"otherKey": 1
	}`
	deps, path := classifierConfigureDeps(t, body)
	var stdout, stderr bytes.Buffer
	code := runClassifierConfigure([]string{
		"jev", "--endpoint", "https://new.test/v1", "--model", "jev-1.13",
		"--tool-result", "--tool-drop", "0.15", "--tool-keep", "0.55", "--tool-min-bytes", "2048",
	}, &stdout, &stderr, deps)
	if code != exitSuccess {
		t.Fatalf("configure failed: %s%s", stdout.String(), stderr.String())
	}
	doc := readClassifierConfigFile(t, path)
	classifier := doc["classifier"].(map[string]any)

	if classifier["enabled"] != true {
		t.Fatalf("capability not enabled by a feature switch: %v", classifier["enabled"])
	}
	features := classifier["features"].(map[string]any)
	toolResult := features["toolResult"].(map[string]any)
	if toolResult["enabled"] != true || toolResult["dropThreshold"] != 0.15 || toolResult["keepThreshold"] != 0.55 {
		t.Fatalf("tool-result feature not written: %v", toolResult)
	}
	compaction := features["compaction"].(map[string]any)
	if compaction["futureKnob"] != true {
		t.Fatalf("unknown feature key not preserved: %v", compaction)
	}
	profiles := classifier["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["endpoint"] != "https://new.test/v1" {
		t.Fatalf("profile endpoint not updated: %v", profiles)
	}
	if profiles[0].(map[string]any)["customFuture"] != float64(7) {
		t.Fatalf("unknown profile key not preserved: %v", profiles[0])
	}
	if doc["otherKey"] != float64(1) {
		t.Fatalf("unrelated top-level key was dropped: %v", doc)
	}
}

// TestClassifierConfigureDisableIsExplicit guards that --compaction=false turns
// one feature off without turning the capability off, and --disable turns the
// capability itself off.
func TestClassifierConfigureDisableIsExplicit(t *testing.T) {
	body := `{
		"classifier": {
			"enabled": true,
			"active": "jev",
			"profiles": [{"name":"jev","endpoint":"https://x.test/v1"}],
			"features": {"compaction": {"enabled": true}, "toolResult": {"enabled": true}}
		}
	}`
	deps, path := classifierConfigureDeps(t, body)
	var stdout, stderr bytes.Buffer
	if code := runClassifierConfigure([]string{"jev", "--endpoint", "https://x.test/v1", "--compaction=false"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("configure failed: %s%s", stdout.String(), stderr.String())
	}
	classifier := readClassifierConfigFile(t, path)["classifier"].(map[string]any)
	features := classifier["features"].(map[string]any)
	if features["compaction"].(map[string]any)["enabled"] != false {
		t.Fatalf("compaction not disabled: %v", features["compaction"])
	}
	if features["toolResult"].(map[string]any)["enabled"] != true {
		t.Fatalf("tool-result should be untouched: %v", features["toolResult"])
	}
	if classifier["enabled"] != true {
		t.Fatalf("a false feature switch must not disable the capability: %v", classifier["enabled"])
	}

	stdout.Reset()
	stderr.Reset()
	if code := runClassifierConfigure([]string{"jev", "--endpoint", "https://x.test/v1", "--disable"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("configure --disable failed: %s%s", stdout.String(), stderr.String())
	}
	classifier = readClassifierConfigFile(t, path)["classifier"].(map[string]any)
	if classifier["enabled"] != false {
		t.Fatalf("--disable did not turn the capability off: %v", classifier["enabled"])
	}
}

// TestClassifierConfigureRegistersNewProfile proves configure upserts a profile
// that `add` would otherwise have to create, so the TUI/ACP forms need one call.
// Registering alone does not enable the capability (matching `add`); enabling a
// feature does.
func TestClassifierConfigureRegistersNewProfile(t *testing.T) {
	deps, path := classifierConfigureDeps(t, `{}`)
	var stdout, stderr bytes.Buffer
	if code := runClassifierConfigure([]string{"fresh", "--endpoint", "https://fresh.test/v1", "--tool-result"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("configure failed: %s%s", stdout.String(), stderr.String())
	}
	classifier := readClassifierConfigFile(t, path)["classifier"].(map[string]any)
	profile, ok := classifier["profiles"].([]any)
	if !ok || len(profile) != 1 {
		t.Fatalf("profile not registered: %v", classifier["profiles"])
	}
	if classifier["active"] != "fresh" {
		t.Fatalf("first profile not made active: %v", classifier["active"])
	}
	if classifier["enabled"] != true {
		t.Fatalf("capability not enabled: %v", classifier["enabled"])
	}

	// Registering only the profile must not enable the capability.
	deps, path = classifierConfigureDeps(t, `{}`)
	stdout.Reset()
	stderr.Reset()
	if code := runClassifierConfigure([]string{"solo", "--endpoint", "https://solo.test/v1"}, &stdout, &stderr, deps); code != exitSuccess {
		t.Fatalf("configure failed: %s%s", stdout.String(), stderr.String())
	}
	if classifier = readClassifierConfigFile(t, path)["classifier"].(map[string]any); classifier["enabled"] == true {
		t.Fatalf("registering a profile alone must not enable the capability: %v", classifier["enabled"])
	}
}

// TestClassifierConfigureRejectsBadThreshold guards the numeric validation so a
// malformed form value fails loudly rather than writing nonsense.
func TestClassifierConfigureRejectsBadThreshold(t *testing.T) {
	deps, _ := classifierConfigureDeps(t, `{"classifier":{"enabled":true,"profiles":[{"name":"jev","endpoint":"https://x.test/v1"}]}}`)
	var stdout, stderr bytes.Buffer
	code := runClassifierConfigure([]string{"jev", "--endpoint", "https://x.test/v1", "--tool-drop", "5"}, &stdout, &stderr, deps)
	if code == exitSuccess {
		t.Fatalf("expected a non-zero exit for an out-of-range threshold")
	}
	if !strings.Contains(stderr.String(), "number in [0,1]") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}
