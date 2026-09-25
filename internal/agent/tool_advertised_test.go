package agent

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// safetyTool is a minimal tools.Tool with a fixed Safety, for advertising tests.
type safetyTool struct {
	name   string
	safety tools.Safety
}

func (t safetyTool) Name() string        { return t.name }
func (t safetyTool) Description() string { return "test tool" }
func (t safetyTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (t safetyTool) Safety() tools.Safety { return t.safety }
func (t safetyTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK}
}

// Every permission mode must advertise the same registered tools — the mode
// decides WHEN a call is approved (prompt vs auto-allowed), never WHETHER the
// tool is visible to the model. Only a PermissionDeny tool (operator-disabled or
// capability-absent) is withheld, in every mode.
func TestToolAdvertisedIsModeIndependent(t *testing.T) {
	read := safetyTool{name: "read_file", safety: tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}}
	write := safetyTool{name: "write_file", safety: tools.Safety{SideEffect: tools.SideEffectWrite, Permission: tools.PermissionPrompt}}
	shell := safetyTool{name: "bash", safety: tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionPrompt}}
	network := safetyTool{name: "net_tool", safety: tools.Safety{SideEffect: tools.SideEffectNetwork, Permission: tools.PermissionPrompt}}
	denied := safetyTool{name: "blocked", safety: tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionDeny}}

	advertised := []tools.Tool{read, write, shell, network}
	modes := []PermissionMode{
		PermissionModeAuto, PermissionModeAsk, PermissionModeUnsafe,
		PermissionModeAskAll, PermissionModeReadOnly, PermissionModeReadWrite,
		PermissionModeBypassAll,
	}

	for _, mode := range modes {
		for _, tool := range advertised {
			if !ToolAdvertised(tool) {
				t.Fatalf("mode %q must advertise %q", mode, tool.Name())
			}
		}
		if ToolAdvertised(denied) {
			t.Fatalf("mode %q must NOT advertise a denied tool", mode)
		}
	}
}
