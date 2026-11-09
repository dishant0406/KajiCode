package tools

import (
	"context"
	"strings"
	"testing"
)

// TestFinishToolIsControlOnly pins the tool's contract: one required summary,
// no side effect, auto-allowed, and never run in a parallel read batch.
func TestFinishToolIsControlOnly(t *testing.T) {
	tool := NewFinishTool()
	if tool.Name() != FinishToolName {
		t.Fatalf("name = %q, want %q", tool.Name(), FinishToolName)
	}
	params := tool.Parameters()
	if len(params.Required) != 1 || params.Required[0] != "summary" {
		t.Fatalf("expected a required summary, got %#v", params.Required)
	}
	if safety := tool.Safety(); safety.SideEffect != SideEffectNone || safety.Permission != PermissionAllow {
		t.Fatalf("expected an auto-allowed no-side-effect control tool, got %#v", safety)
	}
	if caps := CapabilitiesOf(tool); caps.Effect != EffectInteractive || caps.ThreadSafe {
		t.Fatalf("capabilities = %#v, want interactive and not thread-safe", caps)
	}
}

// TestFinishToolRunFallback guards a run that does not require finish: the
// call ends nothing and tells the model how such a run ends.
func TestFinishToolRunFallback(t *testing.T) {
	result := NewFinishTool().Run(context.Background(), map[string]any{"summary": "done"})
	if result.Status != StatusOK || !strings.Contains(result.Output, "plain text") {
		t.Fatalf("unexpected fallback result: %#v", result)
	}
}
