# Making Jev/classifier compaction *actually better* — investigation + plan

Status: **implemented and measured** — see
`docs/compaction-artifacts-report.md`. This document is the record of the
investigation (why the shipped judge changed nothing on the model-visible
outcome) and the plan that was carried out (Option B: judge-selected artifacts
carried verbatim into the injected summary).
Scope: why the shipped compaction judge changes nothing on the model-visible
outcome, and the smallest change that makes it measurable.

Prior docs this builds on (do not re-derive):
`docs/JEV_COMPACTION_FILTER_PLAN.md` (what shipped),
`docs/compaction-judge-outcome-report.md` (the flat-outcome measurement),
`docs/compaction-dryrun-report.md` (the token-reclaim measurement).

---

## 1. Symptom → what is actually happening

**What happens.** With a classifier configured and
`classifier.features.compaction.enabled=true`, compaction runs, the judge asks
`jev-1.13` about every stale item, and the resulting context is **within ±0.1%
of the judge-off context**. Measured on four real sessions: Δ +150 to +1,306
tokens, one −371, over ~1M-token histories.

**What should happen.** The judge keeps the still-relevant artifacts and drops
the relevant-irrelevant ones, so the model should *retain* information it would
otherwise lose — measurably, in the context it reads next turn.

**What the judge actually decides (correct).** `compaction-judge-outcome-report.md`:
across 413 ground-truth "still needed" stale bodies the judge keeps 116 (precision
0.40 / recall 0.28); the free prune keeps 0 (recall 0.00 by construction). The
classifier is doing its job.

**Where the win disappears.** Of the **252 file paths** the judge rescues out of
bodies the blind prune destroys, only **2 (1.5%)** appear in any resulting
summary — and only in 1 of 4 sessions. So the judge's correct decision is made
and then **structurally discarded one stage downstream**.

**When.** Every compaction where the judge runs and the history is large enough
to hit the summarizer. Reproducible: it is arithmetic, not timing.

---

## 2. Root cause

The compaction pipeline is four stages (`internal/agent/compaction.go`):

```
maybeCompact (:636)
  stage 1  free positional prune          prune.go:66        (drops old tool bodies by age)
  stage 2  judge filter (optional)        compaction_judge.go:90  (restores judged-relevant
                                                                bodies, rewrites args/narration)
  stage 3  threshold check                compaction.go:685  (skip summarizer if it now fits)
  stage 4  LLM summarizer                 compaction.go:693 → CompactMessages (:365)
```

**The judge is an *upstream* pre-filter for a *lossy downstream summarizer*.**

At stage 4 `CompactMessages` takes the elided middle
(`middle := messages[systemEnd:boundary]`, `compaction.go:417`) and hands it to
`opts.Summarize(middle)` (`:426`). That summarizer produces a short prose
summary (`summaryTemplate`, `compaction.go:95`), and the long-lived context the
model reads next turn is **only that summary** (`:438-441`) plus the preserved
tail. The judge's rescued bodies live and die inside `middle`: they are input to
the summarizer, not output of it.

Three compounding blocks make the rescues unable to cross that boundary:

1. **Scale mismatch (the dominant one).** The summarizer's input is ~450k–790k
   tokens; the judge rescues 13–132 paths into it. A correct signal at a 0.01%
   duty cycle cannot change a summary.
2. **`renderTranscript` caps every tool body at 2000 bytes**
   (`summarizeToolResultMaxBytes`, `compaction.go:1023`; applied at `:1010`). A
   rescued 20k-token body contributes at most its first 2 KB to the summarizer
   prompt.
3. **The strict summary template** (Objective / Important Details / Work State /
   Next Move / Relevant Files, `compaction.go:95-115`) makes the model emit a
   terse narrative; "Relevant Files" is model-brevity-limited, not judge-driven.

**Therefore: the judge is correct and non-load-bearing.** Its output cannot
affect the model-visible result by construction. This is not a tuning problem —
`0.35` vs `0.5` cannot move a signal nobody reads.

### 2.1 The reference implementations do not do this

Read from source (cloned under `/tmp/jevresearch`, analysis only):

- **`tamaratran/fast-jev-compaction`** (`src/compact.ts`, `src/state.ts`) —
  its README: *"replaces the compaction summary with Jev decisions."* It sends
  the **whole conversation** as state (tool results replaced by
  `ok, N chars (omitted)` skeleton notes, `state.ts:106`), asks two nouls per
  call (`call_${id}` keep-the-call, `result_${id}` keep-the-result,
  `compact.ts:56-67`), and applies a 3-way decision (`decideCall`, `:101-115`):
  `keepResult ≥ T` → keep both; else `keepCall ≥ T` → keep the call + truncate
  the result to `truncateHeadChars` (300) + a note; else drop call **and** result.
  **No LLM summarizer follows.** Nothing is rewritten except staleness deletes, so
  what the judge keeps is exactly what the model sees.
- **`GhalebDweikat/winnow`** (`sidecar/src/winnow/`) — a **live** gate at the
  tool boundary: block → judge → hide confident-no blocks as a 3-line stub **with
  a recall key that restores the full text on demand** (`stub.py`), error guard
  (hide nothing if `error_present`), and a shadow mode.

KajiCode's judge is a blend that falls between the two: pre-filter *and*
summarize. That blend is why per-item correctness has no outcome.

### 2.2 The empirical facts that should drive the change

From winnow's hand-labeled calibration (300 cases, 97 blind hand labels) — the
only published per-bin accuracy data for a Jev-shaped judge:

- Jev's **p<0.1 bin is perfectly clean** (0% of blocks were actually needed);
  its **p≥0.7 bin is 1.00 needed**; it is **systematically underconfident above
  0.1** (observed need runs 0.15–0.30 above the stated probability).
- ⇒ the useful operating point is a **low** threshold. KajiCode uses `0.35`
  (`classifier.DefaultKeepResultThreshold`).
- **Question phrasing** was the biggest lever: moving from a one-line criterion
  to a **`structured`** question (*what / not_for / examples*, referencing the
  state by path, e.g. "Is `blocks.b007` needed to accomplish `task`?") moved
  **2.5× more text into the clean p<0.1 bin at zero hand-label regret**. A
  `strict` ("directly about the task") phrasing made Jev **overconfident and
  unsafe**.
- Ordering (AUC) is only ~0.70 and a lexical baseline is 0.635 — **what earns
  Jev its place is the clean low tail**, i.e. the threshold, not the ranking.

### 2.3 Request budget (constrains any batching change)

`jev-1.13` bounds a request at **64k tokens total** (state + all questions) and
**32k tokens for state + the single longest question**. KajiCode batches by
bytes (`judgeStateByteBudget = 30_000`, `compaction_judge.go:38`) and never sends
a task header, only a `goal`.

---

## 3. Solution options (compared, then chosen)

The outcome can only improve if the judgment **drives what survives into the
context the model reads**. Four ways:

### Option A — Judge **replaces** summarization for kept bodies (fast-jev-compaction)
Rebuild the middle: drop what is judged stale, keep everything else verbatim, no
summarizer.
- **Fit:** highest. It is the one design empirically shown to work, and the judge
  already makes exactly this keep/drop decision.
- **Complexity:** low at the decision layer (judge already exists), **high at the
  rebuild layer** — dropping a tool call + its result while keeping provider
  replay valid is the risky part and KajiCode's `Message`/`ToolCall` model did
  **not** consider this (the judge was written to *never drop a message*,
  `compaction_judge.go:29`).
- **Reliability:** risk of dangling tool results / role-alternation breaks
  (`safeSuffixBoundary`, `compaction.go:458` exists precisely because providers
  reject those). Also changes compaction semantics wholesale: on a huge session
  this keeps a *large* context, the opposite of compaction's purpose.
- **Verdict:** best long-term, too large and too risky to be the first step.

### Option B — **Judge-selected durable artifacts** (this plan's recommendation)
Keep the summarizer, but make the judge's decisions a real *output*:
1. route judged-**relevant** stale bodies into a bounded, **verbatim** block
   appended to the injected summary, reusing the existing
   `appendPreservedState` mechanism (`compaction_preserve.go:466`);
2. give judged-**irrelevant** stale bodies a stable **recall key** in the
   placeholder so a dropped body is *re-fetchable* rather than lost (winnow's
   `stub.py` pattern), stored in the existing content-addressed session blob
   store (`internal/sessions/checkpoint.go`).
- **Fit:** high. Both mechanisms already exist; the judge already produces the
  decision; the output channel (`appendPreservedState`) is exactly "structured
  state carried across compaction verbatim".
- **Complexity:** low-to-moderate. Needs one judge signal for the durable set
  (the existing `keepResult` noul re-used at a **low secondary threshold**), a
  bounded renderer, and a recall tool.
- **Determinism:** the append can be made **fully deterministic** (bounded body
  slices, no new model call), so the outcome is reproducible and testable.
- **Risk:** raises retained tokens — but **bounded** (see §4), and the tail cap
  precedent (`maxPreservedSkillBytes = 2KiB`, `compaction_preserve.go:59`) already
  shows the house pattern for budgeting carried state.

### Option C — Fix the summarizer prompt only
Feed the list of kept paths into `summaryInstructions` and require them in
"Relevant Files".
- **Fit:** trivial, cheap.
- **Reliability:** weak. It addresses only blocker 3, leaves the 2000-byte cap
  (blocker 2) and the scale mismatch (blocker 1) intact, and it is **model-
  dependent and non-deterministic** — the exact property that made the current
  feature unmeasurable.
- **Verdict:** a nice complement to B, not a substitute.

### Option D — Live tool-result gate at the tool boundary (winnow)
Judge each tool result as it is produced (>1500 chars) and hide only the
confident-no ones.
- **Fit:** architecturally where the leverage is (compaction becomes cheap
  because the bloat never accumulates).
- **Complexity/risk:** highest — it touches the tool-result hot path, needs a
  recall key, a finalization hook for in-flight results, and an error guard.
- **Verdict:** correct eventual target; a separate phase, not this one.

**Chosen: Option B, sequenced after two cheap correctness/quality fixes (A1/A2
below).** B is the smallest change that makes the *decision* load-bearing and the
outcome *deterministic and measurable*, it reuses two existing mechanisms
(verbatim preserved-state block + content-addressed blob store), and it does not
touch the provider-replay invariants that Option A would put at risk. C is folded
in as the trivial complement. D is explicitly deferred.

---

## 4. Proposed change (detailed)

### A. Correctness fixes to the judge (small, do first)

**A1 — judge failures must not silently discard the judge's work.**
`maybeCompact` applies the judge to `chosen` (`compaction.go:672-677`), then
calls `CompactMessages` (`:693`). If the summarizer errors, `:704` returns the
**original** `messages`, throwing away the judged slice `chosen` — a strictly
better history is discarded for no reason, and (if A2 lands) the judge's
deletions are lost too. **Fix:** on summarizer failure return the pre-summarizer
`chosen` (pruned + judged) instead of `messages`.

**A2 — an "irrelevant" result must be re-fetchable, not destroyed.**
Today a judged-irrelevant tool body becomes `prunedPlaceholder` — a note saying
"re-run the tool if you need it again" (`prune.go:39`). That is lossy for
non-idempotent reads and for cached/fetched content (`web_fetch`, `code_search`).
Adopt winnow's stub: the placeholder carries a **recall key**, and the body is
stored in the existing session blob store. **Fix:** a shared
`pruneStaleToolOutputAndStore` variant that, when a `Classifier`/blob writer is
available, writes each dropped body to
`Store.blobsDir(sessionID)` keyed by sha256 (`internal/sessions/checkpoint.go:51`,
`:176` non-fatal on failure) and rewrites the placeholder to name the key.
Fail-open: if the blob write fails, emit the current placeholder.

### B. Judge-selected durable artifacts (the outcome lever)

**B1 — a "durable" signal from the judge.**
The judge already asks each stale body one noul: *"is this still needed in full
to make progress, or safe to discard and re-run later if needed?"*
(`compaction_judge.go:397`). Keep that question, but **split its use**:
- `keepResult ≥ keepThreshold (default lowered, see B4)` → **restore verbatim in
  the history** (current behavior); these rarely appear in a dump-heavy session,
  which is why the outcome is flat today.
- `keepResult ≥ durableThreshold` (a **lower** bar, e.g. `0.10`) → also record the
  item in the **durable set** that is written verbatim into the post-compaction
  summary block.

This re-uses the single existing question. One noul, two thresholds. Because
winnow's p<0.1 bin is clean, `0.10` selects a small, high-precision set.

**B2 — render the durable set verbatim into the summary block.**
Extend `appendPreservedState` (`compaction_preserve.go:466`) to take the durable
set and emit a new section under the existing label, mirroring the `RecentEdits`
pattern (name + bounded body):
```
## Preserved state (active plan + loaded skills; carried across compaction)
{ ...existing JSON... , "artifacts": [{"tool":"read_file","path":"a.go","body":"<first N bytes>","recall":"<sha>"}] }
```
- The body is a **head slice** of the original (default 512 B, tunable), not the
  whole thing — the path/symbol/error that mattered is in the head, and the full
  body is re-fetchable via the recall key.
- **Hard cap** on the whole section (default 8 KiB, one constant) and a max item
  count (default 20), same discipline as `maxPreservedSkillBytes` /
  `maxRecentEdits`. Estimated cost ≤ ~2k tokens per compaction — negligible
  against a 1M-token history.
- **Deterministic:** no model call; ordered by the judge's probability desc then
  recency; the same input yields the same block.
- **No unreferenced filler:** bodies still go into the summarizer input (so the
  prose can mention them), *and* into this block (so the facts survive even if
  the prose does not).

**B3 — a recall surface so "dropped" ≠ "lost".**
A small read-only tool, e.g. `recall_pruned` (name to be finalized against
existing tool naming), that resolves a recall key for the current session from
the blob store and returns the body. Register it the same way other local tools
are registered in `internal/tools`; gate it on the store being present.
This is what makes the low `durableThreshold` safe: the model can always get the
bytes back.

**B4 — lower the default threshold and make the question `structured`.**
- Generalize `classifier.DefaultKeepResultThreshold` (currently `0.35`,
  `profile.go:105`) into a keep/durable pair; default **keep ≈ 0.5**,
  **durable ≈ 0.10**, both configurable under
  `classifier.features.compaction`.
- Upgrade `judgeQuestions` (`compaction_judge.go:387`) to the **structured**
  phrasing (what / not_for / examples, referencing state by path) that winnow
  measured as the biggest lever. Mark the exact wording as a **tuning target**,
  not a proven optimal (see Risks).
- Add the agent's **task header** to the judge state (winnow's `task`
  `{user_request, assistant_intent}` shape); today `judgeState` sends only
  `goal` (`compaction_judge.go:373`).

### C. Summarizer complement (small)

Add one line to `summaryInstructions` (`compaction.go:120`): the
`## Relevant Files` section must include the paths listed in the preserved-state
block. Cheap; turns the durable block into a prompt signal too.

---

## 5. Implementation steps

> Do A1 → A2 → B1 → B2 (verify the outcome moves) → B3 → B4 → C, verifying after
> each. Steps 3–5 are the ones that change the outcome; do not batch them.

1. **A1 — return the judged slice on summarizer failure.**
   In `maybeCompact` (`compaction.go:693-705`), change the error branch to return
   `chosen` (falling through to `return chosen, false`) rather than `messages`.
   *Why:* the judge's work is a strict improvement; discarding it on an unrelated
   summarizer error is a bug. Add a regression test (summarizer returns error →
   result equals the judged slice, and the judge's rewrites are present).

2. **A2 — recall keys for pruned bodies.**
   Add a blob-writer seam to the prune path. Concretely: a new
   `prune.go` function `pruneStaleToolOutputStoring(messages, preserveLast, store)`
   that behaves exactly like `pruneStaleToolOutput` but, for each recorded
   replacement, calls `store.WriteBlob(sessionID, body)` (reuse
   `internal/sessions/checkpoint.go:176`; non-fatal) and emits
   `prunedPlaceholderWithKey(tool, tokens, hash)`. Keep the old placeholder
   function for the no-store path. *Why:* makes every pruned body recoverable.
   Test: prune with a store → placeholder contains the key and the blob round-trips.

3. **B1 — durable set from the judge.**
   In `judgeStaleHistory` (`compaction_judge.go:90`), add a second threshold and
   return the durable set alongside the rewritten messages (or stash it on a
   result struct). A body with `answer.Probability >= durableThreshold` is added
   to the durable set **regardless of whether it is restored in the history**.
   *Why:* this is the signal that will actually reach the model.
   Test: `keepResult=0.2`, keep=0.5, durable=0.1 → body is dropped from history
   **and** present in the durable set.

4. **B2 — render the durable set into the preserved block.**
   Extend `appendPreservedState` (`compaction_preserve.go:466`) and
   `formatPreservedState` (`:589`) with the artifacts section; thread the durable
   set from `maybeCompact` → `CompactMessages` (new `CompactionOptions` field) →
   `appendPreservedState`. Enforce the byte/item caps. *Why:* the outcome lever.
   Test: with a durable set, the injected summary message contains the artifact
   paths **verbatim**; caps hold on an oversized set; a nil set produces a
   byte-identical block to today.

5. **Measure the outcome (the point of the whole change).**
   Re-run the four-session A/B in `docs/compaction-judge-outcome-report.md`
   (judge OFF vs ON), and additionally assert the **rescued-path survival rate**
   defined there: fraction of judge-rescued paths present in the final injected
   summary context. **Success criterion: this rises from the measured 1.5% (2/252)
   to ≫50%** for the durable set, deterministically. Expect final context to grow
   by the bounded ~2k tokens. Record in the report; if it does not move, stop and
   re-diagnose before doing B3/B4.

6. **B3 — recall tool.**
   Add the read-only tool that resolves a recall key from the session blob store.
   Register in `internal/tools`; test the round-trip and the missing-key error.

7. **B4 — threshold pair, structured question, task header.**
   `classifier/profile.go` (`DefaultKeepResultThreshold` → a pair),
   `internal/config/classifier.go` + `types.go` (4-place config rule),
   `judgeQuestions` (`compaction_judge.go:387`), `judgeState` (`:356`).
   *Why:* winnow's low bin is clean and phrasing is the biggest lever. Test the
   structured question renders the expected criteria and the task header appears.

8. **C — summarizer prompt line.**
   One string edit in `summaryInstructions` (`compaction.go:120`); test the
   instruction text contains the requirement.

---

## 6. Files affected

**Modified**
- `internal/agent/compaction_judge.go` — durable threshold, durable set, structured
  questions, task header in state.
- `internal/agent/compaction.go` — A1 error branch (`:704`); `CompactionOptions`
  gains the durable set; `appendPreservedState` call site (`:434`); one
  `summaryInstructions` line.
- `internal/agent/compaction_preserve.go` — artifacts section in
  `appendPreservedState` / `formatPreservedState`, plus the caps; tolerant parse
  of a missing section on replay (`parsePreservedStateBlock`, `:623`).
- `internal/agent/prune.go` — the storing variant + keyed placeholder.
- `internal/classifier/profile.go` — keep/durable threshold pair.
- `internal/config/classifier.go`, `internal/config/types.go`,
  `internal/config/validate.go`, `internal/config/resolver.go` — threshold fields
  (the four-place rule).
- `internal/agent/types.go` — `Options` plumbing for the new thresholds.
- `docs/architecture.md` — compaction stage note.

**Created**
- `internal/agent/compaction_artifacts.go` — the durable-set renderer + caps
  (keeps `compaction_preserve.go` from growing past its remit; one focused file).
- `internal/tools/recall_pruned.go` (+ test) — the recall tool, if B3 is kept.
- `internal/agent/compaction_artifacts_test.go` — outcome/selection tests.

**Removed**
- The old `prunedPlaceholder`-with-no-store path is retained only as the
  fail-open branch; if A2 lands everywhere, `prunedPlaceholder` stops being the
  primary path — keep the function, drop any caller that still passes no store
  where a store is available.

No new dependencies. Nothing outside the workspace is touched.

---

## 7. Testing and verification

**Reproduce the original issue (before any change).** With the current binary and
a configured classifier:
```bash
go test ./internal/agent -run TestJudge            # selection is already correct
# and re-run the four-session harness from docs/compaction-judge-outcome-report.md
```
Confirm the flat outcome: final context within ~0.1% ON vs OFF, rescued-path
survival ~1.5%. That is the "before" number.

**After the change, the same harness must show:**
- rescued-path survival in the final injected context **> 50%** for the durable
  set (deterministic, no model in the loop) — this is the acceptance gate;
- final context grows by ≤ the configured cap (~2k tokens), never unbounded;
- the judge-ON context is a **superset** of the judge-OFF context for the durable
  paths (nothing the judge kept disappears).

**Unit/regression tests (per step):** A1 error-branch; A2 blob round-trip; B1
durable-set membership; B2 verbatim render + caps + nil-set byte-identity; B3
recall round-trip; B4 question/state shape; C instruction text.

**Edge cases:** nil classifier (byte-identical to today); blob write failure
(fail-open to the old placeholder); preserved-state parse of a summary written by
the previous build (must not error); an oversized durable set (caps hold, items
dropped deterministically); a message carrying `Reasoning` (never touched);
`todo_write`/`tool_search`/`skill`/`write_file`/`edit_file` arguments (never
dropped).

**Build/type/lint/security:**
```bash
make fmt-check
go vet ./...
go test ./...                       # note: pre-existing tui/config failures on this box — stash to confirm
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```
`go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...` if the blob/recall path
touches file permissions (it writes files under the session dir).

---

## 8. Cleanup

- Delete the temporary A/B harness after step 5 and fold its numbers into
  `docs/compaction-judge-outcome-report.md`; remove any `/tmp` scratch (as the
  previous rounds did).
- If B2 makes "restore verbatim into history" redundant for the durable set,
  remove the duplicated restore branch in `judgeStaleHistory`
  (`compaction_judge.go:129-135`) so there is one durable path, not two.
- Retire the "candidate is only ever kept in history" wording in the judge header
  comment (`compaction_judge.go:24-27`) once the durable path exists.
- Drop any threshold constant left unused after the keep/durable split.
- Keep the outcome/dryrun/dump docs, but update the "unmeasured" caveats in
  `docs/JEV_COMPACTION_FILTER_PLAN.md` to point at the new numbers.

---

## 9. Risks

- **Raises retained context by design.** Mitigated by hard byte/item caps
  (default ≤8 KiB / ≤20 items ≈ ~2k tokens), but it is the *opposite* of shrink.
  This is the correct trade for a relevance feature; state it, don't hide it.
- **Threshold `0.10` is borrowed from winnow's workload, not KajiCode's.**
  winnow's clean p<0.1 bin is the best evidence available, but it was measured on
  per-block *file-read* chunks, not KajiCode's tool bodies. **Must be calibrated
  on KajiCode sessions before shipping the default** (raise toward 0.2 if the
  clean bin is not clean here).
- **Structured question phrasing is a hypothesis, not a proven optimum.** winnow
  measured it as the biggest lever; the exact KajiCode wording is a tuning target.
- **A2 puts pruned bodies in the session blob dir.** Adds disk usage and a new
  read path; must respect the existing `pruneOrphanBlobs` lifecycle
  (`internal/sessions/checkpoint.go:251`) so blobs are not orphaned forever.
- **Option A (judge replaces the summarizer) remains the higher-ceiling design.**
  If B2's measured outcome is still weak, A becomes the next step — and its real
  risk (dropping a tool call + result without breaking provider replay) is the
  thing this plan deliberately avoids doing first.
- **Not measured here:** whether the model *behaves* better (fewer re-reads). B2
  makes the retained facts deterministic and visible; it does not prove the model
  uses them. The next-turn re-read metric is still the only true downstream test.
```
