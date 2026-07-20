package sandbox

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPermissionProfilesControlFilesystemScope(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file.txt")
	engine := NewEngine(EngineOptions{WorkspaceRoot: workspace, Policy: DefaultPolicy()})

	for _, test := range []struct {
		name       string
		mode       PermissionMode
		sideEffect SideEffect
		permission Permission
		want       Action
	}{
		{"ask-all-read", PermissionModeAskAll, SideEffectRead, PermissionPrompt, ActionPrompt},
		{"read-only-read", PermissionModeReadOnly, SideEffectRead, PermissionAllow, ActionAllow},
		{"read-only-write", PermissionModeReadOnly, SideEffectWrite, PermissionPrompt, ActionPrompt},
		{"read-write-write", PermissionModeReadWrite, SideEffectWrite, PermissionAllow, ActionAllow},
		{"bypass-write", PermissionModeBypassAll, SideEffectWrite, PermissionAllow, ActionAllow},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := engine.Evaluate(context.Background(), Request{
				ToolName:       "write_file",
				SideEffect:     test.sideEffect,
				Permission:     test.permission,
				PermissionMode: test.mode,
				Args:           map[string]any{"path": outside},
			})
			if decision.Action != test.want {
				t.Fatalf("action = %q, want %q (%s)", decision.Action, test.want, decision.ErrorString())
			}
		})
	}
}

func TestBypassAllDisablesSandboxBeforeStaticDeny(t *testing.T) {
	engine := NewEngine(EngineOptions{WorkspaceRoot: t.TempDir(), Policy: DefaultPolicy()})
	decision := engine.Evaluate(context.Background(), Request{
		ToolName:       "blocked",
		SideEffect:     SideEffectShell,
		Permission:     PermissionDeny,
		PermissionMode: PermissionModeBypassAll,
	})
	if decision.Action != ActionAllow || decision.Reason != "sandbox disabled" {
		t.Fatalf("decision = %#v, want disabled allow", decision)
	}
}
