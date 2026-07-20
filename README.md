<p align="center">
  <img src="docs/assets/kajicode-logo.png" alt="KajiCode" width="385">
</p>

<p align="center"><strong>A terminal coding agent you own.</strong></p>

<p align="center">
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-MIT-blue"></a>
  <img alt="Go 1.26.5+" src="https://img.shields.io/badge/Go-1.26.5+-00ADD8?logo=go&logoColor=white">
  <img alt="25+ providers" src="https://img.shields.io/badge/providers-25+-34E2EA">
  <br>
  <strong>English</strong> | <a href="README_ZH.md">中文</a>
</p>

KajiCode is an AI coding agent for your local terminal. It can inspect a repository,
edit files, run commands, use browser/terminal helpers, and keep durable local
sessions while you choose the model and the permission level.

```bash
kajicode
kajicode exec "fix the failing test in ./pkg"
kajicode exec --output-format stream-json < turns.jsonl
```

## Why KajiCode

- **Use the model you want.** Bring OpenAI, Anthropic, Gemini, Groq, OpenRouter,
  DeepSeek, Mistral, xAI, Qwen, Kimi, GitHub Models, Ollama, LM Studio, or any
  OpenAI-/Anthropic-compatible endpoint.
- **Stay in control.** File writes, shell commands, network access, and
  out-of-workspace writes go through KajiCode's permission and sandbox policy.
- **Works in the terminal.** The TUI has model/provider pickers, image input,
  slash commands, live plan/tool rendering, scrollback, themes, and resume/fork
  support.
- **Works without the TUI.** `kajicode exec` is scriptable, supports text/JSON/
  stream-JSON I/O, isolated worktrees, spec-first runs, and meaningful exit
  codes for CI.
- **Keeps context local.** Sessions are stored on disk, searchable, resumable,
  and never uploaded as telemetry by KajiCode.
- **Extensible when you need it.** Use MCP servers, skills, plugins, hooks, and
  specialist subagents from the same CLI.

## Install

### npm

```bash
npm install -g @dishant0406/kajicode
kajicode
```

The npm package is a small wrapper whose platform build (Linux and macOS on
x64/arm64, Windows on x64 — including the browser/terminal control helpers)
installs as an optional dependency straight from the npm registry — no install
scripts, no downloads outside npm. Bun, pnpm, and yarn work the same way with
no trust or approval steps. Installs that skip optional dependencies
(`--omit=optional`) still work: the wrapper fetches the binary from the
matching GitHub Release whenever it is missing. Windows on ARM runs the x64
build under emulation. See [docs/NPM_PACKAGING.md](docs/NPM_PACKAGING.md) for
how the package is put together.

### Install scripts

Linux/macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/dishant0406/KajiCode/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/dishant0406/KajiCode/main/scripts/install.ps1 | iex
```

### From source

Source builds require Go 1.26.5+.

```bash
git clone https://github.com/dishant0406/KajiCode.git
cd KajiCode
go run ./cmd/kajicode
```

Release installers and the npm wrapper require published GitHub Release assets.
If you are testing before the first public release, build from source:

```bash
go build -o kajicode ./cmd/kajicode
```

On Linux, build the sandbox helper too if you want native sandboxing:

```bash
go build -o kajicode-linux-sandbox ./cmd/kajicode-linux-sandbox
go build -o kajicode-seccomp ./cmd/kajicode-seccomp   # optional compatibility wrapper
```

Put `kajicode` and `kajicode-linux-sandbox` in the same directory on `PATH`
(`~/.local/bin` is a good default). macOS does not need an extra helper binary.
Windows source builds can use the main `kajicode.exe` as their sandbox helper; release
archives still ship standalone Windows helper executables.

More install details: [docs/INSTALL.md](docs/INSTALL.md).

## First Run

Start the TUI:

```bash
kajicode
```

The setup wizard helps you pick a provider and model. You can also configure
providers from the command line:

```bash
kajicode setup
kajicode providers list
kajicode models list
kajicode doctor
```

For API providers, set the matching environment variable before setup or enter
the key in the wizard:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=...
export GEMINI_API_KEY=...
export AIMLAPI_API_KEY=...
export LONGCAT_API_KEY=...
export MINIMAX_API_KEY=...
export MINIMAXI_API_KEY=...
```

To configure AI/ML API directly, run:

```bash
kajicode providers setup aimlapi --set-active
```

To configure Meituan LongCat (LongCat-2.0) directly, run:

```bash
kajicode providers setup longcat --set-active
```

MiniMax presets use the Anthropic-compatible endpoints for the global and China
regions:

```bash
kajicode providers add minimax --set-active
kajicode providers add minimaxi-cn --set-active
```

To use the OpenAI-compatible endpoints instead, add a custom compatible profile
for the required region:

```bash
kajicode providers add custom-openai-compatible \
  --name minimax-openai \
  --model MiniMax-M3 \
  --base-url https://api.minimax.io/v1 \
  --api-key-env MINIMAX_API_KEY \
  --set-active

kajicode providers add custom-openai-compatible \
  --name minimax-cn-openai \
  --model MiniMax-M3 \
  --base-url https://api.minimaxi.com/v1 \
  --api-key-env MINIMAXI_API_KEY \
  --set-active
```

For local models, run Ollama or LM Studio and then use `kajicode setup` or
`kajicode providers detect`.

## Daily Use

### Interactive TUI

```bash
kajicode
```

Useful controls:

| Control | Action |
|---|---|
| `Enter` | send the prompt |
| `/` | open slash-command suggestions |
| `Ctrl+X` then letter | common slash commands (e.g. `m` → `/model`; `Ctrl+X` `?` for full list) |
| `Ctrl+P` / `Ctrl+N` | previous / next item in menus (arrows still work) |
| `Shift+Tab` | cycle permission mode |
| `Ctrl+B` | show/hide the sidebar |
| `Ctrl+C` | cancel, exit, or return from a `/btw` conversation |

Common slash commands:

| Command | Purpose |
|---|---|
| `/model`, `/provider` | switch the active model/provider |
| `/spec`, `/plan` | draft and review a plan before building |
| `/image` | attach an image for vision-capable models |
| `/resume`, `/rewind` | continue or roll back local sessions |
| `/btw [question]` | ask in an isolated fork without adding the side conversation to the main session |
| `/loop` | repeat a prompt or custom `/command` on an interval (`/loop 5m /babysit-prs`) or self-paced |
| `/compact`, `/context` | manage context usage |
| `/permissions`, `/tools` | inspect available tools and policy |
| `/add-dir` | allow an extra write directory for this session |
| `/theme`, `/doctor`, `/config` | adjust appearance and inspect setup |

### Headless `exec`

```bash
kajicode exec "explain internal/agent/loop.go"
kajicode exec --model claude-sonnet-4.5 "refactor the config loader"
kajicode exec --use-spec "add rate limiting to the API client"
kajicode exec --worktree "try the migration in an isolated worktree"
kajicode exec --resume
kajicode exec --fork <session-id> "try the other approach"
```

Programmatic use:

```bash
kajicode exec --input-format stream-json --output-format stream-json < turns.jsonl
```

The stream-JSON contract is documented in
[docs/STREAM_JSON_PROTOCOL.md](docs/STREAM_JSON_PROTOCOL.md).

## Safety Model

KajiCode is designed to make side effects visible.

- Workspace reads are allowed by default.
- File writes are limited to the workspace unless you grant another directory.
- Shell commands, network access, destructive commands, and elevated actions are
  permission-gated.
- `--add-dir <path>` and `/add-dir <path>` grant additional write roots without
  giving the agent the whole filesystem.
- Unsafe/autonomous modes are explicit opt-ins.
- Secrets are redacted from tool output and logs where KajiCode controls the surface.

Example:

```bash
kajicode --add-dir ../docs-site
kajicode exec --add-dir ../shared "update both repos"
```

Sandbox behavior can be inspected with:

```bash
kajicode sandbox policy
kajicode sandbox grants list
```

## Web And Local Control

KajiCode includes local file/search/edit/shell tools, `web_fetch` for public URLs,
and MCP support for additional tools.

For local dev servers, use shell commands such as `curl` through `exec_command`
so the normal sandbox and permission policy applies. Long-running commands stay
attached to a background terminal session and can be listed or stopped from the
TUI.

The npm package also includes browser and terminal helper packages used by local
browser/terminal tools. Source builds can use the same helpers when they are on
`PATH` or configured in KajiCode's local-control settings.

## Common Commands

```text
kajicode                  interactive TUI
kajicode exec             one-shot or scripted agent run
kajicode setup            first-run provider setup
kajicode auth             OAuth/login helpers for supported providers
kajicode models           model registry and capabilities
kajicode providers        provider profiles and detection
kajicode doctor           setup, key, and connectivity checks
kajicode context          context-budget report
kajicode repo-map         deterministic repository map
kajicode repo-info        local repository summary
kajicode search | find    search local session history
kajicode sessions         inspect, resume, fork, and rewind sessions
kajicode spec             manage spec-mode drafts
kajicode specialist       manage specialist subagents
kajicode skills           manage markdown instruction skills
kajicode plugins          manage plugins
kajicode hooks            manage lifecycle hooks
kajicode mcp              manage MCP servers and tools
kajicode serve --mcp      expose KajiCode tools over MCP stdio
kajicode sandbox          inspect sandbox policy and grants
kajicode worktrees        prepare isolated git worktrees
kajicode verify           detect and run local verification checks
kajicode changes          inspect and commit local git changes
kajicode usage            token usage and estimated cost
kajicode cron             scheduled agent jobs
kajicode update           check for newer releases
```

## Extending KajiCode

### Project and personal instructions

KajiCode appends project-specific guidance to the system prompt from the first
`AGENTS.md`, `KAJICODE.md`, or `.kajicode/AGENTS.md` file found in each directory from
the git root down to your current working directory (checked in that order
per directory). Files are injected general-to-specific, capped at 8 KiB per
file and 32 KiB total.

A personal `KAJICODE.md` under `config.UserConfigDir()/kajicode/KAJICODE.md`
(`$XDG_CONFIG_HOME/kajicode/KAJICODE.md` or `~/.config/kajicode/KAJICODE.md` on Linux/macOS,
`%AppData%\Roaming\kajicode\KAJICODE.md` on Windows) applies across every workspace, ahead of any project guidelines.

### Plugins

Plugins are discovered from `~/.config/kajicode/plugins/<name>/plugin.json` (user
scope — `$XDG_CONFIG_HOME` or `~/.config` on every OS, independent of the
`config.UserConfigDir()` path used above) and `<cwd>/.kajicode/plugins/<name>/plugin.json`
(project scope — resolved from the current working directory, not the repo
root), and managed with `kajicode plugins`. A manifest can declare:

- `tools` — custom tools (`command`, `args`, `inputSchema`, and a
  `permission` of `prompt` or `deny`; `allow` is honored only when manifest tool
  auto-approval is enabled)
- `hooks` — commands run on `beforeTool`, `afterTool`, `sessionStart`, or
  `sessionEnd`
- `prompts` and `skills` — additional prompt/skill files

MCP servers (`kajicode mcp`) and standalone markdown skills (`kajicode skills`) use
the same extension points and can also be wired up outside of a plugin
manifest.

## Appearance And Accessibility

| Control | Effect |
|---|---|
| `NO_COLOR=<anything>` | disables color output |
| `KAJICODE_THEME=<name>` | selects the startup theme (`auto`, `dark`, `light`, or a color theme like `dracula`, `nord`, `gruvbox`, `tokyo-night`, `catppuccin`, `one-dark`, `solarized-dark`, `rose-pine`, `everforest`, `neon`, `solarized-light`, `dune`) |
| `--theme <name>` | selects the TUI theme from the CLI (same names) |
| `/theme` | opens the theme picker inside the TUI (live preview; `/theme <name>` switches directly) |
| `KAJICODE_NO_FADE=1` | disables streaming fade animation |

Meaning does not rely on color alone; diffs, permissions, and statuses also use
text or glyph markers.

## Development

```bash
go test ./...
go run ./cmd/kajicode-release build
go run ./cmd/kajicode-release smoke
go run ./cmd/kajicode-perf-bench
```

Experimental: `KAJICODE_OPENAI_TURN_SESSION=1` enables the optimized OpenAI turn
session (background connection prewarm + request-prefix telemetry) for headless
`kajicode exec` runs against official OpenAI profiles. Off by default; `0`/`false`
disable. A/B-benchmark it by running the same `kajicode-perf-bench` suite with the
variable unset and set.

### Code Quality and Security Checks

Before committing any changes, ensure all Go code quality and security checks pass. Pinned `go run` commands matching CI constraints can be used directly without prior installation:

1. **Formatting**: Run `go fmt ./...` (or `make fmt`).
2. **Vetting**: Run `go vet ./...` (or `make vet`).
3. **Linting**: Run `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run --enable-only unused,ineffassign,staticcheck ./...`.
4. **Vulnerability Scan**: Run `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...`.

If you prefer to install these tools globally on your path, you can run:

```bash
# Install golangci-lint
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

# Install govulncheck
go install golang.org/x/vuln/cmd/govulncheck@v1.3.0
```

### Cross-Compile Examples

```bash
go run ./cmd/kajicode-release build --goos linux --goarch amd64
go run ./cmd/kajicode-release build --goos windows --goarch amd64 --output dist/kajicode.exe
```

## Documentation

- [Install](docs/INSTALL.md)
- [Update flow](docs/UPDATE.md)
- [Themes](docs/THEMES.md)
- [Stream-JSON protocol](docs/STREAM_JSON_PROTOCOL.md)
- [Specialists](docs/SPECIALISTS.md)
- [GitHub Action](docs/GITHUB_ACTION.md)
- [Benchmarks](docs/BENCHMARK.md)
- [Performance](docs/PERFORMANCE.md)
- [Agent evals](docs/AGENT_EVALS.md)

## Community

Questions, setup help, ideas, and sharing all live in
[GitHub Discussions](https://github.com/dishant0406/KajiCode/discussions):

| Category | Use it for |
|---|---|
| [Q&A](https://github.com/dishant0406/KajiCode/discussions/categories/q-a) | Setup help, provider/model configuration, "how do I" questions |
| [Ideas](https://github.com/dishant0406/KajiCode/discussions/categories/ideas) | Feature proposals and design discussion before any PR |
| [Show and tell](https://github.com/dishant0406/KajiCode/discussions/categories/show-and-tell) | Your skills, plugins, MCP setups, themes, and workflows |
| [Announcements](https://github.com/dishant0406/KajiCode/discussions/categories/announcements) | Releases and project news from the maintainers |

For a good Q&A answer fast, include `kajicode --version`, your OS and install
method, the provider/model in use, and `kajicode doctor` output. See
[SUPPORT.md](SUPPORT.md). Bugs belong in
[issues](https://github.com/dishant0406/KajiCode/issues/new/choose); security reports
follow [SECURITY.md](SECURITY.md), never a public thread.

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md), run the
relevant tests, and open a focused pull request.

Security reports should follow [SECURITY.md](SECURITY.md).

## License

KajiCode is released under the [MIT License](LICENSE).
