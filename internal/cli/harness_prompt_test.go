package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
)

func TestRunHarnessPersistsAndListsUserConfig(t *testing.T) {
	deps, userConfig, _ := harnessCommandDeps(t)

	code, stdout, stderr := runCLICommand([]string{"harness", "prompt", "add", "team", "--text", "Prefer rg."}, deps)
	if code != exitSuccess {
		t.Fatalf("prompt add exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	code, stdout, stderr = runCLICommand([]string{"harness", "rule", "add", "shell-ask", "--match", "bash", "--action", "ask", "--command-contains", "curl", "--reason", "Review network shell."}, deps)
	if code != exitSuccess {
		t.Fatalf("rule add exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}

	cfg := readHarnessCLIConfig(t, userConfig)
	if len(cfg.Harness.PromptAddenda) != 1 || cfg.Harness.PromptAddenda[0].ID != "team" {
		t.Fatalf("prompt addenda = %#v", cfg.Harness.PromptAddenda)
	}
	if len(cfg.Harness.PermissionRules) != 1 || cfg.Harness.PermissionRules[0].CommandContains[0] != "curl" {
		t.Fatalf("permission rules = %#v", cfg.Harness.PermissionRules)
	}

	code, stdout, stderr = runCLICommand([]string{"harness", "list"}, deps)
	if code != exitSuccess {
		t.Fatalf("list exit = %d stderr=%q", code, stderr)
	}
	for _, want := range []string{"team [enabled]", "shell-ask [enabled] ask bash"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunHarnessRejectsProjectAllowRule(t *testing.T) {
	deps, _, projectConfig := harnessCommandDeps(t)

	code, _, stderr := runCLICommand([]string{"harness", "rule", "add", "repo-allow", "--match", "bash", "--action", "allow", "--project"}, deps)
	if code != exitUsage {
		t.Fatalf("exit = %d stderr=%q, want usage error", code, stderr)
	}
	if !strings.Contains(stderr, "project harness permission rules cannot use action allow") {
		t.Fatalf("stderr = %q", stderr)
	}
	if _, err := os.Stat(projectConfig); err == nil {
		t.Fatalf("project config %s should not be created for rejected allow rule", projectConfig)
	}
}

func TestRunPromptInspectIncludesHarnessSection(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		profile := config.ProviderProfile{Name: "work", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
		return config.ResolvedConfig{
			ActiveProvider: "work",
			Provider:       profile,
			Providers:      []config.ProviderProfile{profile},
			Harness: config.HarnessConfig{PromptAddenda: []config.HarnessPromptAddendum{{
				ID:   "team",
				Text: "Prefer repository-local scripts.",
			}}},
		}, nil
	}

	code := runWithDeps([]string{"prompt", "inspect", "--json", "--full"}, &stdout, &stderr, deps)
	if code != exitSuccess {
		t.Fatalf("exit = %d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{`"role": "harness-config"`, "Prefer repository-local scripts."} {
		if !strings.Contains(output, want) {
			t.Fatalf("prompt inspect output missing %q:\n%s", want, output)
		}
	}
}

func TestRunPromptInspectFullTextPrintsPrompt(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	deps := commandCenterDeps(t)
	deps.resolveConfig = func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
		profile := config.ProviderProfile{Name: "work", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-4.1"}
		return config.ResolvedConfig{
			ActiveProvider: "work",
			Provider:       profile,
			Providers:      []config.ProviderProfile{profile},
			Harness: config.HarnessConfig{PromptAddenda: []config.HarnessPromptAddendum{{
				ID:   "team",
				Text: "Prefer repository-local scripts.",
			}}},
		}, nil
	}

	code := runWithDeps([]string{"prompt", "inspect", "--full"}, &stdout, &stderr, deps)
	if code != exitSuccess {
		t.Fatalf("exit = %d stderr=%q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Full prompt:", "<harness_prompt_addenda>", "Prefer repository-local scripts."} {
		if !strings.Contains(output, want) {
			t.Fatalf("prompt inspect output missing %q:\n%s", want, output)
		}
	}
}

func TestRunPromptEditCreatesProjectInstructionFile(t *testing.T) {
	root := t.TempDir()
	var editedPath string
	deps := appDeps{
		getwd:          func() (string, error) { return root, nil },
		userConfigPath: func() (string, error) { return filepath.Join(t.TempDir(), "config.json"), nil },
		runEditor: func(path string) error {
			editedPath = path
			return nil
		},
	}

	code, stdout, stderr := runCLICommand([]string{"prompt", "edit", "--project"}, deps)
	if code != exitSuccess {
		t.Fatalf("exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	wantPath := filepath.Join(root, ".kajicode", "KAJICODE.md")
	if editedPath != wantPath {
		t.Fatalf("edited path = %q, want %q", editedPath, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if !strings.Contains(string(data), "# KajiCode Instructions") {
		t.Fatalf("prompt file content = %q", string(data))
	}
}

func harnessCommandDeps(t *testing.T) (appDeps, string, string) {
	t.Helper()

	root := t.TempDir()
	userConfig := filepath.Join(t.TempDir(), "config.json")
	projectConfig := filepath.Join(root, ".kajicode", "config.json")
	deps := appDeps{
		getwd:          func() (string, error) { return root, nil },
		userConfigPath: func() (string, error) { return userConfig, nil },
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			projectPath := ""
			if _, err := os.Stat(projectConfig); err == nil {
				projectPath = projectConfig
			}
			opts := config.ResolveOptions{
				UserConfigPath:    userConfig,
				ProjectConfigPath: projectPath,
				Env:               map[string]string{},
				Overrides:         overrides,
			}
			return config.Resolve(opts)
		},
	}
	return deps, userConfig, projectConfig
}

func runCLICommand(args []string, deps appDeps) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithDeps(args, &stdout, &stderr, deps)
	return code, stdout.String(), stderr.String()
}

func readHarnessCLIConfig(t *testing.T, path string) config.FileConfig {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}
