# KajiCode Architecture

This is the canonical architecture contract for KajiCode. Keep it current when
changing package ownership, startup flow, agent runtime behavior, sandbox policy,
session persistence, extension loading, or release packaging. For the longer
walkthrough, see [HOW_KAJICODE_WORKS.md](HOW_KAJICODE_WORKS.md).

## System Shape

KajiCode is a Go CLI application with three primary surfaces:

- `kajicode`: interactive Bubble Tea terminal UI.
- `kajicode exec`: headless runner for scripts, CI, stream-JSON, and automation.
- ACP/editor bridge: JSON-RPC style integration surface for editor clients.

All surfaces converge on the same runtime spine:

```text
cmd/kajicode
  -> internal/cli
    -> config/provider/tool/sandbox/session assembly
    -> internal/tui OR exec writer OR internal/acp
      -> internal/agent
        -> internal/kajicoderuntime provider interface
        -> internal/tools registry
        -> internal/sandbox policy and platform backend
        -> internal/hooks lifecycle dispatch
        -> internal/sessions event store
```

The model never edits the workspace directly. It emits provider-neutral tool
calls; KajiCode validates, gates, executes, records, and feeds results back into
the next model turn.

## Package Ownership

| Area | Owner | Responsibility |
| --- | --- | --- |
| Binary entrypoint | `cmd/kajicode` | Minimal main package calling `internal/cli.Run`. |
| CLI composition | `internal/cli` | Argument parsing, config resolution, provider creation, registry setup, prompt/harness inspection commands, sandbox/session/plugin/MCP wiring, and launch routing. |
| Interactive UI | `internal/tui` | Bubble Tea model/update/view state, transcript rendering, composer, modals, slash commands, setup, and runtime callbacks. |
| Agent loop | `internal/agent` | Prompt assembly, provider turns, tool execution, compaction, retries, completion policy, self-correction, and callback emission. |
| Self-learning | `internal/config` (learning settings), `internal/agent/learning.go` (event gate), `internal/harness` (store, pipeline, recipes, apply/rollback/decay), `internal/tools` (`learn`, `recall`, `recipe_run`) | Perpetual-memory loop: event-driven passes (failure→fix, user correction, compaction, `learn run`) capture durable lessons, routed by session/project scope, with conflict-checked apply, rollback, decay, and the `learning` CLI. |
| Model facts | `internal/modelsource` | models.dev snapshot (fetch/cache/resolve) plus an embedded seed for offline first-run. The single source of truth for a model's capabilities, modalities, reasoning tiers, context/output limits, and pricing. The curated catalog supplies identity and aliases; there is no model-name heuristic for vision or effort tiers. |
| Provider adapters | `internal/providers`, `internal/aimlapi`, provider catalog packages | API-specific translation for OpenAI, Azure OpenAI, Anthropic, Gemini, compatible gateways, OAuth/API key resolution, model discovery, provider health, and onboarding. |
| Tools | `internal/tools` | Tool interface, registry, built-in tools, redaction, output budgets, display metadata, and mutation tracking. |
| Sandbox/permissions | `internal/sandbox` | Path scope, network policy, command risk, grants, permission decisions, and platform isolation backends. |
| Sessions | `internal/sessions` | Local metadata, append-only event logs, replay, checkpoint, rewind, fork, and lineage. |
| Extensions | `internal/mcp`, `internal/plugins`, `internal/skills`, `internal/agents`, `internal/hooks` | External tools, plugin activation, skill discovery, sub-agents, and lifecycle hooks. |
| Local control | `internal/localcontrol`, `internal/browser`, `internal/background`, `internal/daemon` | Optional browser, terminal, desktop, and daemon-backed helpers. |
| Release | `cmd/kajicode-release`, `internal/release`, `scripts/install.*`, `scripts/npm/*`, `.github/workflows/publish-npm.yml` | Binary archives, checksums, installers, npm wrapper/platform packages, tags, and GitHub releases. |

## Startup Flow

`cmd/kajicode/main.go` only delegates to `internal/cli.Run`. The CLI layer then:

1. Parses top-level commands and flags.
2. Resolves workspace and user config through `internal/config`.
3. Creates the provider from the active provider profile.
4. Builds the tool registry.
5. Loads agents, MCP tools, plugins, skills, hooks, and user commands.
6. Creates sandbox and session stores.
7. Binds the resolved model to `internal/modelsource` (the models.dev snapshot)
   and refreshes that snapshot in the background when stale. `/model refresh`
   (and the picker's "Refresh models" row) re-runs this refresh in-session via
   `RefreshAndReload`, drops the once-guarded snapshot, rebuilds the synthesized
   registry entries, and re-binds — so updated model facts apply without a
   restart.
8. Launches the requested surface: TUI, `exec`, ACP, setup, provider management,
   release helper, daemon command, or another CLI subcommand.

Do not duplicate setup logic in a surface. Add composition behavior in
`internal/cli` and pass the resulting dependency into the surface.

## Interactive TUI Flow

`internal/tui` owns terminal presentation and input. It should not contain
provider-specific or sandbox-specific logic except for rendering state and
calling package-owned commands already assembled by the CLI.

The TUI flow is:

1. `tui.Run` validates TTY input and starts Bubble Tea.
2. `model.Update` handles keys, mouse, window changes, slash commands, and agent
   runtime messages.
3. Prompt submission starts an asynchronous command that calls `agent.Run`.
4. Agent callbacks send text, reasoning, tool calls, permission prompts, usage,
   and final results back into the Bubble Tea loop.
5. `model.View` renders transcript, composer, modals, sidebars, and status.

TUI features should be testable through update/view tests and should preserve
layout across width and height tiers.

## Headless Exec Flow

`kajicode exec` lives in `internal/cli/exec*.go`. It shares the same provider,
registry, sandbox, sessions, and agent loop as the TUI, but writes text, JSON, or
stream-JSON events instead of rendering Bubble Tea frames.

Exec owns:

- non-interactive argument parsing and exit codes;
- prompt, file, image, and stream-JSON input handling (every image surface —
  `/image`, clipboard, PDF pages, stream-json, and ACP client images — normalizes
  through `internal/imageinput` into the `images.*` provider-safe envelope;
  `internal/imageinput` is also the single loader for attached documents, routing
  a PDF by path/content and an SVG by its XML prologue);
- session resume/fork/worktree setup;
- completion-gate semantics for automation;
- trace, self-correct, and verification wiring.

Interactive-only assumptions must not leak into exec.

## Agent Loop

`internal/agent.Run` is the runtime authority for a model turn. Its loop:

1. Builds system/user prompt messages with guidelines, skills, images, runtime
   context, the provider/model harness profile, and configured harness addenda.
   `internal/agent/model_family.go`, `harness_profile.go`, and
   `harness_prompt_addenda.go` keep model-family prompt tuning out of provider
   adapters: each profile can adjust planning, tool-use, context, validation,
   final-response guidance, and compaction defaults while the common prompt stays
   provider-neutral.
2. Partitions visible tools from the registry, including deferred-tool exposure.
3. Plans context pressure from messages plus tool schemas, then prunes or
   compacts context when the request approaches the context window.
4. Streams provider output through `kajicoderuntime.Provider`.
5. Decodes tool calls and applies filters, harness permission rules, permission
   mode, sandbox evaluation, and hooks.
6. Executes tools and appends tool results to the conversation. A tool may return
   image bytes for the model to see (`read_file` on a raster image); the loop
   collects them across the batch and injects them as one synthetic user turn
   after it, because providers accept image parts only on a user role. A
   text-only model instead gets a notice from the tool, gated by
   `modelregistry.SupportsVision`.
7. Runs diagnostics, self-correction, retry, completion-gate, and guardrail logic.
8. Returns a final result or explicit stop/error reason.

Tool calls and tool results must stay provider-valid as paired conversation
messages. Any loop change that can affect message pairing needs regression tests.

### Wire Protocol Routing

A provider can serve its models on several wire protocols from one base URL
(OpenCode Zen serves `union-alpha` on the Anthropic Messages API and `deepseek-*`
on chat-completions). KajiCode routes each model to its real endpoint
automatically from the models.dev catalog, not from per-model config:

- `internal/modelsource` decodes the catalog's per-model `provider.npm`
  (`@ai-sdk/anthropic` → Messages, `@ai-sdk/openai` → Responses,
  `@ai-sdk/openai-compatible` → chat-completions) into `Record.NPM`, and exposes
  `ProviderRecord` — a strict, provider-scoped exact lookup so a custom or
  unlisted provider never inherits another provider's protocol.
- `internal/providers` (the `providers.New` switch) builds the matching adapter:
  `anthropic.New` for `/messages` (with `messagesBaseURL` trimming a trailing
  `/v1` so the path is not doubled), `openai.NewResponsesProvider` for
  `/responses`, and `openai.New` otherwise. Only the OpenAI chat-completions
  family is rerouted; Anthropic/Google/Azure profiles keep their kind's protocol.
- The embedded offline seed (`internal/modelsource/modelsdev_seed.json.gz`) carries
  the same `provider.npm` overrides, so a first run with no cache and no network
  still routes correctly.

### Model Discovery

KajiCode discovers a provider's available models through
`internal/providermodeldiscovery`, which merges the curated
`models.dev/api.json` catalog (`internal/providermodelcatalog`) with a live
`/v1/models` probe (and `/v1beta/models` for Gemini providers). Live responses
are paginated (cursor-based for OpenAI/Anthropic, page-token based otherwise,
capped at 20 pages and 4 MiB) and deduplicated by model id. Gemini discovery
uses `x-goog-api-key` auth and parses `name`/`displayName`/`inputTokenLimit`.

By default the merged list is filtered to "coding models" via
`IsCodingModel`/`LooksLikeCodingModelID`. Users who want every model a provider
serves can opt out in two ways:

- Set `"showAllModels": true` on a provider profile in `kajicode.json` (field
  `ProviderProfile.ShowAllModels`); this threads through `internal/config`
  (upstreams from catalog fallbacks) into the TUI picker, onboarding wizard,
  and `exec` model resolution via `ShowAllModelsEnabled`.
- Run `kajicode providers models` (live, unfiltered probe) or pass `--all` to it
  for one-off raw inspection.
- In the interactive TUI, `/model` opens a picker with a pinned **Refresh
  models** row at the top; choosing it (or running `/model refresh`) re-fetches
  every saved provider's FULL live model list in one shot — forcing ShowAll
  discovery across all providers and dropping the cached per-provider lists
  first, so nothing is left over and no curated filter hides the full set. A
  fetching overlay stays up until every provider resolves.

## Self-Learning

KajiCode learns durable, evidence-backed lessons about a project and applies them
automatically. Auto-learning is on by default and is tuned by the `learning`
config block (`enabled`, `debounceMs` default 30 seconds, `compact`,
`pruneAfterDays` default 90, `maxEntries` default 200). The `kajicode learning`
subcommand inspects and changes these settings and reverts the last pass.

Learning is **event-driven**. The gate (`internal/agent/learning.go`) runs a pass
when the agent hits a real signal — a tool that failed then was fixed, a user
correction, a compaction (when enabled) — or on an explicit `learn run`, subject
to a debounce. A clean run with no signal does no provider work.

The layers, each with its own tests:

- `internal/config/learning*.go` defines the settings, defaults, merge,
  validation, and the config-file writer used by the CLI.
- `internal/agent/learning.go` is the engine: it captures signals from tool
  results and user turns, gates on the debounce, routes proposals by scope, and
  splices a refreshed `<learned_memory>` block into the next request so a
  mid-session lesson takes effect immediately.
- `internal/harness` owns the durable store and pipeline: the review gate, the
  plan pass (anchored so it prefers update-over-create), the guarded apply
  critical section with version-conflict detection, refinement history with a
  self-contained rollback record, decay/cap (`PruneStale`), and Go-native recipe
  manifests that `recipe_run` executes through the tool registry.
- `internal/tools` exposes the `learn` (status/run/CRUD) and `recall` (search)
  tools plus `recipe_run`.

Scopes are `session`, `project` (default; `<workspace>/.kajicode/learning`), and
`global`; legacy per-session stores recorded as `local` are read back as
`session`. Recall is recall-ordered by `lastUsedAt`/`reinforcements` and bounded
per kind and by a whole-block token budget, so a growing store can never blow the
context window.

## Tools, Sandbox, And Hooks

Tools are registered by name in `internal/tools.Registry`. Each tool needs clear
safety metadata, output limits, redaction behavior, and display metadata when it
is shown in the TUI.

Sandbox decisions are centralized in `internal/sandbox`. The sandbox evaluates
path scope, network access, shell command risk, explicit escalation, persistent
or session grants, and platform backend availability.

Hooks in `internal/hooks` run around tool lifecycle events. Hooks may annotate or
block execution, but they should not bypass the sandbox or mutate unrelated
runtime state.

### Tool catalog

The core tools are registered from `internal/tools` and assembled in
`internal/cli`. In addition to the foundational read/write, search, and command
tools, the catalog now includes:

- `multi_edit` — targeted string replacements synced in a fuzzy engine
  (`internal/tools/multi_edit.go`), in `tools.CoreTools`.
- `ls` — recursive ignore-aware directory tree (`internal/tools/ls.go`).
- `code_search` — web-search-backed code search (`internal/tools/code_search.go`).
- `todo_read` / `todo_write` — the canonical task-planning surface: a
  session-persisted todo list (`internal/tools/todo.go` +
  `internal/sessions/state.go`). `todo_write` also mirrors the list in memory
  (`CurrentTodos`) so the TUI plan panel, ACP plan updates, and the agent
  guardrails/completion gate all read one source of truth. The former
  `update_plan` tool was removed in favor of this tool.
- `batch` — explicit batcher that fans out up to 10 sub-calls to
  `Registry.RunWithOptions`, parallelizing only ReadOnly + ThreadSafe + permitted
  calls with non-conflicting resource keys and serializing the rest
  (`internal/tools/batch.go`). Wired via `registerBatchTool` in `internal/cli`
  (exec.go and app.go), gated by operator tool filters, and excluded from the
  core list so it never appears in the agent's eager schema.
- `lsp_navigate` — full operation enum (definition, references, implementations,
  workspace symbol, hover, document symbol, call hierarchy).
- `web_search` — hosted web search with optional full page content
  (`internal/tools/web_search.go`, `web_search_providers.go`). Fans out to a
  provider: **Exa** (`EXA_API_KEY`), **Tavily** (`TAVILY_API_KEY`), a
  self-hosted **SearXNG**/generic backend (`KAJICODE_WEBSEARCH_BASE_URL`), or a
  failover chain of all configured providers. `KAJICODE_WEBSEARCH_PROVIDER`
  (`auto|exa|tavily|searxng|snippet`) forces one. `web_search` is always visible
  and returns a one-line setup hint when nothing is configured; `code_search`
  stays gated on a real backend. Both scrub `EXA_API_KEY`, `TAVILY_API_KEY`,
  `PARALLEL_API_KEY`, and `KAJICODE_WEBSEARCH_API_KEY` in the sandbox.
- `/web-search` — TUI command (`internal/tui/web_search_form.go`) to configure
  web-search credentials without hand-editing shell profiles: picks a provider,
  edits the base URL, enters a masked API key, then persists `export KEY=…` lines
  into a guarded block of the user's shell rc (`internal/tui/shellrc.go`,
  detected from `$SHELL`) and a fallback env file under the config dir
  (`internal/config/envfile.go`, loaded at startup via `os.Setenv` when the live
  env var is unset). `/web-search status` reports what's set; `/web-search remove`
  clears both write sites. Provider list comes from `tools.WebSearchProviders()`
  in `web_search_providers.go`.

The `batch` tool is deliberately not part of `CoreTools()`/`knownToolNames`
because it needs a live `*tools.Registry` at construction and is only available
through the run paths that wire it.

## Persistence

KajiCode persists local session state through `internal/sessions`:

- `metadata.json` stores identity, title, cwd, provider/model, lineage, spec and
  sub-agent metadata, timestamps, and event counts.
- `events.jsonl` stores append-only messages, tool calls/results, permissions,
  usage, checkpoints, rewind/fork metadata, compaction, and specs.

Resume, fork, rewind, sub-agent history, and stream replay should be implemented
from session metadata/events rather than hidden TUI-only state.

### Response style

The operator's freeform speaking style is a first-class, globally-persisted
setting that applies to every reply across sessions and projects:

- **Storage.** `/style` writes a `RESPONSE_STYLE.md` file under the per-user
  config dir (`config.UserConfigDir()/kajicode/`), the same directory that holds
  the per-user `config.json` and `KAJICODE.md`. The file is the source of truth
  for the global style and is readable (case-insensitively) on macOS/Windows.
- **Injection.** `internal/agent` reads that file at run start via
  `responseStyleContext` (in `system_prompt.go`) and injects it verbatim into the
  `promptSectionResponseStyle` section of the system prompt every run. A
  missing/empty file adds nothing, so the prompt stays byte-identical and the
  existing `Options.ResponseStyle` enum (balanced/concise/explanatory/review)
  still applies as a session-only fallback.
- **TUI surface.** `/style` with no argument opens a single-step modal editor
  (the same freeform UX as the `/prompt` editor, minus the slug step) seeded
  from the persisted file; `/style show` lists the current persisted style and
  its on-disk location; `/style clear` removes it; a valid enum argument still
  sets the ephemeral per-session toggle. The editor saves atomically
  (`internal/fsutil` rename) with `0o600` permissions and caps the injected body
  at 4 KiB to bound prompt size.

## Extensions

Extension loading happens before `agent.Run`:

- MCP servers add external tools through `internal/mcp`. Remote servers that
  use OAuth are authenticated with an MCP-spec discovery chain: the server's
  `401 WWW-Authenticate` challenge names its RFC 9728 protected-resource
  metadata, that document names the authorization server, and the
  authorization server's RFC 8414 metadata (with an OIDC
  openid-configuration fallback) supplies the endpoints and scopes. The
  protected-resource document's `scopes_supported` supplies scopes when the user
  configured none, and its `resource` (or the server URL) is sent as the RFC 8707
  resource indicator. Discovery lives in `internal/oauth`; MCP orchestration lives
  in `internal/mcp`. All discovered URLs pass the https/loopback-or-public SSRF
  rule before any request is made, and a server that needs login is reported as
  needs-auth rather than a generic failure. A server whose metadata advertises no
  dynamic registration and that has no configured `clientID` is reported as
  needs-client-registration (a distinct, non-login-fixable state).
- A remote server configured as `http` retries over SSE when the Streamable HTTP
  attempt fails, but an auth failure is surfaced rather than retried. Per-server
  timeouts bound each tool call, and a `notifications/progress` message resets the
  deadline. A server's `initialize` instructions are injected into the system
  prompt, `notifications/tools/list_changed` triggers a live re-list, and
  capability-gated resource (`list_mcp_resources`/`list_mcp_resource_templates`/
  `read_mcp_resource`) and prompt (`list_mcp_prompts`/`get_mcp_prompt`) tools are
  registered only while a matching server is connected.
- Plugins add tool, hook, and skill roots through `internal/plugins`.
- Skills are prompt-loadable instructions discovered by `internal/skills`.
  Discovery is cwd/project-scoped: the agent walks from the git root to the
  touched working directory (`skills.ProjectSkillRoots`) for `.skills` and
  `.agents/skills` roots, surfaces a boot `<available_skills>` catalog in the
  system prompt, and `internal/agent` reconciles that catalog dynamically
  (`guidelineTracker.drainSkillsCatalog`) as the run moves — emitting added /
  no-longer-available deltas instead of a full re-render. Skills may declare
  frontmatter `when_to_use:` (path globs) to auto-coach on path match
  (`guidelineTracker.ObservePath` → auto-load queue), `scope:`, and
  `permission:` (allow|prompt|deny), enforced by the `skill` tool
  (`tools.ArgsPermissioner`) and surfaced as `[prompt]`/`[deny]` catalog
  markers. A built-in lowest-precedence `customize-kajicode` skill is always
  discoverable and auto-loads when editing KajiCode's own internals. The `skill`
  tool resolves loadable skills across the boot roots plus the run's discovered
  project roots.
- Agents expose sub-agent tools through `internal/agents`. A Task call runs the
  child in-process on the parent's registry, provider, sandbox, and permission
  mode, with the child's registry filtered to the tools its own ruleset allows
  (so children can use the same MCP/plugin/skill tools the parent has, and can
  never reach one the parent lacks). Task is capability-classified by its target
  agent's effect (`TaskTool.CapabilitiesForArgs`), so several fresh delegations
  to read-only agents in one turn run concurrently while write-capable ones stay
  sequential. Finished background tasks are pushed back into the parent run as
  `<task_result>` nudges instead of being polled; the definition supports
  mode/hidden/aliases/model/thinking/temperature/topP/steps/disable; and the
  nesting cap is configurable via `agents.depth`.
- User commands are file-backed commands surfaced by CLI/TUI command layers.

New extension types should attach through the existing registry/prompt/hook
surfaces instead of adding special cases to the agent loop or TUI.

## Release And npm Packaging

The release path is deliberately separate from runtime behavior:

1. `cmd/kajicode-release build` builds the main binary.
2. `cmd/kajicode-release smoke` verifies the local binary contract.
3. `cmd/kajicode-release package` creates platform archives and checksums.
4. `scripts/npm/build-platform-packages.mjs` assembles the wrapper and platform
   npm packages from release archives.
5. `.github/workflows/publish-npm.yml` validates, packages every platform,
   creates the GitHub release, verifies public assets, publishes platform
   packages, and publishes the wrapper package.

Runtime changes should not depend on release-only files. Release changes must
validate source build, archive content, checksum verification, npm wrapper
fallback behavior, and install scripts.
