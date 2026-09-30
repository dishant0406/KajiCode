# Compaction dry-run — Jev judge ON vs OFF (filter-everything)

A measured A/B of the rebuilt compaction judge (`internal/agent/compaction_judge.go`)
against the shipped free positional prune (`internal/agent/prune.go`), run on **four
real session histories** from this machine, through the **real** `maybeCompact`
path, with a **real** summarizer provider and a **real** Jev classifier.

- **Provider:** `growwcorp` / `fireworks/DeepSeek_V4.1_Flash`
- **Classifier:** profile `jev`, model `jev-1.13`, `keepResultThreshold=0.35`
- **Context window:** 200,000 tokens
- **Harness:** temporary in-package test driving the real `maybeCompact`, 4 sessions
  in parallel (deleted after producing this report)
- **Reconstruction:** the FULL raw transcript (all `tool_call`/`tool_result` events,
  ignoring `session_compaction` resets) — the history compaction is meant to reduce

## Headline

The rebuilt judge is **no longer inert** — it runs, covers the whole stale history,
and its decision changes the history (it keeps bodies the prune would drop and drops
arguments/narration the prune never touched). But on these dump-heavy sessions the
**final context size after the paid summarizer is essentially unchanged** (Δ within
~0.1%). That is expected: the free prune already reclaims a near-optimal *token*
fraction, so the judge's measurable win is **relevance preservation, not reclaim**,
which this run measures only indirectly.

## End-to-end (judge OFF vs ON, real summarizer)

| Session | msgs | before tok | OFF reclaim | ON reclaim | Δ reclaimed | Judge candidates (results/args) | Judge-stage reclaim |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `…1015_35` | 4,343 | 1,112,113 | 1,103,761 | 1,104,026 | **+265** | 1,185 (841 / 344) | 498,299 |
| `…3195_39` | 4,495 | 915,566 | 903,806 | 903,435 | **−371** | 915 (713 / 202) | 406,402 |
| `…1011_1` | 6,106 | 1,794,902 | 1,761,064 | 1,762,370 | **+1,306** | 1,394 (1,254 / 140) | 891,426 |
| `…1401_336` | 6,220 | 1,348,802 | 1,339,287 | 1,339,437 | **+150** | 1,533 (927 / 606) | 504,961 |

The summarizer ran once per arm (four times on `…1401_336`, which compacts a
mid-stream session repeatedly). The end-state token count is dominated by the
summarizer, so the judge's relevance decision is not visible in it.

## Judge stage in isolation (provider-free)

Running only the judge stage over the full transcripts isolates its own effect from
summarizer variance. Numbers below are the judge's reclaim of the raw history vs the
free prune's, plus how many items of each kind it kept:

| Session | candidates | prune reclaim | judge reclaim | judge vs prune |
| --- | --- | --- | --- | --- |
| `…3195_39` | 915 | 426,376 | 404,276–436,017 | −5% … +2% |
| `…1015_35` | 1,185 | 511,863 | 476,161–511,555 | −7% … −0.1% |
| `…1011_1` | 1,394 | 1,119,870 | 786,761–1,089,388 | −30% … −3% |
| `…1401_336` | 1,533 | 518,559 | 490,851–533,886 | −5% … +3% |

Per-kind decisions (kept / dropped, tokens), `…1015_35`:

| Kind | Kept | Dropped |
| --- | --- | --- |
| tool_result | 64 (55,812 tok) | 777 (472,195 tok) |
| tool_args | 282 (127,263 tok) | 62 (23,065 tok) |

The ranges are not noise in the judge: they are the two regimes the harness hit —
(a) the endpoint answering every batch (full coverage, judge ≥ prune in relevance but
slightly lower in reclaim because it *keeps* relevant bodies), and (b) some batches
timing out (partial coverage, judge reclaims less as more items fall to the prune
floor). Both are fail-open and safe; coverage depends on endpoint health.

## What the judge actually decided

Raw per-item evidence: `docs/compaction-judge-dump.md`. The classifier keeps the
still-referenced artifacts and drops the stale dumps: of 160 tool-result bodies in
the original dump, the ones at/above 0.35 were exactly the `read_file` / `Task` /
`TaskOutput` / `todo_write` results, and the 120 old `bash` dumps were judged
droppable.

## Root cause of the old behavior

1. **Scope defect:** `judgeCandidates` mirrored the prune's rules **plus a cap of 40
   items**, so `judgeReclaimed ≤ pruneReclaimed` always; `max()` at
   `compaction.go:675` then always picked the prune.
2. **Blind spot:** the prune rewrites only tool-result bodies; tool-call arguments
   (22–41% of tokens) and assistant prose were never touched by any stage.

Both are fixed: the judge is now an **override over the prune floor** (keep-more,
never less), and it covers results, arguments, and prose.

## What would still move the needle

The relevance win is real but invisible to a token metric. To measure it: replay a
compaction, then run the *next* model turn with the judge ON vs OFF and compare how
often the model re-reads a file it had already read (the artifact the judge would have
kept). That is the metric the feature actually targets; it is not in this report.

**Update:** that follow-up has now been run — see
`docs/compaction-judge-outcome-report.md`. It measured the judge's selection quality
against session evidence (precision 0.40 / recall 0.28 vs the prune's recall 0.00) and
whether rescued bodies survive the summarizer. Verdict: the filter is measurably
smarter, but the model-visible outcome is still flat (final context +0.1%…+4.5%, and
only ~1.5% of rescued paths appear in any summary), because the summarizer compresses
the judge's input away. The "next-turn re-read" comparison remains the untested
outcome metric.

**Update 2 (`docs/compaction-behavior-report.md`):** the downstream test was run
— the real agent loop, judge OFF vs ON, then a no-tools probe asking the model
which files are in play. The carry block is real but small: it turns 0/27 into
3/27 for paths the OFF context never held, because tool-call arguments already
preserve most in-play paths. Directionally correct, bounded magnitude.

## Caveats

- **No downstream-effect measurement.** Token reclaim is flat by construction.
- **Four sessions, one model, one classifier.** Do not over-generalize.
- **Endpoint coverage** limits how much of the history the judge sees per compaction;
  the prune floor guarantees a safe result regardless.

## Reproduction

The harness was temporary and has been removed. It drove the real
`newCompactionState(...).maybeCompact(...)` over `~/.local/share/kajicode/sessions`
with the `growwcorp` provider and the `jev` classifier profile.
