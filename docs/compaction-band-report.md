# Compaction band + recoverable drops — build, measurement, and honest verdict

Status: **implemented, measured, verified.** This document supersedes the
lever assumptions in `docs/JEV_COMPACTION_IMPROVEMENT_PLAN.md` §5.4: that plan
proposed a default uncertain band of `dropLow = 0.2`. Measured on KajiCode's own
sessions, **that default is wrong** — it is shipped as opt-in, off by default.

Prior docs this builds on (do not re-derive):
`docs/JEV_COMPACTION_FILTER_PLAN.md` (what shipped),
`docs/compaction-judge-outcome-report.md` (the flat-outcome measurement),
`docs/compaction-dryrun-report.md` (token-reclaim measurement),
`docs/compaction-artifacts-report.md` (the carry block),
`docs/compaction-behavior-report.md` (the loop-level behavior probe).

---

## 1. What was built

Two changes to the compaction judge, plus one reuse.

### 1.1 An opt-in uncertain band (removes the single-threshold cliff)

`internal/classifier/profile.go` gains `CompactionFeature.DropResultThreshold`.
A judged body is now:

```
p >= keepThreshold          -> keep verbatim
dropThreshold <= p < keep    -> keep   (the uncertain band)
p <  dropThreshold          -> drop
```

The band is **opt-in**: an unset `dropResultThreshold` returns the keep
threshold, so the band is empty and the decision is exactly the shipped single
threshold. A configured band is clamped to the keep threshold so it can never
invert — the dropped set is always a subset of the single-threshold dropped set.

### 1.2 Recoverable drops (reuses the existing spill)

The plan (`JEV_COMPACTION_IMPROVEMENT_PLAN.md` §5.1/5.2) proposed a new
`WritePrunedBlob` store method plus a new `recall_pruned` tool. **That was
unnecessary.** The repo already has a spill mechanism (`internal/tools/spill.go`)
whose files:

- are secret-scrubbed with the same two scanners the transcript uses,
- are accepted by every scoped read tool (`resolveScopedReadPath` →
  `resolveSpillReadPath`), so `read_file`/`grep` already open them,
- have a retention sweep (`spillRetention`, 7 days), size caps, and
  uid-hardened directory checks.

So the drop path now calls `SpillOutput` and the placeholder names the file:

```
[pruned grep output (~4200 tokens) to reclaim context — full output saved to
 /…/kajicode-tool-output-502/grep-123.txt; read_file or grep it, or re-run the
 tool if you need it again]
```

`SpillOutput` is only called on the **drop** path, so a kept body is never
written to disk. When the spill fails, the rewriter falls back to today's
"re-run the tool" hint. No new store, no new tool, one lifecycle.

## 2. Measurement (the acceptance gate)

Temporary in-package harness (deleted; see §5), driving the **real**
`judgeCandidates` + `judgeStaleHistory` + `spillPrunedBodyRewriter` against the
live `jev-1.13` classifier, over the four largest real sessions reconstructed
from their raw `events.jsonl` **without** applying compaction events (so the
judge sees every stale body the session ever produced). 3,134 judged tool-result
bodies. Four sessions, one model, one classifier.

### 2.1 The band's default is refuted — the mass is not where the plan assumed

| drop floor | bodies kept | tokens still kept | tokens dropped |
|---|---|---|---|
| 0.05 | 3134 | — | 0 |
| 0.10 | 3134 | — | 0 |
| 0.15 | 3134 | — | 0 |
| **0.20** (the plan's default) | **3124** | — | **6,806** |
| 0.25 | 2803 | — | 186,572 |
| 0.30 | 1358 | — | 989,542 |
| **0.35** (shipped single threshold) | **307** | 337,080 | **1,735,635** |
| 0.40 | 61 | — | 1,980,459 |
| 0.50 | 10 | — | 2,053,392 |

Probability histogram over the 3,134 bodies:

| bucket | count |
|---|---|
| 0.1–0.2 | 10 |
| **0.2–0.3** | **1766** |
| **0.3–0.4** | **1297** |
| 0.4–0.5 | 51 |
| 0.5–0.6 | 9 |
| 0.6–0.7 | 1 |

The classifier puts almost all mass in **0.2–0.4**. A drop floor of `0.2` keeps
3,124 of 3,134 bodies and reclaims **6,806** tokens — it stops being a filter.
That is strictly harmful here: a judged history that keeps more stays **over**
the compaction threshold more often, so the paid summarizer runs *more*, on a
*larger* input, and the restored bodies are then compressed away by that
summarizer anyway (the channel `docs/compaction-artifacts-report.md` measured).
The plan's claim that a retention-raising band "cannot make the summarizer run
more often" is **wrong** — it holds only where the kept token count stays under
the threshold, which is not this workload.

**Therefore the band ships OFF by default.** It exists for a caller who has
explicitly accepted the reclaim cost for retention, and it is not a default win.

### 2.2 The real shipped win: judge drops are now recoverable

With the default decision unchanged (band off: 307 kept, the same *decision* as
the single-threshold path), the judge's drop path is now a recovery net. Scoped
honestly: this covers bodies the **judge** drops. A body dropped by the free
prune floor — a duplicate collapse, a body past `judgeMaxWork`, or any body when
the classifier answers nothing — keeps the plain re-run hint and is not spilled
(see §3).

| | value |
|---|---|
| tool-result bodies dropped by the judge and answered (4 sessions) | **2,827** |
| …written to a readable spill file | **2,827 (100%)** |
| …that round-trip **byte-identical** on read-back | **2,827 (100%)** |
| tokens those bodies hold | **1,735,635** |

Per session (from `/tmp/kajicode-band-ab.csv`, folded into this doc):

| session | kept | dropped | recovered | bytes spilled |
|---|---|---|---|---|
| `…03195423_39` | 31 | 682 | 682 | 2,083,840 |
| `…140107_336` | 36 | 832 | 832 | 2,327,025 |
| `…101146_1` | 192 | 686 | 686 | 2,373,955 |
| `…101521_35` | 48 | 627 | 627 | 1,856,029 |
| **TOTAL** | **307** | **2827** | **2827** | **8,640,849** |

Before this change, those 1.7M tokens of pruned output were unrecoverable
unless the tool happened to be idempotent — a `web_fetch`, a `code_search`, or a
read of a since-deleted file could not be recovered at all. Now the model can
`read_file` or `grep` the named path. This is the only measured, unconditional
improvement in the change.

The recovery path is wired end to end: the placeholder names the file, and
`renderTranscript` now preserves that path when it compresses an elided body for
the summarizer, so the path can reach `## Relevant Files` rather than being
replaced by a generic "[cleared]" line. The spill is bounded — at most 64 bodies
per compaction and 2 MiB each (`maxPrunedSpillsPerCompaction`,
`maxPrunedSpillBytes`) — so a pass that drops hundreds of bodies cannot do
hundreds of synchronous filesystem cycles on the turn path.

## 3. What this does NOT claim

- **The band is not a default win.** Measured above; it ships opt-in.
- **Not measured:** whether the recovery net is *used* (does the model actually
  read a spilled pruned body instead of re-running a tool). The spill is
  reachable and verified readable; the behavioral follow-through is not.
- **Not measured:** downstream model behavior (fewer re-reads). Still the only
  true test, still unbuilt.
- One model, one classifier, four sessions. The 0.2–0.4 mass is a property of
  *this* classifier on *this* workload; a differently-calibrated router could
  put it elsewhere.

## 4. Files

**Modified**
- `internal/classifier/profile.go` — `DropResultThreshold`, `EffectiveDrop
  ResultThreshold` (unset ⇒ keep threshold, band off; clamped).
- `internal/agent/compaction_judge.go` — `judgeOptions{dropThreshold,
  keepThreshold, recover}`; three-way decision; recovery hook on the drop path.
- `internal/agent/compaction.go` — state fields; `spillPrunedBodyRewriter`.
- `internal/agent/prune.go` — `prunedPlaceholderWithPath`; `prunedBodyRewriter`.
- `internal/agent/types.go` — `CompactionJudgeDropThreshold`.
- `internal/config/classifier.go` — merge the new field.
- `internal/cli/{app,exec}.go`, `internal/acp/agent.go` — wire the drop threshold.
- `internal/tools/spill.go` — `spillTruncatedOutput` → exported `SpillOutput`
  (the agent package now reuses it).

**Tests**
- `internal/agent/compaction_judge_test.go` — band semantics; unset drop ⇒ plain
  threshold; the recovery hook is handed exactly the dropped body and never a
  kept one; the default rewriter keeps the re-run hint; a **spill round-trip**
  proves a dropped body is written and read back byte-identical.
- `internal/classifier/classifier_test.go` — effective thresholds and the clamp.
- `internal/tools/spill_test.go` — a spill is accepted by the scoped read path.

**Removed**
- The temporary measurement harness (`internal/agent/zz_measure_test.go`) and
  its `/tmp` scratch.
- `spillTruncatedOutput` (renamed, not duplicated — one spill entry point).

## 5. Verification

```bash
make fmt-check                                   # clean
go vet ./...                                     # clean
go test ./...                                    # zero failures
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...   # no vulnerabilities
git diff --check                                 # clean
```

A judge-free path is byte-identical: `Options.CompactionJudge == nil` skips the
whole judge stage, and an unset drop threshold reproduces the single-threshold
decision (proved by `TestJudgeUnsetDropThresholdIsPlainThreshold`).

## 6. Risks

- **The spill is a real new write path** under a temp dir. It reuses an
  existing, hardened, swept, secret-scrubbed mechanism, but it does add disk I/O
  per dropped body. Bounded by `spillRetention` (7 days) and the existing sweep.
- **The band is opt-in and off.** A user who sets `dropResultThreshold` below
  `keepResultThreshold` accepts a retention-for-reclaim trade that this document
  measures as net-negative on this workload. The clamp prevents an inverted band
  from dropping anything the plain threshold would keep.
- **The 0.2–0.4 mass is classifier-specific.** Any future band default must be
  re-measured, not inherited from a plan.
