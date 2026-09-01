# Subagent Architecture: opencode vs KajiCode — Deep Research & Migration Plan

Research date: 2026-02. Sources:
- opencode at `/tmp/oc-src` (v1.18.19): `packages/opencode/src/agent/agent.ts`, `agent/subagent-permissions.ts`, `tool/task.ts`, `tool/task.txt`, `config/agent.ts`, `packages/core/src/v1/config/agent.ts`, `session/session.ts`, `session/prompt.ts`.
- KajiCode (this repo): `internal/specialist/*` (exec.go, task_tool.go, registry.go, manifest.go, builtin.go, envelope.go, streamer.go, output_tool.go, accounting.go), `internal/cli/app.go` (`registerSpecialistTools`), `internal/agent/system_prompt.go`, `internal/agent/parallel_tools.go`, `internal/tools/registry.go`.

---

## Part 1 — How opencode does subagents

opencode's model is **"agents are first-class config objects; subagents are real in-process sessions of the same engine."**

### 1.1 One agent concept, three modes
`Agent.Info` (`agent/agent.ts`) is a single schema used for every role:

```
name, description, mode: "subagent" | "primary" | "all", hidden,
model {providerID, modelID}, variant, temperature, topP, prompt,
options, steps (max agentic iterations), color,
permission: Ruleset   // per-tool allow/ask/deny with glob patterns
```

- `mode: "primary"` = the agent you talk to (built-ins: `build`, `plan`, plus hidden `title`/`summary`/`compaction` utility agents).
- `mode: "subagent"` = only spawnable via the Task tool (built-ins: `general`, `explore`).
- `mode: "all"` = both.

The same object drives the interactive TUI agent switcher AND the Task tool. There is no separate "specialist" subsystem.

### 1.2 Definition sources merge into one registry (`Agent.state`)
1. Built-in agents defined in code with full permission rulesets.
2. Markdown files: `{agent,agents}/**/*.md` and legacy `{mode,modes}/*.md` under every config directory (global + project). YAML frontmatter carries the metadata fields; the body is the system prompt (`config/agent.ts`). Frontmatter keys outside the known set are folded into `options`; deprecated `tools` map is normalized to `permission` entries; `maxSteps` normalizes to `steps`.
3. `opencode.json(c)` `agent` sections, env config content, remote/managed org config — all deep-merged (`mergeDeep`) over builtins.
4. Per-agent overrides: `disable: true` removes a builtin; any other field overrides that field only.

### 1.3 Permissions are per-agent rulesets, evaluated live
- Every agent carries a full `PermissionV1.Ruleset`: ordered rules of `{permission/tool name, glob pattern, action: allow|ask|deny}`.
- Built-in defaults: everything allow, `.env` reads ask, external dirs ask, question/plan tools gated.
- `explore` is locked down by ruleset: `"*": "deny"` then explicit allows for grep/glob/list/bash/read/webfetch/websearch.
- Config user permission merges on top of defaults for every agent.

### 1.4 The Task tool is a session spawner, not a process spawner (`tool/task.ts`)
Parameters: `description`, `prompt`, `subagent_type`, optional `task_id` (resume), optional `command`, optional `background`.

Execution flow:
1. **Depth check**: walks `parentID` chain up from the current session; fails if depth ≥ `subagent_depth` (config, default 1).
2. **Permission ask** on the `task` tool itself with the subagent type as pattern (skipped when invoked internally with `bypassAgentCheck`, e.g. commands).
3. Resolve the agent definition; unknown type → corrective error listing available agents.
4. **Resume**: if `task_id` given, reuse that existing child session (continues its transcript). Otherwise create a new child session with `parentID = ctx.sessionID`.
5. **Child permission derivation** (`agent/subagent-permissions.ts`): child gets parent's *deny* + `external_directory` rules (restrictions propagate down, grants do not), plus default denies for `todowrite` and `task` unless the child's own ruleset explicitly re-allows them. This makes nesting self-limiting: each level must explicitly opt back into spawning.
6. Child model = agent's configured model, else inherit parent's message model (+variant).
7. Runs via `promptOps.prompt(...)` — the SAME prompt loop as the main chat, in-process, sharing the tool runtime, permission service, plugin bus. No subprocess.
8. Foreground waits on the background-job manager (`raceFirst(wait, waitForPromotion)`); result is the child's last text part wrapped in `<task_result>`.
9. **Background mode**: returns immediately with instructions not to poll; on completion, injects a synthetic `<task_result>` user turn into the PARENT session automatically (auto-notification). Parent abort signal propagates to child cancel.
10. Output rendering: `<task id=... state="running|completed|error"><summary>...</summary><task_result>...</task_result></task>`.

### 1.5 Subtasks as protocol parts
Subagent runs also exist as a `SubtaskPart` message type (used by commands): the loop synthesizes an assistant message + running tool part so the UI can show live progress, then executes the same TaskTool with `bypassAgentCheck`. So TUI display, commands, and the model's Task calls all share one code path.

### 1.6 Agent generation
`Agent.generate(description)` asks the model (structured output) to produce `{identifier, whenToUse, systemPrompt}`, listing existing identifiers as forbidden names — powers `/agents create` flows.

### 1.7 Prompt-side guidance (`tool/task.txt`)
Explicit "when NOT to use" list, parallel-launch instruction ("single message with multiple tool uses"), fresh-context warning (put all needed context in the prompt since the child starts blank), resume explanation via task_id, trust note, and proactive-use hint from descriptions.

### 1.8 Session model
Sessions have `parentID`; `Session.children(parentID)` queries descendants; deleting a parent cascades to children. Child sessions are titled and visible in the session picker — subagent transcripts are first-class browsable history.

---

## Part 2 — How KajiCode does it today

KajiCode's model is **"specialists are named prompt+tool profiles executed by a fresh headless KajiCode subprocess."**

- Registry (`internal/specialist/manifest.go`): builtins (worker/planner/explorer/verifier/code-review) plus user/project markdown manifests with frontmatter (`name/description/extends/model/reasoningEffort/tools`). Tools come from coarse categories (`read-only`, `edit`, `execute`, `plan`) resolved to fixed tool-name lists. Forbidden: specialists can't get Task/TaskOutput/TaskStop/GenerateSpecialist.
- Task tool (`internal/specialist/task_tool.go`): params `name/prompt/description/run_in_background/resume`. Permission: static prompt unless manifest resolves read-only → auto-allow; resume always prompts.
- Execution (`internal/specialist/exec.go`): builds CLI args (`exec --init-session-id specialist_xxx ... --auto low|high --enabled-tools ... --depth N --tag specialist --calling-session-id ... --session-title ...`), spawns `os.Executable()` as a subprocess, parses stream-json events, summarizes final text. Depth cap hard-coded at 8. Background runs write output to a file managed by `internal/background`; TaskOutput/TaskStop poll/kill it. Resume maps to `exec --resume <id>` after checking the session is tagged `specialist`.
- Orchestration nudge: `<specialists>` section in system prompt lists names + descriptions (`system_prompt.go`).
- Parallelism: `parallel_tools.go` only pre-runs consecutive read-only tool batches; Task is NOT in the concurrent set (it's shell-effect) — multiple Task calls in one turn run sequentially.
- Accounting: `accounting.go` records start/stop + usage roll-up; sessions store emits `specialist_start/stop` events.
- Swarm shares this executor with an inline manifest for members.

### What already matches opencode's shape
- Declarative file-based definitions merged over builtins ✔
- Description-driven discovery + delegation nudge in system prompt ✔
- Resume by session id ✔
- Background launch + poll/stop tools ✔
- Depth limiting (hard cap 8 vs opencode configurable `subagent_depth`) ✔
- Corrective "available specialists" error ✔
- Read-only auto-approval heuristic ✔

---

## Part 3 — The gaps (ranked by impact)

### G1. Subprocess-per-task: no shared context, no shared permission policy (BIGGEST)
Every specialist is a brand-new process: it reloads config, rebuilds provider clients, re-reads skills/plugins/MCP, replays nothing, and pays full startup cost per call. It cannot see parent conversation, cannot inherit sandbox state cheaply, and its tool outputs never enter the parent transcript (only the final summary string does).

opencode runs children through the same in-process prompt loop: zero startup cost, same tool runtime, same permission service, plugins fire for child tools too.

Consequences unique to KajiCode's design: prompt must be fully self-contained (envelope.go compensates); child failures surface as opaque exit codes + stderr; no streaming of intermediate tool activity into the parent's context; per-call overhead makes many small parallel delegations expensive.

### G2. Sequential Task calls — parallelism exists but isn't wired
`parallel_tools.go` excludes Task (shell side-effect classification). opencode's headline usage note is "launch multiple agents concurrently... single message with multiple tool uses". For research-heavy work this is the difference between 30s and 3min turns. Note: the harness prompt tells the model "launch several specialists in parallel," which the runtime currently cannot honor.

### G3. No background completion notification — polling-only
KajiCode background tasks require the model to call TaskOutput repeatedly. opencode injects the finished result as a synthetic message into the parent automatically (`inject()` + `notify()`), with strict "do NOT sleep/poll" prompt text. Polling burns turns and tokens and risks the model stalling on a wait loop.

### G4. Coarse tool categories instead of per-agent permission rulesets
KajiCode: 4 categories → fixed tool lists; no glob patterns; no per-path rules; no ask-level granularity inside a specialist; read-only detection is a hand-maintained map duplicated between `readOnlySpecialistTools` and `toolCategories["read-only"]` (already drifted once: `update_plan`, `code_search`, `ls` differ across the two).

opencode: every agent has an ordered allow/ask/deny ruleset per tool with patterns; child inherits parent DENYs (restriction propagation) while grants stay local; nesting requires explicit opt-in per level (default `task` deny).

KajiCode's equivalent protection is weaker: children run at `--auto low/high` globally — a binary autonomy switch, not a policy. A worker spawned from a safe parent still can't be granted narrow rights; an unsafe-parent child goes fully unsafe.

### G5. No model/temperature/steps/options per agent beyond model+effort
Manifest supports `model`, `reasoningEffort` only. opencode adds `temperature`, `top_p`, `steps` (iteration cap forcing a final answer), `variant`, `hidden`, `color`, `mode` (primary/subagent/all), `options` passthrough, `disable` to remove builtins.

### G6. No primary-agent duality / mode field
In opencode the same registry powers interactive agent switching (build/plan modes) and subagents. KajiCode has no notion of switching the main session's agent persona, so plan-mode-style behavior must be reimplemented elsewhere; specialists can't be promoted to top-level selectable agents.

### G7. Depth semantics: hard-coded 8, not configurable, no deny-by-default
opencode: `subagent_depth` config key, default 1, enforced via parent-chain walk, plus ruleset default-deny of `task` for subagents (belt and suspenders). KajiCode: compile-time constant 8, enforced numerically. A runaway nested loop can go 8 levels × N siblings before tripping.

### G8. Agent generation is project-file-only
GenerateSpecialist writes a project profile from name+description+prompt supplied by the caller. opencode's `Agent.generate` derives identifier+whenToUse+systemPrompt from a natural-language description via structured output and forbids collisions with existing names. Minor gap.

### G9. Child sessions aren't surfaced as first-class UI objects
opencode: child sessions appear in the session tree (parentID, children query, cascade delete, titles like "@general subagent"). KajiCode: specialist sessions exist in the sessions store with tags/accounting events but there is no parent/child linkage exposed for browsing/resuming from the TUI session list.

### G10. Misc smaller gaps
- No `hidden` flag (can't keep utility agents out of the delegation menu without removing them).
- No `<task_result>`-style structured envelope with state machine (`running/completed/error`) — KajiCode returns plain text summaries; error states are less parseable for the model.
- Abort propagation: parent ctx cancel kills the process group (good) but there's no partial-result salvage like opencode's raceFirst/cancel accounting.
- No `steps` cap → a looping specialist burns until its own internal limits trip.
- Tool-category drift risk noted in G4.

---

## Part 4 — Plan

Guiding principle: keep the subprocess execution model (it is KajiCode's isolation strength and deeply wired: swarm, background manager, accounting, sandbox), but close the behavioral gaps around it in priority order. Phases are independently shippable.

### Phase 0 — Foundations (no behavior change)
Files: `internal/specialist/manifest.go`, `metadata.go` (new small file), tests.
1. Extend `Metadata`: add `Mode` (`all|subagent|primary`, default `all`), `Hidden bool`, `Temperature float64`, `TopP float64`, `Steps int`, `Disable bool`, `Options map[string]any`. Normalize `maxSteps`→`Steps` for compat. Fold unknown frontmatter keys into `Options` exactly like `ConfigAgentV1.normalize`.
2. Add `Permission` field placeholder type now (map[string]string tool→action) even if enforcement lands in Phase 3 — avoids a second schema migration.
3. Update `knownMetadataKeys`, validation warnings (unknown keys currently warned; they should route to Options instead), and `Summary`.
Tests: manifest_test additions for each new key, normalization cases, disable handling.

### Phase 1 — Make parallel Task calls real (highest value, smallest risk)
Files: `internal/agent/parallel_tools.go`, `internal/specialist/task_tool.go`, tests.
1. Classify Task by *resolved manifest effect*, not static SideEffectShell: if the target specialist is read-only (existing `IsReadOnlySpecialist`) treat the CALL as read-only for eligibility; else sequential.
2. Bound concurrency: dedicated semaphore for Task calls (suggest max 4) separate from the 8-way read batcher, because each Task is a whole subprocess (memory/CPU heavy).
3. Preserve ordering guarantees: results consumed in call order (pattern already exists in parallel_tools.go).
4. Fix the honesty bug in the system prompt: the `<specialists>` section already promises "launch several specialists in parallel" — after this phase it becomes true.
Tests: parallel_tools_test for mixed Task/read batches, depth accounting under concurrency (each parallel child must get correct CurrentDepth — verify RunOptions.Depth is captured per call, not shared), semaphore saturation, abort mid-batch.

### Phase 2 — Background completion push (kills the polling stall)
Files: `internal/background` (watcher hook), `internal/agent` (injection path), `internal/specialist/streamer.go` (reuse summarizeTaskData), tests.
1. When a background specialist exits, generate the summary from the recorded stream file (code exists in exec.go onExit).
2. Deliver it to the parent run: preferred mechanism is the existing async event channel pattern (`async_diagnostics.go` style) rather than a synthetic user message if the agent loop supports queued events; otherwise enqueue a synthetic user-visible event marked `synthetic: true` rendered as `<task id=... state="completed">...</task>`.
3. Prompt contract change in Task description + `<specialists>` guidance: "You will be notified automatically when it finishes. DO NOT sleep, poll, or proactively check" — copy opencode's BACKGROUND_STARTED wording, adapted.
4. Keep TaskOutput working for manual checks and for pre-completion reads.
Tests: lifecycle_test extension — background completes while parent idle → next turn contains injected result; error case injects error envelope; dedup (no double injection if model also polled).

### Phase 3 — Real permission inheritance (security parity)
Files: `internal/specialist/exec.go` (arg building), new `internal/specialist/permission.go`, `internal/tools` plumbing for per-child policy, tests.
1. Replace the `readOnlySpecialistTools` duplicate map with derivation from `tools.CapabilitiesOf` (single source of truth; kills drift).
2. Implement restriction-propagation: build the child's effective policy = child manifest rules ∩ parent's denys preserved. Concretely: pass parent's deny decisions down as extra `--deny-tool`/`--deny-path` args (new exec flags) instead of relying solely on the global `--auto low|high` switch.
3. Default-deny `Task` for child specialists unless the manifest's Phase-0 `Permission` block explicitly sets `task: allow` (this is what actually caps runaway nesting alongside depth).
4. Keep fail-safe behavior: unparseable/incomplete policy ⇒ least privilege (current `specialistAutonomy` comment discipline).
Tests: permission_inline_test extensions; matrix tests (parent mode × manifest rules × expected child args); regression: existing swarm member autonomy unchanged.

### Phase 4 — Configurable depth + steps cap
Files: `internal/specialist/exec.go`, config wiring in `internal/cli`, docs/architecture.md, tests.
1. Add config key `specialist_depth` (default 8 = current constant, so no behavior change) replacing `maxSpecialistDepth` uses.
2. Honor manifest `Steps`: translate to the child's existing per-turn iteration/guard budget flag (or a new `--max-steps` exec flag) so a specialist is forced to wrap up instead of looping.
Tests: depth config resolution; steps enforcement in a scripted long-loop fixture.

### Phase 5 — Structured task results + UX polish
Files: `internal/specialist/output_tool.go`, `format.go`, `streamer.go`, TUI transcript rendering, tests.
1. Adopt opencode's envelope: `<task id="..." state="running|completed|error"><summary>…</summary><task_result>…</task_result></task>` for foreground results too (not just errors). Models parse this reliably; state machine enables conditional logic in prompts.
2. Surface child sessions in the TUI session browser via the existing tag/accounting events: show `parent → @name subagent` tree lines (sessions store already persists `specialist_start/stop` + calling-session ids).
3. Respect `Hidden` in the `<specialists>` prompt section and `kajicode specialist list`.
4. Optional: extend GenerateSpecialist to accept natural-language description → derive name/whenToUse/prompt via the model (opencode `generate.txt` pattern), collision-checked against existing names.
Tests: output_tool_test envelope shapes; tui test for session-tree rendering; hidden filtering test.

### Non-goals (explicit)
- Do NOT replace subprocess execution with an in-process child loop. That would be a rewrite of the agent package and would sacrifice process isolation that the sandbox story depends on. Revisit only if per-call startup latency measurably dominates (measure first: `cmd/kajicode-perf-bench`).
- Do NOT introduce primary-agent persona switching in this effort (G6); it touches the TUI session model broadly and deserves its own design doc.
- Do NOT change the swarm launcher semantics in phases 1–4; only Phase 3's permission derivation must be verified against it.

### Sequencing & validation
Order: 0 → 1 → 2 → 3 → 4 → 5. Each phase ships separately with its own tests. After each phase:
```bash
make fmt-check && go vet ./... && go test ./...
go test ./internal/specialist/... ./internal/agent/... ./internal/tools/...
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```
Phase 3 additionally warrants `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...` (sandbox/security-sensitive per AGENTS.md). Update `docs/architecture.md` (specialist ownership) and `docs/SPECIALISTS.md` (new manifest fields) in the same PRs as their enabling phases.

### Effort estimates (rough, single dev)
- Phase 0: 0.5 day
- Phase 1: 1–2 days (concurrency edge cases dominate)
- Phase 2: 2 days (event injection into the loop is the risky bit)
- Phase 3: 2–3 days (policy plumbing + test matrix)
- Phase 4: 0.5 day
- Phase 5: 1–2 days
