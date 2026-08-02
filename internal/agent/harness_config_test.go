package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func TestBuildSystemPromptReportIncludesHarnessConfig(t *testing.T) {
	disabled := false
	report := BuildSystemPromptReport(Options{
		SystemPrompt: "base prompt",
		Harness: config.HarnessConfig{
			PromptAddenda: []config.HarnessPromptAddendum{
				{ID: "team", Text: "Prefer rg before broad scans."},
				{ID: "old", Text: "do not include", Enabled: &disabled},
			},
			PermissionRules: []config.HarnessPermissionRule{
				{ID: "shell-review", Match: "bash", Action: config.HarnessRuleAsk, Reason: "review shell commands"},
			},
		},
	}, true)

	section := systemPromptReportSection(report, promptSectionHarnessConfig)
	if section == "" {
		t.Fatal("missing harness-config prompt section")
	}
	if !strings.Contains(section, "Prefer rg before broad scans.") || !strings.Contains(section, "shell-review") {
		t.Fatalf("harness section missing configured content:\n%s", section)
	}
	if strings.Contains(section, "do not include") {
		t.Fatalf("disabled prompt addendum leaked into prompt:\n%s", section)
	}
}

func TestHarnessPermissionDenyRuleBlocksBeforePrompt(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))
	var events []PermissionEvent

	result, err := executeToolCall(context.Background(), registry, ToolCall{
		ID:        "c1",
		Name:      "bash",
		Arguments: `{"command":"rm -rf build"}`,
	}, PermissionModeAuto, Options{
		Cwd: root,
		Harness: config.HarnessConfig{PermissionRules: []config.HarnessPermissionRule{{
			ID:              "no-rm",
			Match:           "bash",
			Action:          config.HarnessRuleDeny,
			CommandContains: []string{"rm -rf"},
			Reason:          "destructive shell is blocked",
		}}},
		OnPermission: func(event PermissionEvent) { events = append(events, event) },
		OnPermissionRequest: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			t.Fatal("deny rule must not prompt")
			return PermissionDecision{}, nil
		},
	})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError || result.DenialReason != DenialPermissionDenied {
		t.Fatalf("result = %#v, want permission denial", result)
	}
	if !strings.Contains(result.Output, "destructive shell is blocked") {
		t.Fatalf("output = %q, want rule reason", result.Output)
	}
	if len(events) != 1 || events[0].Action != PermissionActionDeny || events[0].DecisionReason != "destructive shell is blocked" {
		t.Fatalf("permission events = %#v", events)
	}
}

func TestHarnessPermissionAskRuleForcesPromptOnAllowedTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	prompted := false

	result, err := executeToolCall(context.Background(), registry, ToolCall{
		ID:        "c1",
		Name:      "read_file",
		Arguments: `{"path":"notes.txt"}`,
	}, PermissionModeAuto, Options{
		Cwd: root,
		Harness: config.HarnessConfig{PermissionRules: []config.HarnessPermissionRule{{
			ID:     "review-reads",
			Match:  "read_file",
			Action: config.HarnessRuleAsk,
			Reason: "inspect reads",
		}}},
		OnPermissionRequest: func(_ context.Context, request PermissionRequest) (PermissionDecision, error) {
			prompted = true
			if request.Action != PermissionActionPrompt || request.Reason != "inspect reads" {
				t.Fatalf("request = %#v", request)
			}
			return PermissionDecision{Action: PermissionDecisionAllow}, nil
		},
	})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if !prompted {
		t.Fatal("ask rule did not prompt")
	}
	if result.Status != tools.StatusOK || !strings.Contains(result.Output, "hello") {
		t.Fatalf("result = %#v, want successful read after approval", result)
	}
}

func TestHarnessPermissionAllowRuleDoesNotBypassWorkspaceWriteGuard(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	registry := tools.NewRegistry()
	registry.Register(tools.NewWriteFileTool(root))

	result, err := executeToolCall(context.Background(), registry, ToolCall{
		ID:        "c1",
		Name:      "write_file",
		Arguments: `{"path":` + quoteJSONString(outside) + `,"content":"nope","overwrite":true}`,
	}, PermissionModeAuto, Options{
		Cwd:     root,
		Sandbox: sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: root, Policy: sandbox.DefaultPolicy()}),
		Harness: config.HarnessConfig{PermissionRules: []config.HarnessPermissionRule{{
			ID:     "allow-writes",
			Match:  "write_file",
			Action: config.HarnessRuleAllow,
		}}},
	})
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError || !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("result = %#v, want workspace write guard despite harness allow", result)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("outside file %s was created despite workspace guard", outside)
	}
}

func systemPromptReportSection(report SystemPromptReport, role promptSectionRole) string {
	for _, section := range report.Sections {
		if section.Role == string(role) {
			return section.Content
		}
	}
	return ""
}
