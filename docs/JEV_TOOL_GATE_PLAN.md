# Tool-output gate — investigation + plan

Status: **shipped — see `docs/tool-gate-report.md` for the implementation and the
live measurement.** This is the third and final item of the classifier pipeline
(1 = compaction ✅ shipped, 2 = tool-output gate ← this document, 3 = completion /
loop gating).

**Outcome:** the gate is implemented, safe, fail-open, **default off**, and
measured live. On four real sessions (975 judged blocks, jev-1.13, AUC 0.705) it
hides ~1.9% of blocks at its shipped threshold (0.2) with **zero** measured
regret — the same small-but-honest effect winnow (~5%) and pi-warden (shipped
off) reported. The plan's acceptance gate kept the default **off**.

Related, do not re-derive:
`docs/JEV_COMPACTION_FILTER_PLAN.md` (what shipped),
`docs/JEV_COMPACTION_IMPROVEMENT_PLAN.md` (§4 Option D is this feature),
`docs/compaction-classifier-ab-report.md` (the measured compaction result),
`docs/compaction-band-report.md` (the recall/spill net reused here).

---

## 1. Understand the issue

**The ask (the user's words, decomposed).** "A classifier loop in itself that
filters out the tool responses and asks the tool again if no satisfactory
response is there from the output, without polluting the agent context."
Plus: "not only grep and reads but also web search and everything else."

Four distinct requirements are bundled here, and they are not the same size:

1. **Judge** each tool result as it is produced.
2. **Keep only the useful part**, so the useless part never enters the model's
   context (contrast with compaction, which cleans up *later*).
3. **Re-query** the tool when the surfaced part is not satisfactory.
4. **Do it without polluting context** — the rejected bytes and the intermediate
   attempts must not accumulate in the parent transcript.

**What is happening today.** There is **no relevance gate at tool time at all.**
A tool result is truncated **deterministically by size only**, then appended to
the transcript verbatim. Nothing ever asks "is this result *relevant* to what the
user asked?" Relevance is only ever considered much later, at compaction.

**What should happen.** A large, non-error tool result that is mostly irrelevant
should be reduced to the part that is relevant (plus a recall path), and the
model should be able to get the rest on demand — or, better, get a *better*
result by re-querying rather than by being handed an inferior one.

**Where it starts.** The single append site:
`internal/agent/loop.go:909` —

```go
messages = append(messages, kajicoderuntime.Message{
    Role:       kajicoderuntime.MessageRoleTool,
    Content:    toolResult.Output,
    ToolCallID: toolResult.ToolCallID,
})
```

Everything a tool returns reaches the model here. Before it, the only reduction
is `internal/tools/output_boundary.go` (`applyRegistryOutputBudget` `:20`, plus
`attachExistingSpill` `:193`), which is **size**-based and **category**-based
(`read_file` 128 KB, `grep`/`glob`/`ls` 64 KB, else the ceiling,
`output_budget.go:11-13`). It has no idea what the task is.

**When.** Every turn that runs a tool whose output exceeds the minimum size.

**Reproducible?** Yes, trivially — it is a missing code path, not a race or a
config bug. There is nothing to "reproduce" beyond observing that no gate exists;
the honest question is **whether building one helps**, which §2 answers with
measurement rather than assumption.

---

## 2. Evidence

### 2.1 Code path (traced end to end, not the first plausible file)

```
model emits tool_call
  └─ loop.go:869  executeToolCall(registry, call, …)          (loop.go:1308)
       ├─ registry.RunWithOptions
       │    └─ registry.go:207-210  redaction → budget → ceiling
       │         ├─ output_boundary.go:20  applyRegistryOutputBudget   (size/category)
       │         │    └─ output_boundary.go:193 attachExistingSpill     (writes full body, appends path notice)
       │         └─ output_boundary.go:52  applySelfManagedOutputBudget (bash/exec_command)
       ├─ loop.go:1665  dispatchAfterTool(ctx, options, call, args, result)
       │    └─ afterTool hooks are APPEND-ONLY (loop.go:2060, appendHookFeedback loop.go:2152)
       └─ → ToolResult
  ├─ loop.go:897  options.OnToolResult(toolResult)     (display only)
  └─ loop.go:909  messages = append(… Role: Tool, Content: toolResult.Output …)   ← THE GATE GOES HERE
```

Findings that constrain the design:

- **The tools package cannot own this.** `afterTool` hooks receive
  `ToolName/ToolCallID/status/changedFiles` (`loop.go:2071-2081`) — **not the
  body, not the task** — and their only channel is to *append* text
  (`appendHookFeedback`). The gate needs the body **and** the task, and must
  **replace** output, so it belongs in `internal/agent`, next to the append site
  and `compactionGoal`.
- **A recall path already exists and is hardened.** `tools.SpillOutput`
  (`spill.go:109`) writes a secret-scrubbed (config redaction **plus** the
  pattern scanner: AWS keys, tokens, PEM, JWT — `spill.go:126-127`) body to a
  per-uid `0o700` dir, sweeps it on retention (`spillRetention = 7d`, `:24`),
  and `resolveSpillReadPath` (`:43`) already lets every scoped read tool open
  files inside it. This is exactly the `winnow_recall` mechanism, already built
  and already on the read allowlist. **Reuse it; do not build a second store.**
- **An in-process child-agent primitive already exists** (`internal/agents` +
  the `Task` tool). The "nested loop that returns only a distilled result"
  (requirement 4) is a well-trodden pattern (§2.3c) and KajiCode already has the
  isolation machinery — the delta is running it for a *single* tool call.
- **Idempotence marker exists.** `isPrunedPlaceholder` (`prune.go:211`) and the
  `[pruned ` prefix mean an already-reduced body is recognized and never
  re-reduced. The gate must set the same kind of marker.

### 2.2 Measured on real sessions (this investigation)

Replayed the four largest sessions in `~/.local/share/kajicode/sessions` from
`events.jsonl` (7,087 tool results, 11.6 MB of tool output):

| tool | calls | total | avg | >8 KB |
|---|---|---|---|---|
| bash | 4,792 | 6.56 MB | 1.3 KB | 1.18 MB |
| read_file | 639 | 3.77 MB | 5.8 KB | 2.24 MB |
| web_fetch | 13 | 0.31 MB | **23.0 KB** | 0.30 MB |
| grep | 69 | 0.09 MB | 1.3 KB | 0.00 MB |
| web_search | 23 | 0.06 MB | 2.5 KB | 0.00 MB |
| browser_snapshot | 14 | 0.08 MB | 5.7 KB | 0.06 MB |

- **73.4 %** of tool bytes are in results > 2 KB; **34.1 %** in results > 8 KB;
  **12.0 %** > 30 KB. So there *is* mass, and it is concentrated in `bash`,
  `read_file`, `web_fetch`, `browser_snapshot` — i.e. "everything else", not just
  grep/reads. This matches Anthropic's own finding that ~96 % of a long baseline
  run's tokens were file-read results.
- **Error gate.** Of the 9.2 MB in results ≥ 1.5 KB, **42 %** carries an error
  signal (traceback / `error` / `failed` / non-zero exit / "not found" / non-`ok`
  status) and must never be gated. **58 %** (5.3 MB) is non-error, and is where
  any gate can act.
- **Re-reference proxy (regret exposure).** Of 1,211 non-error results ≥ 1.5 KB,
  only **12 %** have their target (`path`/`pattern`/`command`/`url`) re-referenced
  within the next 60 events; **88 %** are not. This is the *same weak proxy* the
  compaction reports used, and it is deliberately conservative (it counts a
  re-read that a gate would make cheap). It bounds the upside: most large
  non-error results, on this data, are not looked at again — **but 12 % are**,
  so the recall path is what makes any gate safe.

**Honest reading:** the deterministic size budget already removes the 2.3 % of
bytes in >100 KB results and the head/tail of everything over the ceiling. The
gate's marginal win is therefore **relevance, not size** — exactly the finding
the compaction work reached. That is a reason to ship it **off by default and
shadow-measured**, not a reason to skip it.

### 2.3 External research (primary sources; see `docs/JEV_COMPACTION_IMPROVEMENT_PLAN.md` §3 for the compaction-side refs)

**a. `GhalebDweikat/winnow` — the closest implementation of exactly this.**
Read from `docs/DESIGN.md`, `sidecar/src/winnow/{hooks,questions,policy,stub,config}.py`:

- Split a large result into ~25-line **blocks**; **one `noul` per block** *plus a
  shared error question*, judged against an explicit `task`.
- Gate logic is deterministic (`policy.decide`): hide **only** p < `WINNOW_DROP`
  (**0.1**); `drop ≤ p < WINNOW_KEEP (0.5)` is *uncertain → keep*; a block with no
  answer is **kept**; if an **error** is present, **keep everything**; if hiding
  would save less than `WINNOW_MIN_PRUNE_RATIO (0.2)`, keep everything.
- Hidden blocks are **cached** and replaced by a **stub naming a recall key**;
  `winnow_recall` returns the full text. *"The summarizer is only there on what
  the judge already decided to hide, so it is cheap and its mistakes are
  recoverable."*
- **Shadow mode** exists but *"cannot measure regret: nothing is hidden, so
  nothing is recalled."*
- **Measured on one developer's transcripts:** ROC AUC ≈ **0.70**
  (lexical baseline 0.635); the `p < 0.1` bin was **0 % needed** (perfectly
  clean), above 0.1 Jev is *underconfident*; expected live effect *"about **5 %**
  of large-result text hidden, at zero hand-label regret. Modest, honest."*
  Latency: 86 ms median replay; **370 ms** via command hook; **16 ms** resident.
  Cost: **$0.036 for 300 cases**.

**b. `tamaratran/fast-jev-compaction`** — for the compaction side, but its
**two-question shape** is reusable here: a per-*call* noul ("knowing this call
was made still matters") and a per-*result* noul ("the full output should stay
verbatim; re-running would not do"), `keepThreshold` 0.5, 3-way decision, **no
summarizer**, and *"no result is ever left without its call."* Its calibration
note is the honest one: *"a probability is not a proof that a result is safe to
delete. The assistant can always re-run the tool."*

**c. Nested child loop / sub-agent isolation (requirement 4).** A child context
that returns only a distilled result is standard, and KajiCode already has the
primitive:
- **Claude Code `Agent` tool** — *"Spawns a subagent with its own context
  window… The parent doesn't see the subagent's intermediate tool calls or
  outputs, only that final result."*
- **Cursor subagents** — Explore/Bash/Browser exist *"based on analysis of agent
  conversations where context window limits were hit"*; *"Intermediate output
  stays in the subagent."*
- **Anthropic context-engineering** — subagents *"return only a condensed,
  distilled summary (often 1,000–2,000 tokens)"*; names compaction, note-taking,
  and multi-agent as the long-horizon techniques.
- **OpenAI Agents SDK `Agent.as_tool()` + `custom_output_extractor`** — nested
  run, *"modify the output of the tool-agents before returning it to the central
  agent."* (Its own bug, `openai-agents-js#659`, is the context *leaking* into
  the child — the exact failure the design must avoid.)

**d. The decisive negative result — this is why the plan is gated on a replay.**
`pi-warden`'s *Relevance compaction* is the same idea (one noul per unit, keep
≥ 0.5) and it shipped **OFF**, because: *"In a replay of 48 recorded compactions
its summary was 4.7× the size of Pi's at the median and **kept whole only 1 of
the 34 files the agent read again**."* An LLM/Jev relevance filter on whole tool
results **must be validated on re-fetch behaviour before it is trusted.** Its
**chunked `score` context filter** is the variant that did work, and the note is
sharp: *"the threshold, not the ranking, made the gain."*

**e. Failure modes to design against** (all `[SRC]` unless noted): per-call
latency tax 150–500 ms and a 40 req/s provider limit; dropping the one line that
explains a failure (winnow's error gate exists for this); `Classifier Context
Rot` (arXiv 2605.12366) — classifier recall collapses with state length and is
**worst for the middle** of the input, so a whole result must be **blocked, not
sent as one blob**; batching shifts probabilities ~0.05 and agrees with
one-question-per-request only ~85 % of the time; **prompt injection** — Jev
*"does not treat state as hostile by default"* (TypeSafe jaggedness #6), so tool
output is untrusted input to the judge; **judge calibration ≠ usefulness**
(winnow: *"a judge that says 0.5 to everything is perfectly calibrated… and can
hide nothing"*) → always read ROC AUC and the **low tail**, never ECE alone.

---

## 3. Root cause

Three facts, each verified in code:

1. **There is no relevance stage at tool time.** The only tool-time reduction is
   size/category (`output_boundary.go:20`), because the hook channel
   (`loop.go:2060`) deliberately exposes neither the body nor the task.
2. **Bloat is therefore committed to the transcript permanently** at
   `loop.go:909` and only ever cleaned up later, at compaction, when the model
   has already paid for it in every intervening turn.
3. **The capability the fix needs already exists** — classifier seam
   (`classifier.go:75`), judge question/state/decision patterns
   (`compaction_judge.go:482/451/204`), recall net (`spill.go:109`), child-agent
   primitive (`internal/agents`). The gap is a **call site**, not an
   architecture.

The user's specific complaint is a consequence: because there is no gate, the
*yorst* outcome (a big, mostly-irrelevant result) and the *best* (a big, needed
one) enter context identically, and there is no way to re-ask for a better one.
Compaction's own measured result — **relevance preserved, token total flat** —
is precisely what a tool-time gate would fix *earlier*, which is why this phase
exists as the prerequisite the improvement plan names (Option D).

---

## 4. Options compared

The requirement is separable into (i) filter at tool time, (ii) recall, and
(iii) re-query. Options differ in which they cover and at what cost.

| # | Option | Covers | Fit / reuse | Complexity | Main risk |
|---|---|---|---|---|---|
| A | Harden deterministic truncation (bigger budgets, smarter head/tail) | i (partly) | Trivial | Very low | Already near its ceiling; no relevance; the "silent truncation makes agents lie" failure |
| B | **Classifier block-gate at the append site** (winnow shape) | i + ii | **High** — reuses classifier seam, judge patterns, `SpillOutput` recall, `isPrunedPlaceholder` idempotence | Medium | Latency per call; dropping a needed block; over-filtering |
| C | Classifier/re-­model **replaces** the dispatch, returns only a distilled answer | i+ii+iii | Low — needs a generator, not a classifier | High | Non-determinism, cost, loop; not what a classifier does |
| D | **Nested child loop per tool call** (sub-agent isolation) | i+ii+iii | Medium — primitive exists (`internal/agents`, `Task`) | High | Doubles latency per gated call; context leak into the child (the `as_tool` bug); recursion |
| E | **Interactive re-query loop** (gate re-issues a narrower call until satisfactory) | ii+iii | Low — needs reformulation = a model | High | Unbounded loops, cost, silent churn |

**Decision.**

1. **B is the foundation and is shippable on its own.** It is the smallest
   change that is a strict improvement, it reuses four existing mechanisms, and
   it is the same shape two independent implementations converged on
   (winnow's gate; fast-jev's two-question idea). It is **fail-open** and
   **opt-in**, so the default path is byte-identical.
2. **A is retained as the fallback** (it already runs) and becomes the
   "keep everything" outcome of B, not a separate feature.
3. **E is rejected as a design.** Re-query needs *generation* (reformulate
   arguments), which a probability classifier does not do; an unbounded
   "ask again until satisfactory" loop is exactly the silent-churn failure mode
   §2.3d warns about. **Its useful half — "get a better result without polluting
   context" — is D, and D is deferred behind B's measurement.**
4. **D is the honest realization of requirement 4, and is Phase 2**, gated on
   B's replay result. It is presented in §5.7 so the plan is complete, not so it
   ships first.
5. **C is rejected**: it is a different capability (an agent), it needs a
   generator, and B+D already deliver the outcome with less machinery.

**Chosen: B now, measured in shadow first; D as Phase 2 only if B's replay
shows the gate is safe and worth its latency.**

---

## 5. Proposed solution (detailed)

### 5.1 The gate, and where it sits

One new helper in `internal/agent`, called at the single funnel before the
append at `loop.go:909` (and before the parallel-batch results are consumed, so
both serial and concurrent tool runs pass through one gate):

```
toolResult = maybeGateToolResult(ctx, options, messages, call, toolResult)
```

It mutates **only `result.Output`**, and only when it decides to. Status,
`ToolCallID`, `ChangedFiles`, `FileChanges`, `Display`, `Images`, `Meta` are
preserved; `Meta` gains the gate's decision fields. `Truncated` is set `true`
when blocks are dropped (consistent with the existing meaning of the flag).

**Byte-identical when off:** `options.ToolResultGate == nil` returns the result
untouched. This is KajiCode's house convention (`CompactionJudge`, `Trace`,
`Profile` all behave this way) and is what keeps the existing 81 test packages
green without touching them.

### 5.2 Free pre-screen — run before any network call

The gate only spends a classifier request when it can change the outcome
(winnow `worth_judging`; jev-belay `needsDoneCheck`). **All** must hold:

1. `options.ToolResultGate != nil` and the feature is enabled.
2. Tool is **gate-able**: not in a denylist
   (`write_file`, `edit_file`, `apply_patch`, `multi_edit`, `todo_*`,
   `update_plan`, `ask_user`, `request_permissions`, `Task`, `TaskOutput`,
   `TaskStop`, `GenerateAgent`, `skill`, `recall`, `recipe_run`, `tool_search`,
   `batch`, plus the media/display-bearing readers whose output is an
   image/attachment — those are already correctly handled elsewhere).
3. `len(result.Output) >= gateMinBytes` (**1500** — winnow's `WINNOW_MIN_CHARS`;
   below it a stub saves nothing).
4. **No error signal** — the deterministic belt is **line-anchored and
   structured only**: `panic:`, `fatal error:`, `Traceback (most recent call
   last)`, `--- FAIL:`, a bare `FAIL`, `Command failed with exit code [1-9]`, or
   a non-OK status. A bare word scan for `error`/`failed` is deliberately NOT used
   — audited on real sessions, every such match was Go source containing the word
   `failed`, an 8% false-positive rate that silently disabled the gate. A prose
   failure is covered by the classifier's shared error question instead.
5. Not already a gate/prune placeholder (`isPrunedPlaceholder`).
6. Body is block-splittable into **≥ 2** blocks (a single-block result cannot
   be partially hidden). Blocks close at **25 lines** *or* **1500 bytes**,
   whichever comes first, so a short-but-large body (a ~17-line `web_search`, a
   terse but wide `bash` dump) still yields ≥ 2 blocks. A single-line body is one
   block and is skipped.
7. An **already-truncated** result is gated like any other. Its budget
   truncation is not an exemption — the largest `web_fetch`/`read_file`/
   `browser_snapshot` bodies are exactly the ones the output budget marks
   truncated. When the budget already spilled the body, the gate **reuses** that
   `spill_path` (it holds the fuller pre-budget view) — but only after
   `tools.ResolveSpillReadPath` proves the path is a real file inside the spill
   root a read tool can open; a foreign or swept pointer falls back to a fresh spill.

A result skipped by any of these is returned **byte-identical**.

### 5.3 Judge — blocked, not one blob

- Split `output` into blocks of **~25 lines** (`splitBlocks`, deterministic,
  line-preserving).
- **One `noul` per block** — *never* send the whole result as one question.
  `Context Rot` (arXiv 2605.12366) makes mid-input recall collapse, which is
  precisely why winnow blocks.
- Question phrasing and criteria borrowed from the shipped compaction judge
  for consistency (`compaction_judge.go:482-499`), adapted to a block and with
  the winnow `structured` idea folded in as `criteria`:
  - instructions: *"Would the assistant have to read this block to accomplish the
    task correctly? Judge only this block."*
  - `criteria.true`: *"The assistant must read this text to do the task right."*
  - `criteria.false`: *"The task can be completed correctly without ever reading
    this block. Do not mark a block needed only because it comes from the same
    file or command as something that is needed."*
  - **Plus one shared error question** for the whole result (winnow's
    `ERROR_QUESTION`) — a second, cheap belt above the regex.
- `State`: `{goal, tool:{name, input}, blocks:{b001:…, b002:…}}`, where `goal`
  is `compactionGoal(messages)` (reuse — it already picks the most recent real
  user objective and skips the synthetic continuation cue).
- **Every judge-bound string passes through `scrubForClassifier`**
  (`compaction_judge.go:478`, `redaction.RedactString`) *and* the gate must
  additionally treat tool output as **untrusted** (§2.3e injection): the
  instructions are built by the gate, never taken from the body, and the body is
  always a *value* in the state, never spliced into an instruction.
- Batching: pack blocks under the existing `judgeStateByteBudget` (30 KB,
  `compaction_judge.go:49`) and cap blocks per pass; a batch that errors
  contributes nothing (fail-open per batch, as `judgeAnswers` already does,
  `:266`).

### 5.4 Decision — deterministic, three-part

Reuse the exact semantics the compaction judge already ships
(`compaction_judge.go:204`, `judgeOptions`):

```
p >= keep (0.5)   -> keep block verbatim
drop<=p< keep     -> KEEP (uncertain; keeping is cheap and safe)
p <  drop (0.1)   -> hide block
no answer for id  -> KEEP block
error question fired, or regex belt hit -> keep the WHOLE result
```

Then the retention gate:

```
dropRatio = hiddenBytes / totalBytes
if dropRatio < minPruneRatio (0.2) -> keep everything, byte-identical (reason "below_min_prune_ratio")
```

`drop = 0.1` is not arbitrary: it is the one bin winnow hand-labelled as
**100 % clean** (0 % needed). `minPruneRatio = 0.2` is winnow's. Both are
**borrowed from winnow's file-read workload** and are flagged as **to be
calibrated** on KajiCode data before the default is trusted (§8.3).

### 5.5 Rewrite — keep verbatim, hide behind a recall path

When the decision is "gate":

- Keep the surviving blocks **in original order and verbatim**; replace each run
  of hidden blocks with a marker: `[… N lines hidden …]`.
- **Spill the full original body first** via the existing
  `tools.SpillOutput(call.Name, output)` (`spill.go:109`). It is already
  scrubbed, retention-swept, and readable by every scoped read tool
  (`resolveSpillReadPath`, `spill.go:43`) — so **no new store, no new read tool,
  no new lifecycle**.
- Append a stub naming the path and the reason, in the winnow shape:
  `[gate] 148 of 210 lines hidden (relevance <= 0.22); full output saved to <path>; read_file or grep it if you need it`.
- Set `result.Meta`: `gate_decision=pruned|kept|error_present|below_min_prune_ratio`,
  `gate_hidden_lines`, `gate_relevance_max`, `spill_path`.
- **Fail-open everywhere:** spill failure, classifier error, zero answers, or
  a below-ratio decision all return the **original** result unchanged.

### 5.6 Configuration (4-place rule)

Add a sibling to `CompactionFeature` (`profile.go:94`):

```go
type ToolResultFeature struct {
    Enabled        bool    `json:"enabled,omitempty"`
    KeepThreshold  float64 `json:"keepThreshold,omitempty"`   // default 0.5
    DropThreshold  float64 `json:"dropThreshold,omitempty"`   // default 0.1
    MinPruneRatio  float64 `json:"minPruneRatio,omitempty"`   // default 0.2
    MinBytes       int     `json:"minBytes,omitempty"`        // default 1500
}
```

Plumb it exactly where `Compaction*` already goes:
`classifier.Features` (`profile.go:90`) → `config/types.go:304` (the field is
the whole `classifier.Config`, so the 4 places are `types.go` load/save +
`resolver.go` merge + `validate.go` check) → `Options` (`types.go:371`) →
`classifier_runtime.go:9` builder (a `toolResultGateForRun` beside
`classifierForRun`, and the ACP equivalent at `acp/agent.go:1823`) →
`cli/exec.go:673`, `cli/app.go:945`, `acp/agent.go:950`.

Connecting a classifier still enables **nothing**; `features.toolResult.enabled`
is the separate explicit step, and it defaults **off**.

### 5.7 Phase 2 — the re-query / "no context pollution" half (Option D)

> **Status: implemented, opt-in off.** Shipped as the bounded re-query loop in
> `internal/agent/tool_gate_requery.go` (report §1.2). It is simpler than the
> child-agent sketch below: the generator is a single tool-less
> `Provider.StreamCompletion` call, the generated call is executed directly (never
> through the loop, so no attempt enters the parent transcript), and the improved
> text is appended after the stub. Enable with
> `classifier.features.toolResult.requery`. The child-agent variant below remains
> the higher-isolation option if the tool-less generator proves too weak.

Requirement 3+4 done the way the ecosystem does it (`Task`/subagent isolation,
§2.3c): when the gate decides a result is **mostly irrelevant** (`dropRatio ≥
0.8`) *and* the tool is re-queryable, the gate hands the parent's question and
the raw result to a **child agent** that:
- has its own context (the raw result never enters the parent transcript),
- may re-issue a narrower call (its own tools),
- returns **only** a short distilled answer, which the gate appends in place of
  the raw output.

Constraints that make this safe: bounded (one child, `steps` cap, one retry),
fail-open to the Phase-1 stub, reuses `internal/agents` and the existing
background-Task plumbing, and is **gated on Phase 1's replay result**.

---

## 6. Implementation steps

> Order matters. 1–3 change nothing observable (nil ⇒ byte-identical). 4 turns
> it on. 5–6 measure before any default is changed.

1. **`internal/classifier/profile.go` — `ToolResultFeature` + defaults + the
   `Features` field.** *Why:* one definition of the feature's knobs, shared by
   config/CLI/TUI exactly like `CompactionFeature`. Test: zero value ⇒ inert;
   each `Effective*` helper falls back to the documented default.
2. **`internal/config/{types,resolver,validate}.go` — the 4-place rule.**
   *Why:* the feature must round-trip through config exactly like compaction.
   Test: load→save→load preserves `features.toolResult`; a bad threshold is a
   validation issue.
3. **`internal/agent/tool_gate.go` (new) — the gate.** `maybeGateToolResult`,
   `gateBlocks`, `belt`, `gateDecision`, `renderGated`. *Why:* the whole
   feature, in one focused file (~200 lines, per AGENTS.md). Test (table):
   error belt keeps everything; below-ratio keeps everything; uncertain band
   keeps; a confident-no block hides; no-answer keeps; nil classifier
   byte-identical; already-gated body untouched.
4. **`internal/agent/loop.go` — call it at the funnel.** After
   `executeToolCall` and after the parallel-batch result is read, before
   `loop.go:909`. *Why:* the single append site is the only correct place.
   Test: a stubbed classifier gates a big non-error result and the appended
   `Content` is the gated text; the result's `ToolCallID`/status are unchanged.
5. **`internal/agent/types.go` + the three runtime builders
   (`cli/exec.go`, `cli/app.go`, `acp/agent.go`) — wire the option.** *Why:*
   without it the gate never runs; with a nil classifier every path is
   unchanged. Test: nil ⇒ `maybeGateToolResult` returns the input.
6. **Shadow + replay measurement harness (temporary, then removed).** Drive the
   real loop over the four sessions with the gate in **shadow** (log decisions,
   change nothing), then in **enforce**, and report the metrics in §8.3. *Why:*
   §2.3d is a direct warning that this class of feature fails when unmeasured.

---

## 7. Files affected

**Created**
- `internal/agent/tool_gate.go` — the gate (blocks, belts, decision, render).
- `internal/agent/tool_gate_test.go` — the table tests above.

**Modified**
- `internal/classifier/profile.go` — `ToolResultFeature`, `Features.ToolResult`,
  `Effective*` helpers, defaults.
- `internal/config/types.go` — the `Classifier` field is unchanged (feature
  rides inside it); touch only if a raw-JSON key list needs the new block.
- `internal/config/resolver.go`, `internal/config/validate.go` — merge/validate
  the new feature's thresholds.
- `internal/agent/types.go` — `Options.ToolResultGate` + thresholds (+ a
  `ToolResultGateErr`? no — fail-open needs no error channel).
- `internal/agent/loop.go` — one call at the funnel.
- `internal/cli/classifier_runtime.go`, `internal/cli/exec.go`,
  `internal/cli/app.go`, `internal/acp/agent.go` — build/thread the gate.
- `docs/architecture.md` — one line: the tool boundary now has an optional
  relevance gate before append.
- `internal/cli/classifier_config.go` (only if the existing `classifier`
  subcommand should toggle the feature) — reuse the existing write path; do not
  add a new command surface otherwise.

**Removed**
- Nothing in this phase. (If Phase 2 lands, the Phase-1 stub path stays as its
  fail-open fallback — it is not replaced.)

No new dependencies. Nothing outside the workspace is touched.

---

## 8. Testing and verification

### 8.1 Reproduce the "before" (no gate) — the baseline

The gate is a missing path, so "reproduce" means **measure the baseline it
improves**:

```bash
# 1. Confirm no gate exists on the tool path:
grep -n "ToolResultGate\|maybeGateToolResult" internal/agent/loop.go      # expect: nothing
# 2. Confirm the only tool-time reduction is size/category:
sed -n '18,47p' internal/tools/output_boundary.go
# 3. Reuse the §2.2 replay to re-derive the numbers (errors 42%, >8 KB 34%,
#    re-reference 12%) from ~/.local/share/kajicode/sessions/*/events.jsonl
```

### 8.2 After the change — unit and integration

- **Unit** (step 3 tests): error belt; below-min-ratio; uncertain band; hide a
  confident-no block; no-answer keeps; nil classifier byte-identical;
  idempotence; the denylist never gates a mutating/display tool; a result with
  `Images` is never gated.
- **Integration** (step 4 test): a stubbed classifier + a `grep`-like tool
  returning 200 lines with 10 relevant ones ⇒ the appended message contains the
  10 in order, a `[… N lines hidden …]` marker, and a `spill_path`; the spill
  file exists, is readable, and round-trips byte-for-byte (**recall**).
- **Regression:** `go test ./internal/agent ./internal/tools
  ./internal/classifier ./internal/config ./internal/cli ./internal/tui` with
  the gate **unset** must be green and identical to `main`.

### 8.3 The measurement that decides whether it ships enabled

Run the real loop over the four sessions, gate OFF vs **shadow** vs **enforce**,
4 sessions in parallel, `growwcorp/DeepSeek_V4.1_Flash` + real `jev-1.13`:

| metric | why it decides |
|---|---|
| **blocks hidden / kept**, per tool | is there mass? |
| **regret** = recalled keys / pruned keys, over a **longer (edit-oriented) continuation** | pi-warden's failure (`1 of 34`) is exactly this number; it must be ~0 |
| **ROC AUC** of `p(needed)` against a re-reference label, **per session** | winnow: read AUC + the **low tail**, not ECE |
| **clean-tail check**: `P(needed)` in the `p < 0.1` bin | 0.1 is only safe if this bin is clean *on KajiCode data* |
| **latency** per gated call (p50/p90) and requests/turn | 150–500 ms/call and 40 req/s are real limits |
| **tokens added/removed**, next-turn context | the compaction finding says expect **flat**; do not claim size wins |
| **error-belt recall**: did any result with a real error get gated? | must be 0 |

**Acceptance gate (hard):** ship enabled only if (a) error-belt recall is 100 %,
(b) regret is ~0 on a re-fetch-oriented continuation, and (c) latency p90
**≤ ~300 ms** with a **shadow-first default**. If regret is non-trivial, the
`0.1` bin is dirty and the feature must be re-calibrated (or dropped) — do not
lower the threshold to "make it work".

### 8.4 Build / type / lint / security

```bash
make fmt-check
go vet ./...
go test ./...
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...
```

`govulncheck` is required: the gate writes spill files on the tool hot path and
sends tool output to a remote endpoint.

### 8.5 Edge cases

- nil classifier ⇒ byte-identical (the whole default path).
- Classifier timeout/error/no-answer ⇒ original result (fail-open).
- Spill write fails ⇒ original result, no stub.
- Result already a spill/prune placeholder ⇒ untouched.
- Result carrying `Images` / `Display`-only output ⇒ never gated.
- Non-idempotent tool (`web_fetch`, deleted file) ⇒ hidden text is still
  recoverable via the spill, never by re-running.
- Provider replay: the message role/`ToolCallID`/`ToolCalls` are untouched, so
  no dangling `tool_use`.
- A result that *looks* fine but contains a failing test ⇒ covered by the
  error belt (regex **plus** the shared error question).

---

## 9. Cleanup

- Delete the §8.3 shadow/replay harness and any `/tmp` scratch after the numbers
  are folded into a report `docs/tool-gate-report.md`.
- If, after measurement, the single-threshold path is used and the band unused,
  delete the unused branch (mirroring what the compaction band did).
- Remove any `/tmp` classifier-key file; the key lives only in the credential
  store.
- No comment churn; no dead code — the pre-screen helpers exist only if used.
- Update `docs/JEV_COMPACTION_IMPROVEMENT_PLAN.md` §4 Option D to point at the
  shipped result, so it is not re-derived later.

---

## 10. Risks

- **The one that matters: this class of feature has a documented failure.**
  pi-warden shipped the same idea **off** because it *"kept whole only 1 of the
  34 files the agent read again."* The plan's acceptance gate (§8.3) exists
  precisely to catch that, and the default stays **off** until it passes. Treat
  a flat/negative replay as the expected possible outcome, not a surprise.
- **The win is relevance, not size.** The measured data says 34 % of bytes are
  in >8 KB results but 88 % of large non-error results are not re-referenced;
  the deterministic budget already cuts the extreme tail. Expect a
  **context-quality** win and a possibly-flat token total — the same honest
  conclusion the compaction work reached.
- **Latency on the hot path.** One classifier round trip per gated call
  (150–500 ms measured elsewhere; 40 req/s provider cap). Mitigated by the free
  pre-screen (§5.2), batching, the 8 s `DefaultTimeoutMS`, and fail-open.
- **Threshold borrowing.** `keep 0.5 / drop 0.1 / minRatio 0.2 / minBytes 1500`
  are winnow's file-read numbers, **not calibrated on KajiCode's `bash`/`Task`/
  `browser_snapshot` mix**. Flagged; §8.3 calibrates before the default is
  trusted.
- **Prompt injection.** Tool output is untrusted and Jev does not treat state as
  hostile by default. Mitigation in §5.3 (body is always a value, instructions
  are gate-authored) is necessary but not sufficient; it is a real residual risk
  for any gate that reads fetched content.
- **Recall is necessary, not sufficient.** Making the dropped bytes reachable
  does not prove the model *uses* the recall path — the compaction work found
  the same gap. The regret metric (§8.3) is the only evidence that counts.
- **Scope/sequencing.** Phase 2 (the re-query loop) is now implemented but ships
  **off by default** and only on the read-only auto-allowed tool set: it adds one
  tool-less model call (plus up to `maxRequery` more) per gated result, so it
  inherits the latency, not the context-leak risk — the attempt never enters the
  parent transcript.
