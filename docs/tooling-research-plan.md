# KajiCode Tooling — Deep Research & Improvement Plan

## TL;DR

KajiCode's tool **engine** (sandbox, permissions, capabilities, fuzzy-edit, redaction,
budgeting) is already more ambitious than opencode's. opencode wins on **tool choice and
guidance ergonomics**, not on raw engine power. The biggest concrete gaps in kajicode are:

1. **No `batch` / parallel tool** — opencode stresses "batch → 2–5x efficiency."
2. **No structured `todo` tool** — opencode's `todoread`/`todowrite` is a first-class session surface; kajicode uses an invisible in-memory `update_plan`.
3. **`read_file` doesn't read images/PDFs** and offers no "did you mean" path recovery on missing files.
4. **No code search tool** (opencode wires Exa `codesearch`).
5. **Tool prompt discipline** — opencode's tool descriptions are much more prescriptive about *when to use each tool* and about not mixing file ops into `bash`.

Everything below maps a source anchor (kajicode path `internal/tools/*` and opencode
source `packages/opencode/src/tool/*`) to a concrete, prioritized action.

---

## 1. Research basis

### Where things live

| Concern | KajiCode (Go) | opencode (TS) |
|---|---|---|
| Tool interface / metadata | `internal/tools/types.go`, `registry.go` | `src/tool/tool.ts`, `src/tool/registry.ts` |
| Fuzzy edits | `edit_replacers.go` (9 strategies + indent adaptation) | `edit.ts` (9 strategies) |
| File read | `read_file.go`, `read_minified_file.go` | `read.ts`, `write.ts` |
| Shell | `bash.go`, `exec_command.go` | `bash.ts` |
| Search | `glob.go`, `grep.go` | `glob.ts`, `grep.ts` (ripgrep) |
| Web | `web_fetch.go`, `web_search.go` | `webfetch.ts`, `websearch.ts`, `codesearch.ts` |
| Sub-agents | `internal/specialist/*`, `internal/swarm/*` + `Task` tool | `task.ts` |
| Planning | `update_plan.go` (in-memory) | `todo.ts` (`todoread`/`todowrite`, session-persisted) |
| Parallel | — (missing) | `batch.ts` |
| Skills | `skill.go` | `skill.ts` |

### Who is ahead where

**KajiCode engine advantages (keep, don't regress):**
- `web_fetch.go` has a full SSRF guard (private/loopback/metadata prefixes, embedded-IPv4
  detection, redirect validation). opencode's webfetch does not.
- Capabilities framework (`capabilities.go`) handles concurrency-safety metadata;
  opencode has none of this.
- `edit_replacers.go` matches opencode's fuzzy-replace set and *adds* span adaptation
  (`adaptReplacementToSpan`, `uniformIndentDelta`), disproportionate-match rejection, and
  strict ambiguity handling that opencode lacks.
- Output budgeting, redaction, path-scoping (`PathScope`), and sandbox denial signaling have
  no opencode equivalent.

**opencode advantages (the gaps to close):**
- `batch` tool and its latency-focus.
- `todo` tool persisted per-session and explicitly prompted to be used.
- Image/PDF reading with base64 attachments.
- "Did you mean" suggestions on missing file paths.
- `codesearch` / Exa-backed search.
- Sharper, behavior-shaping tool descriptions (notably `bash.txt`, `read.txt`, `write.txt`,
  `multiedit.txt`).

---

## 2. Concrete improvements (prioritized)

Priority key: **[P0]** high impact, well-scoped / **[P1]** medium / **[P2]** nice-to-have.

### 2.1 [P0] Add a `batch` (parallel tool-call) tool

**Anchor:** opencode `src/tool/batch.ts`; kajicode `internal/tools/registry.go`.

opencode runs up to 10 independent calls concurrently in one turn, marks each part
running/completed/error, returns a summary, and loudly coaches the model
("Keep using the batch tool for optimal performance!"). This materially cuts latency on
read-heavy work (read/grep/glob combos) and is the single most visible UX gap.

Implementation notes (kajicode-safe):
- Reuse the existing `Capabilities`/`ResourceKeys` machinery: only batch calls whose tools
  are `EffectReadOnly` AND `ThreadSafe` (or whose resource keys don't collide) are run in
  parallel; anything else is serialized. This preserves the fail-closed concurrency contract
  instead of opencode's naive `Promise.all`.
- Exclude nested `batch`, deferral-eligible, and interactive tools from batching.
- Cap at 10 calls like opencode; merge results with per-call status; aggregate output +
  per-call meta.
- Route each sub-call through `Registry.RunWithOptions` so sandbox/permission/budget/redaction
  still apply per call.
- Add focused tests in `internal/tools` (batching read-grep-glob, refusal of write/interactive
  tools, resource-key conflicts).

### 2.2 [P0] Add a session-persisted `todo` tool (`todo_read` + `todo_write`)

**Anchor:** opencode `src/tool/todo.ts`, `src/session/todo`; kajicode `update_plan.go`,
`internal/sessions/*`.

opencode persists todos per-session, exposes `todoread`/`todowrite` as ordinary tools, and
embeds strong "when to use / when not to use" coaching in the descriptions. KajiCode's
`update_plan` is process-local and not a durable, user-visible surface.

Directions (choose based on scope you want):
- **Minimal:** upgrade `update_plan` to persist in the local session store
  (`internal/sessions`) and expose a sibling `todo_read`. Keep one list; this reuses the
  proven `PlanItem` model.
- **Canonical (closer to opencode):** add a dedicated `todo` domain with `todo_write`
  (atomic array replace, single `in_progress`, statuses pending/in_progress/completed/
  cancelled) and `todo_read` (no-required-params). Persist via `internal/sessions` and render
  in the TUI (`internal/tui/sidebar.go`).
- Fold the rich opencode "When to Use / NOT to Use" prompts into the descriptions so the model
  self-selects.

### 2.3 [P0] `read_file`: image/PDF support + "did you mean" path recovery

**Anchor:** opencode `src/tool/read.ts`; kajicode `read_file.go`, `read_minified_file.go`,
`internal/kajicoderuntime` (for attachment types).

- **Images/PDF:** when the path resolves to an image, return the file as a base64 `data:` part
  (mime from extension/content) instead of printing binary garbage. KajiCode already has an
  image-rejection path in the agent (`internal/agent/loop_images_test.go`, `image_rejection_test.go`)
  — reuse the attachment/runtime types already there. Keep a clear `file type not supported`
  fallback and keep SVG/text handled separately.
- **Binary detection:** mirror opencode's `isBinaryFile` (extension denylist + ≥30%
  non-printable heuristic) so `read_file` fails cleanly instead of streaming control chars.
- **Did-you-mean:** on `os.IsNotExist`, list the parent dir, fuzzy-match the basename (contains
  either direction), and return up to 3 suggestions like opencode.
- Tests: image/PDF path, binary rejection, missing-file suggestions, SVG/non-binary deltas.

### 2.4 [P1] Add a `code_search` tool (and/or enrich `web_search`)

**Anchor:** opencode `src/tool/codesearch.ts` (Exa Code); kajicode `web_search.go`.

opencode gives the model a first-class "search for library/SDK/API usage patterns" tool backed
by Exa Code with tunable token budget. KajiCode only has `web_search` configured via an
env-chosen backend.

Options:
- Add a `code_search` tool that reuses the `web_search.go` backend abstraction (so it works
  with a configurable backend) but is purpose-shaped for code/API queries, with a
  `max_tokens` style budget.
- Or piggyback on `web_search` with a `type: code` parameter. Prefer a separate tool to match
  opencode's mental model and keep descriptions clean.
- Advertise in auto mode only when a backend is configured (mirror existing
  `defaultSearchBackend() != nil` gate).

### 2.5 [P1] Sharpen tool-description guidance (behavior shaping)

**Anchor:** opencode `bash.txt`, `read.txt`, `write.txt`, `multiedit.txt`, `edit.txt`,
`glob.txt`, `grep.txt`.

opencode's descriptions aren't just schema text; they're **policies** the model follows. The
single most impactful is `bash.txt`:
- "Do NOT use for file ops — use dedicated tools."  (no `find`/`grep`/`cat`/`sed`/`awk`/`echo` for file work)
- "Use `workdir`, not `cd && ...`."
- Git safety + PR workflow built in.
- Encourages multi-tool calls in parallel and chaining independent calls (this pairs with the
  batch tool).

Apply the same discipline to kaecause's description strings (in each tool's `baseTool{
name, description }`):
- `bash.go` / `exec_command.go`: add the "don't use for file ops," "prefer workdir," git-safety,
  and "run independent commands in parallel" guidance.
- `read_file.go`: "prefer reading whole files; use start/end/max_lines only for long files;
  you may read multiple files in one response."
- `write_file.go`: "must read before overwrite; prefer edit over write; never create docs unless
  asked."
- `edit_file.go`: exact-match + replaceAll semantics; unique-match guidance.
- `glob.go` / `grep.go`: "prefer dedicated tools over `find`/`grep` in bash; use Task/subagents
  for open-ended iterative search."

These are pure string edits + test updates, low risk, high reward.

### 2.6 [P2] Add a `multi_edit` tool

**Anchor:** opencode `src/tool/multiedit.ts`; kajicode `edit_file.go`.

opencode lets the model batch multiple find-replace edits on one file in an **atomic** operation
(all-or-nothing). This reduces round-trips when editing many spots in one file and pairs
naturally with the batch feature. KajiCode's `apply_patch` covers full-patch edits, but a
`multi_edit` built on `edit_replacers.go` (reusing all 9 strategies + adaptation) is a natural,
low-risk addition. Validate atomicity (apply to in-memory copy, write once on full success).

### 2.7 [P2] `ls`/directory rendering parity

**Anchor:** opencode `src/tool/ls.ts` (tree rendering with an `ignore` glob list and built-in
noise patterns like `node_modules`, `.git`, `target`, `.venv`).

KajiCode already has `list_directory`; optional polish:
- Honor a default ignore set of noise dirs and an `ignore` param.
- Optionally render a shallow tree like opencode for orientation.

### 2.8 [P2] Surface real-time "per-call running state" like opencode's part updates

opencode streams each tool part's running → completed/error state and lets `batch`/`task`
report per-subcall summaries in metadata. KajiCode already has a stream protocol
(`internal/kajicoderuntime`, `docs/STREAM_JSON_PROTOCOL.md`) and diagnostics callbacks — worth
verifying that batch/multi_edit/todo results flow through it and appear in the TUI.

---

## 3. Suggested sequencing

**Phase 1 (biggest visible wins, ~independent):**
1. `todo` tool (`0`+#2.2) — session-persisted, TUI-visible.
2. `batch` tool (#2.1) — reuse capabilities for safe parallelism.

**Phase 2 (file-engine parity):**
3. `read_file` image/PDF + binary + did-you-mean (#2.3).
4. `multi_edit` atomic tool (#2.6).

**Phase 3 (search + polish):**
5. `code_search` (#2.4).
6. Tool-description guidance passes (#2.5).
7. `ls` ignore/tree polish (#2.7), per-call streaming polish (#2.8).

Each phase ends with the standard validation loop (build, `go test ./...`, `go vet`,
release smoke) and touches the relevant owned packages. TUI additions add focused
view/update tests per AGENTS.md.

---

## 4. Things to NOT change (kajiCode strengths)

- `web_fetch` SSRF protections — do not trade these for opencode-style simplicity.
- `capabilities.go` concurrency contract — extend it for `batch` rather than bypassing it.
- `edit_replacers.go` fuzzy-edit depth and adaptation — keep as the engine under `multi_edit`.

---

## 5. Owned packages (per AGENTS.md)

- `internal/tools` — batch, multi_edit, todo, read_file/binary/image, ls, descriptions.
- `internal/sessions` — todo persistence.
- `internal/tui` — todo + per-call streaming surfacing (with tests).
- `internal/kajicoderuntime` — image/PDF attachments, streamed per-call state.
- `internal/providers` — `code_search` backend wrapping.
