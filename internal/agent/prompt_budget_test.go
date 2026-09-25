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
// interactive turn sends ~2430 tokens. That figure was the auto-mode advertised
// subset; tool advertising no longer depends on the permission mode (every
// non-denied tool is visible in every mode), so the eager set is now the full core
// registry — 22 tools / 5383 tokens on a plugin- and MCP-free session. The ceiling
// was raised deliberately to keep 10% headroom over that full surface rather than
// re-hiding mutators in auto. MCP-heavy sessions still collapse behind tool_search
// via the deferral threshold, so the eager floor stays at the core set.
const (
	maxBaseSystemPromptTokens = 3500
	maxEagerToolSchemaTokens  = 6000
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
	exposed, _ := partitionTools(registry, Options{}, map[string]bool{})
	got := estimateToolDefTokens(exposed)
	t.Logf("eager core tool schemas: %d tokens across %d tools", got, len(exposed))
	if got > maxEagerToolSchemaTokens {
		t.Fatalf("eager tool schemas are %d tokens, over the %d ceiling — defer a tool or raise the ceiling deliberately", got, maxEagerToolSchemaTokens)
	}
}
