# Tool Scoping Under Bypass-All + Shell-vs-Tool Preference

Status: **implemented.** Fix 1 (mode-aware path resolution) and Fix 2
(descriptions + prompt addendum) are in the tree, verified live and by unit
tests. See `internal/tools/workspace.go`, `internal/tools/scope_mode_test.go`,
and the tool descriptions in `internal/tools/*.go`.
Scope: two linked defects in KajiCode's tool layer.

---

## 0. Summary

Two reports, one shared root cause.

1. **Tools are workspace-scoped even in `bypass-all`.** Under bypass-all, `bash`
   can read any file on the machine, but `read_file`/`grep`/`glob`/`edit_file`
   still hard-fail with `must stay inside the workspace`. The two tool families
   disagree about what "no restrictions" means.
2. **The agent overwhelmingly reaches for `bash` instead of the native tools.**
   Measured across 30 real sessions: `bash` is **61.1%** of all tool calls; the
   native `grep` tool is **1.1%**.

These are not independent. The native tools are *less capable* than `bash` when
work crosses the workspace boundary (defect 1) — 13.9% of shell read/search
calls target an absolute path outside the session cwd, and 29.2% of `cd`s leave
the workspace. The model correctly routes around a tool that refuses. Fixing
scoping removes the largest *mechanical* reason to bypass the tools; tightening
the prompt and the advertised descriptions addresses the habitual part.

**The one architectural change** is to make the run's `*sandbox.Scope` the
*containment source of truth* and have the tool layer consult the active
permission mode when it decides what to allow. Today the mode is deliberately
kept out of the tool layer (`tools.PermissionMode` is a bare string used only for
the bypass-all short-circuit in `registry.go:227`), and read/write roots are
frozen at startup.

---

## 1. Understanding the reports

**What is happening.** In a `bypass-all` session, the model asks `read_file` for
a file outside the workspace and gets an error, so it switches to `bash cat`,
which works.

**What should happen.** Under `bypass-all`, the native tools should have the same
reach as the shell. Under the narrower profiles (`read-only`, `read-write`,
`ask-all`) they should keep their current containment — that containment is
intentional and correct.

**Where it starts.** The workspace boundary is imposed independently in two
places that never consult the permission mode:

- `internal/sandbox/engine.go` (the policy engine), and
- `internal/tools/workspace.go` (the native file/search tools).

**When it happens.** Whenever a path resolves outside the workspace root, in
every permission profile.

**Reproducible?** Yes, deterministically. See §2.

**Triggering conditions.** A tool (`read_file`, `grep`, `glob`, `ls`,
`list_directory`, `lsp_navigate`, `edit_file`, `write_file`, `apply_patch`,
`multi_edit`) called with an absolute path outside `--cwd`/workspace root.

---

## 2. Evidence

### 2.1 Reproduced live (real binary, real model)

Built `go build -o /tmp/kc ./cmd/kajicode`; ran `exec` against a workspace that
is *not* an ancestor of the target file.

| mode | `read_file ~/kc-outside-probe/target.txt` |
|---|---|
| `bypass-all` | `Error reading file …: … must stay inside the workspace` |
| `read-only` | same error |
| `read-write` | same error |
| `ask-all` | `Error: Sandbox approval required for read_file: … outside the workspace (use /add-dir …)` |

Same path, same session, same mode (`bypass-all`):

- `bash cat ~/kc-outside-probe/target.txt` → **succeeded**, printed
  `SECRET_HOME_OUTSIDE`.
- `grep pattern=SECRET path=~/kc-outside-probe` →
  `Error running grep: /Users/dishants/kc-outside-probe must stay inside the workspace`.

So within one `bypass-all` run, `bash` reads the file and `read_file`/`grep`
refuse. That is the reported asymmetry, reproduced.

### 2.2 Every large real session is `bypass-all`

From `~/.local/share/kajicode/sessions/*/metadata.json`, the six largest sessions
(the 8.7–16.5 MB ones) **all** carry `permissionProfile = "bypass-all"`. This is
the dominant real-world configuration, not an edge case.

### 2.3 The two enforcement layers

**Engine** — `internal/sandbox/engine.go`:

- `:300-302` — `if request.PermissionMode == PermissionModeBypassAll { policy.Mode = ModeDisabled }`
  → `:307-309` returns `ActionAllow`. The engine is correctly inert under bypass-all.
- `:342-351` — `read-only` widens reads (`enforceWorkspace = false`), `read-write`
  widens reads/writes/out-of-workspace. So the *engine* already models read scope
  per mode.

**Tools** — `internal/tools/workspace.go` (this is the layer that ignores mode):

- `resolveScopedReadPath` `:271-302`, `resolveScopedPath` `:319-348`,
  `resolveScopedTargetPath` `:359-388`, `recheckScopedWriteTarget` `:396-416`.
- All four iterate `scopedRoots`/`scopedReadRoots` (`:246-269`), which fall back
  to `[]string{workspaceRoot}`. When no root contains the path, the error is
  `outsideWorkspaceError` → `"%s must stay inside the workspace"` (`:200-202`).
  **Nothing in this file reads the permission mode.**

**The two layers share one `*sandbox.Scope`**, built once at startup and never
widened for the mode:

- `internal/cli/app.go:716` (TUI)
- `internal/cli/exec.go:376` (exec)
- `internal/cli/acp_runtime.go:28` (ACP)

```go
scope, err := sandbox.NewScope(workspaceRoot,
    append(append([]string{}, resolved.Sandbox.AdditionalWriteRoots...), addDirs...))
```

`Scope.AddRead` / `AddTemporaryRead` exist (`internal/sandbox/scope.go:93-131`)
and are exactly the mechanism a mode-driven widening would use — but the **only**
production caller is `request_permissions`
(`internal/sandbox/request_permissions.go:266-274`).

### 2.4 `request_permissions` is a dead end under bypass-all

`internal/agent/loop.go:2491-2494`:

```go
if permissionMode == PermissionModeBypassAll {
    return requestPermissionsResult(call, sandbox.RequestPermissionsResponse{...}, false), nil
}
```

The call is answered with the requested profile but **short-circuits before**
`GrantRequestPermissions`, so no root is ever added to the scope. Even if the
model did the "right" thing, bypass-all ignores it. (This is defensible — under
bypass-all you should not need to ask — but it means the tool description's
"request additional permissions" path does nothing in the mode that needs it
most.)

### 2.5 Shell-vs-tool preference, measured

30 largest sessions, 31,562 tool calls:

| tool | calls | share |
|---|---|---|
| `bash` | 19,297 | **61.1%** |
| `read_file` | 3,352 | 10.6% |
| `edit_file` | 3,326 | 10.5% |
| `grep` | 341 | **1.1%** |
| `glob` | 17 | **0.1%** |
| `list_directory` / `ls` | 21 | 0.1% |

Classifying the 19,297 `bash` commands (leading verb after stripping `cd`/env
prefixes):

| category | calls | share |
|---|---|---|
| read/search verbs (`grep` 16.7%, `sed` 11.6%, `cat` 8.6%, `ls` 2.9%, …) | 8,054 | **41.7%** |
| **pure** read/search — no mutation, no exec, nothing native can't do | 4,358 | **22.6%** |
| contain a mutation (`rm`/`mv`/`cp`/…) | 1,164 | 6.0% |
| contain an exec (`python3`/`node`/`go`/`git`/…) | 7,148 | 37.0% |
| heredoc (`<<EOF`) file writes | 3,172 | 16.4% |
| shell redirect (`>`) writes | 3,994 | 20.7% |

Boundary-crossing:

- `cd` segments: 11,311 → **3,308 (29.2%) leave the workspace root**.
- read/search `bash` calls referencing an absolute path outside the session cwd:
  **757 / 5,429 (13.9%)**.

Representative commands (real, verbatim):

```
cd /Users/dishants/projects/kajicode && head -12 internal/tools/file_media.go; grep -n "io\.\|os\." internal/tools/file_media.go
cd ~/zerocarbon/erp && sed -n '540,640p' Components/ERP/CCTSForms/FormSb/config.ts
cd /Users/dishants/groww/frontend-release-platform && sed -n '120,230p' server/src/store/store-sync.ts
cd ~/zerocarbon/erp/Components/ERP/CCTSForms/NF-8 && grep -n 'getDropdownValue\|yearRow' index.tsx | head
```

These are `read_file`/`grep` calls wearing a shell costume. They are taken out
because the model is working in a repo that is not the cwd (`~/zerocarbon/erp`,
`kajicode`, `frontend-release-platform` all appear inside single sessions), and
because `read_file` cannot express "lines 540–640" as tersely as `sed -n`.

### 2.6 The prompt already asks for the opposite

`internal/agent/system_prompt.md:33-35`:

> Choose the narrowest tool that safely does the job. Prefer native file tools
> such as read_file, list_directory, glob, grep, write_file, edit_file, and
> apply_patch over shelling out for ordinary file work.

And the DeepSeek/open-weight addendum
(`internal/agent/harness_prompt_addenda.go:33-41`, selected for `deepseek` at
`harness_profile.go:82`):

> Prefer small, verifiable tool calls over broad shell commands.

Both instructions exist. They lose to §2.3: an instruction cannot outvote a tool
that returns an error.

### 2.7 Not the cause: deferral or advertising

`/tmp/kc exec --list-tools` shows `grep [read/allow]`, `read_file [read/allow]`,
`glob [read/allow]`, `ls [read/allow]` **all advertised with full schemas**. Only
MCP tools are `Deferred()` (`internal/mcp/registry.go:434`); no core tool defers,
so `DeferThreshold` (default 3, `internal/config/resolver.go:65`) never hides
them. The model can see the tools and is still not using them.

---

## 3. Root cause

**One root cause, two symptoms.**

The workspace boundary is duplicated across two layers — `internal/sandbox`
(policy engine, mode-aware) and `internal/tools` (path resolution, mode-blind) —
and the run's `Scope` (the thing that actually defines containment) is built once
at startup with the workspace root plus `--add-dir`, **independent of the
permission mode**.

Consequences:

1. `bypass-all` disables the *engine* but not the *tools*, so the two families
   disagree. `bash` (runs unsandboxed, `commandEngine == nil`, `bash.go:107,119`)
   crosses the boundary; `read_file`/`grep` do not.
2. Because the native tools cannot serve boundary-crossing reads/writes, the model
   substitutes shell — 41.7% of `bash` calls are read/search verbs and 22.6% are
   purely native-replaceable, with 13.9% of them explicitly reaching outside the
   cwd.

A secondary, independent contributor to symptom 2: the native tools are less
*expressive* than shell for the very patterns the model uses most (line-range
reads, "find these symbols then show that range", `xargs`-style fan-out), and
`read_file`'s advertised description does not name itself as the alternative to
`cat`/`sed`/`head` the way `lsp_navigate` names itself as the alternative to
`grep`.

---

## 4. Options considered

### Issue 1 — scoping

| # | Option | How it works | Cost | Verdict |
|---|---|---|---|---|
| 1a | Remove the workspace check from the tools entirely | Tools trust the engine | Small diff | **Rejected.** Under `read-only`/`read-write`/`ask-all` the engine only *prompts* for outside paths; the tool check is a second, independent boundary. Deleting it would let a single approval widen a tool with no scope bookkeeping. |
| 1b | Widen `Scope` to the filesystem root when `bypass-all` | `NewScope(root, ...)` seeded with `/` (or the volume root) | Tiny | **Rejected.** The mode is a live toggle (TUI Shift+Tab, `setPermissionProfile`; ACP `setMode`); the scope is built at startup. It would also permanently disable the engine's `DenyRead` list, which `engine.go:180-189` deliberately preserves. |
| 1c | **Make the tools mode-aware via the shared `Scope`** | One small helper decides the effective read/write roots from `(workspaceRoot, scope, mode)`; bypass-all (and the read widening already implied by `read-only`/`read-write`) yields "any existing path"; narrower modes keep today's containment. | Moderate | **Chosen.** Single source of truth (the scope + the mode already on `RunOptions`), no new interface, fixes the duplicate-boundary bug at its source. |
| 1d | Only fix bypass-all, leave read-only/read-write alone | Narrowest | Smallest | **Rejected as the sole fix** — see §2.3: the engine already widens reads for `read-only`/`read-write`, so fixing only bypass-all leaves the same tools/engine disagreement in two more modes. Folded into 1c. |

### Issue 2 — preference

| # | Option | Verdict |
|---|---|---|
| 2a | Stronger prompt wording ("never use cat/sed, always read_file") | **Insufficient alone** — the tool returns an error when it matters; an instruction cannot outvote a refusal. Keep the prompt work as a complement, not the fix. |
| 2b | Make tool descriptions name their shell equivalents (`read_file`: "use instead of cat/head/sed for reading; supports start_line/end_line") | **Adopt.** Directly targets the observed pattern and is cheap. Mirrors the existing successful precedent (`lsp_navigate` already says "Use this instead of grep …"). |
| 2c | Restrict/deny `bash` for read-only-shaped commands | **Rejected.** False positives, a new failure mode for legitimate pipelines, and it fights the model instead of fixing the asymmetry. |
| 2d | Shell-lint the command and rewrite (`cat f` → `read_file`) | **Rejected for this plan.** New machinery, brittle, and it hides the real defect. |
| 2e | Fix scoping (1c) so the native tools serve cross-repo work | **Adopt as the primary lever.** Removes the mechanical reason for substitution. |

---

## 5. Proposed solution

### Fix 1 — the tool layer honors the active permission mode (primary)

Introduce one mode-aware scope decision, used by all scoped resolvers, that
answers "does this mode lift the workspace boundary for this effect?":

- Add `applyModeScope` semantics to the existing `PathScope` plumbing:
  - `bypass-all` → unrestricted read **and** write roots (any existing path).
  - `read-only` → unrestricted **read** roots; writes stay workspace-scoped.
  - `read-write` → unrestricted read **and** write.
  - `ask-all` / unset → today's workspace-only behavior, unchanged.
- Plumb the mode into the tools. The mode is already on `RunOptions.PermissionMode`
  (a `string`) and already reaches the registry (`registry.go:227`); `read_file`,
  `grep`, `glob`, `ls`, `list_directory`, `read_minified_file`, `lsp_navigate`,
  `edit_file`, `write_file`, `apply_patch`, `multi_edit`, `bash`, `exec_command`
  all already receive `RunOptions` via `RunWithOptions`. No new interface is
  needed; pass the mode into the four scoped resolvers (or give the tool structs
  the mode via `RunOptions` at the call).

**Why this and not a new abstraction.** `sandbox.Scope` already models exactly
these root lists (`Roots`, `ReadRoots`, `AddRead`) and is already shared with the
engine. The missing piece is only that the *mode* never reaches it. The simplest
correct expression is a small pure function, e.g.:

```go
// effectiveReadRoots returns the roots a read may resolve against. A mode that
// lifts the workspace boundary (bypass-all, read-only, read-write) allows any
// existing absolute path; the narrower profiles keep the scope's roots.
func effectiveReadRoots(mode string, scope PathScope, workspaceRoot string) ([]string, bool)
```

where the bool means "unrestricted". `resolveScopedReadPath`/`resolveScopedPath`
consume it; the existing single-root safety checks inside `resolveWorkspacePath`
(segments, symlink traversal) still run whenever a concrete root is used.

**Keep one invariant:** with mode `ask-all` or empty, behavior must be
**byte-identical** to today. Guard it with a test.

### Fix 2 — make the read/search tools the obvious choice (complement)

1. **Descriptions name their shell equivalents.**
   - `read_file`: "Read a file (use instead of `cat`/`head`/`tail`/`sed -n`).
     Prefer reading whole files; use start_line/end_line for long files."
   - `grep`: "Search file contents with a regular expression (use instead of
     `grep`/`rg`). Use glob to filter by path." — and correct the doc/prompt drift
     (§6) since `glob` is a *file finder*, not a `grep --glob` filter.
   - `glob`/`ls`: state they replace `find`/`ls`.
   - `write_file`: state it replaces heredoc/redirect writes.
2. **Prompt addendum for the open-weight family**
   (`harness_prompt_addenda.go:33-41`): add one line tying the tools to the shell
   verbs they replace, and one line stating that when a path is outside the
   workspace the native tools are still the right choice (once Fix 1 lands).
3. **Do not** add a hard "never use bash for reads" rule — it is unenforceable and
   wrong for pipelines.

---

## 6. Documentation drift to fix while here

`internal/agent/system_prompt.md:33-35` and the `grep` description both imply
`glob` is a `grep` path filter ("`glob` filter … for example `**/*.go`"). The
`grep` tool's own `glob` **parameter** is that filter; the **`glob` tool** is a
file finder. Fix the wording so the model is not told two contradictory things.

---

## 7. Implementation steps

Ordered; each step is independently verifiable.

1. **Add the mode→roots decision.**
   Where: `internal/tools/workspace.go`.
   What: a pure helper (`effectiveReadRoots` / `effectiveWriteRoots` or a single
   `modeLiftsBoundary(mode, effect)`) that returns "unrestricted" for
   `bypass-all` (+ `read-only`/`read-write` for reads / reads+writes), else the
   scope roots.
   Why: this is the single missing decision the whole bug hinges on.

2. **Thread the mode into the scoped resolvers.**
   Where: `resolveScopedReadPath` (`:271`), `resolveScopedPath` (`:319`),
   `resolveScopedTargetPath` (`:359`), `recheckScopedWriteTarget` (`:396`), and
   their call sites: `read_file.go:82`, `read_minified_file.go:63`, `grep.go:133`,
   `glob.go:94`, `ls.go:130`, `list_directory.go:78`, `lsp_navigate.go:96`,
   `edit_file.go:68,152`, `write_file.go:62,111`, `multi_edit.go:99,129`,
   `apply_patch.go:60,121`, `bash.go:119`, `exec_command.go:546`.
   What: pass `RunOptions.PermissionMode` (already present) through; when the mode
   lifts the boundary and the path exists, accept the resolved absolute path.
   Why: every one of these currently fails identically; fixing only `read_file`
   would leave `grep` (the 1.1%-used tool the model most wants to replace) broken.

3. **Preserve `ask-all`/defaults exactly.**
   What: the helper returns "use scope roots" for `ask-all` and empty. No behavior
   change paths.
   Why: containment for the permission-asking profiles is a security property, not
   an inconvenience.

4. **Do not touch `request_permissions` semantics.**
   Keep the bypass-all early return (`loop.go:2491`). Under Fix 1 the model no
   longer *needs* it under bypass-all. Optionally, in a later change, allow
   `GrantRequestPermissions` to widen `Scope` so the tool's documented behavior
   matches reality — **out of scope here**, note it as follow-up.

5. **Reword the tool descriptions.**
   Where: `internal/tools/read_file.go:36-40`, `grep.go:42`,
   `write_file.go`, `glob.go:31`, `ls.go`, `list_directory.go`.
   Why: put the native tools in the same decision frame as the shell verbs the
   model already reaches for.

6. **Add the addendum line.**
   Where: `internal/agent/harness_prompt_addenda.go` (`openWeightPromptAddendum`).
   Why: DeepSeek is the measured dominant family and gets this block.

7. **Fix the `glob` wording drift.**
   Where: `internal/agent/system_prompt.md:33-35`.
   Why: remove a contradictory instruction (see §6).

---

## 8. Files affected

**Modified**

- `internal/tools/workspace.go` — the mode→roots helper + wiring into the four
  scoped resolvers.
- `internal/tools/read_file.go`, `read_minified_file.go`, `grep.go`, `glob.go`,
  `ls.go`, `list_directory.go`, `lsp_navigate.go`, `edit_file.go`, `write_file.go`,
  `multi_edit.go`, `apply_patch.go`, `bash.go`, `exec_command.go` — pass the mode
  and (for descriptions) reword.
- `internal/agent/harness_prompt_addenda.go` — one line in the open-weight addendum.
- `internal/agent/system_prompt.md` — fix the `glob` drift.

**Created**

- One focused test file per behaviour cluster (see §9) — e.g.
  `internal/tools/scope_mode_test.go`. Do not add a new package.

**Removed**

- None. No dead code is created by this change; `Scope.AddRead`/`AddTemporaryRead`
  stay (still used by `request_permissions`).

---

## 9. Testing and verification

**Reproduce the defect (before fix).**

```bash
go build -o /tmp/kc ./cmd/kajicode
mkdir -p /tmp/ws && cd /tmp/ws && git init -q
mkdir -p ~/kc-outside-probe && echo SECRET > ~/kc-outside-probe/target.txt
# expect: "must stay inside the workspace"
/tmp/kc exec --permissions bypass-all --output-format json \
  "Use ONLY read_file to read $HOME/kc-outside-probe/target.txt; quote the error."
```

**Expected after the fix.**

- `bypass-all`, `read-only`, `read-write`: `read_file`/`grep` on the outside path
  **succeed** (read); writes succeed for `bypass-all`/`read-write` only.
- `ask-all`: unchanged (prompt / denial with the `--add-dir` hint).

**Unit tests (new, focused).**

1. `effectiveReadRoots`/write table test across all four modes + empty, asserting
   unrestricted vs scope-roots.
2. `resolveScopedReadPath` with an outside absolute path: succeeds under
   `bypass-all`/`read-only`, fails under `ask-all` — and the failure message is
   unchanged.
3. `resolveScopedTargetPath` (write): succeeds under `bypass-all`/`read-write`,
   fails under `read-only`/`ask-all`.
4. **Regression:** `ask-all` behavior byte-identical to today (assert the exact
   `must stay inside the workspace` string).
5. Existing scope tests must keep passing unchanged:
   `internal/tools/file_tools_test.go` (`TestScopedToolsAllowExtraRootWrites`,
   `TestScopedToolsAllowReadOnlyRootsWithoutWrite`,
   `TestScopedToolsKeepRelativePathsInWorkspace`),
   `internal/sandbox/scope_test.go`,
   `internal/tools/registry_test.go:251`
   (`TestRegistryBypassAllOverridesDenyAndStripsSandbox`).

**End-to-end (real model).** Run the `exec` matrix above for all four modes and
assert the observed tool result, not just compile success. Disable `bash` in the
prompt (`--disabled-tools bash`) to prove `read_file` alone now succeeds — that
removes the "the model just used bash instead" confound.

**Prompt/description check.** `go test ./internal/agent` for the
`system_prompt_*` and `harness_config` tests; eyeball with
`/tmp/kc prompt inspect --full --json` (section `model`).

**Validators (repo standard).**

```bash
make fmt-check
go vet ./...
go test ./...                 # per AGENTS.md; `make test` if concurrency touched
git diff --check
```

**Preference measurement (optional, to confirm Fix 2).** Re-run the session
histogram from §2.5 on sessions created after the change and compare the
`bash` share and the pure-read/search share. Without this the Fix 2 effect is
unmeasured — say so rather than claiming it.

---

## 10. Cleanup

- No dead code is introduced. If step 5 rewrites `read_file`'s description,
  check no test asserts its exact current string.
- The `glob`/`grep` wording fix must update **both** `system_prompt.md` and the
  `grep` tool description together, or the drift simply moves.
- If Fix 1 makes the `ask-all` denial message unreachable for some path shape,
  confirm the message is still asserted where a test expects it; do not delete the
  assertion, adjust the fixture.

---

## 11. Risks

1. **Security regression is the real risk.** Widening roots for `read-only` /
   `read-write` / `bypass-all` must **not** widen `ask-all` (or empty). This must
   be pinned by tests (§9.3–9.4). The `ask-all` default is the safe path and must
   stay byte-identical.
2. **Mode is a live toggle, roots are startup state.** TUI Shift+Tab and ACP
   `setMode` change `permissionMode` mid-session. Because Fix 1 evaluates the mode
   *per call* (from `RunOptions`) rather than mutating `Scope`, this is handled —
   but it is the assumption the implementation must not violate. Do **not**
   implement Fix 1 by mutating the shared `*Scope` on mode change.
3. **`DenyRead` interaction.** `engine.go:180-189` preserves denied-read
   restrictions even under bypass-all in some paths. Fix 1 decides only the
   *workspace* boundary; it must not silently defeat `DenyRead`. Verify against
   `internal/sandbox/engine_test.go` expectations.
4. **Fix 2 is unmeasured until re-measured.** Description/prompt wording is a
   hypothesis; §9's preference measurement is the only way to know. Do not claim a
   reduction in shell use without it.
5. **Windows/volume roots.** "Unrestricted" must mean the volume root
   (`C:\`) via `filepath.VolumeName`, mirroring the existing walk in
   `NormalizePrefixForRoot` (`scope.go:289-334`), not a hardcoded `/`.
6. **Out of scope (call out, do not fix).** The `bash`-vs-`exec_command` overlap
   is a separate cleanup: `exec_command` is ~0.5% of calls, and several `bash`
   calls looked like PTY needs (`lsof`, `sleep`, dev servers). Worth a follow-up
   plan, but not part of this defect.

---

## 12. Sequencing

1. Fix 1 (scoping) — the highest-value, mechanical fix, and a prerequisite for
   trustworthy measurement of Fix 2.
2. Fix 2 (descriptions + addendum + drift) — cheap, complements Fix 1.
3. Measure the shell-vs-tool histogram on new sessions; keep or revert Fix 2 on
   the data.

Do not start Fix 2 before Fix 1 is verified — otherwise the measurement is
confounded by the tools still failing on outside paths.

---

## 13. Session follow-up (implemented)

### 13.1 Fix 2 shipped: the permission-mode boundary is now legible

Fix 2's wording was implemented as a single shared constant,
`boundaryNote` (`internal/tools/workspace.go`), appended to the description of
every path-taking tool: `read_file`, `read_minified_file`, `write_file`,
`edit_file`, `multi_edit`, `ls`, `list_directory`, `grep`, `glob`, `apply_patch`.
The `grep`/`glob` "searches the workspace and any granted extra root" phrasing was
replaced by it (that wording was the drift the plan called out, and it was also
actively wrong under a boundary-lifting mode). A matching line was added to
`internal/agent/system_prompt.md`.

The wording is intentionally one constant so it cannot drift between tools; a
regression test (`internal/tools/boundary_note_test.go`) pins that each tool
mentions the permission-mode boundary.

**Still unmeasured** (plan §9): whether the model reaches for native tools more
often on new sessions. The description change is a hypothesis until that run.

### 13.2 The `MutationTargets` residual is NOT fixable the way the audit proposed

The earlier audit proposed threading `PermissionMode` into `MutationTargets`
(`internal/tools/mutation_targets.go`) so a write outside the workspace under a
boundary-lifting mode is still snapshotted for rewind. **That change is a
regression and was rejected after verification.**

Root cause: the rewind path is workspace-confined by construction.
`sessions.resolveWithinWorkspace` (`internal/sessions/rewind.go:135`) does
`filepath.Join(cleanRoot, rel)`. Go's `filepath.Join` **does not discard the root
for an absolute second argument** — verified: `Join("/ws", "/Users/x/f.txt")` ==
`"/ws/Users/x/f.txt"`. So returning an absolute outside path from
`MutationTargets` would have the checkpoint read/record a *phantom* in-workspace
file and, on rewind, write or delete the wrong path.

Correct behavior chosen: `MutationTargets` stays **workspace-only** and is
documented as such. Rewinding an outside write under `bypass-all`/`read-write` is
therefore **unsupported** — outside writes are simply not snapshotted, which is
the status quo and not a regression. Making rewind work outside the workspace is
a larger, separate change (the capture/restore layer would need a root-aware path
model, not just a resolved absolute string) and is out of scope here.

`apply_patch`'s helpers (`patchTargets`, `validatePatchPaths`,
`recheckPatchWriteTargets`) were left mode-blind for the same reason: they feed
the same workspace-confined checkpoint/`FileChanges` path.

Regression test: `TestMutationTargetsStaysWorkspaceOnly`.
