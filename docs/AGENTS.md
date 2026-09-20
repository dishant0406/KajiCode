# KajiCode Agents

Agents are named sub-agents that KajiCode can delegate focused work to through
the `Task` tool. An agent is a markdown file with YAML-style frontmatter plus a
system prompt body.

Agents run **in-process** on the parent run's registry, provider, sandbox, and
permission policy. A child's tool set is the parent's registry filtered to the
tools its own ruleset allows, so a child can use the same MCP, plugin, and skill
tools the parent has — and can never reach a tool the parent lacks.

## Scopes

| Scope | Path | Notes |
| --- | --- | --- |
| Built-in | compiled into KajiCode | `worker`, `planner`, `explorer`, `verifier`, and `code-review` ship with the binary. |
| User | `~/.config/kajicode/agents/*.md` | Available across local workspaces. |
| Project | `.kajicode/agents/*.md` | Shared with the current repository when committed. |

Project agents override user and built-in agents with the same name. User agents
override built-ins. A higher-precedence file may set `disable: true` to suppress a
lower-precedence agent of the same name entirely.

## CLI Management

```bash
kajicode agent list
kajicode agent show worker
kajicode agent path

kajicode agent create api-review \
  --project \
  --description "Reviews API changes" \
  --tools read,plan \
  --prompt "Review API changes for compatibility and missing tests."

kajicode agent edit api-review --project
kajicode agent delete api-review --project
```

Use `--json` with `list`, `show`, `path`, `create`, or `delete` when scripting.
`create --force` replaces an existing manifest, but refuses symlink overwrites.
`edit` also refuses symlink manifests before opening `$VISUAL` or `$EDITOR`.

## Definition Format

```markdown
---
name: api-review
description: Reviews API changes for compatibility and missing tests.
mode: subagent
tools:
  - read
  - plan
---

Review API changes for behavior regressions, compatibility breaks, and missing
tests. Report concrete findings with file paths.
```

Supported frontmatter keys:

| Key | Purpose |
| --- | --- |
| `name` | Lowercase agent id. Use letters, numbers, and dashes. |
| `description` | Short summary shown in listings and the Task tool's agent directory. |
| `extends` | Optional base agent to inherit prompt/model/tools/sampling from. |
| `model` | Optional model override. Empty means inherit the parent model. |
| `thinking` | Optional reasoning-effort override. |
| `tools` | Array of tool categories, tool ids, or globs (e.g. `mcp_github_*`). Empty means read-only. |
| `excludeTools` | Array of categories/tools to deny even when `tools` grants them. |
| `mode` | `all` (default), `subagent`, or `primary`. Primary agents are reserved names: spawnable but never listed for delegation. |
| `hidden` | Boolean. Keeps the agent spawnable via Task but removes it from the delegation directory. |
| `aliases` | Array of alternate names that resolve to this agent. |
| `temperature` | Sampling override forwarded to the child run (0–2). |
| `topP` | Sampling override forwarded to the child run (0–1). |
| `steps` | Iteration cap for the child run, forcing a wrap-up instead of an unbounded tool loop (1–200). `maxSteps` is a deprecated alias — set only one. |
| `disable` | Boolean. A project/user manifest with `disable: true` suppresses a lower-precedence agent of the same name entirely. |

If the body is empty and `description` is set, KajiCode uses the description as
the system prompt and reports a warning.

## Tool Selection

A tool entry is either a **category** (expanded to concrete tool names), a
**tool id**, or a **glob**. Categories:

| Category | Tools |
| --- | --- |
| `read` | `read_file`, `read_minified_file`, `list_directory`, `ls`, `glob`, `grep`, `lsp_navigate`, `skill`, `recall`, `recipe_run`, `code_search`, `web_fetch`, `web_search`, `tool_search`, `batch` |
| `edit` | `write_file`, `edit_file`, `apply_patch`, `multi_edit` |
| `execute` | `exec_command`, `write_stdin`, `bash` |
| `plan` | `todo_read`, `todo_write` |
| `task` | `Task`, `TaskOutput`, `TaskStop`, `GenerateAgent` |

MCP tools are granted by their synthesized `mcp_<server>_<tool>` name (globs
work, e.g. `mcp_github_*`). Plugin and skill tools are granted by their own
names. Only tools that already exist in the parent run's registry can be granted,
so an agent can never exceed its parent.

`tools` omitted defaults to `read` (read-only) so an agent that forgets to list
tools cannot mutate the workspace or run commands by accident.

## Agent Tools

KajiCode registers these tools for top-level runs:

| Tool | Purpose |
| --- | --- |
| `Task` | Launch an agent sub-run for a focused prompt. |
| `TaskOutput` | Read or block on a background agent task's result. |
| `TaskStop` | Cancel a running background agent task. |
| `GenerateAgent` | Create a project-local agent definition from a name, description, and prompt. |

`GenerateAgent` is project-scoped: it writes to `.kajicode/agents`, not the user
directory.

Example LLM-facing `Task` payload:

```json
{
  "agent": "explorer",
  "description": "Find session storage code",
  "prompt": "Find the files that create, load, and list sessions."
}
```

Background task payload:

```json
{
  "agent": "worker",
  "description": "Audit release docs",
  "prompt": "Check the release docs for stale TypeScript references.",
  "run_in_background": true
}
```

The returned `task_id` is the child session id. Use it with `TaskOutput` or
`TaskStop`.

## Background completion push

When a background Task finishes, the interactive session delivers its final
summary to the running agent automatically as a `<task_result>` nudge on the
next turn — no `TaskOutput` polling loop required. `TaskOutput` still works for
manual checks. Headless one-shot runs keep the poll-only behavior.

## Nesting depth

Agent nesting via Task is capped by `agents.depth` in `kajicode.json` (default
4). Lower it to bound runaway delegation chains:

```json
{ "agents": { "depth": 2 } }
```
