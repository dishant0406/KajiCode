# Multi-Model Task Routing

KajiCode can route a run to a task role (e.g. `design`, `implement`) that carries
its own model, and can automatically route image attachments to a vision-capable
model when the active model does not support images. Routing is **opt-in** — with
no routing configured, every existing run is byte-identical to before.

This page is the config + behavior contract. For the code map, see
`docs/architecture.md` → *Multi-Model Routing*.

## Configuration

All routing is declared in `kajicode.toml` (and merges through the standard
config resolution):

```toml
# Map a task role to a model selector. Selector forms:
#   "provider:model"  -> explicit provider + model id
#   "<alias>"         -> a model alias from the model catalog/registry
#   "@role"           -> reference another role's selector
# Roles not present here fall back to the active model / `defaultModel`.
modelRoles = { design = "anthropic:claude-sonnet-4-5", implement = "gpt-4.1" }

# Model used when no role override applies and no active profile model is selected.
defaultModel = "gpt-4.1"

[images]
# How to handle image attachments when the active model is NOT vision-capable:
#   "auto"  -> route the request to the first vision-capable configured profile
#   "model" -> use modelRoles["vision"]
#   "off"   -> legacy behavior: drop the images and warn (the default)
visionRouting = "model"
```

- `EffectiveVisionRouting()` collapses anything other than `auto`/`model` to `off`,
  so unknown/malformed values never change legacy behavior.

## Selecting a role from the CLI

```bash
kajicode exec --role design "review the wireframes"
```

`--role <name>` routes the run to that role's model. It is an explicit override:
the headless loop has no live plan/worklist signal for the automatic classifier, so
`--role` is the reliable path in exec. Without `--role`, headless runs stay on the
default/active model.

Use `--role clear` in the TUI (see below) to unset a role at runtime.

## How routing works in the agent loop

`internal/agent` owns the loop-side switch:

- `agent.Options.RoleRouting *RoleRouting` — when non-nil, the loop queries
  `RoleFor(RoleContext)` at the start of each turn. If the selected role differs
  from the one in force, it swaps the run's provider to that role's profile via
  `Current(ctx, role)` — the exact same session/swap seam as mid-run model
  escalation. Messages are preserved (a role switch is a model swap, not a
  conversation reset), and the compactor's context-window budget is refreshed via
  `ContextWindowFor(profile)` when it is wired.
- `RoleSelector` (implemented by `ExplicitRole` and `AutoRole`, both pure) turns a
  `RoleContext` into a role string. `ExplicitRole` returns a fixed role; `AutoRole`
  is conservative — it routes only when a todo list is present *and* the last tool
  mutated a file, otherwise it returns `""` (stay on default). A prompted role
  (`RoleContext.PromptedRole`, set by explicit `/role` / `--role`) always wins over
  the heuristic, so intent is never overridden by auto-routing.

A routing/swap error is **non-fatal**: the run records a brief note and continues on
the current provider. A nil `RoleRouting` leaves the loop byte-identical.

## Vision routing in exec (`images.visionRouting`)

Headless `internal/cli/exec` implements the `images.visionRouting` modes. When the
run has image attachments and the effective model is not vision-capable:

- `model` → `RoleRouter.ProfileFor("vision")` must resolve to a different,
  vision-capable model.
- `auto` → the first configured profile whose model `modelregistry.SupportsVision`.

If routing engages, `resolved.Provider` is swapped to the vision profile **before**
the run provider is built, so the whole run — provider, `currentModel`, usage
attribution, compaction — uses the vision model. If routing cannot engage (off, no
vision-capable profile, or a build error), the legacy `drop + warn` path runs and
the images are cleared. Image handling is best-effort and never fatal.

## Image rejection mid-run

If a provider still rejects an image mid-stream (e.g. a role-routed model that turns
out not to be vision-capable), the agent loop returns a routing-aware hint: with role
routing active it points at the vision route; otherwise it suggests a known
vision-capable model.

## Code map

- `internal/config/types.go` — `ModelRoles`, `DefaultModel`, `ImagesConfig`,
  `EffectiveVisionRouting()`.
- `internal/modelregistry/roles.go` — role selector → provider profile resolution.
- `internal/modelregistry/default_roles.go` — the built-in canonical role catalog
  (`default`, `plan`, `design`, `implement`, `vision`, `review`, `fast`) with
  capability tags and `DefaultSelector` hints.
- `internal/agent/rolerouter.go` — `RoleRouter` (effective model, profile lookup,
  provider build, and capability-based fallback to a built-in default role).
- `internal/agent/roleselector.go` — `RoleContext`, `RoleSelector`,
  `ExplicitRole`, `AutoRole`, `RoleRouting`.
- `internal/agent/loop.go` — per-turn role switch (`applyRoleRouting`) and the
  `imageRejectHint` on rejection.
- `internal/cli/exec*.go` — `--role` flag and the vision-routing gate.
- `internal/config/writer.go` — `SetModelRole` persists a role→model binding.
- `internal/tui/role_command.go` — interactive `/role`: stage-1 role list →
  stage-2 model picker (`roleBindTarget`), `/role add <name>`, and the text paths
  (`/role status|list|clear|name`).
