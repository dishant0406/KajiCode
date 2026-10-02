# Session Trace — `zero_20261001200650_1790885210670660000_1`

Read-only trace of one real session, run to answer two questions: **did it use
the classifier?** and **why did it use shell commands instead of native tools?**

No production code was changed by this investigation.

---

## 0. The session

| field | value |
|---|---|
| id | `zero_20261001200650_1790885210670660000_1` |
| title | OpenCode vs Kajicode Feature Research |
| cwd | `/Users/dishants/projects/kajicode` |
| provider / model | `growwcorp` / `fireworks/DeepSeek_V4.1_Flash` |
| permission profile | **`bypass-all`** |
| events | 111 (32 tool calls, 32 results, 28 permission decisions, 15 usage) |
| created → updated | 2026-10-01T20:06:50Z → 2026-10-01T20:11:46Z (~5 min) |
| peak prompt tokens | **70,496** |
| child session | `zero_20261001200730_1790885250642181000_1` (agent `explorer`, depth 1) |

**Task:** "if you check /tmp we have opencode i guess what does it have that we
dont in kajicode resreach please" — i.e. explore a presumed opencode checkout,
then diff its feature surface against KajiCode.

---

## 1. Did it use the classifier? — **No. Three independent proofs.**

### 1.1 No gate metadata on any result
All 32 `tool_result` events carry the **output-budget** keys only
(`output_budget_*`, `estimated_tokens`, `emitted_bytes`, …). **Zero** `gate_*`,
`gate_decision`, `gate_hidden`, `gate_coverage`, or `spill_path` keys. A gated
result is always stamped with those (see `renderGated` in
`internal/agent/tool_gate.go`). None are present.

### 1.2 The feature was not enabled in config
The config that ran this session:

```json
"classifier": {
  "active": "jev", "enabled": true,
  "features": { "compaction": { "enabled": true, "keepResultThreshold": 0.35 } },
  "profiles": ["jev"]
}
```

There is **no `toolResult` entry**. `toolResultGateForRun`
(`internal/cli/classifier_runtime.go:37`) returns `nil` unless
`cfg.Enabled && cfg.Features.ToolResult.Enabled`:

```go
if !cfg.Enabled || !cfg.Features.ToolResult.Enabled {
    return nil   // loop runs the unchanged tool path
}
```

`nil` ⇒ the loop never calls the gate ⇒ byte-identical to a no-classifier run.

### 1.3 The gate code WAS present in the tree — this is *not* why (correction)
An earlier draft claimed the session ran on committed HEAD `825fba1` and so had no
gate code. **That is wrong for a `go run ./cmd/kajicode` dev run**, and the file
timestamps disprove it:

```
internal/agent/tool_gate.go            Sep 30 20:37:09 2026
internal/agent/tool_gate_requery.go    Sep 30 20:35:04 2026
internal/cli/classifier_runtime.go     Sep 30 19:12:04 2026
session events.jsonl                   Oct  2 01:41:46 2026   ← ran ~29h later
```

`tool_gate.go` is untracked in git, but it existed on disk **29 hours before** the
session. So a `go run` from the working tree *did* compile it; `toolResultGateForRun`
was in the binary. The only thing that kept it inert is **1.2 — the config flag
was off**. The earlier "the gate is entirely later working-tree work" line was a
git-status inference that the timestamps refute; corrected here.

### 1.4 The compaction judge — the one consumer that *was* enabled — also never fired
That is correct, not a bug: the judge only runs inside `maybeCompact`, which only
runs at the compaction threshold. Threshold =
`window × 0.7 − 4096` (`compaction.go:52,336`) ≈ 139k for a 200k window. Peak
prompt was **70,496**. No compaction event appears in the transcript. So the
classifier was **reachable and configured**, but neither consumer (gate, judge)
executed in this session.

**Verdict:** the classifier was connected but **not used at all** in this run —
by config (gate off) and by timing (context never filled enough to compact).

---

## 2. Why shell commands instead of native tools?

Tool-call inventory for the parent:

| tool | calls |
|---|---|
| `bash` | 16 |
| `web_fetch` | 11 |
| `todo_write` | 3 |
| `web_search` | 1 |
| `Task` | 1 |
| **`read_file` / `grep` / `glob` / `ls` / `list_directory`** | **0** |

Zero native file tools, 16 shell calls. Three layered causes.

### 2.1 Cause A — half the task was **out-of-workspace**, and the *description* said native tools can't go there
**13 of the 16** bash calls target paths outside the workspace (`/tmp`,
`/private/tmp`, `~`, `~/.cache`, `~/projects`): the "is there an opencode checkout
on this box, and what's in it?" half of the task.

**Correction to the first draft.** The first pass blamed a hard code dead-end:
"at HEAD the native tools are mode-blind, so bash was the *only* tool that could
reach an outside path." That is **false for the binary that ran this session.**
The mode-lifting scoping fix (`modeLiftsBoundary` in `internal/tools/workspace.go`)
was already on disk — `workspace.go` mtime **Oct 1 16:33**, the scoping test
`scope_mode_test.go` **Oct 1 16:34**, both **~9 hours before** the session ran.
Re-verified live on that same tree just now:

```
go build ./cmd/kajicode
./kajicode exec --permissions bypass-all "read_file /tmp/kc-outside/t.txt"
→ File: /private/tmp/kc-outside/t.txt (1 lines) 1 | SECRET_OUTSIDE_123   ✅
```

So under `bypass-all` the native `read_file`/`grep`/`glob`/`ls` **could** reach
outside the workspace in that run. The dead-end was gone.

**What remained is a *description* problem, not a code one.** Even with working
scoping, the model-visible contract still told the model the tools were
workspace-only:

- `grep`: *"Search file contents with a regular expression. Use this instead of
  `grep`/`rg`. **Searches the workspace and any explicitly granted extra root.**"*
  (`internal/tools/grep.go:44`)
- `glob`: *"Find files by glob pattern… **Scans the workspace and any explicitly
  granted extra root.**"*
- `read_file` / `ls` / `list_directory`: no mention that `bypass-all` lifts the
  boundary — "Defaults to workspace root."

None of these descriptions names the permission mode as a boundary lever. A model
reading them has no signal that an absolute `/tmp/...` path will succeed under the
active `bypass-all` mode, so the safe-looking route for an out-of-tree target is
still `bash`. **No containment error ever appeared** (`must stay inside the
workspace` → 0 occurrences, 0 errored results): the model never tried a native
outside read and got refused — it simply never considered the native tool as
capable of it. That is a *description/context* cause, and it leaves the door open
to a genuine documentation fix: the native tool descriptions and the prompt should
state that the active permission mode decides the boundary.

The out-of-tree calls are also natural shell jobs regardless:

```
ls -la /tmp | head -50
for d in /private/tmp/opencode-*; do echo "=== $d ==="; ls -la "$d"; done
find /private/tmp/opencode* -maxdepth 4 | head -100
```

One residual code gap stays true: `request_permissions` short-circuits under
`bypass-all` (`loop.go:2491`, before `GrantRequestPermissions`), so there is no
in-band way to *explicitly* widen the scope even though the mode grants it.

### 2.2 Cause B — the 3 in-workspace calls were genuine **shell jobs**
The 3 `cd $PWD` calls are not purely-replaceable reads; they chain multiple
things native tools don't compose:

```
cd /Users/dishants/projects/kajicode; echo "== serve.go =="; sed -n '1,60p' internal/cli/serve.go;
  grep -rn "ListenAndServe\|net/http" --include=*.go internal/ cmd/ | grep -v _test | head -20
cd …; for p in formatter gitlab share zen …; do printf "%-12s: " "$p"; grep -rli "$p" … | wc -l; done
```

So even in-repo, ~all of it is legitimately shell-shaped, not laziness.

### 2.3 Cause C — the "prefer native tools" instruction has **no teeth** at HEAD
The system prompt already says it (`HEAD:internal/agent/system_prompt.md:33`):

> Choose the narrowest tool that safely does the job. Prefer native file tools
> such as read_file, list_directory, glob, grep … **over shelling out for
> ordinary file work.**

But the tool descriptions themselves **do not advertise the native alternative or
the mode lever** (this is the remaining, real gap):

- `read_file`: *"Read a file with optional line range and max line cap. Use this
  instead of `cat`/`head`/`tail`/`sed -n`…"* — names the shell equivalent (good),
  but never says the **permission mode** decides the boundary.
- `grep`: *"…Searches the workspace and any explicitly granted extra root."*
- `glob`: *"…Scans the workspace and any explicitly granted extra root."*

A prose preference cannot outvote (a) descriptions that read as workspace-only and
silent on the mode lever, and (b) shell commands that are the natural expression of
the out-of-tree half of the task.

### 2.4 The decisive control — the child `explorer` proves the model *can* choose native tools

The parent spawned one `explorer` sub-agent (`Task` call). That agent is
category-gated to read-only (`internal/agents/builtin.go:24`,
`Tools: []string{"read"}` → **no bash**) and told *"Do not edit files or run shell
commands."* Same repo, same provider/model, same `bypass-all` mode.

| | parent | `explorer` child |
|---|---|---|
| `glob` | 0 | **18** |
| `grep` | 0 | **21** |
| `read_file` | 0 | **17** |
| `batch` | 0 | 4 |
| `bash` | **16** | **0** |
| native file tools | **0** | **56** |

When bash is not in the toolset, the model explores the identical codebase
cleanly and efficiently with native tools. So the parent's shell-first behavior is
**not** a model incapability — it is the parent toolset + task mix + weak
descriptions letting bash win.

---

## 3. Root cause

Two separate findings, one per question.

**Classifier unused** — not a defect in this run. It is gated purely by config:
`features.toolResult` was **absent**, so `toolResultGateForRun` returned `nil`.
(The gate code *was* in the dev binary — see §1.3.) The enabled consumer, the
compaction judge, did not fire because the context never reached the compaction
threshold (70k ≪ ~139k). Neither is a bug.

**Shell-over-native** — the mode-lifting scoping fix **was already active** in
this run, so an absolute out-of-workspace path *did* work through native tools
(verified live, §2.1). The dominant cause is therefore split:
1. **Description/context** — the native tool descriptions read as workspace-only
   and never mention that the active permission mode lifts the boundary, so the
   model never treats a native tool as an option for `/tmp`, `~`, `~/projects`
   targets (13/16 calls), where bash looks like the only route.
2. **Task shape** — the out-of-tree *and* in-workspace calls are naturally
   shell-shaped (`for` loops, chained `sed`+`grep`+`head`, `printf`/`wc`), and
   there is a proved control (§2.4) that the model uses native tools when bash is
   absent.

The code fix (mode-lifted scoping) is done and working. The remaining fixable
lever is **making the contract legible**: name the shell equivalent **and** state
that the permission mode decides the boundary, so the model considers the native
tool instead of defaulting to `bash`.

---

## 4. What this means for the existing plan

`docs/TOOL_SCOPING_AND_PREFERENCE_PLAN.md` (already written) targets exactly this:

- **Fix 1 (primary):** make the native tools honor the active permission mode, so
  `bypass-all` lifts the boundary the engine already lifts — unlocking native
  `ls`/`glob`/`grep`/`read_file` for the out-of-tree half. *This is the change that
  would have converted Cause A's 13 bash calls.* The implementation is present in
  the working tree (`modeLiftsBoundary` in `internal/tools/workspace.go`) but
  **uncommitted**, so it was not in the binary that ran this session.
- **Fix 2 (complement):** name the shell equivalent in each native tool's
  description + the addendum. Also present in the working tree, uncommitted.

**This trace is the field evidence for that plan.** It confirms the whole premise
on a fresh real session: the *majority* of shell usage was a workspace-scoping
dead end, not a preference the model ignored, and the child-agent control
isolates the variable cleanly (bash removed → 56 native calls, 0 bash).

---

## 5. Verification method

Reproduce this trace:

```bash
S=~/.local/share/kajicode/sessions/zero_20261001200650_1790885210670660000_1
# tool inventory
python3 -c "import json,collections;c=collections.Counter();[c.update([json.loads(l)['payload']['name']]) for l in open('$S/events.jsonl') if l.strip() and json.loads(l)['type']=='tool_call'];print(dict(c))"
# gate metadata present?
grep -c gate_decision "$S/events.jsonl"    # → 0
# outside-workspace bash share
grep -o '"/private/tmp\|"/tmp\|~/projects\|~/.cache' "$S/events.jsonl" | wc -l
# child control
grep -c '"name":"bash"' ~/.local/share/kajicode/sessions/zero_20261001200730_1790885250642181000_1/events.jsonl  # → 0
```

---

## 6. Risks / uncertainties (stated, not hidden)

- **Which binary ran it is not recorded in the transcript — but the tree it ran from is inferable.** Every feature file (gate `Sep 30 20:37`, scoping fix `Oct 1 16:33`, descriptions `Oct 1 16:17–16:33`) predates the session (`Oct 2 01:36`), and a live `go build` of that same tree reproduces working out-of-workspace native reads under `bypass-all` (§2.1). So the session almost certainly ran *with* the scoping fix active — which is why Cause A is now framed as a **description** gap, not a code dead-end. This does not affect the classifier conclusion (config-gated, proved independently).
- **n = 1.** One session, one model, one task shape (an external-research task is
  unusually out-of-tree — a pure in-repo coding task would exercise native tools
  more). The measured 61% bash share in the scoping plan's 30-session sweep is the
  representative number; this trace is the mechanism, not the rate.
