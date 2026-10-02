# OpenCode ⇄ KajiCode — Feature Gap Analysis

Research notes comparing [opencode](https://opencode.ai) (sst/anomalyco, TypeScript/Bun)
against KajiCode's current Go surfaces. Written to answer: **what does opencode
have that KajiCode does not?**

## 0. What was actually on disk in `/tmp`

The `/tmp/opencode`, `/tmp/opencode-src`, `/tmp/opencode-research`,
`/tmp/opencode-probe`, and `/tmp/opencode-agents` trees are **stale, empty
skeletons** — 60 broken symlinks each (300 total), all pointing into a monorepo
(`../../ui/src/assets/...`, `packages/docs/openapi.json`) that no longer exists
locally. **No opencode source survives on this machine**, so this comparison is
built from opencode's published docs (opencode.ai/docs, README) as of 2026-10-02,
cross-checked against KajiCode's repo.

## 1. Where the architectures differ (the root of most gaps)

| | opencode | KajiCode |
| --- | --- | --- |
| Core language | TypeScript on Bun | Go |
| Process model | **Client/server**: a headless HTTP server (OpenAPI 3.1) + TUI is just a client | Single process; TUI/exec/ACP are in-process launch modes |
| Client surfaces | TUI, desktop (Tauri/Beta), web, VS Code/Cursor/etc. extension, Zed/JetBrains/Neovim via ACP, JS/TS SDK | TUI, `exec`, ACP bridge (`kajicode acp`), separate Rust `kajicode-desktop` |
| Extension language | JS/TS modules (npm or local files) | JSON manifests + external commands (no in-process scripting) |
| Config | `opencode.json` (+ `tui.json`, `AGENTS.md`) | `kajicode.json` + `AGENTS.md`/`KAJICODE.md` |

The single biggest structural difference: **opencode exposes its whole engine as
an HTTP API**, so every other client (web, IDE, SDK, TUI control) is a thin
consumer. KajiCode has no equivalent server/API surface.

---

## 2. Things opencode has that KajiCode does not

Ordered roughly by strategic weight.

### 2.1 Headless HTTP server + OpenAPI + generated SDK — **MISSING**
- `opencode serve` runs a headless HTTP server publishing an **OpenAPI 3.1** spec
  at `/doc`; clients are generated from it.
- Rich REST surface: `/session` (create/list/fork/abort/revert/summarize/diff/
  share/permissions), `/session/:id/message`, `/session/:id/command`,
  `/session/:id/shell`, `/find` (text/file/symbol), `/file`, `/lsp`, `/formatter`,
  `/mcp`, `/agent`, `/tui/*` (drive the TUI remotely), `/event` (SSE bus), `/auth`.
- Type-safe **JS/TS SDK** (`@opencode-ai/sdk`) with `session.prompt`,
  structured output, `noReply`, events.
- **KajiCode:** no HTTP server, no OpenAPI spec, no published SDK. `kajicode serve`
  only exposes *tools over MCP stdio* (`serve --mcp`), not the agent engine.
  The `internal/daemon/remote` bridge is a narrow TLS device-pairing channel, not
  a session API.

### 2.2 Structured output (JSON-schema-constrained responses) — **MISSING**
- opencode `session.prompt({ format: { type: "json_schema", schema, retryCount }})`
  returns validated `structured_output` via a `StructuredOutput` tool, with
  retries and a `StructuredOutputError`.
- **KajiCode:** `exec` supports text/json/stream-json I/O, but there is no
  schema-constrained model output mode. No `json_schema`/`structured_output`
  references anywhere in the tree.

### 2.3 In-process JS/TS plugins with a rich lifecycle bus — **PARTIAL**
- opencode plugins are JS/TS modules (local `.opencode/plugins/` or **npm**,
  auto-installed via Bun) that can **mutate tool args before execution**
  (`tool.execute.before`), react to ~30 events (`file.edited`, `lsp.client.diagnostics`,
  `message.*`, `permission.*`, `session.*`, `todo.updated`, `shell.env`, `tui.*`),
  add custom tools, inject env, and **override the compaction prompt**
  (`experimental.session.compacting`).
- **KajiCode:** plugins are JSON manifests that add tools/hooks/prompts/skills, and
  hooks run external commands on `beforeTool`/`afterTool`/`sessionStart`/`sessionEnd`.
  No JS/TS module, no npm auto-install, no arg-mutation hook, no compaction hook,
  and a much smaller event surface.

### 2.4 IDE extension (VS Code / Cursor / Windsurf / VSCodium) — **MISSING**
- opencode ships an extension auto-installed when you run `opencode` in the
  integrated terminal: `Cmd+Esc` split terminal, **selection/tab context sharing**,
  `@File#L37-42` reference shortcuts.
- **KajiCode:** ACP covers Zed/JetBrains/Neovim-style editors, but there is no
  first-party VS Code-family extension.

### 2.5 Web UI (`opencode web`) — **MISSING**
- Browser app served locally (`--port`, `--hostname 0.0.0.0`, `--mdns`,
  `--cors`), optional HTTP basic auth (`OPENCODE_SERVER_PASSWORD`), and
  `opencode attach http://host:port` to attach a TUI to a running server sharing
  sessions.
- **KajiCode:** no browser surface (the Rust `kajicode-desktop` is a separate
  native app, not a web server).

### 2.6 Session sharing (public links) — **MISSING / opposite**
- opencode `share`/`unshare` (and `POST/DELETE /session/:id/share`) sync a
  conversation to a public `opncd.ai/s/<id>` URL; modes `manual`/`auto`/`disabled`;
  enterprise SSO/self-host options.
- **KajiCode:** explicitly **local-only** ("never uploaded as telemetry"); no
  share/upload path. This is a deliberate stance, not an oversight — listed here
  as a capability opencode has.

### 2.7 Named "references" (external dirs + Git repos in context) — **MISSING**
- opencode `references` map aliases to a local `path` or a `repository`
  (Git URL / `owner/repo`) + `branch`, with `description` (advertised to the agent)
  and `hidden`. Exposed via `@alias` autocomplete and auto-allowed through the
  external-directory boundary.
- **KajiCode:** `--add-dir`/`/add-dir` grants extra *write roots*, and `repo-map`
  maps the current repo — but there is no named, described, agent-advertised
  reference set and no Git-repo materialization.

### 2.8 Provider policies (allow/deny provider use) — **MISSING**
- opencode `experimental.policies`: `{effect, action:"provider.use", resource}`
  with wildcards and last-match-wins, replacing `disabled_providers`/
  `enabled_providers`; global policy beats project policy (a repo cannot re-enable
  a globally denied provider).
- **KajiCode:** has sandbox policy/grants and harness permission rules, but no
  provider-availability policy layer.

### 2.9 Configurable, per-action permission rules — **PARTIAL**
- opencode permissions are keyed by tool with **granular pattern objects**:
  `bash: {"git *":"allow","rm *":"deny"}`, `edit: {"**/*.mdx":"allow"}`,
  plus `external_directory`, `webfetch` (URL match), `websearch` (query match),
  `question`, `lsp`, `task`, `skill`, and a **`doom_loop`** guard (repeated
  identical tool call → ask). `~`/`$HOME` expansion; `--auto` mode.
- **KajiCode:** strong sandbox (path scope, network, command risk, grants) and
  harness permission rules with `commandContains`/`match`/`action`, plus
  `--auto` autonomy levels. But the *rule schema* is coarser than opencode's
  per-tool glob map, and there is no equivalent of `doom_loop` by that name.

### 2.10 Rich, remappable keybind system (leader key + ~150 actions) — **PARTIAL**
- opencode `tui.json` exposes ~150 named actions with multi-binding, leader key
  (`ctrl+x`), `preventDefault`, and `"none"` to disable.
- **KajiCode:** has keybindings (`internal/tui/keybindings.go`) and a `Ctrl+X`
  leader chord (`internal/tui/leader.go`, 2s timeout) for common slash commands,
  but a far smaller, less configurable action set.

### 2.11 Auto-installing LSP + formatter registries — **PARTIAL**
- opencode **auto-downloads/installs** LSP servers (astro, clangd, kotlin-ls,
  lua-ls, php, svelte, terraform, tinymist, vue, yaml-ls…) and has a large
  built-in **formatter** registry (prettier, biome, gofmt, rustfmt, ruff, shfmt,
  black/uv, zig, nixfmt, clang-format, oxfmt…) — both configurable/overridable,
  with a `/lsp` and `/formatter` status API.
- **KajiCode:** has a solid LSP client + a ~28-extension `serverCommands` map
  (gopls, typescript-language-server, pyright, rust-analyzer, jdtls, …) and
  `format_on_write` (opt-in `KAJICODE_FORMAT_ON_WRITE=1`). Gaps: **no
  auto-install** of language servers, and format-on-write is a single
  env-gated path rather than a configurable per-language formatter registry.

### 2.12 `@`-references and context attachment ergonomics — **PARTIAL**
- opencode: `@file` references in commands/rules, `@alias/` reference search,
  `@general` subagent invocation in messages, file-reference shortcuts from the
  IDE.
- **KajiCode:** has `@` skill mentions and `@`-style flows in places, but no
  general `@file`/`@alias` reference grammar unified across commands and rules.

### 2.13 Smaller opencode-only items
- **`opencode run --auto`** non-interactive auto-approve (KajiCode has `--auto`
  autonomy levels in `exec` — comparable, verify parity).
- **`/undo` + `/redo`** (paired) in the TUI; KajiCode has revert/rewind but
  not a matching undo/redo pair by those names.
- **`/init`** that *improves an existing AGENTS.md in place* and asks targeted
  questions (KajiCode has `/init`; compare behavior).
- **Claude Code compat fallbacks**: reads `CLAUDE.md` and `.claude/skills/`,
  toggled by `OPENCODE_DISABLE_CLAUDE_CODE*` env vars.
- **Custom commands** in JSON config *and* markdown, with `!` shell-output
  injection and `@file` includes (KajiCode's user commands support `$ARGUMENTS`/`$1..$9`
  and model/agent frontmatter, but not `!`-shell or `@file` injection).
- **Desktop app**: opencode ships an official desktop build (Beta). KajiCode's
  desktop is a separate repo (`kajicode-desktop`, Rust/gpui).

---

## 3. Where KajiCode is ahead (for balance)

- **Go single binary** vs. Bun/Node runtime dependency; npm/install-script/`go build` distribution.
- **Sandbox depth**: platform isolation backends (Linux seccomp/landlock helper,
  macOS, Windows), grants, network policy — richer than opencode's permission
  config alone.
- **`exec` stream-JSON protocol** with documented contract for CI/automation.
- **Self-learning / perpetual memory** (`learn`/`recall`/`recipe_run`, harness
  apply/rollback/decay) — no opencode equivalent.
- **Compaction stack**: judge, artifacts, spill, tail preservation, calibration.
- **Fast classifier seam** (`internal/classifier`) for tool-result relevance gating.
- **Local control** (browser/terminal/desktop helpers), cron jobs, worktrees,
  verify/changes commands, usage/cost reporting.

---

## 4. Suggested priorities if we want parity

1. **Engine server + OpenAPI + SDK** (§2.1) — the keystone; unlocks web, IDE,
   and scripted clients. Highest leverage, largest effort.
2. **Structured output** (§2.2) — small, high value for automation/evals.
3. **Plugin bus: arg-mutation + compaction hooks + npm/local JS** (§2.3).
4. **Provider policies** (§2.8) and **finer permission globs + doom_loop** (§2.9).
5. **Named references incl. Git repos** (§2.7).
6. **Formatter/LSP registries with auto-install** (§2.11).
7. **VS Code extension** (§2.4) and/or **web UI** (§2.5) — clients on top of #1.

> Verification note: opencode facts come from its live docs on 2026-10-02;
> KajiCode facts were verified against this repo's source. Items marked PARTIAL
> may have more capability than surfaced here — confirm in source before scoping.
