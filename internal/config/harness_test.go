package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMergesHarnessConfigWithProjectRestrictions(t *testing.T) {
	userPath := writeConfig(t, `{
		"activeProvider": "work",
		"providers": [{"name": "work", "provider": "openai", "apiKey": "sk", "model": "m"}],
		"harness": {
			"promptAddenda": [{"id": "team", "text": "Use rg before broad file scans."}],
			"permissionRules": [{"id": "shell-policy", "match": "bash", "action": "allow", "reason": "trusted user default"}]
		}
	}`)
	projectPath := writeConfig(t, `{
		"harness": {
			"promptAddenda": [
				{"id": "team", "text": "Project guidance wins."},
				{"id": "repo", "text": "Keep edits scoped."}
			],
			"permissionRules": [
				{"id": "shell-policy", "match": "bash", "action": "deny", "commandContains": ["rm -rf"], "reason": "no destructive shell"},
				{"id": "network", "match": "web_*", "action": "ask"}
			]
		}
	}`)

	resolved, err := Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(resolved.Harness.PromptAddenda) != 2 {
		t.Fatalf("prompt addenda = %#v, want merged replacement plus project addendum", resolved.Harness.PromptAddenda)
	}
	if resolved.Harness.PromptAddenda[0].Text != "Project guidance wins." {
		t.Fatalf("prompt replacement = %#v", resolved.Harness.PromptAddenda[0])
	}
	if len(resolved.Harness.PermissionRules) != 2 || resolved.Harness.PermissionRules[0].Action != HarnessRuleDeny {
		t.Fatalf("permission rules = %#v, want project deny to replace user allow", resolved.Harness.PermissionRules)
	}

	badProjectPath := writeConfig(t, `{"harness":{"permissionRules":[{"id":"repo-allow","match":"bash","action":"allow"}]}}`)
	_, err = Resolve(ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: badProjectPath, Env: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "project harness permission rule") {
		t.Fatalf("Resolve error = %v, want project allow rejection", err)
	}
}

func TestValidateBytesReportsHarnessIssues(t *testing.T) {
	_, issues := ValidateBytes([]byte(`{
		"harness": {
			"promptAddenda": [{"id": "blank", "text": " "}],
			"permissionRules": [{"id": "bad", "match": "bash", "action": "maybe", "minRisk": "severe"}]
		}
	}`))
	if len(issues) < 3 {
		t.Fatalf("issues = %#v, want prompt text, action, and risk issues", issues)
	}
	for _, want := range []string{"harness.promptAddenda[0].text", "harness.permissionRules[0].action", "harness.permissionRules[0].minRisk"} {
		if !hasIssuePath(issues, want) {
			t.Fatalf("issues = %#v, missing %s", issues, want)
		}
	}
}

func TestHarnessWriterPersistsAndNormalizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if _, err := SetHarnessPromptAddendum(path, HarnessPromptAddendum{ID: " team ", Text: " Prefer ripgrep. "}); err != nil {
		t.Fatalf("SetHarnessPromptAddendum: %v", err)
	}
	if _, err := SetHarnessPermissionRule(path, HarnessPermissionRule{
		ID:              " shell ",
		Match:           " bash ",
		Action:          "prompt",
		CommandContains: []string{" curl ", ""},
		Reason:          " Review network shell. ",
	}); err != nil {
		t.Fatalf("SetHarnessPermissionRule: %v", err)
	}

	cfg := readHarnessConfigFile(t, path)
	if got := cfg.Harness.PromptAddenda[0]; got.ID != "team" || got.Text != "Prefer ripgrep." {
		t.Fatalf("prompt addendum = %#v", got)
	}
	if got := cfg.Harness.PermissionRules[0]; got.ID != "shell" || got.Action != HarnessRuleAsk || len(got.CommandContains) != 1 || got.CommandContains[0] != "curl" {
		t.Fatalf("permission rule = %#v", got)
	}

	if _, err := RemoveHarnessPromptAddendum(path, "TEAM"); err != nil {
		t.Fatalf("RemoveHarnessPromptAddendum: %v", err)
	}
	if _, err := RemoveHarnessPermissionRule(path, "shell"); err != nil {
		t.Fatalf("RemoveHarnessPermissionRule: %v", err)
	}
	cfg = readHarnessConfigFile(t, path)
	if !cfg.Harness.Empty() {
		t.Fatalf("harness config = %#v, want empty after removals", cfg.Harness)
	}
}

func hasIssuePath(issues []Issue, path string) bool {
	for _, issue := range issues {
		if issue.FieldPath == path {
			return true
		}
	}
	return false
}

func readHarnessConfigFile(t *testing.T, path string) FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}
