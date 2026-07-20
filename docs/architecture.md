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
| CLI composition | `internal/cli` | Argument parsing, config resolution, provider creation, registry setup, sandbox/session/plugin/MCP wiring, and launch routing. |
| Interactive UI | `internal/tui` | Bubble Tea model/update/view state, transcript rendering, composer, modals, slash commands, setup, and runtime callbacks. |
| Agent loop | `internal/agent` | Prompt assembly, provider turns, tool execution, compaction, retries, completion policy, self-correction, and callback emission. |
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

1. Builds system/user prompt messages with guidelines, skills, images, and
   runtime context.
2. Partitions visible tools from the registry.
3. Compacts context when the request approaches the context window.
4. Streams provider output through `kajicoderuntime.Provider`.
5. Decodes tool calls and applies filters, permission mode, sandbox evaluation,
   and hooks.
6. Executes tools and appends tool results to the conversation.
7. Runs diagnostics, self-correction, retry, completion-gate, and guardrail logic.
8. Returns a final result or explicit stop/error reason.

Tool calls and tool results must stay provider-valid as paired conversation
messages. Any loop change that can affect message pairing needs regression tests.

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
