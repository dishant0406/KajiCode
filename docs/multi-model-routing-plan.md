# MULTI-MODEL TASK ROUTING — HARD IMPLEMENTATION PLAN

Status: **PLAN (ready to implement)**
Latest revision: v2 (hard, granular)
Owner pass log: see section "IMPLEMENTATION TRACKING LOG" at the very bottom.

> **HOW TO USE THIS FILE (read each time you start working):**
> 1. Read section **WORKFLOW** and **DECISIONS BEFORE YOU START**.
> 2. Read the section that matches the phase you are on (A→E).
> 3. Implement in the file:line order given in that section.
> 4. After EVERY completed step, append a row to **IMPLEMENTATION TRACKING LOG** at the
>    bottom describing exactly what you changed and what you verified. Never finish a
>    work session without updating this log. It is your source of truth for resuming.
> 5. Do NOT re-do steps already checked in the log.

This file is deliberately GRANULAR: every integration point has its exact existing
code, the exact edit to make, the exact file:line, and the exact validation command.
If a step does not have a real symbol/line, STOP and re-derive it before editing.

---

## 0. WORKFLOW

The plan is split into 6 phases. Each phase is independently shippable and has its own
test / build gate. **Recommended order: B1 (config schema) → A (registry resolution) →
D (vision routing, highest value) → C2 (explicit role switch) → C1 (auto classifier, defer) →
E (TUI + docs).** The phases were researched so each builds on the previous with minimal
rework. Do not implement all at once; implement and verify phase-by-phase.

Global validation gate (run after every completed function/file):
```bash
cd /Users/dishants/projects/KajiCode
make fmt-check
go vet ./...
go test ./...                       # full; use -run <Pkg>.<Test> for fast feedback first
git diff --check
```
Per-phase test targets are listed inside each phase. Run these focused targets first and
the full suite at the end of each phase.

**Golden rules for edits:**
- Never rewrite a whole file for this feature. Apply targeted edits at the exact lines
  given, matching surrounding style (comments before the edit, same indentation).
- Preserve the user's working tree: no unrelated files, no `gofmt` of unrelated code.
- Read the target file fully once before editing (some call sites differ slightly from
  the snippets here; confirm before you patch).
- Add small focused tests per AGENTS.md (test permission metadata, capability, fallback,
  semantics — see each phase).

---

## 0.5. DECISIONS BEFORE YOU START (make these ONCE, record in log)

These are the open choices from the research. The plan assumes the defaults below.
Your first log entry should record which you chose.

1. **Router dispatch option (C1)** — A: wrap provider, or B: extend ModelSessionSwitcher.
   **Default chosen: A (wrapping provider).** Rationale: minimal loop churn; all
   reconnect/compaction already go through the same `provider` local; no session
   semantics rewritten. Method B is documented as the alternative.
2. **Temporary vs persisted switches** — **Default: temporary (keep shared message
   history)** like oh-my-pi `setModelTemporary`/`{ephemeral}`, so role switches do not
   lose conversation. The active provider swap mechanism (`loop.go:849-902`) already
   keeps `messages` intact — reuse it verbatim.
3. **Context-window on switch** — **Default: recompute** `options.ContextWindow` from the
   new model's registry entry during a role switch; fix the KNOWN LIMITATION at
   `loop.go:895-899` (the compactor budget). Provide a fallback: if unknown, keep the
   prior budget.
4. **Vision routing scope (D)** — **Default: session-level routing** (whole request goes
   to a vision-capable profile). Content-rewriting (pi-deepseek-vision style) is a
   SEPARATE, later feature — explicitly out of scope for this plan's Phase D.
5. **Auto-role classifier (C1)** — **Default: CONSERVATIVE.** Only auto-route when there
   is a strong signal (plan accepted → implement, i.e. a todo list exists and an
   edit/write tool fires). Otherwise stay on default. Provide ExplicitRole as the
   reliable path.

---

# PHASE A — MODEL REGISTRY: ROLE-AWARE RESOLUTION

Owner packages: `internal/modelregistry`, `internal/config`.
Goal: resolve a `modelRoles` value (`provider:model`, a registry alias, or `@role`) to a
concrete `ModelEntry` + provider profile, with a fallback chain and a cycle guard.

### A1. New file `internal/modelregistry/roles.go`
Create this file. It holds the pure resolver (no I/O, no config dependency — mirrors
how `resolve.go` stays dependency-free).

```go
package modelregistry

import (
	"fmt"
	"strings"
)

// RoleRef is a single model selector for a task role. It is the resolved form of a
// modelRoles value after alias/`@role` expansion.
type RoleRef struct {
	// Selector is the expanded, alias-free selector. Empty means "no role override
	// is meaningful; fall back to the active model".
	Selector string
	// ProviderHint is the optional "provider:" prefix on the selector ("" if none),
	// already stripped from Selector.
	ProviderHint string
	// FromAlias reports whether this came from an `@role` alias expansion (used only
	// for cycle diagnostics + transcript messages).
	FromAlias bool
}

// ExpandRoleSelector expands a single modelRoles value. Supported forms:
//   - ""            -> empty (caller falls back to active model)
//   - "@role"       -> recursively expand modelRoles[role] (requires a roles map)
//   - "provider:model" / "alias" / "model" -> pass through unchanged (bare model)
//
// Returns (expanded, error). A cycle (@a -> @b -> @a) is an error; an unknown @role
// is an error. orbis: path of already-expanded roles to detect cycles.
func ExpandRoleSelector(selector string, roles map[string]string, orbis map[string]bool) (RoleRef, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return RoleRef{}, nil
	}
	if strings.HasPrefix(selector, "@") {
		role := strings.TrimPrefix(selector, "@")
		if orbis[role] {
			return RoleRef{}, fmt.Errorf("model role cycle detected involving %q", role)
		}
		if orbis == nil {
			orbis = map[string]bool{}
		}
		next, ok := roles[role]
		if !ok {
			return RoleRef{}, fmt.Errorf("unknown model role %q", role)
		}
		orbis[role] = true
		ref, err := ExpandRoleSelector(next, roles, orbis)
		ref.FromAlias = true
		return ref, err
	}
	// bare / provider:model — split the provider hint if a ":" is present.
	ref := RoleRef{Selector: selector}
	if i := strings.IndexByte(selector, ':'); i > 0 {
		ref.ProviderHint = selector[:i]
		ref.Selector = selector[i+1:]
		// If both sides are non-empty it's provider:model; else it's a lone model.
		if ref.Selector == "" {
			ref.ProviderHint = ""
			ref.Selector = selector
		}
	}
	return ref, nil
}

// ResolveRole resolves a modelRoles value against the registry to a ModelEntry.
// It expands @role aliases first, then runs the existing Resolve on the final model
// text. Returns the entry, whether a provider hint is present, and ok.
func (registry Registry) ResolveRole(value string, roles map[string]string) (ModelEntry, bool, bool) {
	ref, err := ExpandRoleSelector(value, roles, nil)
	if err != nil {
		return ModelEntry{}, false, false
	}
	if ref.Selector == "" {
		return ModelEntry{}, false, false // no override
	}
	entry, ok := registry.Resolve(ref.Selector)
	if !ok {
		// Maybe a deprecated id — honor the deprecation redirect.
		entry, _, ok = registry.ResolveWithFallback(ref.Selector)
	}
	return entry, ref.ProviderHint != "", ok
}

// ResolveRoleWithFallback expands a value, then resolves it, honoring deprecation
// redirects, returning the effective entry and an optional notice.
func (registry Registry) ResolveRoleWithFallback(value string, roles map[string]string) (ModelEntry, string, bool) {
	ref, err := ExpandRoleSelector(value, roles, nil)
	if err != nil {
		return ModelEntry{}, "", false
	}
	if ref.Selector == "" {
		return ModelEntry{}, "", false
	}
	entry, notice, ok := registry.ResolveWithFallback(ref.Selector)
	return entry, notice, ok
}

// MatchRoleProvider filters a resolved entry against a provider hint. If the hint is
// non-empty and does not match the entry's Provider (or any of its APIProviders),
// it is treated as not-matching so the caller can fall back. normalize is typically
// strings.ToLower.
func (entry ModelEntry) MatchRoleProvider(hint string, normalize func(string) string) bool {
	if hint == "" {
		return true
	}
	match := func(k ProviderKind) bool { return normalize(string(k)) == normalize(hint) }
	if match(entry.Provider) {
		return true
	}
	for _, p := range entry.APIProviders {
		if match(p) {
			return true
		}
	}
	return false
}
```

**Validation (A1):**
```bash
go test ./internal/modelregistry/ -run 'TestExpandRole|TestResolveRole|TestMatchRoleProvider'
```
Add `internal/modelregistry/roles_test.go` covering: bare selector, `provider:model`,
`@a`→`@b`→`@a` cycle error, unknown `@role` error, deprecation fallback, and
`MatchRoleProvider` hint filtering. (Confirm `ProviderKind` is a string type; if it is
`string`, `string(k)` works.)

### A2. Config schema: `modelRoles`, `defaultModel`, `images.visionRouting`
Edit `internal/config/types.go`.

a. Add to `FileConfig` (after `Providers []ProviderProfile`, ~line 372):
```go
	// ModelRoles maps a task role name to a model selector ("provider:model", a
	// registry alias, or "@role" to reference another role). Roles not present fall
	// back to the active model / DefaultModel.
	ModelRoles map[string]string `json:"modelRoles,omitempty"`
	// DefaultModel, when set, is the model used when no role override applies and no
	// active profile model matches a selector. Empty means "use the active profile's
	// own Model field".
	DefaultModel string `json:"defaultModel,omitempty"`
	// Images configures image-input handling.
	Images ImagesConfig `json:"images,omitempty"`
```

b. Add the same three fields to `Overrides` (~line 458) and a merged form into
`ResolvedConfig` (~line 464):
```go
	ModelRoles  map[string]string
	DefaultModel string
	Images       ImagesConfig
```

c. Add the `ImagesConfig` struct (new, near the other nested configs in `types.go`):
```go
type ImagesConfig struct {
	// VisionRouting controls how image attachments are handled when the active model
	// is not vision-capable. "auto" routes the request to the first vision-capable
	// available profile; "model" uses ModelRoles["vision"]; "off" keeps the legacy
	// drop+warn behavior.
	VisionRouting string `json:"visionRouting,omitempty"`
}

func (c ImagesConfig) EffectiveVisionRouting() string {
	switch strings.TrimSpace(c.VisionRouting) {
	case "auto", "model":
		return strings.TrimSpace(c.VisionRouting)
	default:
		return "off"
	}
}
```
(`strings` is already imported in `types.go` — it uses it in `HasProviderProfile`.)

d. Wire default/merge in `applyOverrides` (`internal/config/resolver.go:726`): add
```
	mergeRoleOverrides(&cfg, overrides) // copy ModelRoles map, set DefaultModel/Images if set
```
Implement `mergeRoleOverrides` in `resolver.go` (mirrors `mergeLearningConfig`):
```go
func mergeRoleOverrides(cfg *FileConfig, overrides Overrides) {
	if len(overrides.ModelRoles) > 0 {
		if cfg.ModelRoles == nil {
			cfg.ModelRoles = map[string]string{}
		}
		for k, v := range overrides.ModelRoles {
			cfg.ModelRoles[k] = v
		}
	}
	if strings.TrimSpace(overrides.DefaultModel) != "" {
		cfg.DefaultModel = overrides.DefaultModel
	}
	if strings.TrimSpace(overrides.Images.VisionRouting) != "" {
		cfg.Images.VisionRouting = overrides.Images.VisionRouting
	}
}
```

e. Thread through `Resolve` (`resolver.go:154-176`): copy `ModelRoles`, `DefaultModel`,
`Images` into the returned `ResolvedConfig`. Use a defensive copy of the map.

f. Unknown-field validation: `unknownfields.go` reflects over `FileConfig` JSON tags, so
`modelRoles`, `defaultModel`, `images` are automatically known once added — no edit needed
there. Add semantic validation in `validate.go` (`validateSemantics`, ~line 54): each
`ModelRoles` value must be non-empty OR an `@role`; a `DefaultModel` should resolve via the
registry (best-effort: if it can't resolve, produce an `Issue`, not an error).

**Validation (A2):**
```bash
go test ./internal/config/ -run 'Test.*Role|Test.*ModelRoles|Test.*Vision'
```
Add `internal/config/role_config_test.go` and `internal/config/images_config_test.go`.

### A3. Keep ALL profiles available to the router
`normalizeProvidersWithOptions` (`resolver.go:932-986`) ALREADY returns the full
normalized `[]ProviderProfile` slice plus the single active pick. `ResolvedConfig.Providers`
also already holds all of them (`types.go:462`). **No change needed** — but add a test that
proves the router can find a non-active profile by provider+model:
```go
// resolver role test: with two providers A (active) and B, ResolvedConfig.Providers
// must contain B with its Model intact so the router can build a provider for B.
```

---

# PHASE B — RUNTIME ROUTER (build providers from any profile)

Owner: `internal/agent`, `internal/cli`, `internal/providers`.
Goal: a `RoleRouter` that turns a `(role, context)` into a `ProviderProfile` + builds a
provider for it, reusing the existing switch seam.

### B1. `internal/agent/rolerouter.go` — the router core
Create. It MUST NOT import `cli`. It consumes `config.ResolvedConfig`, a
`modelregistry.Registry`, and a provider-builder function.

```go
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

// ProviderBuilder builds a runtime provider from a resolved profile. Wire it to
// cli's deps.newProvider in app.go/exec.go (which already applies stored keys).
// ctx is optional; cli's deps.newProvider ignores it.
type ProviderBuilder func(ctx context.Context, profile config.ProviderProfile) (kajicoderuntime.Provider, error)

// RoleRouter maps a task role to a concrete provider profile and builds providers.
// It is stateless except for the resolver inputs (construct per run).
type RoleRouter struct {
	Resolved    config.ResolvedConfig
	Registry    modelregistry.Registry
	NewProvider ProviderBuilder
	// defaultModel wins over the active profile's own Model when set.
	defaultModel string
}

// EffectiveModel returns the model that should seed the run: DefaultModel if set and
// resolvable, else the active profile's Model.
func (r *RoleRouter) EffectiveModel() string {
	if strings.TrimSpace(r.defaultModel) != "" {
		if m, _, ok := r.Registry.ResolveWithFallback(r.defaultModel); ok {
			if m.APIModel != "" {
				return m.APIModel
			}
		}
	}
	return r.Resolved.Provider.Model
}

// ProfileFor returns the provider profile for a role. It returns the active profile
// when role is empty/"default", when there is no role override, or when no matching
// profile is found (non-fatal fallback).
func (r *RoleRouter) ProfileFor(role string) (config.ProviderProfile, bool) {
	if role == "" || role == "default" {
		return r.Resolved.Provider, true
	}
	selector, ok := r.Resolved.ModelRoles[role]
	if !ok || strings.TrimSpace(selector) == "" {
		return r.Resolved.Provider, true // fall back to active
	}
	if strings.HasPrefix(selector, "@") {
		selector, ok = resolveRoleAlias(selector, r.Resolved.ModelRoles, map[string]bool{})
		if !ok {
			return r.Resolved.Provider, false
		}
	}
	return r.profileForSelector(selector)
}

func (r *RoleRouter) profileForSelector(selector string) (config.ProviderProfile, bool) {
	pname, model, hasProvider := splitRoleSelector(selector)
	if hasProvider {
		if p, ok := r.findProfileByName(pname); ok {
			if model != "" {
				p.Model = model
			}
			return p, true
		}
		return r.Resolved.Provider, false
	}
	if p, ok := r.findProfileByModel(selector); ok {
		return p, true
	}
	return r.Resolved.Provider, false
}

// ProviderFor builds a runtime provider for a role and returns it with the profile.
// On any routing failure it falls back to the active profile (non-fatal).
func (r *RoleRouter) ProviderFor(ctx context.Context, role string) (kajicoderuntime.Provider, config.ProviderProfile, error) {
	profile, ok := r.ProfileFor(role)
	if !ok {
		profile = r.Resolved.Provider
	}
	if r.NewProvider == nil {
		return nil, profile, fmt.Errorf("agent: RoleRouter has no NewProvider builder wired")
	}
	p, err := r.NewProvider(ctx, profile)
	return p, profile, err
}
```
Private helpers (all in `rolerouter.go`):
- `resolveRoleAlias(value string, roles map[string]string, orbis map[string]bool) (string, bool)`
  — recursive `@` expansion with a cycle guard; returns the final selector string, or
  ("",false) on unknown role/cycle.
- `splitRoleSelector(selector string) (provider, model string, hasProvider bool)` — if a
  `:` with a non-empty provider part, split; else bare model.
- `findProfileByName(name string) (config.ProviderProfile, bool)` — loop
  `r.Resolved.Providers`, match `Profile.Name` or `Profile.Provider` (case-insensitive).
- `findProfileByModel(model string) (config.ProviderProfile, bool)` — loop
  `r.Resolved.Providers`; resolve each `profile.Model` via `r.Registry.Resolve`; if the
  entry APIModel/ID matches the selector (or selector == profile.Model), return it. Track
  "best match" preference: an exact `profile.Model == selector` wins over a registry
  alias resolution.

**Validation (B1):**
```bash
go test ./internal/agent/ -run 'TestRoleRouter|TestProfileFor|TestResolveRoleAlias|TestSplitRoleSelector'
```
Add `rolerouter_test.go` — 2 fake profiles (A active openai:gpt-4.1, B anthropic:
claude-sonnet-4-5), a modelRoles map, a Registry; assert ProfileFor("design") returns B;
ProfileFor("") returns A; unknown role returns A; `@x` alias resolves.

> NOTE: `RoleRouter` needs `config`, `modelregistry`, `kajicoderuntime`. Check for an
> import cycle: `internal/agent` already imports `internal/config` (per harness_profile.go
> and types.go Options.Harness). Confirm agent does NOT create a cycle by importing
> `internal/modelregistry` (modelregistry does not import agent — safe) and that
> `internal/config` does not import agent (safe). Verify in the build.

### B2. Wire the router into exec + app
Edit `internal/cli/exec.go`:
- In `exec()` after `resolved` is computed (line ~295), construct the router:
```go
	router := agent.RoleRouter{
		Resolved:     resolved,
		Registry:     modelRegistry,
		NewProvider:  func(_ context.Context, p config.ProviderProfile) (kajicoderuntime.Provider, error) {
			return deps.newProvider(p)
		},
		defaultModel: resolved.DefaultModel,
	}
```
  (Confirm `deps.newProvider`'s real signature — research shows `func(config.ProviderProfile)(...);`
  if it does NOT take ctx, the wrapper above adapts it.)
- Use `router.EffectiveModel()` for seeding where the active profile model feeds the run:
  in `exec.go` where `overrides.Provider.Model` is derived (line ~278-290), after
  `resolved` is available, if `options.model`/specModel is empty AND
  `resolved.DefaultModel != ""`, seed `modelOverride = resolved.DefaultModel` and merge
  into a profile. (Simplest: keep the existing `--model` override handling; add: when
  `modelOverride == ""`, set the run model via the router's EffectiveModel.)

Edit `internal/cli/app.go`:
- In the TUI launch, add `RoleRouter: router` (with `NewProvider` adapter) to `tui.Options`
  so the TUI's `/set role` and vision routing share one router.
- Thread `ModelRoles`/`DefaultModel`/`ImagesConfig` through the TUI `AgentOptions` (not set
  today — add them):
```go
		ModelRoles:      resolved.ModelRoles,
		DefaultModel:    resolved.DefaultModel,
		ImagesConfig:    resolved.Images,
```

**Validation (B2):** build + targeted exec tests.

---

# PHASE C — AGENT LOOP: PER-ROLE DISPATCH

Owner: `internal/agent`, `internal/cli`.
Goal: the loop routes each provider call to the model for the current role.

### C1. Wrap the provider (Option A)
Add a `routingProvider` in `internal/agent/rolerouter.go` that implements
`kajicoderuntime.Provider` and picks the role model per call:

```go
// RoleProvider returns the provider+profile for the current role (ok=false => use
// fallback).
type RoleProvider func(ctx context.Context) (kajicoderuntime.Provider, config.ProviderProfile, bool)

// routingProvider wraps the loop's provider so each StreamCompletion can route to a
// role's model. Holds the router + current-role callback. Falls back to the active
// provider on any routing error (non-fatal).
type routingProvider struct {
	current  RoleProvider
	fallback kajicoderuntime.Provider
}

func (rp routingProvider) StreamCompletion(ctx context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p, _, ok := rp.current(ctx)
	if !ok || p == nil {
		p = rp.fallback
	}
	return p.StreamCompletion(ctx, request)
}
```

**How the role is chosen each turn** — add a **phase selector**:
- `internal/agent/roleselector.go`:
  ```go
  type RoleContext struct {
  	HasTodoList            bool
  	LastToolWasWriteOrEdit bool
  	NumTurns               int
  	PromptedRole           string // explicit role set via /set role or --role
  }
  type RoleSelector interface { RoleFor(ctx RoleContext) string }
  ```
- Implementations:
  - `ExplicitRole{Role string}` — returns the user-set role (drives `/set role`).
  - `AutoRole` — returns `"implement"` when `HasTodoList && LastToolWasWriteOrEdit`
    (conservative), else `""` (default).

**Wire into `Run`:** at `internal/agent/loop.go`:
- Add `RoleRouting *RoleRouting` to `Options` (`internal/agent/types.go`), where
  ```go
  type RoleRouting struct {
  	Current RoleProvider
  	RoleFor func(ctx RoleContext) string
  }
  ```
- After `provider = sessionProvider{session: session}` (line ~164), wrap:
  ```go
  	if options.RoleRouting != nil {
  		provider = routingProvider{current: options.RoleRouting.Current, fallback: provider}
  	}
  ```
- The per-turn choice: before building each request (the request block ~319-324), call
  `options.RoleRouting.RoleFor(lastCtx)` and, if it differs from the current routed
  profile, perform a provider swap using the SAME pattern as the mid-run escalation block
  (`loop.go:849-902`): build the new session for the role's profile (via the router's
  `ProviderFor`), swap `session`/`provider`, update `options.Model`/`ContextWindow`, keep
  `messages`.

**Fix the context-window KNOWN LIMITATION** (`loop.go:895-899`): when a swap happens,
recompute `options.ContextWindow` from the new model's registry entry. Provide a
`ContextWindowFor func(profile config.ProviderProfile) int` callback on `RoleRouting`
(because the loop does not currently hold the model registry; wire it in exec/app from
the registry there). Fallback to the prior window if unknown.

**Validation (C):** `go test ./internal/agent/ -run 'TestRun.*Rol|TestRoleRouter|TestRoutingProvider'`
Regression: existing escalation tests MUST still pass unchanged (routing is off by default).

### C2. Explicit role: `/set role` + `--role` / `--model-role`
- TUI: add a `commandRole` kind to `internal/tui/commands.go` (`commandKind` iota block)
  and a definition `/role` (group model). Handler in `command_center.go` (mirror
  `handleModelCommand`, `:390`): parse `role [role]` / `role <sel>`, update
  `ModelRoles[role]`, persist via the config writer, and call the router to switch. Show
  current role->model map on `/role` with no args.
- CLI: add `--role`/`--model-role role=selector` to `internal/cli/exec_parse.go` (mirror the
  `--image` flag handling) → `options.modelRoles map[string]string` →
  `overrides.ModelRoles`.

---

# PHASE D — VISION ROUTING

Owner: `internal/cli` (exec image gate), `internal/agent`, `internal/modelregistry`.
Goal: when images are present and the effective model is not vision-capable, route the
request (whole session) to a vision-capable profile instead of dropping the images.

### D1. Replace the drop+warn gate (`internal/cli/exec.go:371-376`)
Current:
```go
	if len(images) > 0 && !modelregistry.SupportsVision(modelRegistry, resolved.Provider.Model) {
		if _, err := fmt.Fprintf(stderr, "Model %s does not support image input; ignoring %d image(s).\n", resolved.Provider.Model, len(images)); err != nil {
			return exitCrash
		}
		images = nil
	}
```
Replace with a router-aware gate:
```go
	// Vision routing: if the effective model can't take images and routing is enabled,
	// switch the run's provider to a vision-capable profile. "off" preserves the old
	// drop+warn behavior.
	effectiveModel := router.EffectiveModel()
	needVision := len(images) > 0 && !modelregistry.SupportsVision(modelRegistry, effectiveModel)
	if needVision {
		switch resolved.Images.EffectiveVisionRouting() {
		case "model":
			if profile, ok := router.ProfileFor("vision"); ok && profile.Model != effectiveModel {
				if switched, err := deps.newProvider(profile); err == nil && switched != nil {
					if err := installVisionProvider(&providerVar, &resolved, switched, profile); err != nil {
						return err
					}
					break
				}
			}
			fallthrough // profile route failed -> try auto
		case "auto":
			if profile, ok := firstVisionCapableProvider(resolved, modelRegistry); ok && profile.Model != effectiveModel {
				if switched, err := deps.newProvider(profile); err == nil && switched != nil {
					if err := installVisionProvider(&providerVar, &resolved, switched, profile); err != nil {
						return err
					}
					break
				}
			}
			fallthrough
		default: // "off" or routing failure
			if _, err := fmt.Fprintf(stderr, "Model %s does not support image input; ignoring %d image(s).\n", effectiveModel, len(images)); err != nil {
				return exitCrash
			}
			images = nil
		}
	}
```
Helpers (in `exec.go`):
- `firstVisionCapableProvider(resolved config.ResolvedConfig, registry modelregistry.Registry) (config.ProviderProfile, bool)` — scan
  `resolved.Providers`, return the first where `modelregistry.SupportsVision(registry, p.Model)`.
- `installVisionProvider(provider *kajicoderuntime.Provider, resolved *config.ResolvedConfig, switched kajicoderuntime.Provider, profile config.ProviderProfile) error` — sets
  `resolved.Provider = profile`, updates `currentModel = profile.Model`, assigns the new
  provider into the variable actually passed to `agent.Run`, and emits a notice to `stderr`:
  `Vision routing: %s does not support images; using %s for this run.\n`.

**Important:** the `provider` built earlier in `exec()` (from `resolved.Provider`, fed to
`agent.Run`) must be replaced by `switched` so the whole run uses the vision profile.
Find the exact local holding the run provider and reassign it inside `installVisionProvider`.
If `agent.Run`'s provider arg is passed inline, refactor that local to a variable first.

### D2. Guard the agent-side image-reject path
`internal/agent/loop.go:339-341` turns a provider-side image rejection into a
"try a vision-capable model" hint. When role/vision routing is active and a provider
rejects images MID-run, emit a consistent notice and (best-effort) attempt a fallback to
a vision profile via the router, mirroring the escalation block. Keep this non-fatal.

**Validation (D):** `go test ./internal/cli/ -run 'Test.*Vision|Test.*Image'`
and the existing `exec_images_test.go`, `exec_streamjson_images_test.go` MUST pass
(routing is off by default → unchanged behavior).

---

# PHASE E — TUI SURFACE + DOCS

Owner: `internal/tui`, `internal/config`, docs.

### E1. TUI role/model picker
- Add a `/role` command (C2) plus a model-role section in the `/model` command output
  showing the current `ModelRoles` map (role → model).
- Provide a picker in `internal/tui/provider_wizard.go` or a new `role_picker.go`: list
  roles, let the user bind a role → model from the registry + saved providers. Use the
  `SavedProviders: usableSavedProviders(resolved.Providers)` already passed to TUI
  (app.go:848).
- Show the ACTIVE routed model per phase (feed `RoleRouting` into the status line via
  `OnPhase`).

**Validation (E1):** `go test ./internal/tui/ -run 'Test.*Role|Test.*Model'`

### E2. Docs
Per AGENTS.md: this change touches package ownership, startup flow, agent-loop behavior,
config surface → MUST update:
- `docs/architecture.md` — add `RoleRouter`, the routing provider, phase-selector, and
  vision-routing to the architecture map and package-ownership list.
- `docs/HOW_KAJICODE_WORKS.md` — add a section explaining role→model→profile dispatch,
  the temp-switch semantics, and the vision-route path.
- New `docs/MULTI_MODEL_ROUTING.md` — user-facing: roles, `modelRoles`, `defaultModel`,
  `images.visionRouting` (`off|auto|model`), `/role` and `--role` usage, configuration
  examples, and the auto-vs-explicit phase model.

---

# regression gates (run before PR)
```bash
make fmt-check
go vet ./...
go test ./...                       # full suite
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```
For release/sandbox/security-sensitive surfaces (vision routing touches provider
construction): run `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...`.

---

# ADDITIONAL RESEARCH NOTES (reference)

## oh-my-pi `modelRoles`
- Core primitive: `settings.modelRoles` = `role → "provider/model[:level]"`.
  Bundled roles: `default, smol, slow, vision, plan, designer, commit, tiny, task, advisor`
  (`packages/coding-agent/src/config/model-roles.ts:22-66`).
- `@role` aliases; recursive expansion with cycle guard (`model-resolver.ts:1033-1067`).
- Priority chains per role in `priority.json` (designer → gemini-3.1-pro, etc.) — the
  out-of-the-box fallback pattern ours mirrors in `findProfileByModel` / fallback.
- Temporary vs persisted switches: `setModelTemporary(...,{ephemeral})` for plan-mode/
  prewalk handoffs; `setModel(...,{persist})` for user prefs. Our temp-switch decision in
  DECISIONS #2 mirrors this.

## pi-deepseek-vision
- Trigger gate: only deepseek provider + target model + images present; else pass-through.
- Delegate: vision model `input.includes("image")` + configured auth → VLM describes images.
- Substitute: numbered markers + VLM text analysis; target never sees raw images.
- Fail-closed. Config: single JSON, `visionModel.provider/.id`, `targetModels`.
- Ours: session-level routing (DECISIONS #4), not content rewriting.

## opencode
- Per-agent `model` override; `build` (all tools) vs `plan` (edit/bash denied); subagents
  inherit the invoking model. Same "different model per task" idea, agent-level rather
  than phase-level.

## KajiCode facts verified during research
- `ResolvedConfig.Providers` already holds ALL profiles (`types.go:462`); `Provider` is the
  single active copy. Router needs NO new config plumbing for profile availability.
- The only provider call site is `streamWithReconnect(ctx, provider, request, ...)`
  (`loop.go:337`), plus final-answer `loop.go:1016`, reconnect.go, compaction.go — all route
  through the `provider` local, so wrapping `provider` (Option A) is sufficient.
- The mid-run swap mechanism (`loop.go:849-902`) already swaps `session`/`provider`, updates
  `options.Model`, and keeps `messages` intact — the exact template for role switches.
- Known limitation NOT yet fixed: context-window budget fixed at run start
  (`loop.go:895-899`) — Phase C1 fixes it.
- Vision capability: `modelregistry.SupportsVision` (vision.go:17-27) + name heuristic
  `VisionCapableByName` (vision.go:33-71). Caveat: it returns `false` (not unknown) for a
  catalog-known non-vision model, and falls back to name-match for unknown ids.
- Images: `ImageBlock{MediaType,Data}`, `Message.Images`, `Options.Images`
  (`kajicoderuntime/types.go:223-226,95-102`; `agent/types.go:308`). `--image` flag
  (`exec_parse.go:118-130`); ACP `image` content blocks (`acp/agent.go:509`); stream-json
  images merged at `exec.go:365-369`.
- Mid-run power switch rebuilds provider by cloning `resolved.Provider`, mutating only
  `.Model`, then `deps.newProvider` (`exec.go:429-447,458-477`). For routing to a DIFFERENT
  profile we swap the whole profile — this is what `RoleRouter.ProfileFor` returns.
- `TurnSessionProvider` lives in `internal/kajicoderuntime/session.go:73-83`; its
  `Capabilities()`/`ProviderCapabilities.SupportsVision` is the runtime-typed vision signal
  (`session.go:14-35`), distinct from `modelregistry.SupportsVision`.
- Slash commands: `commandKind` iota in `internal/tui/commands.go`; definitions in
  `commandDefinitions`; handlers in `command_center.go` (mirror `handleModelCommand`,
  `:390`). CLI flags parsed in `exec_parse.go` (mirror `--image`/`--model`).
- `PhaseEvent`/`OnPhase` already exist (`internal/agent/phase.go`; `types.go:377`) — use it
  to surface the active routed model in the TUI status.
- `Overrides` is applied in `applyOverrides` (`resolver.go:726`) BEFORE `normalizeProviders`.
- `deps.newProvider` signature (from `app.go`): wrapped by `fillAppDeps` to apply the stored
  key, so the router can call it for any profile and stay authenticated.

---

# IMPLEMENTATION TRACKING LOG

Read this file and update this log at the start and end of EVERY work session.
Format: add a new row at the top of the table. Do NOT delete old rows.

| # | Date | Phase/Step(s) | What I changed (file:line) | What I verified (command + result) | Next step |
|---|------|----------------|----------------------------|-----------------------------------|-----------|
| 1 | (fill) | DECISIONS | Recorded choices: A1=wrap provider, temporary switches, recompute ctx-window, session-level vision, conservative auto | n/a | Start A1 |
|   |       |               |                            |                                   |           |

Fill rows as you go. When resuming, start at the phase/step in the FIRST row that is NOT
marked complete, and read that phase's section fully before editing.
