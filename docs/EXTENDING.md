# Extending KajiCode

KajiCode is an open-source terminal coding agent. Out of the box it does the obvious things — read, edit, run, search — but the design point of the project is that **every surface is configurable**. This document is the user-facing guide for that configuration.

If you only want to *use* KajiCode, the [README](../README.md) is enough. This page is for the other three jobs:

1. Tell the agent about *your* project (drop an `AGENTS.md` in your repo).
2. Add new specialist sub-agents.
3. Wire KajiCode into the rest of your toolchain (MCP, skills, hooks, plugins).

## 1. Drop a project `AGENTS.md`

When KajiCode starts in a directory, it looks for project-level instructions and injects them into the system prompt. The lookup walks from your current working directory **up to the nearest git root** and reads the first matching file at each level — general rules at the repo root, more specific rules in sub-trees. Files are labeled with their directory in the prompt (e.g. `## Project guidelines (services/api/AGENTS.md)`).

Accepted file names, in priority order at each level:

| Path | Notes |
| --- | --- |
| `./AGENTS.md` | The classic spot — committed to your repo, shared with the team. |
| `./KAJICODE.md` | Brand-specific alias. Same format, lower priority. |
| `./.kajicode/AGENTS.md` | Project-local, hidden, gitignored. Personal notes that stay out of git. |

Matching is **case-insensitive** on the basename, so `AGENTS.md`, `Agents.md`, and `agents.md` resolve to the same file on Windows and macOS. The git-tracked filename in this repo is `AGENTS.md` — keep that on case-sensitive filesystems (Linux, the WSL filesystem, or a CI runner) to match what the loader looks for.

Both files use the same format. YAML frontmatter is optional; the markdown body is loaded as instructions for the agent. KajiCode reads the file once at session start, so changes take effect on the next `kajicode` launch — not mid-session.

```markdown
# Project conventions for <your project>

- Build with `make`, not `go build` directly.
- Tests live next to the source file (`foo_test.go` next to `foo.go`).
- Run `make lint` before opening a PR.
- Never edit files under `third_party/` — those are vendored.
```

Tips:

- Keep each file under ~8 KiB. KajiCode caps the **total** across all matched files at 32 KiB; everything past the cap is dropped.
- Re-state rules in the imperative voice: "Run `make lint`", not "you should consider running the linter".
- Don't put secrets, model IDs, or environment-specific paths in `AGENTS.md`. Use `config.json` for those.
- In a monorepo, drop a narrower `AGENTS.md` in each sub-tree (e.g. `services/api/AGENTS.md`). KajiCode picks those up automatically when you launch from inside the sub-tree.
- A YAML frontmatter block (`---\n...\n---`) at the top is preserved verbatim in the injected prompt but is not parsed for `globs:` or `alwaysApply:` scoping today — keep the body self-contained.

### Personal guidelines, across every project

For preferences that follow *you*, not a specific repo (tone, tooling habits, workflow), drop a `KAJICODE.md` in your user config directory: `~/.config/kajicode/KAJICODE.md` on Linux/macOS, `%AppData%\kajicode\KAJICODE.md` on Windows — the same directory as `config.json` and your personal specialists. Same format and 8 KiB cap as the project files above, and the same case-insensitive basename match.

This file is injected as its own `## User guidelines` section, before the project's `AGENTS.md`/`KAJICODE.md`, and is labeled as personal preference in the prompt: project guidelines are the later, more specific instruction and take precedence over it when the two conflict.

## 2. Custom specialists

Specialists are KajiCode's sub-agents. Three scopes, in priority order:

| Scope | Path | Shared? |
| --- | --- | --- |
| Built-in | compiled into KajiCode | yes — `worker`, `explorer`, `code-review` |
| User | `~/.config/kajicode/specialists/*.md` | no — your machine only |
| Project | `./.kajicode/specialists/*.md` | yes — the repo team |

Project overrides user overrides built-in when names collide.

A specialist is a markdown manifest with frontmatter and a system prompt:

```markdown
---
description: Reviews API changes for breaking-change risk and missing tests.
tools: read-only,plan
---

You review API changes. For every changed hunk in `internal/api/` or any file
that ends in `_api.go`:

1. Confirm the public signature is backward-compatible, or note the breaking
   change explicitly with the migration path.
2. Confirm a corresponding test exists in `internal/api/*_test.go` and that
   the new behaviour is exercised.
3. Flag any new exported symbol without a doc comment.

Reply with one JSON object per finding: `{"file", "line", "severity", "message", "fix"}`.
```

CLI management (the prompt is passed inline via `--prompt`):

```bash
kajicode specialist list
kajicode specialist show api-reviewer
kajicode specialist create api-reviewer \
    --project \
    --description "Reviews API changes" \
    --tools read-only,plan \
    --prompt "$(cat api-reviewer.md)"
kajicode specialist edit api-reviewer --project
kajicode specialist delete api-reviewer --project
kajicode specialist path                       # prints the resolved specialists directory
```

The full format spec (frontmatter fields, tool scopes, prompt conventions) is in [`docs/SPECIALISTS.md`](SPECIALISTS.md).

> **Roadmap.** An in-UI specialist manager (create / edit / delete / preview) is on the backlog. Today you use the `kajicode specialist` CLI subcommands above.

## 3. Skills

Skills are markdown instruction packs the agent can pull in on demand. Each skill is a directory containing a `SKILL.md`. Standalone project skill directories are not supported in this version (shared project-wide skills must go in `AGENTS.md` or as a hook). However, project plugins (section 6) may bundle skills, which are merged into the active run.

Discovery roots (earlier wins on name collisions):

1. **Primary KajiCode dir** — `$KAJICODE_SKILLS_DIR` if set, else `$XDG_DATA_HOME/kajicode/skills`, else `~/.local/share/kajicode/skills/`
2. **Shared multi-agent dir** — `~/.agents/skills/` when present (read-only discovery; never an install target)
3. **Plugin skill roots** — skills bundled by active plugins (section 6)

A missing directory is fine — KajiCode just omits it. Management commands (`kajicode skills add` / `remove` / `lock`) always write to the primary KajiCode dir only; `list` / `info` search primary + `~/.agents/skills`.

```text
~/.local/share/kajicode/skills/          # primary (install/remove/lock target)
  run-benchmarks/
    SKILL.md
  write-changelog/
    SKILL.md

~/.agents/skills/                    # optional shared multi-agent root
  shared-review/
    SKILL.md
```

`SKILL.md` format:

```markdown
---
description: Run the project's benchmark suite and summarize the deltas.
---

# Run benchmarks

1. `make bench` — captures the wall-clock and RSS before and after.
2. `benchstat before.txt after.txt` — diffs the two.
3. Report any regression > 5% with the function name and the previous value.
```

Only `name` and `description` are recognized in the frontmatter today. The `name` defaults to the directory name. Within a single skills root, duplicate names are resolved by lexicographic directory order. Across roots, KajiCode loads the primary directory first, then `~/.agents/skills`, then plugin skill roots; earlier roots win name collisions. Plugin-declared skills (section 6) are merged into the active agent run at plugin activation time, so bundled skills appear in the available skills list and can be loaded with the `skill` tool.

The `skill` tool lets the agent load any discovered skill by name (primary, agents, or plugin).

## 4. Hooks

Hooks fire shell commands on lifecycle events. Configure them in JSON:

- User: `~/.config/kajicode/hooks.json`
- Project: `./.kajicode/hooks.json`

```json
{
  "enabled": true,
  "hooks": [
    {
      "id": "block-rm-rf",
      "event": "beforeTool",
      "matcher": "bash",
      "command": "/usr/local/bin/kajicode-hook-block-rmrf.sh",
      "enabled": true
    },
    {
      "id": "log-session",
      "event": "sessionStart",
      "command": "/usr/local/bin/kajicode-hook-log.sh",
      "enabled": true
    }
  ]
}
```

The `args` array (when present) is passed verbatim to `exec.CommandContext`. The actual hook payload — event name, matcher, tool call id, tool name, tool input, tool output, status — is delivered to the command as **JSON on stdin**, not via `${...}` substitution. A typical handler reads stdin and decides what to do:

```bash
#!/usr/bin/env bash
# /usr/local/bin/kajicode-hook-block-rmrf.sh
set -euo pipefail
payload="$(cat)"
if printf '%s' "$payload" | grep -q '"input":"[^"]*rm[[:space:]]+-rf'; then
  echo "refusing rm -rf" >&2
  exit 1
fi
```

Events the agent emits (in dispatch order):

| Event | Fires when | Matcher allowed? |
| --- | --- | --- |
| `beforeTool` | A tool is about to run | yes (tool name) |
| `afterTool` | A tool just returned | yes (tool name) |
| `sessionStart` | A session begins | no |
| `sessionEnd` | A session ends | no |
| `specialistStart` | A sub-agent is spawned | yes (specialist name) |
| `specialistStop` | A sub-agent ends | yes (specialist name) |

A hook's exit code decides what happens next: `0` continues, non-zero blocks the tool call (`beforeTool`) or surfaces an error (`afterTool`). Hook execution is recorded in the audit log; the audit is reachable from the agent's view of past actions, not from a dedicated `kajicode doctor` check.

> **Roadmap.** An in-UI hooks manager is on the backlog. Today you edit the JSON directly.

## 5. MCP — Model Context Protocol

KajiCode is both an **MCP client** (it can call external MCP servers) and an **MCP server** (other agents can call its tools).

### As a client — configure MCP servers in `config.json`

```json
{
  "mcp": {
    "servers": {
      "docs": {
        "type": "stdio",
        "command": "docs-mcp",
        "args": ["--port", "7777"]
      },
      "github": {
        "type": "http",
        "url": "https://api.example.com/mcp",
        "headers": { "Authorization": "Bearer YOUR_TOKEN_HERE" }
      }
    }
  }
}
```

Manage via CLI:

```bash
kajicode mcp add docs --type stdio -- docs-mcp --port 7777
kajicode mcp add github --type http --url https://api.example.com/mcp \
    --header "Authorization=Bearer YOUR_TOKEN_HERE"
kajicode mcp list
kajicode mcp check docs
kajicode mcp remove github
kajicode mcp oauth login github
kajicode mcp oauth status github
```

### OAuth for remote MCP servers

Set `"auth": "oauth"` on a remote (`http`/`sse`) server to use OAuth 2.0 with
PKCE. KajiCode discovers the endpoints itself, following the MCP authorization
spec:

1. It POSTs an unauthenticated `initialize` to the server URL and reads the
   `WWW-Authenticate: Bearer resource_metadata="..."` challenge from the `401`.
2. It fetches that RFC 9728 **protected-resource** document, which names the
   authorization server and advertises the server's supported scopes.
3. It fetches the authorization server's RFC 8414 metadata (falling back to the
   OIDC `openid-configuration` document) for the authorize and token endpoints.

Any explicitly configured endpoint always overrides discovery, and if the config
already supplies both the authorization and token endpoints, discovery is
skipped entirely.

If the server supports **dynamic client registration** (RFC 7591), KajiCode
registers itself and no `clientID` is needed. Some servers, such as Slack, do
not — you must create an app there and supply its credentials **and** register
the exact loopback redirect URI KajiCode will use.

To use your **personal Slack account**, create the app in your own workspace
(an *internal* app; Slack allows internal or directory-published apps only), add
yourself as the only allowed user, and request **user token scopes**. The
resulting token is a Slack *user token* (`xoxp-`), so every action runs as you —
your DMs, private channels, and history are visible, subject to the scopes you
grant. Use Slack's user-token endpoints:

| Field | Value |
| --- | --- |
| `authorizationEndpoint` | `https://slack.com/oauth/v2_user/authorize` |
| `tokenEndpoint` | `https://slack.com/api/oauth.v2.user.access` |

Slack's metadata advertises `token_endpoint_auth_methods_supported:
["client_secret_post"]`, which KajiCode already uses — it sends `client_id` and
`client_secret` as form fields, so no extra configuration is needed.

```json
{
  "mcp": {
    "servers": {
      "slack": {
        "type": "http",
        "url": "https://mcp.slack.com/mcp",
        "auth": "oauth",
        "oauth": {
          "clientID": "YOUR_SLACK_APP_CLIENT_ID",
          "clientSecret": "YOUR_SLACK_APP_CLIENT_SECRET",
          "redirectURI": "http://127.0.0.1:45999/callback",
          "scopes": ["search:read.public", "channels:history", "chat:write", "users:read"]
        }
      }
    }
  }
}
```

Scopes are per-tool: search needs `search:read.public` / `search:read.private` /
`search:read.im` / `search:read.mpim` / `search:read.users` / `search:read.files`;
reading a channel or thread needs `channels:history` (plus `groups:history`,
`im:history`, `mpim:history`); sending needs `chat:write`; user profiles need
`users:read` and `users:read.email`; files need `files:read` / `files:write`;
canvases need `canvases:read` / `canvases:write`; lists need `lists:read` /
`lists:write`. Ask only for what you need. When `scopes` is omitted, KajiCode
requests everything Slack's protected-resource document advertises.

Note that Slack does not issue refresh tokens for this flow — if the app is
later uninstalled or the token is revoked, rerun `kajicode mcp oauth login slack`.

`oauth` fields:

| Field | Meaning |
| --- | --- |
| `clientID` / `clientSecret` | OAuth app credentials. Optional when the server supports dynamic client registration. |
| `scopes` | Requested scopes. When omitted, the scopes the protected resource advertises are used. |
| `authorizationEndpoint` / `tokenEndpoint` | Pin endpoints; override discovery. When both are set, discovery is skipped. |
| `registrationEndpoint` | Pin the dynamic client registration endpoint. |
| `issuerURL` | Override the base URL used for discovery (instead of the server URL). |
| `redirectURI` | Pin the loopback redirect URI. Required by servers that reject an ephemeral port. Must be a loopback `http://` URL. |
| `callbackPort` | Shorthand for a fixed callback port on `127.0.0.1`; ignored when `redirectURI` is set. |
| `resource` | RFC 8707 resource indicator. Defaults to the server URL or the value the protected-resource document advertises. |

All discovered URLs are validated before use: discovery and endpoint URLs must
be `https` (loopback exempt) and may not resolve to private, link-local, or
unspecified addresses.

Servers are merged from user and project configs (project wins on conflicts). Token-bearing values in `config.json` are sent verbatim — there is no `${env:...}` expansion — so prefer one of:

- A wrapper script that sources the secret and execs the real command.
- A `--header` value produced by command substitution (`"Authorization=Bearer $(print-token)"`) in a private shell config that you keep out of git.
- A secret manager that injects the env var your MCP server reads on its own (the `command` and `args` then run inside that environment).

Project MCP config remains supported for shared team servers, but a project layer
that changes an existing server's target (`url`, `command`, or `args`) does not
inherit user credentials. Clear or replace `headers`, `env`, or `oauth` explicitly,
or use a new server name. OAuth tokens are bound to the resolved server identity,
so changing a server target can require `kajicode mcp oauth login <server>` again.
OAuth server names ending in `.<32 hex chars>` are reserved for token storage.

### Remote transport, timeouts, and tool filtering

A remote server configured as `"type": "http"` uses Streamable HTTP; if that
fails, KajiCode automatically retries over the legacy HTTP+SSE transport, so an
older server still connects. An authentication failure is never retried over SSE
(it would repeat the same rejected request) — instead KajiCode reports it as
needing login.

Each server may declare a per-call timeout (milliseconds) that overrides the
built-in default for that server's tool calls. The deadline resets whenever the
server sends a `notifications/progress` message, so a long but progressing tool
call is not killed while a genuinely hung one still is.

```json
{
  "mcp": {
    "servers": {
      "slow": {
        "type": "http",
        "url": "https://api.example.com/mcp",
        "timeout": 600000,
        "tools": { "allow": ["search_*", "fetch"], "deny": ["search_internal"] }
      }
    }
  }
}
```

`tools.allow` / `tools.deny` are case-insensitive glob lists; `allow` wins over
`deny`, and an empty filter registers every tool the server advertises.

### Server instructions, resources, and prompts

When a server returns `instructions` during `initialize`, KajiCode includes them
in the system prompt inside an `<mcp_instructions>` block, so the model sees the
server's own guidance.

Connected servers that advertise **resources** gain three tools:
`list_mcp_resources`, `list_mcp_resource_templates`, and `read_mcp_resource`.
Servers that advertise **prompts** gain two more: `list_mcp_prompts` and
`get_mcp_prompt`. These tools only exist while a server advertising the matching
capability is connected. Resource listings paginate to completion, and a large
resource blob (over 10 MiB) is summarized rather than inlined.

### Server-initiated changes

If a server sends `notifications/tools/list_changed`, KajiCode re-lists that
server's tools and updates the registry live — no restart needed.

### When login cannot help

If a server's authorization metadata advertises no `registration_endpoint` and no
`clientID` is configured, startup reports a **needs client registration** warning
distinct from the needs-login warning: logging in cannot fix it, you must create
an app and set `oauth.clientID` (with `oauth.clientSecret` when required). When a
desktop session is detected, `kajicode mcp oauth login` opens the authorization
URL in your browser; in headless/SSH sessions it prints the URL to copy.

### As a server — expose KajiCode's tools to another agent

```bash
kajicode serve --mcp
kajicode serve --mcp -C /path/to/workspace
kajicode serve --mcp --add-dir /path/to/extra
```

The server speaks MCP over stdio. Configure it from the receiving side as a `stdio` server whose command is `kajicode serve --mcp`.

`-C/--cwd` sets the workspace root exposed as MCP resources and used by core tools. `--add-dir` (repeatable) widens that resource/tool scope beyond the workspace without granting the sandbox temp roots used by interactive runs.
## 6. Plugins

A plugin is a self-contained directory that bundles tools, hooks, and skills for one capability. Plugins live at:

- User: `~/.config/kajicode/plugins/<id>/`
- Project: `./.kajicode/plugins/<id>/`

Each plugin has a `plugin.json` manifest:

```json
{
  "id": "github-pr-review",
  "name": "GitHub PR Review",
  "description": "Adds review tools for GitHub PRs.",
  "version": "1.0.0",
  "tools": [
    { "name": "list_prs", "command": "./tools/list_prs.sh" }
  ],
  "hooks": [
    { "name": "pre-merge-check", "event": "beforeTool", "command": "./hooks/pre-merge.sh" }
  ],
  "skills": [
    { "path": "./skills/review-checklist/SKILL.md" }
  ]
}
```

Install and manage:

```bash
kajicode plugins add ./github-pr-review      # copy into ~/.config/kajicode/plugins/ or ./.kajicode/plugins/
kajicode plugins list
kajicode plugins remove github-pr-review    # alias: rm
```

A plugin is enabled by being present in the plugins directory and disabled by removing it (or by the user setting `"enabled": false` in its `plugin.json`). Plugins are not enabled or disabled by a CLI subcommand today.

Plugin commands run with the plugin directory as their working directory. Use relative paths; the loader resolves them at activation time.

> **Roadmap.** An in-UI plugins manager (browse, install, enable / disable) is on the backlog. Today you use the `kajicode plugins` CLI subcommands above.

## 7. Configuration locations

Five configuration sources, in precedence order (later sources override earlier ones):

| Source | Path / Key | Notes |
| --- | --- | --- |
| Built-in defaults | compiled in | Lowest priority. |
| User config | `~/.config/kajicode/config.json` | Your machine. Never committed. |
| Project config | `./.kajicode/config.json` | The repo. Committed (or not, your call). |
| Environment | `KAJICODE_*` | Provider commands, secrets, skills dir override. |
| CLI flags | `--model`, `--mode`, ... | Highest priority, per-invocation. |

The user config holds things that should follow the user across projects (default provider, default model, theme). The project config holds things the team agreed on (provider catalog, sandbox policies, model restrictions).

The sandbox `additionalWriteRoots` key is **ignored in project config** by design — a checked-out repo cannot widen its own sandbox. Set it in the user config or pass `--add-dir` per-invocation.

## 8. End-to-end example

A team that wants every contributor's KajiCode to behave the same way commits:

- `AGENTS.md` — project conventions, build commands, do-not-edit lists.
- `.kajicode/config.json` — provider catalog, default model, allowed tools.
- `.kajicode/specialists/api-reviewer.md` — the team's PR-review specialist.
- `.kajicode/hooks.json` — block `rm -rf` on `beforeTool`.
- `.kajicode/plugins/internal-tooling/` — a plugin that adds the team's internal CLI tools to the agent's toolset.

Each contributor adds only:

- `~/.config/kajicode/config.json` — their personal API keys, theme, default mode.
- `~/.config/kajicode/KAJICODE.md` — personal preferences that follow them across every project (see section 1).
- `~/.local/share/kajicode/skills/` — personal skills they keep across projects.

That's it. Run `kajicode` from the repo root and the agent has the team's full instruction set, every contributor's personal setup, and nothing else.

## 9. Reference

- [README](../README.md) — install, quickstart, command reference.
- [docs/SPECIALISTS.md](SPECIALISTS.md) — full specialist manifest spec.
- [docs/STREAM_JSON_PROTOCOL.md](STREAM_JSON_PROTOCOL.md) — `kajicode exec` I/O contract.
- [docs/INSTALL.md](INSTALL.md) — install from source or release.
