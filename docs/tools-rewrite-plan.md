# KajiCode Tools — Complete Rewrite Plan

Status: **Plan (no code changed yet)**. Scope: rebuild the builtin tool layer in
`internal/tools` to mirror opencode's tool ergonomics, add the tools opencode has that we
lack, and eliminate the error-prone per-tool arg-parsing duplication.

Read alongside `docs/tooling-research-plan.md` (earlier gap analysis). This doc is the
actionable build spec. Sources studied:
- opencode: `packages/opencode/src/tool/*.{ts,txt}` (read, write, edit, multiedit, patch,
  batch, todo, task, bash, glob, grep, ls, lsp, webfetch, websearch, codesearch, skill, tool)
- kajicode: `internal/tools/*.go`, `internal/specialist/*`, `internal/agent/parallel_tools.go`,
  `internal/agent/loop.go`.

---

## 0. Core finding

KajiCode's tool **engine** is already more advanced than opencode's in three ways we keep:

1. **Automatic parallel tool execution** — `internal/agent/parallel_tools.go` +
   `loop.go:726` already runs read-only, thread-safe, non-conflicting tool calls
   concurrently *without* the model asking. This is architecturally superior to opencode's
   opt-in `batch` tool (below).
2. **Capabilities metadata** (`Effect`, `ThreadSafe`, `ResourceKeys`) that makes that
   parallelism safe.
3. **Fuzzy-edit + indentation-adaptation engine** (`edit_replacers.go`) that at least matches
   and partly exceeds opencode's.

The rewrite should therefore **rewrite the tool *surface* (descriptions, params, helpers,
error handling, guidance) and add missing tools** — not throw away the safety engine.

### What actually breaks today / is error-prone
- Every tool hand-rolls arg parsing (aliases, `intArg`, `boolArg`, `stringArg`,
  `additionalProperties`) — inconsistent, verbose, and a major source of schema/arg drift.
- Tool descriptions are short and descriptive, not **behavior-shaping**. opencode's
  `bash.txt`/`read.txt`/`write.txt` tell the model *when not to use* the tool and how to
  batch, which measurably cuts misuse.
- Missing tools: `todoread`/`todowrite` (session-persisted todo), `batch` (latency, below),
  `multiedit` (atomic multi-edit), `code_search`, image/PDF read, "did-you-mean" on missing
  paths, `ls` tree + ignore list.
- `update_plan` is in-memory/process-local and invisible to the TUI/session.

---

## 1. Target tool line-up (opencode parity)

| opencode tool | KajiCode today | Action |
|---|---|---|
| `read` | `read_file`, `read_minified_file` | **Rewrite** — binary/image/PDF detect, did-you-mean, cat -n format |
| `write` | `write_file` | **Rewrite** — read-before-overwrite, guidance, diff preview |
| `edit` | `edit_file` | **Rewrite** — reuse fuzzy engine; unique-match + diff + diagnostics |
| `multiedit` | — | **Add** — atomic multi-edit on one file |
| `patch` | `apply_patch` | **Keep** (do-not-use guidance already present) |
| `batch` | auto-parallel (agent loop) | **Add as thin tool** OR keep auto-parallel (see §3 decision) |
| `todoread`/`todowrite` | `update_plan` | **Add** session-persisted todo |
| `task` | `Task` (`internal/specialist`) | **Keep** (already stronger: subagent_type+background+resume) |
| `bash` | `bash`, `exec_command`, `write_stdin` | **Rewrite description/guidance**; keep dual-tool split |
| `glob` | `glob` | **Rewiite** — sorting by mtime, limit, ignore |
| `grep` | `grep` | **Rewrite** — ripgrep output, sorted by mtime, count mode |
| `ls` | `list_directory` | **Rewrite** — tree render, ignore list, limit |
| `lsp` | `lsp_navigate` | **Rewrite to opcode's operation enum** (9 ops) |
| `webfetch` | `web_fetch` | **Keep + harden** (already has SSRF guard) — add timeout/format |
| `websearch` | `web_search` | **Keep** (backend-agnostic, has domain filter) — add modes |
| `codesearch` | — | **Add** — reuse `web_search` backend abstraction |
| `skill` | `skill` | **Keep** (equivalent) |
| `task` tomultiedit | — | covered above |

---

## 2. New Go architecture

### 2.1 Tool definition — replace hand-rolled schemas with a typed builder

Goal: kill the `intArg`/`boolArg`/`stringArg`/alias duplication. Introduce a small
declarative schema DSL that produces both the JSON Schema (for provider advertising) and
the Go arg extractor. Location: `internal/tools/argspec/` (new package) to keep file sizes
small per AGENTS.md.

```go
// argspec/argspec.go (design)
package argspec

type Kind int
const (
    KindString Kind = iota
    KindInt
    KindBool
    KindStringSlice
    KindIntSlice
    KindObject // nested for additional_permissions-style sub-schemas
    KindRaw    // pass-through (tool_search tool_calls, batch tool_calls)
)

type Spec struct {
    Name        string
    Kind        Kind
    Required    bool
    Aliases     []string      // e.g. path: path, file, file_path
    Description string
    Default     any
    Enum        []string
    Min, Max    *int
    Items       *Spec         // for slices
    Properties  []*Spec       // for KindObject
}

// Methods:
func (s *Spec) ToSchema(deps SchemaRef) tools.Schema   // -> registry PropertySchema
func (s *Spec) Parse(args map[string]any) (map[string]any, error) // validate+coerce
```

- Each tool declares one `[]*argspec.Spec`.
- One typed helper generates the `tools.Schema` from the spec (single source of truth, so
  `AdditionalProperties:false` stays correct everywhere).
- One helper validates & coerces args → structured Go values, returning a consistent
  `Error: Invalid arguments for <tool>: ...` message (which existing tests likely assert on —
  preserve the prefix).

### 2.2 Tool interface — keep, but add a shared `Base`

Current `types.go` `Tool` interface is fine. Add:
- A `baseTool` that already exists stays. Add a new `MustHaveSchema()` guard or keep the
  existing schema-based path. Recommend: keep `Tool` interface verbatim (public contract,
  used by providers/MCP), only change *how each concrete tool builds its fields*.

### 2.3 Registry & catalog — keep, minor additions

- Keep `Registry`, `RunOptions`, permission/sandbox/capability flow (all good).
- `BuiltinCatalog` gains the new tools (`todo_read`, `todo_write`, `multi_edit`, `code_search`,
  and optionally `batch`).
- Add a `Tools()` builder that composes read/write/shell/network/agent sets consistently.

### 2.4 Session-persisted todo — new domain + tools

Reuse `internal/sessions` for durability so todos survive across turns/restarts (unlike the
in-memory `update_plan`).

```
internal/tools/todo.go         // todo domain model + two tools registered here
internal/sessions/todo.go      // persistence (file-backed like session metadata)
```

- `todo_write`: `{ todos: [{content, status, priority, notes}] }` — atomic replace, enforce a
  single `in_progress`, close to periodic. Returns formatted list.
- `todo_read`: no required params — read current list.
- Wire `todo_write`/`todo_read` into the TUI sidebar (`internal/tui/sidebar.go`) and into
  session store so they're durable and user-visible.
- Decide relationship to `update_plan` (recommend: deprecate `update_plan` in favor of todo;
  see decision §3).

### 2.5 Batch tool — optional thin wrapper (decision required)

The **automated** parallel executor already exists and is superior. Two options:

- **A (recommended): no `batch` tool.** Keep auto-parallel in the loop. Document it. Add the
  opencode-style "run multiple independent calls in one message" *guidance* to descriptions
  instead.
- **B: add a thin `batch` tool** (`{tool_calls:[{tool, parameters}]}`) that fans out to
  `Registry.RunWithOptions`, respecting capabilities (only parallelize `EffectReadOnly &&
  ThreadSafe`, else serialize), max 10 calls, mirrors opencode's status summary. Sits on top of
  the existing parallel executor logic (reuse `parallelSafeToolCall`-style checks, now in the
  tools package).

Recommend **A** for v1 (less surface, no nested-tool security work), revisit **B** if a
provider's harness forces the model to batch.

### 2.6 Multi-edit tool

```
internal/tools/multi_edit.go
```
- Params: `{ filePath, edits: [{oldString, newString, replaceAll}] }`.
- Apply all edits to an **in-memory copy** of the file using the existing
  `edit_replacers.go` engine; write once only if **all** edits succeed (atomic), else fail
  with an index of which edit failed.
- Reuse `fileResourceKeys`, `promptSafety(Write)`, diff preview, inline diagnostics.
- This is the single lowest-risk, highest-value *tool addition*.

### 2.7 code_search (add)

```
internal/tools/code_search.go
```
- Reuse `web_search.go`'s `searchBackend` abstraction (env-configured), add a `code`-flagged
  query for APIs/libraries/SDKs. Params mirror opencode: `{ query, tokensNum (1000-50000,
  default 5000) }`.
- Only advertise in auto when a backend is configured (`defaultSearchBackend() != nil`),
  same as `web_search`.

### 2.8 read_file rewrite (binary + image/PDF + did-you-mean)

```
internal/tools/read_file.go   // keep, extend
internal/tools/file_binary.go // new: isBinaryFile (ext denylist + >30% non-printable)
internal/tools/file_media.go  // new: image/PDF -> base64 data part via runtime attachment type
```
- cat -n style numbering (`00001|`) to match opencode's `<file>` block and improve LLM
  tokenization.
- Optional `offset`/`limit` (0-based in opencode; kajicode uses 1-based `start_line`/`end_line`
  — keep kajicode's 1-based and ALSO accept opencode `offset`/`limit` aliases).
- Missing path → parent-dir scan → top 3 fuzzy suggestions ("Did you mean one of these?").
- Image/PDF → return as base64 `data:` attachment so the model sees the actual file (reuse
  the attachment type already present in `internal/kajicoderuntime` for the agent loop).
- SVG and other text handled normally.

### 2.9 write_file / edit_file / patch guidance

Port opencode's policy text into the descriptions (pure string changes):
- write: "must read before overwrite; prefer edit; never create docs unless asked."
- edit: "exact match; unique-match error; use replaceAll for renames; keep indentation."
- bash(/exec): "do NOT use for file ops — use glob/grep/read/edit/write; prefer workdir over
  `cd &&`; quote paths with spaces; run independent commands in parallel; git safety + PR flow."

### 2.10 ls rewrite  ✅ done (internal/tools/ls.go)

New `ls` tool: params `{ path, depth (3), include_dirs (true), ignore (default noise dirs), limit (100) }`.
Renders a directory tree (directories first, indented, slash-suffixed) honoring default + caller
ignore globs and sandbox read exclusions. Truncates above limit with a visible marker + `truncated`
meta. Registered in CoreReadOnlyToolsScoped.

### 2.11 grep / glob rewrite  ✅ done

- `grep`: content and files_with_matches results now sort by file mtime (newest first, like opencode),
  with deterministic ties; count mode unchanged. Content scan keeps its head_limit early-stop.
- `glob`: results sort by mtime descending, keeping `include_dirs`/`limit` and the truncation marker.

### 2.12 lsp_navigate → full operation enum  ✅ done (internal/tools/lsp_navigate.go + internal/lsp/navigate.go)

Added ops to the existing tool: `hover`, `document_symbol`, `prepare_call_hierarchy`, `incoming_calls`,
`outgoing_calls` (new NavOps with a richer `NavResult` carrying locations/symbols/hover/outline/
call-items/call-edges). document_symbol is whole-file; the rest are position-anchored.
- Kept the `internal/lsp` manager; split decode/format logic across navigate.go (schema + position ops
  stay in lsp_navigate.go). New decode helpers: decodeOutline (nested DocumentSymbol + flat
  SymbolInformation), decodeCallHierarchyItems (single/array), decodeCallHierarchyCalls.
- Tool still confines the path to the workspace and degrades to "no language server" for unsupported types.

---

## 3. Decisions to confirm before coding

1. **Batch**: (A) rely solely on existing auto-parallel executor, or (B) add an explicit
   `batch` tool too? Recommend **A**.
2. **update_plan fate**: deprecate it in favor of session-persisted `todo_read`/`todo_write`?
   (Recommend yes — durability + TUI visibility, but note `update_plan` is referenced in the
   harness profile / prompts; keep both transiently.)
3. **Breaking param renames**: opencode uses camelCase (`filePath`, `oldString`,
   `subagent_type`, `run_in_background`, `session_id`, `tokensNum`). kajicode uses snake (and
   accepts aliases). Do we (a) keep snake + add camel aliases (safe, recommended), or (b) go
   full camelCase like opencode (cleaner parity, breaks existing prompts/configs)?
4. **Scope / files**: keep accumulating in large `internal/tools` files, or split into the new
   small files listed above (recommended to match AGENTS.md "keep new files under 200 lines").

---

## 4. Build order (phased) + tests

**Phase A — foundation (no behavior change first):**
1. Add `internal/tools/argspec` and port 2-3 tools (read_file, write_file) to it incrementally.
   Gate: existing `internal/tools/*_test.go` still pass; schema output unchanged.

**Phase B — high-value new surface:**
2. `todo_read`/`todo_write` + `internal/sessions/todo.go` + TUI sidebar.
3. `multi_edit` (reuse fuzzy engine).
4. `read_file`: binary detect + did-you-mean + image/PDF.

**Phase C — add & round out:**
5. `code_search`.
6. `ls` tree + ignore; `glob`/`grep` mtime sorting + opencode output.
7. `lsp_navigate` op enum.

**Phase D — guidance pass:**
8. Rewrite all description strings with opencode-style policy text (bash/write/edit/read/glob/grep).

**Tests (each landed tool):**
- argspec: schema-param consistency, unknown-key rejection, alias coercion.
- todo: single in_progress, persistence across registry rebuild (temp session store), TUI state.
- multi_edit: atomic (one bad edit ⇒ no writes), sequential apply, fuzzy engine reuse.
- read_file: binary rejection, image base64 path, did-you-mean suggestions, cat -n formatting.
- code_search: backend-nil guard; with fake backend returns/errors.
- ls/grep/glob: mtime sort, ignore list, limit/truncation meta.
- lsp: each new op only when a server is available (existing lsp test scaffold).
- Full loop: `make fmt-check`, `go vet ./...`, `go test ./...`, release build+smoke,
  `git diff --check`. Concurrency/sandbox touching changes also `make test` + govulncheck.

---

## 5. Explicitly NOT rewriting

- `web_fetch` SSRF guards (keep; only add `timeout`/`format` params).
- Sandbox/permission/capability engine in `registry.go` / `internal/sandbox`.
- `edit_replacers.go` fuzzy engine (reuse under `multi_edit`).
- Agent-loop auto-parallel executor (`internal/agent/parallel_tools.go`).
- `internal/specialist` `Task` tool (already exceeds opencode's).
