# Classifier compaction judge — complete before/after on four real sessions

The question: **does turning on the Jev-style classifier judge in compaction
produce a measurably different outcome?** Answered on four real sessions from
this machine, running the **real** `newCompactionState(...).maybeCompact(...)`
(and the real `judgeStaleHistory(...)`) — judge **OFF vs ON** — with:

- **Provider:** `growwcorp` / `fireworks/DeepSeek_V4.1_Flash` (real keychain key)
- **Classifier:** profile `jev` → `jev-1.13` at
  `https://openrouter.ai/api/v1/systemone` (real keychain key)
- **Judge:** `keepResultThreshold = 0.35`, uncertain band off (shipped default)
- **Window:** 200,000 · `PreserveLast` 12 · params identical in both arms
- **4 sessions in parallel**, each run twice (OFF / ON), full raw transcript

Harness was a temporary in-package test; deleted after this report.

## What "before/after" means here

Three layers are measured, because the earlier reports showed that the judge's
decision is only one of them and that a decision that dies downstream is not a
result:

1. **Isolated** — what the judge changes relative to the free prune, with no
   summarizer. Tests whether the feature is even *active*.
2. **Pipeline** — the real `maybeCompact` end to end, including the paid
   summarizer; the final context size. Tests what compaction actually produces.
3. **Downstream recall** — after compaction, the real model is asked (with **no
   tools**, using the exact compacted context) to name the files in play. Tests
   whether the carried artifacts reach the model.

## Corpus

| session | project | raw transcript tok |
|---|---|---|
| `zero_20260909101146_…_1` | kajicode (image input) | 1,794,902 |
| `zero_20260908140107_…_336` | zerocarbon / backend | 1,348,802 |
| `zero_20260903082911_…_1` | groww / frontend-release-platform | 728,892 |
| `zero_20260914101521_…_35` | zerocarbon / backend | 1,112,113 |

Raw transcript = the event log folded ignoring prior compactions, so the
compaction under test runs on a full history rather than an already-compacted
prefix.

## Layer 1 — Isolated: the judge is active, and it grows context

| session | free-prune tok | judged tok | Δ | candidate bodies | restored | args dropped |
|---|---|---|---|---|---|---|
| `…101146_1` | 675,032 | 893,423 | **+32.4%** | 1,254 | 196 | 5 |
| `…140107_336` | 830,243 | 835,988 | +0.7% | 927 | 33 | 78 |
| `…082911_1` | 379,182 | 460,173 | **+21.4%** | 393 | 58 | 4 |
| `…101521_35` | 600,250 | 608,625 | +1.4% | 841 | 42 | 61 |

The judge **restores 33–196 tool bodies the blind prune destroys** and **reduces
4–78 tool-call argument sets** the prune never touches. That is exactly the
"filter first, then compact" behavior: it is running and it is doing real work.
(This is the opposite of the earlier `+90 tokens over 5,082,667` finding, which
was measured before the scope defect was fixed.)

Holding **more** context is the intended direction — relevance preservation, not
shrink. It is the trade the feature is designed around.

## Layer 2 — Pipeline: the difference that survives to the model

Real `maybeCompact` → real summarizer → injected context. Two clean runs were
**identical**; the numbers below are from the second.

| session | OFF final tok | ON final tok | Δ | artifacts carried | carried paths |
|---|---|---|---|---|---|
| `…101146_1` | 32,779 | 33,901 | +3.4% | 17 | 23 |
| `…140107_336` | 19,516 | 19,772 | +1.3% | 15 | 16 |
| `…082911_1` | 17,982 | 19,308 | +7.4% | 15 | 26 |
| `…101521_35` | 17,229 | 18,821 | +9.2% | 16 | 32 |
| **total** | **87,506** | **91,802** | **+4.9%** | 63 | 97 |

Compaction output is **+1.3% to +9.2% larger with the judge on** (total +4.9%).
That is the relevance-over-shrink trade reaching the model: the ON context
carries a bounded `## Carried artifacts` block the OFF context does not have.

## Layer 3 — Downstream: does the model know more? Yes.

After compaction, the model was asked (no tools, exact compacted context) to
list the in-play files; the answer was scored against the basenames the carry
block adds. Identical across both runs:

| session | carried basenames | OFF named | **ON named** |
|---|---|---|---|
| `…101146_1` | 23 | 12 | **21** |
| `…140107_336` | 16 | 13 | **15** |
| `…082911_1` | 26 | 9 | **19** |
| `…101521_35` | 32 | 1 | **4** |
| **total** | **97** | **35 (36%)** | **59 (61%)** |

**Before the carry block the compacted model could name 35 of 97 in-play files;
after, 59.** A +24-file improvement, consistently across every session and both
runs. The model that sees the judge-ON context names the files it is working on
roughly **1.7× more often**.

## Verdict

**Turning the classifier judge on changes the compaction outcome measurably, in
the right direction, across all three layers.**

- The judge is **active**: it keeps 33–196 bodies the prune destroys and strips
  4–78 argument sets the prune ignores.
- Compaction output grows **+1.3–9.2%** (total +4.9%) — the deliberate
  relevance-over-shrink trade, bounded by the artifact caps.
- The model's ability to name in-play files rises from **36% → 61%**.

It is not a token win — it is a **context-quality** win, and it is real and
reproducible on this machine's data.

## Honest limits

- **Summarizer flakiness.** These transcripts push ~700k–830k tokens into the
  summarizer and it intermittently errors on the gateway. One of three runs hit
  this and fell back (fail-open) to the prune floor with no summary. The harness
  now retries a failed arm a bounded number of times so a transient upstream
  error is excluded rather than reported as a result — it is *stated*, not
  hidden.
- **Recall naming is necessary, not sufficient.** A model can name a path and
  still not act on it. The 36%→61% number is the boundary the artifacts cross,
  not proof of better downstream task behavior.
- **The OFF context already leaks paths.** Tool-call `Arguments` survive the
  prune, so OFF names 35/97 of the carried files without any judge. The judge's
  marginal contribution is the difference, not the total.
- **Four sessions, one model, one classifier, one threshold, one window.** Do
  not over-generalize.
- **Baseline is the shipped prune + summarizer**, not an empty context — the
  only fair comparison, and a strong one.

## Reproduction

Temporary harness (removed): an in-package test driving the real
`newCompactionState(...).maybeCompact(...)` and `judgeStaleHistory(...)` with
`providers.New(growwcorp/DeepSeek_V4.1_Flash)` from the stored key, the real
`jev-1.13` classifier from the credential store, and
`Options{ContextWindow: 200_000, CompactionPreserveLast: 12}`; ON adds
`CompactionJudge` + `keepResultThreshold 0.35`. Four sessions ran in parallel;
the downstream recall probe reuses each arm's produced messages with no tools.
