# Does the Jev judge make a *better compaction outcome for the model*? — measured

This is the follow-up the earlier reports named as the one true downstream test:
**does the compaction the judge produces actually leave the model better off?**
It is answered on **four real sessions from this machine**, by driving the
**real** agent loop (`agent.Run` → `maybeCompact` → judge → summarizer →
provider), judge **OFF vs ON**, and then measuring what the model *knows* and
*does* next.

- **Provider:** `growwcorp` / `fireworks/DeepSeek_V4.1_Flash`
- **Classifier:** profile `jev` → `jev-1.13`, `keepResultThreshold=0.35`
- **Loop:** real `Options` (ContextWindow 200k, read-only tools, `MaxTurns=4`),
  real `InitialMessages` (a real session transcript), a real user prompt.
- **Harness:** temporary in-package test; deleted after this report.

## Short answer

**The judge's carry block is real, it is in the right direction, and it is
small.** On these four sessions it changes what the compacted model can name
about its own work — but most of the in-play file knowledge was *already*
present in the judge-OFF context, so the incremental good is modest, and it
costs a consistent context increase.

Two numbers state it:

- Of the **27 distinct file basenames** the carry block adds that the OFF context
  never contained, the model could name **3 with the judge ON vs 0 with it OFF**
  (probe below). Right direction, small magnitude.
- The judge-OFF context **already named 8 of the 24** carried basenames in the
  biggest session, and **10 of 14** in another — the paths leak in through
  tool-call arguments (which the prune never rewrites) and the summarizer's own
  `## Relevant Files` section.

So: **not a dramatic win, not nothing.** The honest verdict is closer to "the
judge prevents a handful of in-play paths from being lost, at a
single-digit-percent context cost," than to "compaction is now smarter for the
model."

## How this was measured

`agent.Run` was driven with a real session transcript as `InitialMessages` and a
real following user prompt, so compaction runs through the real path. Two
probes capture "outcome":

1. **Re-read (the advertised metric).** After the run, count how often the model
   re-read a file the judge had rescued. **Result: not observable here** — in a
   4-turn read-only run neither arm re-read those specific paths (0.00 both) —
   the model explored *other* files. See "Honest limits".
2. **Recall-with-no-tools (the robust metric).** Ask the model, with the *exact
   context the loop produced* and **no tools**, to name the file paths in play.
   A path the model can name is a path it did not lose. This is deterministic,
   provider-free in its scoring, and it directly tests the block's purpose. It is
   the number the tables below report.

The harness (`zz_ab_*_test.go`) is deleted; the raw per-arm dumps are
reproducible by re-adding an env-gated copy.

## Results

**Context and carry.** ON grows the final context by **+1.3% to +15%** — the
intended relevance-over-shrink trade. The carried block is non-empty in 3 of 4
sessions; the fourth never reached the paid summarizer at this cut, so there was
nothing for the carry to attach to.

| Session | raw tok | final tok OFF → ON | Δ | carried | OFF tools | ON tools |
|---|---|---|---|---|---|---|
| `kajicode-imageinput` | 327,665 | 81,311 → 81,761 | +0.6% | none¹ | 5 | 4 |
| `zerocarbon-erp` | 306,727 | 20,137 → 21,427 | **+6.4%** | 8.7 KB | 4 | 4 |
| `zerocarbon-backend` | 248,182 | 25,906 → 29,879 | **+15.3%** | 10.1 KB | 6 | 7 |
| `zerocarbon-erp-ccts` | 207,845 | 22,052 → 22,807 | **+3.4%** | 7.3 KB | 4 | 4 |

¹ This session compacted *without* the summarizer at this cut — the free prune
alone cleared the threshold — so no summary (and no carry block) was injected.

**Recall-with-no-tools (basename-deduped).** "genuinely new" = carried basenames
absent from the OFF arm's entire block (its context, tool args, and answers):

| Session | carried basenames | genuinely new | probe names them OFF | probe names them **ON** |
|---|---|---|---|---|
| `kajicode-imageinput` | 0 | 0 | – | – |
| `zerocarbon-erp` | 14 | 4 | 0/4 | **2/4** |
| `zerocarbon-backend` | 24 | 16 | 0/16 | 0/16 |
| `zerocarbon-erp-ccts` | 14 | 7 | 0/7 | **1/7** |
| **total** | | **27** | **0 (0%)** | **3 (11%)** |

**The other, load-bearing number.** How much of the *carried* set the OFF
context already surfaced anyway:

| Session | carried basenames | named by OFF probe | named by ON probe |
|---|---|---|---|
| `zerocarbon-erp` | 14 | 10 | 12 |
| `zerocarbon-backend` | 24 | 8 | 8 |
| `zerocarbon-erp-ccts` | 14 | 7 | 4 |

## Why the effect is small (root cause, from the data)

The judge's carry block is fighting a channel that is **already mostly intact**:

1. **Tool-call arguments survive compaction.** The free prune rewrites tool
   *result bodies*; it never touches a tool call's `Arguments`. So every
   `read_file {"path": …}` and `grep {"path": …}` in the history still names its
   target in the OFF context. That is why the OFF probe already names 8/24 and
   10/14 of the carried files without the judge.
2. **The summarizer and preserved state name paths too.** The summary's
   `## Relevant Files` section and the `recent_edits` list in the preserved-state
   JSON both carry exact paths, judge-OFF included.
3. So the carry block's *marginal* contribution is only the paths that appear
   **nowhere else** — the 27 genuinely-new basenames — of which it lands 3.

The judge is doing its job; the pipeline around it already leaks path knowledge,
so the block adds less than the "1.5% survival" figure from
`docs/compaction-judge-outcome-report.md` implied it would (that figure measured
survival into the *prose summary* alone, before tool-arg leakage was counted).

## Honest limits (what this does and does not prove)

- **The re-read metric stayed unobservable.** A 4-turn, read-only run did not
  re-read the rescued files in either arm, so "fewer re-reads" is **not**
  demonstrated — it needs a longer, edit-oriented continuation seeded from the
  same point, which this run did not build.
- **The carry block is capped at 20 path targets / 6 KiB.** Paths beyond the cap,
  or relevance in a non-path target (`command`, `query`), are not covered.
- **Probe naming is necessary, not sufficient.** A model can name a path and
  still not act on it. The probe under-counts ON's benefit only slightly,
  because OFF's tool args already carry most of the same strings.
- **One session never reached the summarizer** at the 200k cut, so the carry
  block had no surface — that is a property of a dump-heavy transcript whose free
  prune alone clears the threshold, not a judge failure.
- **Four sessions, one model, one classifier, one threshold, 4 turns.** Do not
  over-generalize.
- **Baseline tested is the shipped free prune + summarizer, not an empty
  context.** The comparison is "judge vs the pipeline as it exists," which is the
  only fair one — but it means the judge is judged against a strong baseline.
- **Transient gateway errors** (`no such host` resolving the growwcorp upstream)
  hit 2 of 8 arms in an earlier pass; the probe now retries, and the reported
  pass completed with all 8 arms clean.

## What would actually move the needle

These follow from the data; none is implemented here.

1. **Stop relying on the summarizer to preserve paths at all.** Carry the judge's
   target block as a *structured, non-summarized* tail alongside the summary
   (already done) **and** suppress the redundant path echo elsewhere, so the
   block is the single source of truth for "files in play" — reduces duplication,
   not just adds more.
2. **Measure re-reads on a real continuation.** Seed a longer, edit-oriented turn
   from the same compaction point and count `read_file` calls on paths the judge
   kept vs dropped. That is the metric the feature advertises and this run could
   not reach.
3. **Make OFF genuinely worse (control) to size the ceiling.** Suppress
   tool-argument path echo in a synthetic OFF arm to see the block's *potential*
   contribution. If it is still single-digit-percent, the feature's value is
   bounded by how much path knowledge the rest of the pipeline leaks.

## Verdict

**A better compaction outcome for the model: measured — small, directionally
correct, and bounded by a pipeline that already preserves most in-play paths.**
The judge's carry block turns 0/27 into 3/27 for the paths that would otherwise
be lost, at a +1–15% context cost. It is not the "compaction is smarter now"
result the earlier open question hoped for; it is real but minor on these
sessions, and the only test that could change that verdict — next-turn re-reads
over a longer continuation — was not reachable in this run.

## Reproduction

Temporary harness (removed): an env-gated test driving the real
`newCompactionState(...).maybeCompact(...)` through **`agent.Run`**, with:

- `providers.New(growwcorp/DeepSeek_V4.1_Flash)` from the stored key,
- the real `jev-1.13` classifier from the store,
- `tools.CoreReadOnlyToolsScoped(<session cwd>)`, `PermissionModeReadOnly`,
  `MaxTurns=4`, `ContextWindow=200_000`,
- `InitialMessages` = the raw transcript of each session up to the first real
  user prompt after ~200k tokens, and that prompt as the run prompt.

Sessions:

- `zero_20260909101146_1788948706312871000_1` (kajicode/tui-image)
- `zero_20260908140107_1788876067019677000_336` (zerocarbon/erp)
- `zero_20260914101521_1789380921789735000_35` (zerocarbon/backend)
- `zero_20260903195423_1788465263836881000_39` (zerocarbon/erp-ccts)