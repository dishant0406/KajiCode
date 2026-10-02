# Tool-output gate — implementation + live measurement report

Status: **shipped, default off, measured.** This is the tool-time half of the
classifier pipeline (1 = compaction ✅, 2 = tool-output gate ← this, 3 =
completion / loop gating). The design plan is `docs/JEV_TOOL_GATE_PLAN.md`; this
file records what was built and what the live measurement actually showed.

---

## 1. What shipped

The gate lives at the single tool-result funnel. Before a tool result is appended
to the transcript (`internal/agent/loop.go`, the one append site), it is judged:

- **Split** into blocks of `gateBlockLines` (25) lines — or fewer when 1500 bytes
  (`gateBlockBytes`) is reached first — never the whole result as one question,
  because classifier recall collapses with input length, and never by line count
  alone, because a short-but-large `web_search` or terse `bash` dump costs
  context while having few lines.
- **Judge** one `noul` per block (`would the assistant have to read this block?`)
  plus a shared semantic error question, batched under `judgeStateByteBudget`.
- **Decide** deterministically: a block is hidden only when
  `p < dropThreshold`; the uncertain band `[drop, keep)` KEEPS; no answer KEEPs;
  a structured error signature or the semantic error question KEEPS the whole
  result; and if fewer than `minPruneRatio` of the lines would be hidden, the
  whole result is kept.
- **Render** surviving blocks verbatim, in order, with hidden runs replaced by
  `[… N lines hidden …]` and a stub naming a **readable spill file** — so a
  "dropped" block is recoverable with `read_file`/`grep`, not lost, and never by
  re-running a non-idempotent tool.
- **Fail-open** everywhere: a nil gate (the default), a classifier error, a spill
  failure, or a below-ratio decision all return the original result unchanged.

It is **off by default**: `Options.ToolResultGate == nil` on every path the
binary ships unless `classifier.features.toolResult.enabled` is set. Nil ⇒
byte-identical (proved by `TestGateNilClassifierIsByteIdentical` and the
regression suite).

### 1.1 Coverage fix (this revision)

The gate originally skipped two kinds of result before the classifier ever saw
them, and both swallowed the *largest* bodies (measured in §3.1):

- a hard **50-line minimum** — line count is not context cost, so a ~17-line /
  2.6 KB `web_search` was never judged;
- a blanket **skip of already-truncated results** — but the output budget runs
  *before* the gate, so the biggest `web_fetch`, `read_file`, `grep`, and
  `browser_snapshot` bodies were exactly the ones exempted.

Both guards are now gone. Blocks close at 25 lines **or 1500 bytes**, so any body
of ≥ 2 lines over 1500 bytes gates; a pre-truncated body is gated like any other
and **reuses its `spill_path`** when one exists (a fresh spill otherwise, and a
fresh spill when the pointer's file has been swept). `MinBytes = 1500` stays:
the sub-1500-byte mass is genuinely small and not worth a classifier call.

Files: `internal/agent/tool_gate.go` (+ `tool_gate_test.go`), the
`ToolResultFeature` config (`internal/classifier/profile.go`), the merge
(`internal/config/classifier.go`), and the wiring across
`agent/types.go`, `cli/{exec,app,acp,classifier_runtime}.go`, `acp/agent.go`.

### 1.2 The coverage signal + the bounded re-query loop

The gate alone can only *hide*; it cannot tell the model "and what's left is not
enough". That second signal, and the bounded attempt to do better, is a separate
opt-in layer (`classifier.features.toolResult.requery`, off by default):

- **Coverage question** (`gateCoverageQuestionID`) — one extra `noul` on the
  gated result: *is the text that survived enough to proceed?* A probability
  below `coverageThreshold` (default 0.6) triggers the loop. It is only asked
  when `requery` is on, so a plain filter pays nothing.
- **The loop** (`internal/agent/tool_gate_requery.go`) — a tool-less,
  out-of-band `Provider.StreamCompletion` call asks a *small* model to write a
  narrower read-only call; that call is executed (never through the loop, so no
  attempt enters the parent transcript), and the improved text is **appended
  after the stub**, never in place of it.

Four properties make it safe: it only runs read-only tools the policy already
auto-allows (`requeryAllowed`, same contract as the parallel read batcher); it is
bounded by `maxRequery`; it stops on a repeated identical call; and it strips
every interactive callback (`OnPermissionRequest`, `Hooks`, `OnPhase`,
`OnToolResult`, …) so an invisible retry can never surface a prompt or look like
an extra tool call. Fail-closed everywhere: any error leaves the stub as it was.

**Why adoption is unconditional (a fixed bug).** The loop originally kept its
result only when the classifier then judged that result *sufficient* — and, with
the same no-clean-tail distribution that limits the drop side, that verdict
almost never fired: measured live, the loop ran its 2 rounds and **discarded
them**. Requiring a blessing was the bug, not a safety measure: since the
improved text is appended after the stub, adopting it can only *add* information,
so a real re-query result is now always kept, and the recorded coverage is
re-asked about the adopted text rather than the pre-re-query stub.

---

## 2. The live measurement (the part that decides)

Driven through the **real** `maybeGateToolResult` over the four largest sessions
in `~/.local/share/kajicode/sessions` with the **real `jev-1.13`** classifier
(OpenRouter), 4 sessions in parallel.

### 2.1 The classifier has no clean low tail on this workload

975 judged blocks (240 candidate results):

| session | blocks | min | p10 | median | p90 | max | AUC |
|---|---|---|---|---|---|---|---|
| `…1146_1` | 324 | 0.19 | 0.42 | 0.55 | 0.63 | 0.70 | 0.758 |
| `…0107_336` | 194 | 0.15 | 0.33 | 0.47 | 0.57 | 0.73 | — |
| `…1521_35` | 191 | 0.10 | 0.31 | 0.54 | 0.61 | 0.68 | 0.522 |
| `…5423_39` | 266 | 0.14 | 0.27 | 0.50 | 0.60 | 0.73 | 0.721 |
| **all** | **975** | | | | | | **0.705** |

**AUC 0.705** — in line with winnow's 0.70 on its own workload, so the classifier
is doing its job. But the distribution is shifted **up**: the `p < 0.1` bin is
**empty**, and the mass sits at **0.3–0.7**. This is the finding that matters.

### 2.2 Threshold sweep (regret = a block that was *needed* but hidden)

`needed` is a supervised label: a result is "needed" when the session later
re-references the same target (`path`/`pattern`/`command`/`url`); a block inherits
its result's label. 405 of 975 blocks are needed.

| drop floor | blocks hidden | regret (needed lost) |
|---|---|---|
| 0.10 (winnow's) | 1 (0.1%) | 0 |
| **0.20 (shipped default)** | **19 (1.9%)** | **0** |
| 0.30 | 68 (7.0%) | 2 |
| 0.40 | 167 (17.1%) | 15 |
| 0.50 | 407 (41.7%) | 98 |

**The economy is real but small and constrained.** There is **no leakage-free
mode**: more hiding always buys regret (the 0.30 row already loses 2 needed
blocks; by 0.50 it loses 98). The shipped default is the lowest floor with
**zero regret on this data**.

### 2.3 The pipeline, end to end, at the shipped default

| session | candidates | gated | note |
|---|---|---|---|
| `…1146_1` | 60 | 0 | mostly `read_file`; nothing confidently irrelevant |
| `…0107_336` | 60 | 2 | old `bash` dumps |
| `…1521_35` | 60 | 1 | a legacy `glob` |
| `…5423_39` | 60 | 0 | |
| **total** | **240** | **3 (1.2%)** | |

The plumbing is proven to engage: at `drop<0.35` the same sample gates 2
results / 4,707 bytes; at `drop<0.50`, 10 results / 88,013 bytes.

### 2.4 The error belt was over-broad and is fixed

The first implementation scanned the whole body for `error`/`failed`/etc. Audited
against OK-status results, **every** match was Go source containing the word
`failed` in an identifier or comment (`failed := row.status == tools.StatusError`)
— an 8% false-positive rate that silently disabled the gate on most code-reading
results. The belt is now **line-anchored and structured only**
(`panic:`, `fatal error:`, `Traceback (most recent call last)`, `--- FAIL:`,
a bare `FAIL`, `Command failed with exit code [1-9]`), and the classifier's shared
error question covers prose failures. After the fix, every belt match on real data
is a **genuine** failure (`--- FAIL: Test…`, `Traceback …`).

---

## 2.5 A fresh-eyes review found four real bugs — all fixed

1. **The gate ran after persist/display/observe (high).** It was wired *after*
   `OnToolResult`/`task.observe`, so the session event log recorded the ungated
   body while the model saw the gated one — a resumed session would replay
   differently, and shadow decisions were unobservable. The call was moved to
   **before** `recordOutputBudgetTrace`/`task.observe`/`OnToolResult`, so the
   displayed and persisted result is exactly what the model sees. This is also
   what makes the shadow-mode metadata real.
2. **Already-truncated results were re-gated (medium→fixed).** Originally,
   gating a body the output budget had already truncated was judged to compound
   the loss and overwrite `Meta["spill_path"]`, so `result.Truncated`
   short-circuited before any classifier call. That guard was **removed in the
   coverage fix (§1.1)**: the bodies it exempted are the *largest* ones. The
   original concern is addressed instead by **reusing** a valid spill pointer
   (`tools.ResolveSpillReadPath`-validated) rather than spilling again.
3. **ACP cache key omitted the profiles (medium).** `classifier.New` derives the
   endpoint/model/key from the active profile, so editing a profile mid-session
   did not change the key and the stale client was reused. `classifierConfigKey`
   now fingerprints every profile field and all feature knobs.
4. **Batch packing over-counted (low).** `blockBytes` charged the full block size
   while `gateState` sends at most `judgeResultBytes`; batches now pack to what is
   actually sent.

Regression tests were added for each: multi-batch gating, edge/all-hidden
rendering, and meta preservation. (The already-truncated test was later
superseded by the coverage-fix tests in §1.1.)

---

## 3. Verdict, stated plainly

**The gate is correct, safe, and genuinely engaged — and on this workload its
effect is small.**

It matches the finding the plan predicted twice over: winnow's own result
(*"~5% of large-result text hidden, at zero hand-label regret"*) and pi-warden's
decision to ship the same idea **off** (*"kept whole only 1 of the 34 files the
agent read again"*). KajiCode is on the smaller end of that range: **~2% of
blocks hidden at the shipped default**.

The reasons are structural, not bugs:

1. **No clean low tail.** The classifier is well-calibrated (AUC 0.70) but
   under-confident: it rarely says "confidently irrelevant" about a block that
   came from a tool the model chose to call. A gate can only act on the tail, and
   the tail is thin.
2. **The deterministic budget already cut the extreme.** `output_boundary.go`
   already truncates by size/category, so the marginal, *relevance*-based win is
   small — the same conclusion the compaction work reached (relevance, not size).
3. **Tool-call arguments leak the target anyway.** As the compaction reports
   found, every `read_file {"path": …}` already names its file even without the
   gate, so the recall/regret signal is muted.

**Therefore the gate ships default-off**, exactly as the plan's acceptance gate
required, with a calibrated safe threshold (0.2) and the full plumbing available
to anyone who accepts the small effect for the recall path. It is not enabled on
the strength of a number this report does not show.

### 3.1 Coverage: the guards removed, the threshold re-checked

Replaying the gate's own pre-screen over the four sessions attributed each
result's bytes to the first guard that skips it:

| session | eligible | `below_minlines` | `pre_truncated` | `below_minbytes` | denylist |
|---|---|---|---|---|---|
| `…1146_1` | 65.1% | 12.2% | 8.1% | 9.0% | 5.0% |
| `…0107_336` | 49.1% | 12.5% | 3.8% | 25.6% | 6.4% |
| `…1521_35` | 59.0% | 14.8% | 0.0% | 18.4% | 6.5% |
| `…5423_39` | 58.7% | 11.9% | 2.3% | 19.1% | 7.2% |

`below_minlines` (12–15% of bytes: `bash` 1.68 MB, `read_file` 200 KB,
`web_search` 72 KB, `grep` 51 KB) and `pre_truncated` (0–8%: `web_fetch` avg
22.9 KB, `read_file`, `grep`, a 65 KB `bash`, a 64 KB `browser_snapshot`) are
both now eligible, so **ELIGIBLE rises from 49–65% to the high 70s**.
`below_minbytes` (9–26%) is genuinely small results and stays excluded.

**Re-calibration on the newly covered tools.** With the guards removed, 112
candidate results / 569 blocks were judged live (even sampled across the four
sessions, `jev-1.13`), labeling a block "needed" when its result's target is
re-referenced later:

| drop floor | blocks hidden | regret (needed lost) |
|---|---|---|
| 0.10 | 0 (0.0%) | 0 |
| **0.20 (shipped default)** | **0 (0.0%)** | **0** |
| 0.30 | 9 (1.6%) | 0 |
| 0.40 | 72 (12.7%) | 0 |
| 0.50 | 236 (41.5%) | 0 |

The distribution is still shifted high — **0 blocks below 0.20** (1.6% in
0.2–0.3, 11.1% in 0.3–0.4, 28.8% in 0.4–0.5, 58.5% ≥ 0.5) — so the `p<0.1` bin
stays empty and **0.2 remains the right default**: it is the lowest floor that
is still effectively free, and 0.3 is the first floor that hides anything (1.6%,
still zero regret on this sample). The 42%-hidden regime (0.5) still costs on the
larger 975-block sample (§2.2) even though this 569-block sample measured zero
regret there.

### 3.2 Latency (previously unmeasured)

Measured live, one judge call per candidate result (batched blocks), 112
candidates:

| p50 | p90 | p99 | max |
|---|---|---|---|
| 421 ms | 534 ms | 731 ms | 2.34 s |

The plan's gate was **p90 ≤ ~300 ms**; at 534 ms it is **missed**. Per-result
latency is bounded by the classifier's 15 s timeout and is fail-open, so the cost
is latency, not correctness — but this is the number a default-on decision has to
weigh, and it argues for shadow-first.

---

## 4. Honest limits (not smoothed over)

- **Ground truth is a string-reuse proxy**, the same weak one the compaction
  reports used. It counts a re-read that a gate would make cheap; precision is
  therefore modest by construction. The *relative* ordering (2% vs 42% hidden) is
  robust; the absolute regret count is a lower bound on what a human would call a
  loss.
- **Four sessions, one model, one classifier, one threshold.** Per-session AUC
  varies 0.52–0.76.
- **Re-reference is not re-use.** A hidden block the model never needed, and a
  hidden block it silently did without, are indistinguishable here.
- **Latency** was not measured against the plan's p90 ≤ 300 ms gate in the first
  report; it is measured now (§3.2): **p50 421 ms / p90 534 ms / p99 731 ms** —
  the p90 gate is **missed**, and the classifier is fail-open with a 15 s timeout.
- **Prompt injection**: the body is always a *value* in the classifier state and
  the instructions are gate-authored, never taken from the body — necessary but
  not sufficient (Jev does not treat state as hostile by default).

---

## 5. Cleanup

The measurement harnesses (the earlier `internal/agent/zz_measure_test.go` and
this revision's `internal/agent/zz_gate_calibration_test.go`) and all `/tmp`
scratch were removed after the numbers above were captured. No production code
depends on them. The permanent regression suite is
`internal/agent/tool_gate_test.go`, `internal/classifier/profile_test.go`, and
`internal/config/classifier_test.go`. The superseded
`TestGateSkipsAlreadyTruncated` was deleted (not skipped) and replaced by the
pre-truncated gating tests.

---

## 6. Live confirmation (real binary, real model, all four tool surfaces)

A live `exec` run with the gate ON (`classifier.features.toolResult.enabled`),
`growwcorp/DeepSeek_V4.1_Flash`, and the real `jev-1.13` produced a per-tool
`gate_decision` for every tool result, confirming the coverage fix end-to-end:

| tool | result | gate decision |
|---|---|---|
| `read_file` `pkg/big_service.go` | 89,135 B / 1,204 lines | **judged → kept** (0 hidden) |
| `grep` `TODO` `pkg` (head_limit 900) | 65,506 B / 725 lines | **judged → kept** (0 hidden) |
| `web_search` `"Go net/http Server timeouts 2026"` | 2,338 B / 15 lines | **judged → kept** (0 hidden) |
| `web_fetch` `https://go.dev/blog/go1.24` | 7,632 B / 344 lines | **judged → pruned** — 192 hidden / 150 kept, `gate_max_hidden_relevance` 0.190 |

The `web_search` and `grep` verdicts are the coverage fix working live: both were
previously **skipped** (`web_search` on the 50-line rule; `grep` on the
pre-truncated rule) and now reach the classifier. `web_fetch` was genuinely
pruned, and its spill file round-tripped: the stub advertises
`…/web_fetch-508977072.txt`, which is present (7,632 B, 341 lines) and contains
the hidden tail. `gate_max_hidden_relevance` of 0.190 — just under the 0.2 drop
floor — is the same threshold-edge fragility §4 already flags; the verdict
flipped between runs in the earliest live test for the same reason.

Provider usage for the run peaked at **77,933** prompt tokens (the compact
200k-class window is far away, so the compaction judge correctly did not fire).
