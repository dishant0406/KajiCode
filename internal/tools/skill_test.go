package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, dir string, name string, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func TestSkillToolIsReadOnly(t *testing.T) {
	tool := NewSkillTool(t.TempDir())
	if tool.Name() != "skill" {
		t.Fatalf("Name = %q, want skill", tool.Name())
	}
	if tool.Safety().SideEffect != SideEffectRead {
		t.Fatalf("SideEffect = %s, want read", tool.Safety().SideEffect)
	}
	if tool.Safety().Permission != PermissionAllow {
		t.Fatalf("Permission = %s, want allow", tool.Safety().Permission)
	}
	if tool.Parameters().Type != "object" {
		t.Fatalf("schema type = %s, want object", tool.Parameters().Type)
	}
}

func TestSkillToolReturnsContentForKnownSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "confirmation-policy", "---\nname: confirmation-policy\ndescription: ask first\n---\n\n# Confirmation Policy\n\nAsk before risky actions.")

	tool := NewSkillTool(dir)
	result := tool.Run(context.Background(), map[string]any{"name": "confirmation-policy"})
	if result.Status != StatusOK {
		t.Fatalf("Status = %s, want ok (output: %s)", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "Ask before risky actions.") {
		t.Fatalf("Output missing skill body: %q", result.Output)
	}
}

func TestSkillToolAcceptsSkillAlias(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "demo", "body of demo")

	tool := NewSkillTool(dir)
	result := tool.Run(context.Background(), map[string]any{"skill": "demo"})
	if result.Status != StatusOK {
		t.Fatalf("Status = %s, want ok (output: %s)", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "body of demo") {
		t.Fatalf("Output = %q", result.Output)
	}
}

func TestSkillToolUnknownSkillErrorsAndListsAvailable(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "alpha", "a")
	writeSkillFile(t, dir, "beta", "b")

	tool := NewSkillTool(dir)
	result := tool.Run(context.Background(), map[string]any{"name": "missing"})
	if result.Status != StatusError {
		t.Fatalf("Status = %s, want error", result.Status)
	}
	if !strings.Contains(result.Output, "alpha") || !strings.Contains(result.Output, "beta") {
		t.Fatalf("error should list available skills, got: %q", result.Output)
	}
}

func TestSkillToolMissingNameErrors(t *testing.T) {
	tool := NewSkillTool(t.TempDir())
	result := tool.Run(context.Background(), map[string]any{})
	if result.Status != StatusError {
		t.Fatalf("Status = %s, want error", result.Status)
	}
}

func TestSkillToolNoSkillsAvailable(t *testing.T) {
	tool := NewSkillTool(filepath.Join(t.TempDir(), "missing"))
	result := tool.Run(context.Background(), map[string]any{"name": "anything"})
	if result.Status != StatusError {
		t.Fatalf("Status = %s, want error", result.Status)
	}
	if !strings.Contains(strings.ToLower(result.Output), "no skills") {
		t.Fatalf("expected a no-skills message, got: %q", result.Output)
	}
}

// TestSkillToolResolvesProjectSkillRoots proves the core skill tool is cwd-aware:
// skills delivered via RunOptions.ProjectSkillRoots (a subtree .skills dir the
// run has discovered) are resolved even though they are not under the tool's
// configured dir, and they appear as least-precedence after the configured dir.
func TestSkillToolResolvesProjectSkillRoots(t *testing.T) {
	globalDir := t.TempDir()
	writeSkillFile(t, globalDir, "shared", "global shared body")

	projectRoot := t.TempDir()
	writeSkillFile(t, projectRoot, "local-only", "local-only body")
	// A name clash: project wins over nothing here, but a global skill with the
	// same name must still resolve (project merged after global, no clobber).
	writeSkillFile(t, projectRoot, "shared", "project shared body")

	tool := NewSkillTool(globalDir)

	// Without project roots, the local-only skill is unknown.
	missing := tool.Run(context.Background(), map[string]any{"name": "local-only"})
	if missing.Status != StatusError {
		t.Fatalf("expected error for local-only without project roots, got %s", missing.Status)
	}

	// With project roots via options, Run gets the local-only skill and still sees
	// the global shared skill.
	withOptions := tool.RunWithOptions(context.Background(), map[string]any{"name": "local-only"}, RunOptions{
		ProjectSkillRoots: []string{projectRoot},
	})
	if withOptions.Status != StatusOK {
		t.Fatalf("cwd-aware resolve failed: %s %s", withOptions.Status, withOptions.Output)
	}
	if !strings.Contains(withOptions.Output, "local-only body") {
		t.Fatalf("project skill output missing body: %q", withOptions.Output)
	}

	// Global precedence: the configured dir (globalDir) comes first in the roots
	// merge, so the global "shared" wins the clash and is returned.
	global := tool.RunWithOptions(context.Background(), map[string]any{"name": "shared"}, RunOptions{
		ProjectSkillRoots: []string{projectRoot},
	})
	if global.Status != StatusOK {
		t.Fatalf("shared resolve failed: %s %s", global.Status, global.Output)
	}
	if !strings.Contains(global.Output, "global shared body") {
		t.Fatalf("expected global shared to win precedence, got: %q", global.Output)
	}
	if strings.Contains(global.Output, "project shared body") {
		t.Fatalf("project shared leaked over global precedence: %q", global.Output)
	}
}

func TestSkillToolBuiltinCustomizeKajicodeResolves(t *testing.T) {
	tool := NewSkillTool(t.TempDir())
	result := tool.Run(context.Background(), map[string]any{"name": "customize-kajicode"})
	if result.Status != StatusOK {
		t.Fatalf("builtin resolve failed: %s %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "customize-kajicode") {
		t.Fatalf("builtin output missing name: %q", result.Output)
	}
	// A real on-disk skill of the same name shadows the builtin.
	dir := t.TempDir()
	writeSkillFile(t, dir, "customize-kajicode", "---\nname: customize-kajicode\ndescription: user override\n---\n\nuser body")
	tool = NewSkillTool(dir)
	result = tool.Run(context.Background(), map[string]any{"name": "customize-kajicode"})
	if result.Status != StatusOK {
		t.Fatalf("override resolve failed: %s %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "user body") {
		t.Fatalf("expected real skill to shadow builtin, got: %q", result.Output)
	}
}

func TestSkillToolDenyPermissionBlocksLoad(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "secret-skill", "---\nname: secret-skill\ndescription: restricted\npermission: deny\n---\n\n# SECRET BODY\n\nDo not leak.")
	writeSkillFile(t, dir, "open-skill", "---\nname: open-skill\ndescription: public\n---\n\n# Open\n\nFine to load.")

	tool := NewSkillTool(dir)

	// Run: the deny skill's body must never be returned.
	denied := tool.Run(context.Background(), map[string]any{"name": "secret-skill"})
	if denied.Status != StatusError {
		t.Fatalf("deny skill Status = %s, want error", denied.Status)
	}
	if strings.Contains(denied.Output, "SECRET BODY") {
		t.Fatalf("deny skill body leaked: %q", denied.Output)
	}
	if !strings.Contains(denied.Output, "denied") {
		t.Fatalf("deny message missing, got: %q", denied.Output)
	}

	// PermissionForArgs: the agent loop sees deny for the restricted skill and
	// allow for the unconstrained skill and an unknown name. The concrete *skillTool
	// implements it directly (NewSkillTool returns the concrete type).
	if got := tool.PermissionForArgs(map[string]any{"name": "secret-skill"}); got != PermissionDeny {
		t.Fatalf("PermissionForArgs(secret-skill) = %s, want deny", got)
	}
	if got := tool.PermissionForArgs(map[string]any{"name": "open-skill"}); got != PermissionAllow {
		t.Fatalf("PermissionForArgs(open-skill) = %s, want allow", got)
	}
	if got := tool.PermissionForArgs(map[string]any{"name": "unknown"}); got != PermissionAllow {
		t.Fatalf("PermissionForArgs(unknown) = %s, want allow", got)
	}
}

func TestSkillToolPromptPermissionFlagged(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "guard-skill", "---\nname: guard-skill\ndescription: needs care\npermission: prompt\n---\n\n# Guard\n\nCareful body.")
	tool := NewSkillTool(dir)
	if got := tool.PermissionForArgs(map[string]any{"name": "guard-skill"}); got != PermissionPrompt {
		t.Fatalf("PermissionForArgs(guard-skill) = %s, want prompt", got)
	}
}

// TestSkillToolDenyYieldsToBypassAll links the skill permission gate to the same
// bypass-all setting as every other tool: a frontmatter deny skill stays hard
// blocked, but under bypass-all RunOptions the deny guard relaxes and the body
// loads (the loop's profilePermission already lets bypass-all through).
func TestSkillToolDenyYieldsToBypassAll(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "secret-skill", "---\nname: secret-skill\ndescription: restricted\npermission: deny\n---\n\n# SECRET BODY\n\nDo not leak.")
	tool := NewSkillTool(dir)

	// ask-all (or no mode): deny stays hard-blocked, body never returned.
	denied := tool.RunWithOptions(context.Background(), map[string]any{"name": "secret-skill"}, RunOptions{PermissionMode: "ask-all"})
	if denied.Status != StatusError {
		t.Fatalf("ask-all Status = %s, want error", denied.Status)
	}
	if strings.Contains(denied.Output, "SECRET BODY") {
		t.Fatalf("deny skill body leaked under ask-all: %q", denied.Output)
	}

	// bypass-all: deny guard relaxes, body loads (shares the global bypass).
	allowed := tool.RunWithOptions(context.Background(), map[string]any{"name": "secret-skill"}, RunOptions{PermissionMode: "bypass-all"})
	if allowed.Status != StatusOK {
		t.Fatalf("bypass-all Status = %s, want ok (output: %s)", allowed.Status, allowed.Output)
	}
	if !strings.Contains(allowed.Output, "SECRET BODY") {
		t.Fatalf("bypass-all should return the deny skill body, got: %q", allowed.Output)
	}

	// An unknown mode normalizes to ask-all, so the deny guard still holds.
	invalid := tool.RunWithOptions(context.Background(), map[string]any{"name": "secret-skill"}, RunOptions{PermissionMode: "not-a-mode"})
	if invalid.Status != StatusError {
		t.Fatalf("invalid-mode Status = %s, want error", invalid.Status)
	}
}
