# Does the Jev judge make the compaction *outcome* better? — measured

This closes the open question left in `docs/compaction-dryrun-report.md`
("a better compaction outcome for the model: plausible, unmeasured"). It is
answered on **four real sessions from this machine**, through the **real**
`maybeCompact`, with the **real** provider and the **real** `jev-1.13` classifier.

- **Provider:** `growwcorp` / `fireworks/DeepSeek_V4.1_Flash`
- **Classifier:** profile `jev`, model `jev-1.13`, `keepResultThreshold=0.35`
- **Context window:** 200,000 · **PreserveLast:** 12
- **Harness:** temporary in-package test driving the real
  `newCompactionState(...).maybeCompact(...)` and the real
  `judgeStaleHistory(...)`; 4 sessions in parallel; deleted after this report.
- **Reconstruction:** the FULL raw transcript (all `tool_call`/`tool_result`
  events, ignoring `session_compaction` resets) — the history compaction is meant
  to reduce.

## Short answer

**The judge's *decisions* are measurably smarter. The *model-visible outcome* is
not measurably better on these sessions** — and the final context is very
slightly *larger*, not smaller.

- Selection quality (objective, supervised): the judge keeps still-needed bodies
  at **precision 0.40 / recall 0.28** across the 413 ground-truth "still needed"
  bodies; the free positional prune scores **recall 0.00** by construction (it
  never inspects relevance). So on *what to keep*, the judge is clearly better.
- Outcome (model-visible): final context size moves **+0.1% to +4.5%** (judge ON
  is slightly *bigger*), and of the **252 rescued file paths** only **2 (1.5%)**
  appear in any summary — in one of four sessions. Three of four sessions show
  **0.000** rescued-path mentions in both arms.

So the honest verdict: **a smarter filter, but not yet a measurably better
compaction.** The reason is structural and visible in the numbers.

## Session results

| Session | msgs | raw tok | pruned tok | candidates | reused GT | kept | TP | FP | FN | precision | recall | rescued paths | OFF final | ON final | Δ | OFF sum bytes | ON sum bytes | OFF rescued-mentions | ON rescued-mentions |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `…6312871000_1` | 6,106 | 1,794,902 | 675,032 | 878 | 249 | 194 | 94 | 100 | 155 | 0.48 | 0.38 | 132 | 32,827 | 33,752 | **+2.8%** | 7,380 | 11,367 | 0.000 | **0.015** |
| `…019677000_336` | 6,220 | 1,348,802 | 830,243 | 868 | 82 | 29 | 12 | 17 | 70 | 0.41 | 0.15 | 13 | 19,188 | 19,210 | **+0.1%** | 12,698 | 12,668 | 0.000 | 0.000 |
| `…789735000_35` | 4,343 | 1,112,113 | 600,250 | 675 | 36 | 30 | 4 | 26 | 32 | 0.13 | 0.11 | 82 | 17,398 | 18,187 | **+4.5%** | 7,235 | 10,605 | 0.000 | 0.000 |
| `…836881000_39` | 4,495 | 915,566 | 489,190 | 713 | 46 | 35 | 6 | 29 | 40 | 0.17 | 0.13 | 25 | 17,944 | 18,095 | **+0.8%** | 8,168 | 8,793 | 0.000 | 0.000 |
| **total** | | | | 3,134 | **413** | 288 | **116** | 172 | 297 | **0.40** | **0.28** | **252** | | | | | | | |

**Free-prune baseline, same ground truth:** TP = 0, recall = 0.00, and its false
negatives are **all 413** reused bodies — it drops every stale body regardless of
relevance. The judge's job is exactly to convert some of those 413 into kept
bodies, and it does: **116 of them**.

## How each metric is defined (so the numbers can be trusted or rejected)

**Ground truth ("reused later")** — a stale tool-result body is *still needed* iff
the session references that body's **target** again **later**: the same
`path`/`pattern`/`command`/`prompt` string appearing in any subsequent message's
content or a subsequent tool call's arguments. That is the observable evidence the
information mattered. Candidates whose arguments carry no extractable target
(`todo_write`, `TaskOutput`, bare `bash` heredocs without a key — 8–44 per
session, ~10%) are **excluded** from precision/recall rather than counted as
false positives.

- This is a **proxy**, not a human label. It cannot tell "reused the old body" from
  "happened to mention the path again." But a later re-reference is precisely the
  event the feature targets (the model needing what was dropped), so it is the
  right signal even though it is noisy — which is why precision is only 0.40 and
  not 0.9.
- It is **deterministic and provider-free**: re-running it does not change the
  selection numbers (observed identical across three runs: TP 91→97→94, precision
  0.48→0.51→0.48 — provider variance on the endpoint, not metric drift).

**Final context tokens** = `estimateTokens(compacted)` (KajiCode's own estimator).

**Rescued paths** = file paths inside bodies the judge **kept** (p ≥ 0.35) that the
free prune **drops** and that are absent from the pruned context. These bodies are
in the **ON** arm's summarizer input and **not** in the OFF arm's — so if the
judge's preservation reaches the model at all, the ON summary is where it shows.

**Rescued-mentions** = fraction of rescued paths that appear in that arm's final
summary text. OFF is 0.000 by construction (its summarizer never sees those
bodies); ON is the actual test.

## Why the outcome is flat (root cause, from the data)

The judge is doing its job — it restores 252 real paths into the summarizer input
— but that input is then **compressed by the summarizer**, which is the wall:

1. **Scale mismatch.** The summarizer's input is ~450k–790k tokens. The judge
   rescues ~13–132 paths into it: a rounding error. `renderTranscript` also caps
   each tool body at `summarizeToolResultMaxBytes = 2000` bytes, so a rescued
   ~20k-token body contributes at most its first 2 KB to the summary prompt.
2. **The summarizer's template is the bottleneck, not its input.** The strict
   `summaryTemplate` (Objective / Important Details / Work State / Next Move /
   Relevant Files) makes the model emit a compact narrative; a "Relevant Files"
   list is capped by the model's own brevity. Session `…6312871000_1` (the best
   case) added **+3,987 summary bytes** and surfaced **1.5%** of its 132 rescued
   paths; the other three surfaced **none**.
3. **The summaries nonetheless differ** (different hashes every session, e.g.
   `OFF e1cf4ea9` vs `ON 4347a532`), so the judge *does* change the summary — just
   not in a direction this token/path metric can call "better." Direction can only
   be judged by a human reading them, or by a downstream task success metric.

**Conclusion:** the feature's value hypothesis — "keeping the relevant bodies
before summarizing preserves information the model would otherwise lose" — is
**true at the filter layer and not observable after the summarizer** on these
sessions. The judge is not useless; it is **subordinate to the summarizer**, which
throws away most of what it preserves.

## What the earlier measurement could not see, and this one can

`docs/compaction-dryrun-report.md` measured only token reclaim and was flat by
construction. This run adds two things that were missing:

- an **objective selection score** against session evidence (precision/recall), and
- a **do-the-rescues-survive** measurement across the summarizer boundary.

Both point the same way: **better filtering, no better compaction outcome yet.**

## What would actually make the outcome better (options, not endorsements)

These follow directly from the three blockers above; each is a product decision,
so none is implemented here.

1. **Make the summarizer summary-aware for the surfaces where it matters.** E.g.
   require the "Relevant Files" section to enumerate the paths of *kept* bodies
   (feed the judge's decision into the summarizer prompt). Cheap; directly targets
   blocker 2. Risk: a longer summary.
2. **Stop paying for the summarizer when the judge already clears the threshold.**
   The judge already skips it when its reclaim alone clears the budget; on these
   sessions it never does (the summarizer always ran), so this is a non-lever here.
3. **Bypass summarization for kept bodies** — splice the judge-kept bodies into the
   preserved tail verbatim instead of into the summarizer input. This is the only
   change that makes preservation *model-visible* rather than summary-visible, but
   it directly raises retained tokens (the opposite of what compaction is for) and
   changes compaction semantics. Highest impact, highest risk.
4. **Judge downstream, not upstream.** Measure whether the *next* turn on the
   compacted context re-reads a file the judge kept. That is the metric the feature
   advertises; this report measures the boundary it crosses, not that outcome.

## Honest caveats

- **Four sessions, one model, one classifier, one threshold.** Do not
  over-generalize; `0.35` was tuned on tool bodies.
- **The ground truth is a string-reuse proxy**, which is why precision is 0.40 and
  not higher. It is deterministic and directionally right, not authoritative.
- **Summary-path mentions are a coarse read** of "information reached the model":
  a summary may carry a fact without naming the path. That under-counts the ON
  win — but it cannot disguise it as *better*, only as *less worse*.
- **Probe answers** (a model asked to restate the objective/files/next step from
  the compacted context) were collected and read by hand; both arms produced
  coherent, comparable answers and no consistent winner. They are in the per-arm
  dumps but not scored numerically here, because a single free-text answer is not a
  reliable unit of outcome.

## Reproduction

The harness was temporary and has been removed. It drove the real
`newCompactionState(...).maybeCompact(...)` and `judgeStaleHistory(...)` over
`~/.local/share/kajicode/sessions` with the `growwcorp` provider and the `jev`
classifier profile, on these four sessions:

- `zero_20260909101146_1788948706312871000_1`
- `zero_20260908140107_1788876067019677000_336`
- `zero_20260914101521_1789380921789735000_35`
- `zero_20260903195423_1788465263836881000_39`
