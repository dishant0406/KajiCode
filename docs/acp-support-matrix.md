# ACP Support Matrix & Implementation Tracker

Tracking document for KajiCode's ACP surface (`kajicode acp`, `internal/acp`)
against **every** ACP schema that exists, not just v1 stable.

- **Spec source (authoritative):** `https://github.com/agentclientprotocol/agent-client-protocol`
  — `schema/v1/schema.json`, `schema/v1/schema.unstable.json`,
  `schema/v2/schema.json`, `schema/v2/schema.unstable.json`.
- **KajiCode speaks:** `protocolVersion: 1` (`internal/acp/types.go:7`).
- **Method counts (from schema `x-method`):** v1 stable **25**, v1 unstable
  **42**, v2 stable **16**, v2 unstable **33**.
- **Status:** `[ ]` not started · `[~]` in progress · `[x]` done · `[n/a]` deliberately out of scope.
- **Last verified against code:** shipped — v1 stable client→agent and the
  update variants it depends on are implemented and covered by
  `internal/acp/lifecycle_test.go`; verified live via the §0 harness.

### Status snapshot

Implemented and verified:

- **Session lifecycle:** `session/new` (with `configOptions`), `session/load`
  (replays history), `session/resume`, `session/list`, `session/close`,
  `session/delete` (+ `sessions.Store.Delete`), `session/prompt`,
  `session/cancel`, `session/set_mode`, `session/set_config_option`.
- **Config options (native selectors):** model, permissions, reasoning effort,
  turn budget, response style — with `config_option_update` on change.
- **Slash commands:** `available_commands_update` advertises a 23-command
  catalog covering every read/inspect CLI subcommand; `/name args` in a prompt is
  routed to that same subcommand (`internal/cli/acp_commands.go`). Output is
  capped at 64 KiB per command so listing commands can't flood the wire.
- **Elicitation:** `elicitation/create` is capability-gated and routes the
  agent's `ask_user` tool to the editor as a form; without the capability the
  agent uses its headless fallback (never hangs).
- **Updates:** `user_message_chunk`, `agent_message_chunk`,
  `agent_thought_chunk`, `tool_call`, `tool_call_update`, `plan`,
  `current_mode_update`, `config_option_update`, `available_commands_update`,
  `usage_update`.
- **Capabilities:** `loadSession`, image + embedded-context prompts,
  `sessionCapabilities` {list,resume,close,delete,fork,additionalDirectories},
  `mcpCapabilities` (both false — KajiCode owns MCP).
- **Session fork:** `session/fork` branches a new session from a stored one via
  `Store.Fork`, replaying the inherited transcript.
- **Auth:** `authenticate` (rejects agent-type ids) and `logout` (clears the
  active provider's OAuth token + stored key). `initialize` advertises terminal
  login methods (one per OAuth-capable catalog provider, e.g. `login-openrouter`)
  only when the client sets `clientCapabilities.auth.terminal`; the editor runs
  `kajicode auth login <provider>` in a real terminal, so the token never crosses
  the ACP wire. `/add-provider` collects only NON-SECRET provider config via a
  session-scoped form elicitation (name/baseUrl/model/authHeader/authScheme/
  customKind) and writes it through `providers add`; API keys are never
  requested over ACP (form elicitation MUST NOT carry secrets), so the reply
  points at `kajicode providers add --api-key-env` / `kajicode auth login`.
- **Retitle / compact / export:** shared headless logic in `internal/sessions`
  (`title.go`, `compact.go`, `export.go`) reused by the TUI, the new
  `kajicode sessions retitle|compact|export` subcommands, and ACP
  (`/retitle` emits `session_info_update`; `/compact` and `/export` run against
  the active session).

Deliberately out of scope (`[n/a]`): `authenticate`/`logout` (BYOK),
`fs/*` + `terminal/*` (KajiCode uses native local tools), URL elicitation,
`providers/*`, `nes/*`, `mcp/*`, the v1-unstable `document/*` surface, and the v2
draft. Interactive-only TUI modals (provider/MCP/skill pickers, thread browser)
are surfaced through config options and the command catalog where a text-output
contract exists, and otherwise remain editor-UI concerns.

> **Scope decision (see §3):** target **v1 stable** first (parity with Zed/Neovim),
> then the useful **v1 unstable** methods, and treat **v2** as a separate future
> track. Unstable methods are explicitly "may be removed or changed" per the schema.

---

## 0. How to reproduce current gaps

```bash
go build -o /tmp/kajicode ./cmd/kajicode

printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{}}}' \
'{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"'"$PWD"'","mcpServers":[]}}' \
'{"jsonrpc":"2.0","id":3,"method":"session/list","params":{"cwd":"'"$PWD"'"}}' \
'{"jsonrpc":"2.0","id":4,"method":"session/resume","params":{"sessionId":"x","cwd":"'"$PWD"'","mcpServers":[]}}' \
'{"jsonrpc":"2.0","id":5,"method":"session/delete","params":{"sessionId":"x"}}' \
'{"jsonrpc":"2.0","id":6,"method":"authenticate","params":{"methodId":"m"}}' \
'{"jsonrpc":"2.0","id":7,"method":"logout","params":{}}' \
| /tmp/kajicode acp 2>/dev/null
```

Observed today: every listed method resolves (no `-32601`); `initialize`
advertises the full capability set; `session/new` returns `configOptions`;
`session/load` replays the transcript via `session/update`.

---

## 1. Client → Agent methods

### 1A. v1 stable (target now)

| # | Method | Status | Notes |
|---|--------|--------|-------|
| 1.1 | `initialize` | `[x]` | advertises session/prompt/mcp/auth capabilities + dynamic authMethods |
| 1.2 | `authenticate` | `[x]` | handler present; agent-type ids rejected (logins are terminal auth) |
| 1.3 | `logout` | `[x]` | clears active provider OAuth token + stored key (`runAuthLogout`) |
| 1.4 | `session/new` | `[x]` | `agent.go` — returns full `configOptions` |
| 1.5 | `session/load` | `[x]` | `agent.go` — replays history via `session/update` |
| 1.6 | `session/resume` | `[x]` | `agent.go` handleSessionResume |
| 1.7 | `session/list` | `[x]` | `agent.go` handleSessionList |
| 1.8 | `session/close` | `[x]` | `agent.go` handleSessionClose (detach) |
| 1.9 | `session/delete` | `[x]` | `agent.go` + `Store.Delete` |
| 1.10 | `session/prompt` | `[x]` | `agent.go:192` |
| 1.11 | `session/cancel` (notif) | `[x]` | `agent.go:406` |
| 1.12 | `session/set_mode` | `[x]` | `agent.go:352` (auto/ask only) |
| 1.13 | `session/set_config_option` | `[x]` | model/mode/effort/turns/style; returns full state |
| 1.14 | `_kajicode/set_model` | `[x]` | `agent.go:393` — vendor ext (keep) |

> There is **no `session/set_model`** in ACP. Model selection = config option
> with `category:"model"`.

### 1B. v1 unstable — IMPLEMENTED

All six `document/*` notifications, the five `nes/*` methods, and the three
`providers/*` methods are implemented (`internal/acp/unstable.go`, wired in
`internal/cli/acp_unstable.go`). `initialize` advertises `providers`, `nes`
(with `events.document`, full sync), and `positionEncoding: utf-8`. NES is
provider-backed: `nes/suggest` runs the active model over the session's buffered
document. Clients that don't speak these ignore the capability and never call
them (Zed currently implements none of them).

| # | Method | Status | Notes |
|---|--------|--------|-------|
| 1.15 | `session/fork` | `[x]` | `handleSessionFork` → `Store.Fork`; gated by `sessionCapabilities.fork` |
| 1.16 | `document/didOpen` (notif) | `[x]` | buffer stored per session; `nes.events.document` advertised |
| 1.17 | `document/didChange` (notif) | `[x]` | full sync (also applies range edits) |
| 1.18 | `document/didClose` (notif) | `[x]` | drops the buffer |
| 1.19 | `document/didSave` (notif) | `[x]` | accepted (no-op; buffer already current) |
| 1.20 | `document/didFocus` (notif) | `[x]` | cursor recorded |
| 1.21 | `providers/list` | `[x]` | lists profiles + active routing config (never the key) |
| 1.22 | `providers/set` | `[x]` | switches the active provider (`providers use`) |
| 1.23 | `providers/disable` | `[x]` | switches off the disabled provider (refuses if it is the only one) |
| 1.24 | `nes/start` | `[x]` | accepted |
| 1.25 | `nes/suggest` | `[x]` | provider-backed single-edit suggestion from the buffered doc |
| 1.26 | `nes/accept` (notif) | `[x]` | accepted |
| 1.27 | `nes/reject` (notif) | `[x]` | accepted |
| 1.28 | `nes/close` | `[x]` | accepted |

### 1C. v2 (future track — do NOT implement now)

| # | Method | Status | Notes |
|---|--------|--------|-------|
| 1.29 | `auth/login` | `[ ]` | v2 rename of `authenticate` |
| 1.30 | `auth/logout` | `[ ]` | v2 rename of `logout` |
| 1.31 | `session/resume` | `[ ]` | **required** in v2; gains `replayFrom` |
| 1.32 | `session/list` | `[ ]` | **required** in v2 |
| 1.33 | `session/close` | `[ ]` | **required** in v2 |
| 1.34 | `session/new` | `[ ]` | v2: `mcpServers` optional; `modes` dropped |
| 1.35 | `session/prompt` | `[ ]` | v2: returns `messageId` insertion ack |
| — | `session/load` | `[n/a]` | **removed in v2** |
| — | `session/set_mode` | `[n/a]` | **removed in v2** (use config option `category:"mode"`) |

## 2. Agent → Client methods

| # | Method | Status | Notes |
|---|--------|--------|-------|
| 2.1 | `session/update` (notif) | `[x]` | emits a subset (§5) |
| 2.2 | `session/request_permission` | `[x]` | `agent.go:322` |
| 2.3 | `fs/read_text_file` | `[n/a]` | KajiCode has native file tools; dead const removed |
| 2.4 | `fs/write_text_file` | `[n/a]` | KajiCode has native file tools; dead const removed |
| 2.5 | `terminal/create` | `[n/a]` | KajiCode runs tools locally (`translate.go:221`) |
| 2.6 | `terminal/output` | `[n/a]` | same |
| 2.7 | `terminal/wait_for_exit` | `[n/a]` | same |
| 2.8 | `terminal/kill` | `[n/a]` | same |
| 2.9 | `terminal/release` | `[n/a]` | same |
| 2.10 | `elicitation/create` | `[x]` | wired to `ask_user` when the client advertises form elicitation |
| 2.11 | `elicitation/complete` (notif) | `[n/a]` | only needed for URL elicitation, which is unused |
| 2.12 | `mcp/connect` (v1 unstable) | `[ ]` | KajiCode owns MCP config → likely `[n/a]` |
| 2.13 | `mcp/disconnect` (v1 unstable) | `[ ]` | likely `[n/a]` |
| 2.14 | `mcp/message` (v1 unstable, both) | `[ ]` | likely `[n/a]` |

## 3. Scope rationale

- **v1 stable = target.** Needed for Zed/JetBrains/Neovim parity today.
- **v1 unstable = selective.** `session/fork` is genuinely useful (`Store.Fork`
  already exists). `document/*` is a large surface; only if editor doc-sync is
  wanted. `providers/*`, `nes/*`, `mcp/*` conflict with KajiCode's BYOK/local
  design → `[n/a]`.
- **v2 = separate future track.** Reorganizes capabilities, removes `session/load`
  and `session/set_mode`, drops all `fs/*`+`terminal/*`, changes update variants.
  Do not mix with v1 work.

## 4. `initialize` capabilities

### v1
| # | Field | Status | Notes |
|---|-------|--------|-------|
| 4.1 | `loadSession` | `[x]` | `agent.go:126` |
| 4.2 | `promptCapabilities.image` | `[x]` | `agent.go:127` |
| 4.3 | `promptCapabilities.audio` | `[n/a]` | not supported |
| 4.4 | `promptCapabilities.embeddedContext` | `[x]` | advertised; resource/resource_link prompts parsed |
| 4.5 | `mcpCapabilities.http` | `[ ]` | advertise only if implemented |
| 4.6 | `mcpCapabilities.sse` | `[n/a]` | deprecated |
| 4.7 | `sessionCapabilities.list` | `[x]` | advertised |
| 4.8 | `sessionCapabilities.resume` | `[x]` | advertised |
| 4.9 | `sessionCapabilities.close` | `[x]` | advertised |
| 4.10 | `sessionCapabilities.delete` | `[x]` | advertised |
| 4.11 | `sessionCapabilities.additionalDirectories` | `[x]` | advertised; honored + confined to the workspace |
| 4.12 | `auth.logout` | `[x]` | advertised when a logout handler is wired |
| 4.13 | `authMethods` | `[x]` | terminal login methods per OAuth provider, gated on `clientCapabilities.auth.terminal` |

### v1 unstable additions
| # | Field | Status |
|---|-------|--------|
| 4.14 | `sessionCapabilities.fork` | `[x]` | advertised |
| 4.15 | `providers` capability | `[x]` | advertised |
| 4.16 | `nes` capability | `[x]` | advertised with `events.document` (full sync) |
| 4.17 | `positionEncoding` | `[x]` | `utf-8` |

## 5. `session/update` variants

### v1 stable
| # | Variant | Status | Notes |
|---|---------|--------|-------|
| 5.1 | `agent_message_chunk` | `[x]` | `translate.go:192` |
| 5.2 | `agent_thought_chunk` | `[x]` | `translate.go:198` |
| 5.3 | `user_message_chunk` | `[x]` | load replay |
| 5.4 | `tool_call` | `[x]` | `translate.go:91` |
| 5.5 | `tool_call_update` | `[x]` | `translate.go:103` |
| 5.6 | `plan` | `[x]` | `translate.go:145` |
| 5.7 | `current_mode_update` | `[x]` | `translate.go:213` |
| 5.8 | `available_commands_update` | `[x]` | emitted on session start |
| 5.9 | `config_option_update` | `[x]` | emitted on set/start |
| 5.10 | `session_info_update` | `[x]` | emitted on `/retitle` (headless title via `sessions.RetitleSession`) |
| 5.11 | `usage_update` | `[x]` | wired to `agent.Options.OnUsage` |

### v1 unstable additions
| # | Variant | Status |
|---|---------|--------|
| 5.12 | `notice` | `[ ]` |
| 5.13 | `compaction_update` | `[ ]` |
| 5.14 | `compaction_summary_chunk` | `[ ]` |
| 5.15 | `plan_removed` | `[ ]` |

### v2 (future)
`user_message`, `agent_message`, `agent_thought` (whole-message upserts),
`state_update` (`running`/`idle`/`requires_action`), `tool_call_content_chunk`,
`terminal_update`, `terminal_output_chunk`; `plan`→`plan_update` (with `planId`).
`tool_call` and `current_mode_update` removed.

## 6. Protocol-level

| # | Method | Status | Notes |
|---|--------|--------|-------|
| 6.1 | `$/cancel_request` | `[n/a]` | `session/cancel` covers turn cancellation |

---

## 7. Ordered implementation plan

### Phase 1 — v1 correctness bugs (do first)

- [x] **1A. `session/load` history replay.** In `handleSessionLoad`
  (`internal/acp/agent.go:157`), after `registerSession`, emit each `turnRecord`
  from `loadHistory` as `user_message_chunk` then `agent_message_chunk` before
  returning. Add `userMessageChunk` in `translate.go`.
- [x] **1B. Advertise + return `configOptions`.** Build a `select`
  (`id:"model"`, `category:"model"`, `currentValue`, `options`) in
  `handleSessionNew` (`agent.go:151`) and `handleSetConfigOption`; emit
  `config_option_update`.
- [x] **1C. Emit `usage_update`.** Set `agent.Options.OnUsage` in `runTurn`
  (`agent.go:259`) → notifier sends `usage_update` from `Usage`.

### Phase 2 — v1 session lifecycle

- [x] **2A. `session/list`.** Handler → `Store.List()` (`internal/sessions/store.go:278`)
  filtered by `cwd`; return `SessionInfo{sessionId,cwd,title,updatedAt}`. Register
  in `NewAgent` (`agent.go:89`).
- [x] **2B. `session/close`.** Drop from `a.sessions` (detach, keep on disk).
- [x] **2C. `session/delete`.** Add `Store.Delete(sessionID)` (`internal/sessions/store.go`),
  remove session dir under the file lock; handler removes from map + calls it.
- [x] **2D. `session/resume`.** Like load but no replay (`agent.go`).

### Phase 3 — v1 capabilities + polish

- [x] **3A. `initialize` capability block.** Add `McpCapabilities`,
  `SessionCapabilities`, `auth` (`agent.go:120`); flag only what landed. Update
  stale comment (`agent.go:122-128`).
- [x] **3B. `available_commands_update`.** Static catalog
  (`internal/acp/commands.go`) emitted on session start. Do **not** couple to
  `internal/tui` (`tui/commands.go:10-60` is presentation).
- [x] **3C. `session_info_update`.** Emitted on `/retitle`, backed by the headless `sessions.RetitleSession` (`internal/sessions/title.go`); TUI and CLI share it.

### Phase 4 — v1 unstable (opt-in, per PR)

- [x] **4A. `session/fork`.** `handleSessionFork` reuses `Store.Fork`; gated by
  `sessionCapabilities.fork`.
- [ ] **4B. `document/*` notifications.** Only if editor doc-sync is wanted.
- [ ] **4C. `notice` + compaction updates.** Map KajiCode compaction
  (`internal/agent/compaction.go`) to `compaction_update`/`notice`.

### Phase 5 — v2 track (separate; do not mix)

- [ ] **5A. Design v2 capability/`info` reshuffle + `state_update` prompt lifecycle.**
- [ ] **5B. Implement v2 methods once the spec leaves `alpha`.**

### Deferred / n/a

- [ ] `authenticate`/`logout` — only if real credential management is exposed.
- [ ] `fs/*` — gate on `clientCaps.FS`; low value (native file tools exist).
- [x] `elicitation/create` — routes `ask_user` to the editor (capability-gated).
- [ ] `terminal/*`, `providers/*`, `nes/*`, `mcp/*` — out of scope by design.

---

## 8. Files

**Modify:** `internal/acp/agent.go`, `internal/acp/types.go`,
`internal/acp/translate.go`, `internal/sessions/store.go`.
**Create:** `internal/acp/commands.go` (Phase 3B).
**Tests:** `internal/acp/agent_test.go`, `internal/acp/translate_test.go`,
`internal/sessions/store_test.go`.

## 9. Testing per phase

- Unit: `go test ./internal/acp/... ./internal/sessions/...`.
- New tests: (a) load replay chunks; (b) `initialize` capabilities;
  (c) `session/new` config option; (d) `session/list` returns created session;
  (e) `session/delete` then load 404s; (f) `usage_update` payload.
- Live: run §0 harness; assert no `-32601` for methods marked `[x]`.
- Gates: `make fmt-check && go vet ./... && go test ./...`.

## 10. Cleanup

- Update stale comment `internal/acp/agent.go:122-128`.
- Resolve dead constants `MethodAuthenticate` (`types.go:12`),
  `MethodFSReadTextFile` (`types.go:21`), `MethodFSWriteTextFile` (`types.go:22`),
  `UpdateAvailableCommands` (`types.go:37`) — implement or remove.
- **Keep** `_kajicode/set_model` (`types.go:26`) — valid `_`-prefixed extension.

## 11. Risks

- **Unstable schemas drift.** v1 unstable / v2 are "may be removed or changed";
  pin to a schema version and gate behind capabilities.
- `session/load` replay chunk type: `user_message_chunk`/`agent_message_chunk`
  (v1-safe); verify against a real client.
- `session/close` (detach) vs `session/delete` (destroy) semantics.
- `Store.Delete` touches durable storage — hold the session file lock.
- Command catalog duplication with `internal/tui` risks drift.
- No live editor client (Zed/Neovim) exercised yet.

---

## 12. KajiCode feature → ACP exposure matrix

Goal: **every KajiCode feature reachable from the TUI/CLI should be reachable
from an ACP client.** ACP exposes features through exactly four mechanisms:

1. **`available_commands_update`** (§3.8) — the slash-command palette. ACP runs
   commands as ordinary prompt text (`/name args`), so the client sends
   `/effort high` as a `session/prompt`. Covers any command whose effect is
   "mutate session state, then answer with text".
2. **`configOptions`** (§3.9) + `session/set_config_option` — the native
   *picker/modal* surface. Best fit for enumerated knobs (model, mode, effort,
   profile, self-correct, style, turns). Uses `category` (`model`, `mode`,
   `model_config`, `thought_level`) and boolean toggles where the client
   advertises `session.configOptions.boolean`.
3. **Session lifecycle methods** — `session/list`/`resume`/`fork`/`delete`/`close`
   cover `/resume`, `/new`, `/retitle`, rewind, `/export`.
4. **Vendor `_kajicode/*` methods** — for anything with no clean native
   representation (provider management, MCP/plugin/skill config, diagnostics,
   threads, loop, web-search credentials).

### 12.1 TUI slash commands

Source: `internal/tui/commands.go:81-370`. "Mechanism" = simplest correct exposure.

| Command | Kind | Behaves as | Best ACP mechanism | Status |
|---------|------|-----------|--------------------|--------|
| `/model` | picker | mutate model | `configOptions` `category:model` | `[ ]` |
| `/provider` | modal | provider CRUD | vendor `_kajicode/provider/*` | `[ ]` |
| `/permissions` | picker | permission profile | `configOptions` `category:mode` | `[ ]` |
| `/effort` | picker | reasoning effort | `configOptions` `category:thought_level` | `[ ]` |
| `/profile` | text | exec profile | `configOptions` (custom) or command | `[ ]` |
| `/selfcorrect` | text | self-correct depth | command / `configOptions` | `[ ]` |
| `/turns` | text | turn budget | `configOptions` or command | `[ ]` |
| `/style` | modal | response style | command (`/style concise`) | `[ ]` |
| `/theme` | picker | TUI theme | client-owned (editor theme) | `[n/a]` |
| `/add-dir` | text | write roots | command; ties to `sessionCapabilities.additionalDirectories` | `[ ]` |
| `/compact` | text | compaction | command `/compact` (session-scoped) | `[x]` |
| `/init` | agent turn | write AGENTS.md | command `/init` | `[ ]` |
| `/resume` | picker | load session | native `session/list`+`session/load` | `[ ]` |
| `/new` | text | new session | native `session/new` (+`session/close`) | `[ ]` |
| `/retitle` | text | generate titles | session command + `session_info_update` | `[x]` |
| `/export` | text | export transcript | session command `/export` (prints) | `[x]` |
| `/thread` | modal | browse/revert | vendor `_kajicode/thread/*` (rewind exists) | `[ ]` |
| `/btw` | text | side conversation | vendor `_kajicode/session/btw` | `[ ]` |
| `/loop` | text | interval loop | vendor `_kajicode/loop/*` | `[ ]` |
| `/image` | text | attach image | native prompt `image` ContentBlock | `[x]` |
| `/search`, `/find` | text | search events | vendor `_kajicode/session/search` | `[ ]` |
| `/mcp`, `/mcp-status` | modal | MCP config | vendor `_kajicode/mcp/*` | `[ ]` |
| `/skills` | picker | list/run skills | command (skill name) + `available_commands_update` | `[ ]` |
| `/tools` | text | list tools | command `/tools` (text result) | `[ ]` |
| `/harness` | text | prompt addenda/rules | vendor `_kajicode/harness/*` | `[ ]` |
| `/prompt` | modal | prompt snippet | vendor `_kajicode/prompt/*` | `[ ]` |
| `/prompt-inspect` | text | prompt report | command (text result) | `[ ]` |
| `/web-search` | form | credentials | vendor `_kajicode/websearch/*` or `elicitation` | `[ ]` |
| `/doctor` | text | diagnostics | command (text result) | `[ ]` |
| `/config` | text | show config | command (text result) | `[ ]` |
| `/context` | text | context card | command (text result) | `[ ]` |
| `/ps`, `/stop` | text | bg terminals | vendor `_kajicode/terminal/*` | `[ ]` |
| `/sandbox-setup` | subprocess | sandbox setup | command | `[ ]` |
| `/help` | text | help | client renders `available_commands_update` | `[ ]` |
| `/clear`, `/transcript` | presentation | TUI view only | editor-owned UI | `[n/a]` |
| `/debug` | text | debug status | command (text result) | `[ ]` |
| `/retry` | text | resend prompt | client can resend | `[n/a]` |
| `/exit`, `/quit` | meta | quit | native `session/close` | `[ ]` |
| `!cmd` | bash escape | run shell | unsafe-only, local | `[n/a]` |

### 12.2 Runtime knobs → config options (native pickers)

Highest-value additions: ACP clients render these as real dropdowns/toggles.
All read from `config.PreferencesConfig` / `agent.Options`.

| Knob | Source | ACP option id | Category | Type |
|------|--------|---------------|----------|------|
| model | `ProviderProfile.Model` | `model` | `model` | select |
| permission mode/profile | `PreferencesConfig.PermissionProfile` (`types.go:132`) | `mode` | `mode` | select |
| reasoning effort | `agent.Options.ReasoningEffort` (`agent/types.go:329`) | `effort` | `thought_level` | select |
| exec profile | `internal/execprofile` | `profile` | `_kajicode_profile` | select |
| self-correct depth | `/selfcorrect` | `selfcorrect` | `_kajicode` | select |
| turns budget | `agent.Options.MaxTurns` (`types.go:294`) | `turns` | `_kajicode` | select |
| response style | `agent.Options.ResponseStyle` (`types.go:333`) | `style` | `_kajicode` | select |
| recaps | `PreferencesConfig.Recaps` | `recaps` | `_kajicode` | boolean |

### 12.3 CLI subcommands

CLI and TUI share backing packages, so once the ACP surface adds a vendor method
or command for a subsystem, both are covered.

| Group | Subcommands | ACP route |
|-------|-------------|-----------|
| Session | `sessions`, `search`, `context` | native lifecycle / `_kajicode/session/*` |
| Model | `models`, `providers`, `setup`, `auth` | `configOptions` + `_kajicode/provider/*` |
| Config | `config`, `harness`, `learning`, `prompt`, `sandbox` | commands / `_kajicode/*` |
| Extensions | `mcp`, `plugins`, `skills`, `hooks`, `tools`, `agents` | `_kajicode/*` |
| Workflows | `worktrees`, `verify`, `changes`, `cron`, `repo-map`, `repo-info`, `eval` | commands (text turns) |
| Ops | `doctor`, `usage`, `update`, `backends`, `daemon` | commands (text turns) |

### 12.4 Phase 6 — feature parity (after Phases 1-5)

- [x] **6A. `available_commands_update` catalog.** Shipped for the inspection command subset via `internal/cli/acp_commands.go`. Build the static list from the
  commands marked "command" in §12.1. Emit on `session/new` and after `/new`.
  The agent must recognize `/name` in `session/prompt` text and route it.
  (`internal/acp/commands.go`.)
- [x] **6B. `configOptions` set.** Shipped: model/mode/effort/turns/style. Advertise the §12.2 knobs as select/boolean
  options; accept them in `session/set_config_option`; emit `config_option_update`.
  Gate `type:boolean` on the client's `session.configOptions.boolean`.
- [~] **6C. Vendor `_kajicode/*` namespace.** Not needed yet: the current feature set maps onto native config options, session methods, and the command catalog. One small file
  (`internal/acp/vendor.go`) registering the non-native features (provider, MCP,
  skills, thread, loop, web-search, bg terminals, harness). Clients that don't
  know them ignore them per the `_` convention.
- [x] **6D. Command routing in `session/prompt`.** Shipped via `Deps.RunCommand`. Detect a leading `/name` in the
  prompt text, resolve against the catalog, and execute the same handler the TUI
  uses (extract shared handlers only where they are pure state mutations, never
  presentation).

### 12.5 Risks specific to feature parity

- **TUI-coupled handlers.** Many command handlers live in `internal/tui` and
  mutate Bubble Tea state. Only pure, surface-independent logic can be reused;
  presentation must not leak into `internal/acp`.
- **Command catalog drift** between `internal/tui/commands.go` and the ACP
  catalog. Consider a single shared catalog package if drift becomes real.
- **Client support varies.** `configOptions` booleans, elicitation, and unstable
  methods are capability-gated; always provide a working default.
- **Security:** never expose `unsafe` permission mode or `!cmd` over the wire
  (matches today's `handleSetMode` refusal, `agent.go:367`).
