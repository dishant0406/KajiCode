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
| Self-learning | `internal/config` (learning settings), `internal/agent/learning.go` (runtime gate), `internal/harness` (learn pipeline, recipes, apply/rollback), `internal/tools` (`_learning_apply`, `_learning_review`, `_learning_recipe`) | Perpetual-memory loop: turn/compaction-triggered reviews produce durable learned lessons, reviewed with diff + tests, applied on approval with rollback, and controlled by the `learning` CLI/`cmd_kajicode` settings. |
| Provider contract | `internal/kajicoderuntime` | Provider-neutral messages, tool calls, stream events, usage, images, and turn sessions. |
| Provider adapters | `internal/providers`, `internal/aimlapi`, provider catalog packages | API-specific translation for OpenAI, Azure OpenAI, Anthropic, Gemini, compatible gateways, OAuth/API key resolution, model discovery, provider health, and onboarding. |
| Tools | `internal/tools` | Tool interface, registry, built-in tools, redaction, output budgets, display metadata, and mutation tracking. |
| Sandbox/permissions | `internal/sandbox` | Path scope, network policy, command risk, grants, permission decisions, and platform isolation backends. |
| Sessions | `internal/sessions` | Local metadata, append-only event logs, replay, checkpoint, rewind, fork, and lineage. |
| Extensions | `internal/mcp`, `internal/plugins`, `internal/skills`, `internal/specialist`, `internal/swarm`, `internal/hooks` | External tools, plugin activation, skill discovery, sub-agents, teams, and lifecycle hooks. |
| Local control | `internal/localcontrol`, `internal/browser`, `internal/background`, `internal/daemon` | Optional browser, terminal, desktop, and daemon-backed helpers. |
| Release | `cmd/kajicode-release`, `internal/release`, `scripts/install.*`, `scripts/npm/*`, `.github/workflows/publish-npm.yml` | Binary archives, checksums, installers, npm wrapper/platform packages, tags, and GitHub releases. |

## Startup Flow

`cmd/kajicode/main.go` only delegates to `internal/cli.Run`. The CLI layer then:

1. Parses top-level commands and flags.
2. Resolves workspace and user config through `internal/config`.
3. Creates the provider from the active provider profile.
4. Builds the tool registry.
5. Loads specialists, MCP tools, plugins, skills, hooks, and user commands.
6. Creates sandbox and session stores.
7. Launches the requested surface: TUI, `exec`, ACP, setup, provider management,
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
- prompt, file, image, and stream-JSON input handling;
- session resume/fork/worktree setup;
- completion-gate semantics for automation;
- trace, spec, self-correct, and verification wiring.

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
6. Executes tools and appends tool results to the conversation.
7. Runs diagnostics, self-correction, retry, completion-gate, and guardrail logic.
8. Returns a final result or explicit stop/error reason.

Tool calls and tool results must stay provider-valid as paired conversation
messages. Any loop change that can affect message pairing needs regression tests.

## Self-Learning

KajiCode can learn durable, cross-session lessons about a project's conventions
and apply them automatically. Auto-learning is on by default and is tuned by a
`learning` config block (`enabled`, `turnInterval` >= 0 defaulting to 10,
`compact` on/off, `cooldownMs` defaulting to 20 minutes). The `learning` CLI
subcommand inspects and changes these settings.

The loop lives in several owned layers, each with its own tests:

- `internal/config/learning*.go` defines the settings, defaults, merge,
  validation, and the config-file writer used by the CLI.
- `internal/agent/learning.go` gates triggers: a review runs after N assistant
  turns (`turnInterval`) or after a context compaction when `compact` is on,
  subject to the cooldown. It bubbles reviews up and applies results through
  callbacks so the agent loop stays provider-neutral.
- `internal/harness` owns the learning pipeline/pipeline recipe types. A recipe
  standard can turn a run into a repeatable learn (prompts, tool budget,
  expectations). Pipeline executions may apply changes only after a diff and
  test verification, with file locks, state records, and rollback on failure.
- `internal/tools` exposes `_learning_apply`, `_learning_review`, and
  `_learning_recipe` tools that inspect/apply learned lessons and recipes.

Learned lessons are surfaced to the model as durable memory
(`<learned_memory>` prompt addendum) and treated as project/user conventions that
yield to any current, explicit instruction. Apply is safe-by-default: diff +
tests gate the change, and a failed apply rolls back to the pre-run state.

Recall mirrors compaction's budgeted-tail principle: entries carry a `lastUsedAt`
stamp (`harness.TouchEntry`) that the injected `learning.Context` view orders by
(freshest-first, capped per kind and by a whole-block token budget), and the plan
pass is anchored (`buildPlanPrompt`) to preserve the curated state by preferring
update-over-create, backstopped by a normalized-title dedup guard in `apply.go`.

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
- `todo_read` / `todo_write` — session-persisted todo list
  (`internal/tools/todo.go` + `internal/sessions/state.go`).
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

## Extensions

Extension loading happens before `agent.Run`:

- MCP servers add external tools through `internal/mcp`.
- Plugins add tool, hook, and skill roots through `internal/plugins`.
- Skills are prompt-loadable instructions discovered by `internal/skills`.
- Specialists and swarm members expose sub-agent tools through
  `internal/specialist` and `internal/swarm`.
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
