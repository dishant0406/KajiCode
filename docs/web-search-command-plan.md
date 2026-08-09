# `/web-search` command — provider setup via TUI + shell profile

Status: Design complete (research-verified against the repo). Not yet implemented.
Goal: a `/web-search` slash command in the KajiCode TUI that opens a form to pick a
web-search provider (with known base URLs prefilled), paste an API key, then writes
`export KEY=…` into the user's shell rc (`.zshrc`/`.bashrc`) and sources it.

---

## 1. Problem / motivation

`web_search` (already implemented, see `docs/web-search-fix-plan.md`) supports
Exa / Tavily / SearXNG / snippet providers, but only reads keys from **process
environment variables** (`EXA_API_KEY`, `TAVILY_API_KEY`,
`KAJICODE_WEBSEARCH_BASE_URL`/`_API_KEY`) at tool-construction time. There is no
first-class UI to configure them: a user must hand-edit their shell profile, which
is error-prone and undocumented. This command turns that into a guided TUI form.

---

## 2. Verified seams (read-only research)

### 2.1 Slash-command registry — all in `internal/tui`, not `internal/cli`

- `commandKind` enum: `internal/tui/commands.go:7-59`. Add `commandWebSearch`.
- `commandDefinition` struct + registry slice: `commands.go:71-86`.
  Add one entry; `/help` auto-documents it (`commandHelpLinesForGroup`,
  `commands.go:474-518`).
- Parser/dispatch: `parseCommand` (`commands.go:412`), then
  `dispatchCommand` switch in `internal/tui/model.go:4474-4890`.
  Add `case commandWebSearch:`.

### 2.2 TUI form/widget primitives — the project builds custom overlays, not `bubbles/form`

Only `textinput` (composer) is vendored+used. The reliable template for
**a masked API-key modal** is `internal/tui/stt_key_prompt.go`:

- State struct: `sttKeyPromptState{provider, label, input, optional}` (`stt_key_prompt.go:20-26`).
- Mount: `openSTTKeyPrompt(...)` sets `m.sttKeyPrompt = &…` (`:49`).
- Modal key gate: `handleSTTKeyPromptKey` owns all keys — Enter saves, Esc
  cancels, Backspace/Ctrl-U/edit, printable chars accumulate into `input`
  (`:62-88`). **This is the exact controller to copy.**
- Overlay render: `sttKeyPromptOverlay(width)` — centered modal, masked key via
  `maskedProviderWizardKey(...)` (`provider_wizard.go:2032`), input line mirrors
  the provider-wizard credential step (`:1821-1840`).
- Submit: `submitSTTKey` trims + saves (`:91-109`).

The **single-select provider picker** is the generic `commandPicker`
(`internal/tui/picker.go:55-1041`), re-used by `/model`, `/effort`, `/theme`.
Add a `pickerWebSearch` kind (`picker.go:22-31`) and build items from the
providers.

### 2.3 Modal plumbing in `model.go` (3 places to add a field)

- Field on `model`: add `webSearch *webSearchFormState` near `sttKeyPrompt *sttKeyPromptState` (`model.go:205`).
- Key gate in `Update`'s modal chain: add a branch before `m.input.Update(msg)`
  (the chain is at `model.go:1930-1976`; picker swallows keys at `:1952-1963`).
  Insert `if m.webSearch != nil { return m.handleWebSearchKey(msg) }`.
- View overlay switch (`model.go:2794-2810`): compute
  `webSearchOverlay := m.webSearchFormOverlay(width)` and render it like
  `sttKeyOverlay`/`pickerOverlay`.
- Paste-preview path: `sttKeyPrompt` also gets a peek at `model.go:1303-1304`
  (paste into the key field). Reuse identical handling.

### 2.4 `dispatchCommand` wiring

- `case commandWebSearch:` in `model.go` (`:4490`+). Bare `/web-search` opens the
  form; `[status]` shows current env/config; `[remove]` offers removal. Guard on
  `m.pending` like `/model` does (`pickerBusyText`).

### 2.5 Where keys are read (the gap to close)

- `web_search` providers are built from `envToSearchEnv()` →
  `os.Getenv(EXA_API_KEY|TAVILY_API_KEY|KAJICODE_WEBSEARCH_BASE_URL|…)` at
  backend construction (`internal/tools/web_search_providers.go:342-401`).
- Base URLs are **hardcoded consts** in `web_search_providers.go`
  (`exaDefaultURL = "https://api.exa.ai/search"`,
  `tavilyDefaultURL = "https://api.tavily.com/search"`) — Exa/Tavily are NOT in
  `providercatalog`. So "already has the base url" = prefill the form from these
  consts (re-exported via a small helper). No catalog change needed.
- Credential store (`internal/config/credentials.go`): `ProviderKeyStore()` /
  `APIKeyGetter/Setter` (`:37-44`) and `SecureProviderProfile` (`:52`). **But the
  search pipeline does not consult the store today** — it reads env only. Two
  orthogonal options below.

### 2.6 Shell-profile write — greenfield (no existing Go code)

Repo has **no** code that reads `$SHELL`, resolves rc paths, or appends exports
(verified: no `zshrc|bashrc|.bash_profile|DetectShell` matches in `internal/**`;
`scripts/install.sh` only *prints* PATH advice, never edits a profile). This is
new functionality:
- Resolve `$SHELL` (basename) → `.zshrc` / `.bashrc` / `.profile`, falling back
  to `$HOME/.zshrc` when `$SHELL` is unset/unknown.
- Append a guarded, idempotent `export` block (avoid duplicate lines across runs).

---

## 3. Design

### 3.1 New command surface

- `/web-search` — opens the setup form (provider picker → base-URL prefilled →
  API-key masked → summary).
- `/web-search status` — text card: current env/config keys set, provider
  resolved, rc file path.
- `/web-search remove` — removes the provider's `export` line from the rc + env.

Group: `commandGroupTools`. Usage: `/web-search [status|remove]`.

### 3.2 Form flow (custom overlay + picker, matching existing patterns)

1. **Provider picker** (reuse `commandPicker`, new `pickerWebSearch` kind):
   Exa · Tavily · SearXNG · Custom/Snippet.
2. **Base URL step**: prefilled from the provider constant (Exa/Tavily) or
   `KAJICODE_WEBSEARCH_BASE_URL` (SearXNG). Editable text field (same layout as
   the provider-wizard endpoint step, `provider_wizard.go:1782-1789`). For
   SearXNG the user is expected to change it to their instance.
3. **API key step**: masked input (stt_key_prompt pattern). Required for
   Exa/Tavily; optional for SearXNG (keyless) — if blank, skip `export`.
4. **Summary**: shows provider, key env-var name, rc path, and the exact
   `export` line that will be appended.

### 3.3 Key env-var mapping (constant table in the TUI/form)

| Provider | Env var | API key required? | Base URL prefill |
|----------|---------|-------------------|------------------|
| Exa | `EXA_API_KEY` | yes | `https://api.exa.ai/search` |
| Tavily | `TAVILY_API_KEY` | yes | `https://api.tavily.com/search` |
| SearXNG | `KAJICODE_WEBSEARCH_BASE_URL` (+ optional `KAJICODE_WEBSEARCH_API_KEY`) | no | current env/blank |
| Custom | `KAJICODE_WEBSEARCH_BASE_URL` + `KAJICODE_WEBSEARCH_API_KEY` | no | current env/blank |

### 3.4 Shell-profile writer + `~/.kajicode` fallback (new `internal/tui/shellrc.go` + startup load)

Reusable, testable functions:
- `detectShellRC() (path string, err error)` — `$SHELL` basename → rc; zsh
  user-level = `~/.zshrc`; bash login-safe = prefer `~/.bashrc`; fallback
  `~/.profile`; unknown → `~/.zshrc` if `ZSH_VERSION`/`~/.oh-my-zsh` exists,
  else `~/.bashrc`.
- `writeEnvToRC(path, exports []string) (action string, err error)` —
  idempotent: if a `# >>> kajicode admin start >>>` block exists, replace it in
  place (not duplicate). Appends a guarded block:
  ```sh
  # >>> kajicode admin start >>>
  export EXA_API_KEY='…'
  # <<< kajicode admin end <<<
  ```
  File perms 0600 via `os.WriteFile` (contains secrets).
- `sourceRC(path)` — best-effort: if the session's parent shell is zsh/bash and
  it's an interactive login, run `source <rc>` via a background/notify; else
  show "restart your shell / run `source <path>`" guidance. (Non-blocking.)

**`~/.kajicode` fallback store (user decision):** In addition to the shell rc,
the command writes the same key to a KajiCode-owned file under the user config
dir (macOS: `~/.config/kajicode/`, derived from `config.UserConfigDir()` —
`internal/config/paths.go:49-61`). This file is the universal fallback: it is
loaded at process startup (see 3.7) so `EXA_API_KEY`/`TAVILY_API_KEY`/web-search
env vars are present regardless of the user's shell or whether their rc was
sourced. The rc file remains the mechanism for the user's *interactive* shell to
see the vars; `~/.kajicode` guarantees KajiCode itself sees them on next launch.

### 3.5 Making the key live in the *current* session (two independent fixes — decide in step 4)

Because `web_search` reads `os.Getenv` at tool construction, writing to rc has no
effect on an already-running KajiCode. Two reinforcements:

1. **Also write to `envToSearchEnv`'s source at runtime** — the cleanest is to
   have the TUI, on submit, additionally call `os.Setenv(key, value)`. That makes
   the **running** process pick it up for new `defaultSearchBackend()` calls.
   (Cheap, local, no API change.)
2. **Tag the tool's built backend** so it re-reads env on each `Run` instead of
   once at construction — a `SearchOptions`/provider seam so a mid-session env
   change is honored. More invasive; recommended as a Phase 2 hardening.

### 3.6 Security

- The key is written **plaintext** to a 0600 rc file AND a 0600 `~/.kajicode`
  env file (user-chosen fallback; NOT the encrypted `internal/config/credentials.go`
  keyring). This is a deliberate trade-off vs `SecureProviderProfile` — the web
  search tool reads env vars, so it needs plaintext access at runtime.
- Sandbox scrub already covers `EXA_API_KEY`, `TAVILY_API_KEY` (added earlier),
  so shell children can't leak them; the rc/store write is a TUI-side file write,
  not a sandboxed shell command.
- Never log/mask-mismatch: the summary preview masks the key; only the confirmed
  `export` line shows it.

### 3.7 Startup env load (new `internal/config/envfile.go` — the `~/.kajicode` load)

`config.Resolve`/`DefaultResolveOptions` (`internal/config/paths.go`, `internal/config/resolver.go`)
is the earliest process seam. Add a step that, on startup, reads a `kajicode.env`
file under the user config dir and calls `os.Setenv` for each `KEY=VALUE` it holds,
**only when that key isn't already set in the process env** (real env wins over
the stored fallback). This makes the `/web-search`-saved key available to
`web_search`/`code_search` on the next launch without requiring the user's shell
to have sourced anything. Skipped in tests that point config at a temp dir unless
they opt in.

---

## 4. Files to change

| File | Change |
|------|--------|
| `internal/tui/commands.go` | Add `commandWebSearch` const + definition entry |
| `internal/tui/model.go` | Add `webSearch *webSearchFormState` field; modal key gate branch; `case commandWebSearch:` in `dispatchCommand`; view overlay + paste path |
| `internal/tui/picker.go` | Add `pickerWebSearch` kind + `newWebSearchProviderPicker()` |
| `internal/tui/web_search_form.go` (new) | `webSearchFormState`, mount/handle/submit keys, overlay render (copy `stt_key_prompt.go`) |
| `internal/tui/web_search_status.go` (new) | `/web-search status` text builder |
| `internal/tui/shellrc.go` (new) | `detectShellRC`, `writeEnvToRC`, `sourceRC` |
| `internal/config/envfile.go` (new) | Startup load of `kajicode.env` from user config dir into `os.Setenv` (falls-back, never overrides live env); `WriteEnvFile` used by the TUI submit |
| `internal/config/paths.go` | (reuse `UserConfigDir`) no real change |
| `internal/config/resolver.go` / `DefaultResolveOptions` | Call env-file load at startup seam |
| `internal/tools/web_search_providers.go` | Export base URLs + env-var names for prefill (small `WebSearchProviderMetadata()` helper) |
| `internal/config/credentials.go` | (Decision-based) add web-search-key store accessor if we ALSO persist to credstore |
| `internal/sandbox/runner.go` | (Already done for EXA/TAVILY/PARALLEL) |
| tests | `web_search_form_test.go`, `shellrc_test.go`, `commands`/`picker` tests |

---

## 5. Tests to add

1. **Shell detection**: `$SHELL=/bin/zsh`→`~/.zshrc`; `/bin/bash`→`~/.bashrc`;
   fish/unknown fallbacks; unset `$SHELL`.
2. **RC write**: idempotent — second run replaces block, no duplicate exports;
   preserved surrounding lines; 0600 perms; round-trip read.
3. **Env-file store**: `WriteEnvFile` writes `kajicode.env` under user config
   dir 0600; startup load injects it; **live env beats stored fallback**; absent
   file is a no-op; config-in-temp-dir doesn't touch `$HOME`.
4. **Form controller**: provider selection → base-URL prefill → key masked →
   submit writes correct env var; Esc cancels; empty key on optional (SearXNG)
   skips export; invalid provider rejected.
4. **dispatchCommand**: `/web-search` opens form; `[status]` no panic when
   nothing configured; `[remove]` idempotent.
5. **Meta prefill**: exported metadata returns right base URLs + env names.
6. **mtimes/partial**: rc write with a pre-existing unrelated admin block
   (from a prior version) still updates cleanly.

---

## 6. Status — implemented (this plan is the design+as-built record)

All four phases are complete and green (build/vet/fmt pass; the two
`internal/tui` harness failures are pre-existing and unrelated — they need a live
`growwcorp` provider in the temp config).

- **Phase 1**: `internal/tools/web_search_providers.go` exports
  `WebSearchProvider` + `WebSearchProviders()` +
  `WebSearchProviderDefaultBaseURL`; `internal/config/envfile.go` (`LoadEnvFile` /
  `WriteEnvFile` / `RemoveEnvFileKeys`) + `loadStartupEnvFile` seam inside
  `config.DefaultResolveOptions`; `internal/tui/shellrc.go` (`detectShellRC` /
  `writeEnvToRC` / `sourceRC`). Unit tests for each (envfile, shellrc).
- **Phase 2**: `commandWebSearch` const + `/web-search` definition (grouped under
  Tools) + `case commandWebSearch:` dispatch + `/web-search status` text.
- **Phase 3**: provider picker step + base-URL/API-key entry overlay
  (`internal/tui/web_search_form.go`) + submit that writes the rc AND the env
  fallback AND `os.Setenv` (live in-session). Controller tests cover open, select,
  type, submit, required-key validation, Esc cancel, status, and remove.
- **Phase 4**: `/web-search remove` strips the rc block + env-file keys and
  un-sets the live env vars; docs updated (`docs/architecture.md`).
- **Validation**: `go build ./...`, `go vet ./internal/{tui,config,tools}`,
  focused + full `go test`, `gofmt`. Both write sites are 0600 and protected by
  guarded blocks, so user rc content is preserved.

## 7. Risks / mitigations

- **Plaintext secret in rc + `~/.kajicode` env file**: user-chosen; 0600 perms +
  guarded block + clear UI disclosure. Not the encrypted keyring, so the file is
  readable by any process running as the user — acceptable given web_search reads
  env vars.
- **Current-session staleness**: `os.Setenv` on submit makes it live immediately;
  Phase-4 env re-read seam hardens future launches.
- **Concurrent-writer/editor interference** (observed elsewhere in repo): re-read
  before each `edit_file`; small byte-exact patches; build after each file.
- **Shell-variant rc semantics**: fish/other shells not covered → clear fallback
  + error message, no silent failure (the `~/.kajicode` store makes the shell rc
  best-effort rather than load-bearing).
- **RC write failure** (read-only home, permissions): surface as notice, never
  crash; provide the line to paste manually; `~/.kajicode` fallback still works.

## 8. Not in scope

- Editing other apps' config or the provider catalog descriptions.
- Adding Exa/Tavily as "providers" in `/provider` (separate concern) — this
  command targets the web-search env surface only.
- Encrypted credential-store (`keyring`) persistence for the web-search key —
  user chose plaintext rc + `~/.kajicode` fallback over the keyring.
- A non-TUI/headless variant (out of scope for a slash command).
