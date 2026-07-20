# KajiCode

These instructions apply to all work in this repository. KajiCode is a Go CLI
coding agent with an interactive Bubble Tea TUI, headless `exec` mode, ACP
bridge, tool runtime, sandbox policy, local sessions, extension surfaces, and
release/npm packaging.

## Build And Run

```bash
make build                         # Build ./kajicode
go run ./cmd/kajicode              # Run from source
go run ./cmd/kajicode exec "..."   # Headless run
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
```

Use the Go version declared in `go.mod`. Do not hardcode a different local
toolchain version in scripts, docs, or workflows.

## Validation

Run focused checks for narrow changes and the full loop before shipping broad or
release-facing work:

```bash
make fmt-check
go vet ./...
go test ./...
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```

Use `make test` when the change touches concurrency or when CI parity matters.
Run `go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...` for release,
dependency, sandbox, provider, installer, and security-sensitive work.

## Architecture

- The canonical architecture map is `docs/architecture.md`; keep it current
  when changing package ownership, startup flow, agent loop behavior, sandbox
  policy, session persistence, extension loading, or release packaging.
- `docs/HOW_KAJICODE_WORKS.md` is the detailed deep dive. Update it when a
  behavior needs more than the concise architecture contract can explain.
- Keep surface code and runtime code separated:
  - `cmd/kajicode` stays a tiny entrypoint.
  - `internal/cli` owns command parsing, config resolution, provider/tool/sandbox
    assembly, and launch paths.
  - `internal/tui` owns Bubble Tea presentation state only.
  - `internal/agent` owns model turns, tool execution, compaction, retries,
    completion gates, and runtime callbacks.
  - `internal/kajicoderuntime` owns provider-neutral message/tool/stream types.
  - `internal/providers` owns provider-specific API adapters.
  - `internal/tools` owns tool interfaces, registry entries, output budgeting,
    redaction, and local tool implementations.
  - `internal/sandbox` owns permission policy, grants, path/network checks, and
    platform sandbox backends.
  - `internal/sessions` owns durable local session metadata and event replay.
  - `internal/mcp`, `internal/plugins`, `internal/skills`,
    `internal/specialist`, `internal/swarm`, and `internal/hooks` own extension
    surfaces.
  - `cmd/kajicode-release`, `internal/release`, `scripts/install.*`,
    `scripts/npm/*`, `package.json`, and `.github/workflows/publish-npm.yml`
    own release and npm distribution.
- Provider-specific, platform-specific, and extension-specific behavior must stay
  in the package that owns that concern. Do not add hardcoded checks in unrelated
  layers when a registry, interface, or package-local implementation already
  exists.

## Code Rules

- Each file should do one thing and do it well. Prefer small files with
  descriptive names over generic `utils` or `helpers`.
- Keep new files under 200 lines when practical. If an existing large package
  owns the feature, add a focused file beside it instead of making a large file
  larger unless the local pattern clearly requires the edit.
- Less code is better when it preserves correctness. Delete unused code and
  replace large code with simpler code when the simpler code is easier to verify.
- Avoid duplication. Check existing code before adding a new helper, abstraction,
  command path, tool wrapper, or renderer.
- Use early returns instead of nested conditionals.
- Do not patch symptoms. Trace the real path and fix the root cause.
- Comments should explain non-obvious contracts, safety decisions, or lifecycle
  constraints. Do not add narration that merely restates the code.
- Preserve the user's working tree. Do not overwrite, delete, stage, or commit
  unrelated tracked or untracked files.

## Feature Guidance

- TUI work belongs in `internal/tui` and should include focused view/update tests
  for modal behavior, key handling, width/height responsiveness, and transcript
  persistence when applicable.
- Agent-loop changes belong in `internal/agent` and must include regression tests
  for turn progression, tool pairing, retries, compaction, permissions, or
  completion semantics touched by the change.
- Tool changes belong in `internal/tools` unless they are MCP/plugin/specialist
  tools. Include permission metadata, output-budget behavior, redaction behavior,
  and sandbox expectations in tests.
- Sandbox changes belong in `internal/sandbox`; test path scope, network,
  command-risk, grant lifetime, and platform fallbacks.
- CLI changes belong in `internal/cli`; test argument parsing, config merging,
  exit codes, output formats, and non-interactive behavior.
- Release changes must validate `cmd/kajicode-release`, generated checksums,
  install scripts, npm wrapper/platform packaging, and GitHub workflow behavior.

## Pull Requests

- Keep each change focused on the approved or assigned scope.
- Do not include unrelated fixes, refactors, formatting churn, generated output,
  or existing local changes in the same commit or pull request.
- PR descriptions should be short: what changed, why, and what was tested.
- Include screenshots or recordings for user-visible TUI changes when practical.

## Code Review

- Review against the purpose of the PR or issue first. Report unrelated findings
  separately.
- Apply review recommendations only after user confirmation.
