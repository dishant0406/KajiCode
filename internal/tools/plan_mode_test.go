package tools

import (
	"context"
	"strings"
	"testing"
)

// TestExitPlanModeToolIsControlOnly pins the tool's contract: it advertises no
// parameters, performs no side effect, is auto-allowed, and classifies as an
// interactive control tool so the plan-mode gate can allowlist it by name.
func TestExitPlanModeToolIsControlOnly(t *testing.T) {
	tool := NewExitPlanModeTool()
	if tool.Name() != ExitPlanModeToolName {
		t.Fatalf("name = %q, want %q", tool.Name(), ExitPlanModeToolName)
	}
	if params := tool.Parameters(); params.Type != "object" || len(params.Properties) != 0 {
		t.Fatalf("expected a parameterless object schema, got %#v", params)
	}
	if safety := tool.Safety(); safety.SideEffect != SideEffectNone || safety.Permission != PermissionAllow {
		t.Fatalf("expected an auto-allowed no-side-effect control tool, got %#v", safety)
	}
	if caps := CapabilitiesOf(tool); caps.Effect != EffectInteractive {
		t.Fatalf("effect = %v, want interactive", caps.Effect)
	}
}

// TestExitPlanModeToolRunDeclinesWithoutFrontend guards the headless path: the
// tool's own Run() declines rather than fabricating approval.
func TestExitPlanModeToolRunDeclinesWithoutFrontend(t *testing.T) {
	result := NewExitPlanModeTool().Run(context.Background(), map[string]any{})
	if result.Status != StatusOK {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Output), "no interactive user") {
		t.Fatalf("expected a no-interactive-user decline, got %q", result.Output)
	}
}
