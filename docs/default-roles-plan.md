# Default Model Roles — Research Findings + Implementation Plan

> Goal: model omp's **default role system** where roles are pre-canned (not
> user-created) and the user only assigns a model to each role. Routing—task
> intent (plan/implement/design), vision, review—happens automatically. This
> replaces nothing that works; it layers "default roles" on top of the existing
> free-form `modelRoles` surface and wire them into the interactive `/role` UI.

---

## Part 1 — Research

### 1.1 omp (Oh My Pi): the model we are imitating

Source: `/tmp/omp-src` (can1357/oh-my-pi), `packages/coding-agent`.

**Fixed role catalog** (`src/config/model-roles.ts`):

```ts
type ModelRole =
  | "default" | "smol" | "slow" | "vision" | "plan"
  | "designer" | "commit" | "tiny" | "task" | "advisor";
```

Each has metadata `{ tag, name, color, hidden }` (`MODEL_ROLES`), e.g.
`default` → tag `DEFAULT`, `plan` → name `Architect`, `designer` → `Designer`,
`vision` → `VISION`. `hidden` roles aren't shown in the selector UI but are
still functional. `getKnownRoleIds()` always lists built-ins first, then appends
custom roles from `cycleOrder`/`modelRoles`/`modelTags` in config — so **custom
arbitrary roles coexist with the fixed catalog**.

**Model search-priority defaults** (`ROLE_PRIORITY_ALIAS` + `MODEL_PRIO`):
each built-in role maps to a fallback chain of model patterns (e.g. `default`
→ the "best" chain, `smol` → a fast chain, `slow` → a thinking/reasoning chain,
`vision` → vision-capable). `advisor` aliases `slow`; `tiny` aliases `smol`.
Omitting `modelRoles.<role>` = "auto-resolve to that role's priority chain".
This is the key idea we can adapt: **a role can resolve with no explicit
binding** by falling back to a capability-derived default.

**Assign/clear** (`src/modes/controllers/selector-controller.ts` ~858): assigning
the `default` role switches the live session model; assigning any other role
just writes `setModelRole(role, "provider/model")` (no live switch). Clearing a
non-default role writes `setModelRole(role, undefined)` → "auto-selection
applies". Scoped storage: `modelRoleStorage` = global (default) or project.

**Role aliases**: `@default`, `@smol`, ..., canonical prefix `@`, legacy
`pi/`. `*` is shorthand for the default role. A role selector can reference
another role (loop-guarded).

**How roles are *used* (routing triggers)** — this is the part to study for our
intent-routing:

- **Vision** (`src/utils/image-vision-fallback.ts:103-119`): when the active
  model is text-only and images arrive, resolve a vision model by priority
  `@vision` → `@default` → active → first image-capable available model. It then
  **saves the image and asks the vision model to describe it**, injecting the
  text `<description>` in place of the image block (so the text-only main model
  never gets raw image bytes). DeepSeek-specific variant (pi-deepseek-vision,
  §1.2) does the same as a preprocessing extension.
  - **Implication for us**: a `vision` role is consumed by the *vision-routing*
    path (image handling), NOT by turning the whole run into a vision model.
    KajiCode's CLI vision gate already does `router.ProfileFor("vision")` under
    `images.visionRouting=model` (exec.go:429) — we just need the default role to
    be discoverable without config.
- **Plan/designer**: omp ties these to *modes* (architect / design mode), not to
  a classifier. The "Architect" role is the model used while plan-mode is active.
- **Task/advisor**: `task` is the subagent/task-spawn model; `advisor` is the
  second-opinion reviewer model.

**Key architectural takeaway to mirror in KajiCode**:
1. Roles are a **fixed catalog** with metadata + capability tags.
2. **Unset roles still resolve** (to a capability-appropriate default chain).
3. Assigning a non-default role only writes config; the *active* role decides
   the live model.
4. Vision is routed **through the vision role only for the image-handling path**,
   not as a global model switch.

### 1.2 pi-deepseek-vision

Source: `/tmp/pi-dsv-src` (hqman/pi-deepseek-vision).

A Pi extension for **text-only DeepSeek + a separate vision VLM**:
- Config: `~/.pi/agent/deepseek-vision.json` → `{ visionModel: {provider,id},
  targetModels, language, maxAnalysisChars, cache }`.
- Triggers only when: provider == `deepseek`, model id in `targetModels`, AND
  the visible context contains an image.
- Behavior: replaces each `ImageContent` with `[Image N — analyzed by vision
  model]` + a text joint-visual-analysis block; DeepSeek always stays text-only.
- **Fail-closed**: on any vision error, the turn aborts and unprocessed images
  are never forwarded.
- **Single vision model, no fallback chain**, config-only (no `/` command).

**Relevance**: it's the same "preprocess images with a vision model, keep main
model text-only" idea as omp's vision-fallback, but hard-coded to DeepSeek. For
KajiCode we prefer omp's **fallback chain** (`@vision` → `@default` → active →
first vision-capable) and doing it in-path rather than as a separate extension.

### 1.3 KajiCode current state (verified by reading the code)

**Config** (`internal/config/types.go`):
- `FileConfig.ModelRoles map[string]string` — free-form role → selector.
- `FileConfig.DefaultModel string`.
- `ImagesConfig.VisionRouting` — `auto`/`model`/`off` (default `off`).

**Registry/capabilities** (`internal/modelregistry`):
- `models.go`: `ModelCapabilityVision`, `ModelCapabilityReasoning`, ... each
  `ModelEntry` has `Capabilities` + `standardReasoningEfforts()`.
- `catalog.go`: `ListByCapability(ModelCapability)` and
  `SupportsCapability(pattern, capability)` exist — **we can derive "default
  model for a role" from capabilities**.
- `vision.go`: `SupportsVision(registry, modelID)`.
- `roles.go`: `ExpandRoleSelector`, `ResolveRole`, `ResolveRoleWithFallback`,
  `MatchRoleProvider` — already handle `@role` aliases + provider:model.

**Agent loop routing (`internal/agent`)**:
- `roleselector.go`: `RoleContext{HasTodoList, LastToolWasWriteOrEdit,
  NumTurns, PromptedRole}`; `RoleSelector` interface; `ExplicitRole` (always
  returns the role); `AutoRole` (routes to "implement" only when `HasTodoList
  && LastToolWasWriteOrEdit`, and never overrides PromptedRole). `RoleRouting`
  struct wires `Current`/`RoleFor`/`ContextWindowFor` into the loop.
- `rolerouter.go`: `RoleRouter.ProfileFor(role)` maps role → provider profile
  (empty/"default"/unconfigured → active; `@role` aliases expanded; then
  `provider:model` or find-by-model). `FirstVisionCapableProvider`.

**TUI `/role` (`internal/tui/role_command.go`)**: bare `/role` opens the
interactive role→model picker (`newRolePicker`), per-role rows mark the active
role with `●`, plus control rows `➕ add new role` / `clear current role` /
`default model`. `openRoleModelPicker` reuses the model picker; `choosePicker`
(model.go:4352) binds when `roleBindTarget` set via `bindRoleToModel` →
`config.SetModelRole`. `activeRole` (explicit) drives `roleRoutingOptions`
(model.go:5259) which sets `agent.Options.RoleRouting`. Title bar shows
` · role <name>` (view.go:170).

**CLI vision gate (`internal/cli/exec.go:417-450`)**: when images present and
effective model lacks vision: `visionRouting=model` → `router.ProfileFor("vision")`;
`auto` → first vision-capable; else drop+warn.

**What exists vs. what is missing for "default roles":**
- ✅ Role→model binding UI (`/role`, interactive picker), persistence
  (`SetModelRole`), routing (`RoleRouter`, `RoleRouting`), vision role dispatch
  (CLI `model` mode), `@role` alias machinery, capability model.
- ❌ No **fixed default role catalog** (roles are all free-form).
- ❌ Unset roles **don't resolve** — `ProfileFor` returns the active profile when
  a role has no `modelRoles` entry; there's no capability-derived default chain.
- ❌ No **intent-based auto-routing** beyond the conservative todo+edit
  "implement" signal (`AutoRole`).
- ❌ TUI image path does **not** route through a vision role (it drops images at
  submit; see `modelSupportsVisionTUI`), whereas the CLI does.

---

## Part 2 — Design: Default Model Roles for KajiCode

Concretely inherited from omp, mapped onto KajiCode's existing names.

### 2.1 The fixed role catalog

Add a single source of truth in `internal/modelregistry/default_roles.go`
(kept small), describing each built-in role and its capability-derived fallback.

```
default     → resolve: configured DefaultModel → active model
plan        → capability REASONING, prefers largest context (architect)
design      → capability REASONING (designer)
implement   → capability TOOL_CALLING (default loop model)
vision      → capability VISION (only used by the vision path)
review      → capability REASONING, picks a *different* model than default
fast        → capability CHAT, prefers cheapest/low-latency (omp "smol")
```

Each entry: `id`, `displayName`, `tag`, `summary`, `fallback` (a function or
ordered set of capability-aware candidate selectors), and `defaultSelector`
(a concrete model string used only as a *suggestion*, applied never implicitly —
see §2.4).

**Why these seven**: they map 1:1 onto omp's meaningful roles
(plan/designer/implement/vision/task=subimpl/review=advisor/fast=smol) while
staying small enough to be genuinely "pre-canned". `default`/`vision` special-
case the existing routing seams; the rest are intent-roles for future tasks.

`getKnownRoleIds()` (omp) becomes KajiCode `DefaultRoles()` + `RoleInfo(id)`,
merged with configured `modelRoles` for the `/role` picker (**built-ins first,
then custom**) so existing free-form roles keep working.

### 2.2 Unset roles resolve (capability-derived fallback)

Extend `RoleRouter.ProfileFor` (rolerouter.go) so an unconfigured role returns
a *resolved* profile for known built-ins instead of silently falling back to the
active model:

1. `""`/`"default"` → active (unchanged).
2. `modelRoles[role]` set → existing resolution (unchanged).
3. else if role is a known built-in → use its `fallback` to pick the best
   available model (via `Registry.ListByCapability`), preferring one that the
   user actually has a saved provider for; if none can be selected → active.
4. else (unknown/custom free-form) → active (unchanged).

This gives omp-style behavior out of the box: `@plan` routes to a reasoning
model even with zero `modelRoles` config, and users only assign a model when
they want to override.

### 2.3 Default role → suggested-model hint (not implicit)

A role's `defaultSelector` is shown in the `/role` picker meta as a *suggestion*
("suggest: claude-opus-4.1") so a user assigning models sees a sensible starting
point. It is **never applied implicitly** — applying it would surprise users the
moment catalog defaults drift. Explicit assignment (existing `SetModelRole`)
always wins. This keeps the "pre-canned, assign per role" UX without silent
model churn.

### 2.4 Vision role already wires correctly (CLI); unify the TUI path

- CLI: `visionRouting=model` → `router.ProfileFor("vision")` already exists
  (exec.go:429). With §2.2 + default `vision` role (fallback first vision-capable
  via `registry.SupportsCapability(...Vision)`), the existing `model` mode now
  resolves **even when `modelRoles.vision` is unset**.
- TUI: the image path drops images when the active model lacks vision
  (`modelSupportsVisionTUI`, submit-time gate). To match omp, add a TUI vision
  fallback that (a) if a vision-capable profile resolves, swaps the *image
  handling* to round-trip a generated description (like omp's
  `image-vision-fallback`), falling back to `@vision` → first vision-capable,
  then to drop+warn. This is the one genuinely new runtime feature; it can be a
  follow-up PR (smaller, needs its own tests) rather than part of the role
  catalog PR.

### 2.5 Intent auto-routing (the "routing happens automatically" ask)

omp doesn't autodetect plan/implement from prose — it binds them to *modes*.
For KajiCode we extend the existing `AutoRole` conservatively:

- Keep `plan`/`implement` tied to explicit modes/commands rather than magic
  prose parsing, to avoid regression risk:
  - `plan-mode`: when plan meta/prompt mode is active, active role = `plan`.
  - implement signal (existing): todo list + last tool wrote → `implement`.
  - design: future (needs a design-mode/command); omit from auto for now, keep
    it as an assignable role awaiting a trigger.
- Add constants for the built-in role ids (`RoleDefault`, `RolePlan`,
  `RoleImplement`, `RoleVision`, ...) so `AutoRole`/`Roles.RoleFor` reference
  canonical ids, and update `AutoRole.Role` default to `RoleImplement`.

This keeps the current conservative model; we do **not** add a free-form intent
classifier in this PR (that's omp's biggest divergence and the riskiest).

---

## Part 3 — Implementation Plan (ordered, test-first)

Each step keeps all regression gates green (`go build`, `go vet ./...`,
`make fmt-check`, `git diff --check`, `go test ./...`, release build+smoke,
govulncheck).

### Phase A — Catalog + capability fallback (core, self-contained)

1. **`internal/modelregistry/default_roles.go`** (new, <200 lines):
   - `RoleID` constants: `RoleDefault`, `RolePlan`, `RoleDesign`,
     `RoleImplement`, `RoleVision`, `RoleReview`, `RoleFast`.
   - `RoleInfo{ID, Name, Tag, Summary, defaultSelector string}`.
   - `DefaultRoleIDs() []string` (stable order, built-ins first).
   - `RoleInfoByID(id) (RoleInfo, bool)`.
2. **`internal/modelregistry/default_roles.go` fallback resolution**:
   - `SuggestModelForRole(registry, roleID, requiredCapability)` → picks the
     best available model of that capability (reuse `ListByCapability` +
     cost/context ordering). Returns a selector.
3. **`internal/agent/rolerouter.go`**: extend `ProfileFor` so unconfigured
   built-in roles resolve via capability fallback (§2.2). Keep custom/unknown →
   active.
4. **Tests**:
   - `internal/modelregistry/default_roles_test.go` — catalog integrity,
     `RoleInfoByID`, fallback picks a vision/reasoning model.
   - `internal/agent/rolerouter_test.go` — unset `plan`/`vision` resolve to an
     available capable profile; unknown role still falls back to active.
   - `internal/agent/roleselector_test.go` — canonical default `RoleImplement`.
5. **`internal/config/validate.go`**: keep the modelRoles validator as-is; add a
   soft note (non-issue) when a known default role, e.g. `vision`, is needed but
   not set — optional/advisory only.

### Phase B — TUI `/role` surfaces the default roles

6. **`internal/tui/role_command.go`**:
   - `newRolePicker`: build rows from **default roles** (with their suggested
     meta) merged with configured custom roles (built-ins first, active marked
     `●`); keep the add/clear/default control rows. An unconfigured default
     role shows its suggestion (`suggest plan → claude-opus-4.1`) instead of
     "unset".
   - `roleStatusText`/`roleListText`: list default roles with their current
     binding (or suggestion) + custom roles.
7. **`internal/tui/role_command_test.go`**: a fresh session (no `modelRoles`)
   still shows the seven default roles in the picker; each advances to
   `openRoleModelPicker`; binding persists via `SetModelRole` (unchanged flow).
8. **`internal/tui/view.go`**: title segment unchanged (` · role <name>`), but
   now reachable for default roles too once routed.

### Phase C — Intent auto-routing glue (small)

9. **`internal/agent/roleselector.go`**: add canonical role constants usage in
   `AutoRole.Role` default; add a `promptedPlanMode`-style boolean to
   `RoleContext` so plan-mode can route to `RolePlan` (only where a plan-mode
   flag already exists today — no new modal state in this PR).
10. Route existing plan/spec meta to `RolePlan` if a plan-mode flag is already
    threaded; otherwise leave as a documented future hook.

### Phase D — Docs + TUI runtime (optional follow-up)

11. **Docs**: update `docs/architecture.md`, `docs/HOW_KAJICODE_WORKS.md`,
    `docs/MULTI_MODEL_ROUTING.md` to describe default roles, the fallback
    resolution rule, and the suggested-vs-applied distinction.
12. **(Follow-up PR)** TUI image→vision fallback matching the CLI gate (§2.4),
    so image handling routes through the default `vision` role instead of
    dropping.

---

## Risks / Guardrails

- **No silent model drift**: the capability fallback concerns *routing to a
  model the user has a saved provider for*; the `defaultSelector` suggestion is
  display-only. Explicit `modelRoles`/active model always win.
- **Custom roles keep working**: built-ins are merged, never replace,
  `modelRoles`; unknown roles still fall back to active.
- **Tests preserved**: all changes additive; existing `/role`, `AutoRole`,
  `RoleRouter`, and vision-gate tests must stay green.
- **Scope discipline**: Phase A+B is the "default roles" ask; C is minimal
  intent glue tied to existing signals (no free-form classifier); D-something
  (TUI vision fallback) is a separate PR because it adds a new runtime call
  path needing its own test suite.
