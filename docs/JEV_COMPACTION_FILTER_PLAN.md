# Jev classifier in KajiCode — filter-then-compact

Status: **implemented and measured**. This document is the record of the idea, the
root cause it uncovered, the implementation that shipped, and the numbers it
produced. It replaces the earlier investigation-only drafts (`JEV_JUDGE_PLAN.md`,
the first `JEV_COMPACTION_FILTER_PLAN.md`).

## The idea

> Every user, assistant, tool call, and thinking block — instead of compacting it
> directly, first pass it through a classifier: is it useful, and will it be
> useful in the compaction? If yes keep, if no remove. Then run the existing
> compaction on the filtered data.

KajiCode already had a narrower version of this: an optional **compaction judge**
(`internal/agent/compaction_judge.go`) that asked a fast classifier which stale
**tool-result bodies** were still needed. It was live, correct, and **inert** — it
changed compaction by +90 tokens over 5,082,667 (0.002%). This work found why and
rebuilt it into the capability the idea describes.

## Root cause (measured, not guessed)

Two defects, both proven by replaying real sessions, made the old judge a no-op:

1. **Scope defect — the judge could never win.** `judgeCandidates` reused the free
   prune's protection rules **plus a hard cap** (`judgeMaxItems = 40`), so its
   candidate set was a strict subset of the prune's. `maybeCompact` then kept
   whichever reclaimed more (`max(judge, prune)` at `compaction.go:675`). The judge
   tied or lost every time; its "keep the relevant ones" signal was discarded.
2. **Blind spot — the biggest reclaimable class was never touched.** The free prune
   rewrites only tool-result **bodies**. Replaying the four largest sessions showed
   tool-call **arguments** are 22–41% of tokens and **no stage looked at them**.

The classifier itself was fine: the decision dump showed it kept exactly the
still-referenced `read_file`/`Task`/`TaskOutput`/`todo_write` artifacts and dropped
120 stale `bash` dumps. **The plumbing was broken, not the model.**

## What shipped

### The judge is now a filter, and the prune is its floor

`judgeStaleHistory` (`compaction_judge.go`) runs only when the classifier answers,
and it rewrites the whole stale history:

| Item | Judged relevant | Judged irrelevant |
| --- | --- | --- |
| tool-result body | kept verbatim | `prunedPlaceholder` |
| tool-call arguments | kept verbatim | `"{}"` (valid JSON) |
| assistant narration | kept verbatim | cleared |

`maybeCompact` now runs the free prune first as a **deterministic floor**, then lets
the judge **restore/remove** over it — so the **judge can only ever keep MORE than
the blind prune, never less**, and enabling it can never make the summarizer run
more often. (The old comment claimed this and the code did the opposite; it is now
true and tested.)

The classifier is asked about **all** stale items, newest-first, in
**size-bounded batches** (`judgeStateByteBudget`) up to a bounded work cap
(`judgeMaxWork`). Items are one noul ("still needed" ⇄ "safe to discard") each.

### What is never rewritten

- a message carrying `Reasoning` blocks (Anthropic/Gemini replay would 400);
- `todo_write`, `tool_search`, `skill`, `write_file`, `edit_file` arguments (the
  preserved-state readers parse them — the active plan, loaded skills/tools, and the
  recent-edit list would silently drop out of the summary);
- the preserved recent window and the trailing tool-output protection window;
- anything already reduced to a placeholder (idempotent).

### Fail-open

A nil classifier, an error, a batch error, or a missing answer leaves the input
untouched and falls back to the free prune — **byte-identical to the judge-off
path**. The judge-off path is unchanged by construction.

### Preconditions

A classifier profile must be configured (`kajicode classifier add`, or `/classifier
add` in the TUI) **and** `classifier.features.compaction.enabled` must be true.
Connecting a classifier turns nothing on.

## Measurement

Real sessions from this machine, through the real `maybeCompact`, with
growwcorp/`fireworks/DeepSeek_V4.1_Flash` and the real `jev-1.13` classifier.

**Judge stage in isolation** (the feature's own effect; the reliable number):

| Session | candidates | judge reclaim | prune reclaim | judge vs prune | kept (results / args) |
| --- | --- | --- | --- | --- | --- |
| `…3195_39` | 915 | 404,276–436,017 | 426,376 | ±2% | ~5 / ~50 |
| `…1015_35` | 1,185 | 476,161–511,555 | 511,863 | −0.1% | ~10 / ~50–280 |
| `…1011_1` | 1,394 | 786,761–1,089,388 | 1,119,870 | −3% | 34 / 9 |
| `…1401_336` | 1,533 | 490,851–533,886 | 518,559 | +1–3% | 22 / 334 |

Final end-to-end run (judge ON vs OFF, real summarizer): the resulting context
matched within ~0.1% (Δ +150 to +1,306 tokens; one −371), summarizer ran once per
arm. See `docs/compaction-dryrun-report.md`.

**Interpretation, stated plainly.** On these dump-heavy histories the free prune is
already a near-optimal *token* reclaim, so the judge's token delta is ~0. That is not
a failure — the judge's intended win is **relevance preservation**: it restores the
~1–5% of bodies a blind age-based prune destroys (the `read_file`/`Task` artifacts),
and additionally strips arguments/narration the prune cannot touch. Whether that
reduces the model's downstream re-reads is **not measured here** and remains the open
question.

## Files

Created: `internal/classifier/*`, `internal/agent/compaction_judge.go`, the CLI/TUI
classifier surfaces, and config plumbing.
Modified: `internal/agent/compaction.go` (`maybeCompact` override), `types.go`
(doc), `internal/config/classifier.go`, `docs/architecture.md`.
Removed: the unused `classifier.Features.ToolResult`/`Completion` stubs.

## Verification

`go test ./internal/agent -run 'TestJudge|TestMaybeCompact'` covers: keep relevant /
drop irrelevant, keep *more* than the blind prune, reduce irrelevant arguments
(JSON-valid), never rewrite preserved-state arguments, never rewrite reasoning
messages, clear stale narration, idempotence, the override-not-`max()` semantics,
and judge-unavailable fallback.

## Risks

- **No downstream-effect measurement.** Token reclaim is flat by construction. A
  later implementation (`docs/compaction-artifacts-report.md`) makes the judge's
  relevance win *model-visible* — the rescued-path survival rose from 1.5% to
  0.65–0.90 — by carrying the judged targets into the injected summary. Whether
  that reduces the model's downstream re-reads is still the untested metric.
- **Endpoint health.** Judge coverage depends on the classifier answering; when
  batches time out the arm falls back to the prune (safe, but the relevance win is
  lost for that compaction).
- **Threshold.** `0.35` was tuned on tool bodies. Results keep ~1–5%; arguments in
  these sessions were judged keep-more-often (>50% kept on several sessions), which
  is the direction that preserves content, but the per-kind optimum is unmeasured.
- **Cost/latency.** Bounded by `judgeStateByteBudget`/`judgeMaxWork` and the request
  timeout; a large history costs tens of calls.
