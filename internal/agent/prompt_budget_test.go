package agent

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// These ceilings are a deliberate ratchet on KajiCode's fixed per-turn overhead — the
// system prompt and the eager tool schemas that ride on EVERY request. They are
// set ~10% above the current measured cost, using ApproxTextTokens (the same
// estimate the compaction loop and /context use, ~non-whitespace-bytes/4). A change
// that pushes either past its ceiling must be justified (and the ceiling raised
// deliberately) or trimmed — the per-turn floor should not creep up silently.
//
// Measured baselines (2026-07): base system prompt ~3160 tokens; the tools a normal
// (auto permission-mode) interactive turn sends ~2430 tokens. The tool figure is
// the auto-mode advertised set, not the full core registry — higher-risk tools that
// only advertise in other modes are excluded, matching what a real turn actually
// pays. The todo_read/todo_write, multi_edit, and ls tools advanced this floor and
// are part of the intended eager (read-only, low-risk) surface, so the ceiling was
// raised deliberately to keep 10% headroom over the measured 2828 tokens.
//
// The declarative tool-spec + LSP work added web_search (319), lsp_navigate (289),
// and the new ask_user/request_permissions permission flow tooling to the eager
// surface; all are read-only / low-risk interaction tools that must be advertised
// in auto mode, so the ceiling was raised deliberately to keep ~10% headroom over
// the measured 3327 tokens rather than trimming the surface.
const (
	maxBaseSystemPromptTokens = 3500
	maxEagerToolSchemaTokens  = 3600
)

func TestSystemPromptTokenBudget(t *testing.T) {
	// Minimal render: a model is set (so the session block renders) but no Cwd, so
	// the workspace map, project guidelines, and repo map are excluded — this is the
	// fixed base every session pays regardless of the workspace it runs in.
	prompt := buildSystemPrompt(Options{Model: "claude-opus-4-8"})
	got := ApproxTextTokens(prompt)
	t.Logf("base system prompt: %d tokens (%d bytes)", got, len(prompt))
	if got > maxBaseSystemPromptTokens {
		t.Fatalf("base system prompt is %d tokens, over the %d ceiling — trim it or raise the ceiling deliberately", got, maxBaseSystemPromptTokens)
	}
}

func TestEagerToolSchemaTokenBudget(t *testing.T) {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(t.TempDir(), nil) {
		registry.Register(tool)
	}
	// Options{} keeps DeferThreshold at 0, so deferral is inactive and every core
	// tool is exposed eagerly — exactly what a plugin-free session sends each turn.
	exposed, _ := partitionTools(registry, PermissionModeAuto, Options{}, map[string]bool{})
	got := estimateToolDefTokens(exposed)
	t.Logf("eager core tool schemas: %d tokens across %d tools", got, len(exposed))
	if got > maxEagerToolSchemaTokens {
		t.Fatalf("eager tool schemas are %d tokens, over the %d ceiling — defer a tool or raise the ceiling deliberately", got, maxEagerToolSchemaTokens)
	}
}
