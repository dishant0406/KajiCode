package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestACPClassifierConfigureMapsFields guards the ACP field → CLI flag mapping:
// blank fields are omitted (stored values preserved), bools are only sent when
// parseable, multi-line headers become one --header each.
func TestACPClassifierConfigureMapsFields(t *testing.T) {
	deps, path := classifierConfigureDeps(t, `{
		"classifier": {"enabled": true, "active": "jev",
			"profiles": [{"name":"jev","endpoint":"https://old.test/v1","model":"keep-me"}]}
	}`)
	configure := acpClassifierConfigure(deps)
	summary, err := configure(context.Background(), map[string]string{
		"name":       "jev",
		"endpoint":   "https://new.test/v1",
		"headers":    "X-A=1\n\nX-B=2",
		"toolResult": "true",
		"toolShadow": "true",
		"model":      "", // blank: leave the stored value alone
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !strings.Contains(summary, "Configured classifier jev") {
		t.Fatalf("unexpected summary: %s", summary)
	}
	profiles := readClassifierConfigFile(t, path)["classifier"].(map[string]any)["profiles"].([]any)
	profile := profiles[0].(map[string]any)
	if profile["endpoint"] != "https://new.test/v1" {
		t.Fatalf("endpoint not updated: %v", profile)
	}
	if profile["model"] != "keep-me" {
		t.Fatalf("a blank field must preserve the stored value: %v", profile)
	}
	headers := profile["headers"].(map[string]any)
	if headers["X-A"] != "1" || headers["X-B"] != "2" {
		t.Fatalf("headers not mapped: %v", headers)
	}
	toolResult := readClassifierConfigFile(t, path)["classifier"].(map[string]any)["features"].(map[string]any)["toolResult"].(map[string]any)
	if toolResult["enabled"] != true || toolResult["shadowMode"] != true {
		t.Fatalf("feature switches not applied: %v", toolResult)
	}
}

// TestACPClassifierConfigureRequiresName guards the early validation so a bare
// form submit fails with a clear message instead of a CLI error.
func TestACPClassifierConfigureRequiresName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps, _ := classifierConfigureDeps(t, `{}`)
	if _, err := acpClassifierConfigure(deps)(context.Background(), map[string]string{"endpoint": "https://x.test"}); err == nil {
		t.Fatalf("expected an error for a missing name (stdout=%s stderr=%s)", stdout.String(), stderr.String())
	}
}

// TestClassifierHelpListsConfigure guards that the new subcommand is documented.
func TestClassifierHelpListsConfigure(t *testing.T) {
	var out bytes.Buffer
	if code := writeClassifierHelp(&out); code != exitSuccess {
		t.Fatalf("help exit code %d", code)
	}
	if !strings.Contains(out.String(), "classifier configure") {
		t.Fatalf("help does not document configure:\n%s", out.String())
	}
}
