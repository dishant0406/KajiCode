package agent

import (
	"context"
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

type profileTestTool struct {
	name       string
	sideEffect tools.SideEffect
	permission tools.Permission
}

func (tool profileTestTool) Name() string                                     { return tool.name }
func (tool profileTestTool) Description() string                              { return tool.name }
func (tool profileTestTool) Parameters() tools.Schema                         { return tools.Schema{} }
func (tool profileTestTool) Run(context.Context, map[string]any) tools.Result { return tools.Result{} }
func (tool profileTestTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tool.sideEffect, Permission: tool.permission}
}

func TestPermissionProfileMatrix(t *testing.T) {
	profiles := []struct {
		mode  PermissionMode
		read  tools.Permission
		write tools.Permission
		shell tools.Permission
	}{
		{PermissionModeAskAll, tools.PermissionPrompt, tools.PermissionPrompt, tools.PermissionPrompt},
		{PermissionModeReadOnly, tools.PermissionAllow, tools.PermissionPrompt, tools.PermissionPrompt},
		{PermissionModeReadWrite, tools.PermissionAllow, tools.PermissionAllow, tools.PermissionPrompt},
		{PermissionModeBypassAll, tools.PermissionAllow, tools.PermissionAllow, tools.PermissionAllow},
	}
	for _, profile := range profiles {
		t.Run(string(profile.mode), func(t *testing.T) {
			for _, test := range []struct {
				name string
				side tools.SideEffect
				want tools.Permission
			}{
				{"read", tools.SideEffectRead, profile.read},
				{"write", tools.SideEffectWrite, profile.write},
				{"shell", tools.SideEffectShell, profile.shell},
				{"network", tools.SideEffectNetwork, profile.shell},
				{"local-control", tools.SideEffectLocalControl, profile.shell},
				{"none", tools.SideEffectNone, tools.PermissionAllow},
			} {
				tool := profileTestTool{name: test.name, sideEffect: test.side, permission: tools.PermissionAllow}
				if got := profilePermission(profile.mode, tool, nil); got != test.want {
					t.Errorf("%s permission = %q, want %q", test.name, got, test.want)
				}
			}
		})
	}
}

func TestSafePermissionProfilesNeverOverrideStaticDeny(t *testing.T) {
	tool := profileTestTool{name: "denied", sideEffect: tools.SideEffectRead, permission: tools.PermissionDeny}
	for _, mode := range []PermissionMode{PermissionModeAskAll, PermissionModeReadOnly, PermissionModeReadWrite} {
		if got := profilePermission(mode, tool, nil); got != tools.PermissionDeny {
			t.Errorf("%s permission = %q, want deny", mode, got)
		}
	}
	if got := profilePermission(PermissionModeBypassAll, tool, nil); got != tools.PermissionAllow {
		t.Errorf("bypass-all permission = %q, want allow", got)
	}
}
