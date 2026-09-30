# Making Jev/classifier compaction actually better — investigation + plan

Status: **investigation complete, plan implemented — with one lever refuted by
measurement.** The default uncertain band this plan proposed in §5.4
(`dropLow = 0.2`) was tested on KajiCode's own sessions and is **wrong for this
workload**: the classifier puts almost all mass in 0.2–0.4, so a 0.2 floor keeps
3,124/3,134 bodies and reclaims ~6.8k tokens — it stops filtering and makes the
summarizer run more. The band therefore ships **opt-in (off by default)**. The
implemented, measured outcome is in `docs/compaction-band-report.md`; read that
first.

This document supersedes the lever assumptions in
`docs/JEV_COMPACTION_OUTCOME_PLAN.md`: the lever that plan leaned on (*winnow's*
`structured` phrasing + a lower threshold) was tested on KajiCode's own data and
**does not transfer**. See §2.

Prior docs this builds on (do not re-derive):
`docs/JEV_COMPACTION_FILTER_PLAN.md` (what shipped),
`docs/compaction-judge-outcome-report.md` (the flat-outcome measurement),
`docs/compaction-dryrun-report.md` (token-reclaim measurement),
`docs/compaction-artifacts-report.md` (the carry block), and
`docs/compaction-behavior-report.md` (the loop-level behavior probe).

---

## 1. Understand the issue

**What is happening.** With a classifier configured and
`classifier.features.compaction.enabled=true`, compaction runs, the judge asks
`jev-1.13` about every stale item, and the compaction the model ends up reading
is **within ±0.1% of the judge-off compaction**. The judge is correct and inert.

**What should happen.** Compaction should get *better for the model*: the stale
items that are still needed should survive, and the ones that are not should go.

**Where it starts.** `maybeCompact` (`internal/agent/compaction.go:663`):
free prune (`prune.go:66`) → judge filter (`compaction_judge.go:102`) →
threshold check (`:714`) → paid summarizer (`CompactMessages`, `:379`).

**When.** Every compaction where the judge runs and the history is big enough to
reach the summarizer. Fully reproducible — it is arithmetic, not timing.

**Reproducible? Yes** — proven three separate ways in the reports above
(token delta ~0, rescued-path survival into the *prose summary* 1.5%, loop-level
behavior probe 0/27 → 3/27). This document adds a fourth, and it is the
disqualifying one for the prior plan.

---

## 2. New evidence (this investigation) — the prior plan's lever is refuted

The carried-forward plan (`JEV_COMPACTION_OUTCOME_PLAN.md`) offered two changes
that were supposed to make the judge load-bearing:

1. switch the judge question to winnow's **`structured`** phrasing (`what` /
   `not_for` / `examples`), and
2. lower the default threshold from `0.35` toward the **clean `0.10` bin**.

Both were measured this pass, on **KajiCode's own four largest sessions**, with
the real `jev-1.13`. A throwaway probe (deleted; see §7) reconstructed each
session's full transcript from `events.jsonl`, applied `judgeCandidates`' staleness
rule, built a weak ground truth (**a stale body is "needed" iff its target string
is re-referenced later in the same session** — the same proxy
`compaction-judge-outcome-report.md` used), and sent 400 bodies per phrasing to
the live endpoint. 1,200 judged bodies total.

**Result — the phrasing does not help, and the low bin is not clean here:**

| phrasing | AUC | n | p<0.1 bin | kept @0.35 | precision @0.35 | needs caught @0.35 |
|---|---|---|---|---|---|---|
| **`current`** (what KajiCode ships) | **0.628** | 400 | 0 | 51 | 0.47 | 24/105 |
| `structured` (winnow, what the plan proposed) | 0.560 | 400 | 2 (both needed) | 180 | 0.31 | 55/105 |
| `fastjev` (fast-jev-compaction production) | 0.438 | 400 | 0 | 10 | 0.10 | 1/105 |

Read honestly:

- **The `structured` phrasing is *worse* here, not better** (AUC 0.560 vs 0.628).
  winnow's own numbers (0.701 vs 0.635) were measured on **per-block file-read
  chunks**; KajiCode's items are whole tool bodies. The lever is **workload-
  specific and did not transfer**.
- **The clean p<0.1 bin does not exist on KajiCode data.** The shipped phrasing
  puts **0 of 400** bodies below 0.1; `fastjev` also 0; `structured` puts 2, and
  **both are needed**. winnow's "0% needed below 0.1" was n=10. So the plan's
  proposed `durable threshold = 0.10` would select **an empty or wrong set** —
  it would prune *nothing* (probe: `thr=0.1 keep 400/400`) or keep only the two
  wrongly-classified bodies.
- **`fastjev`'s production phrasing is anti-correlated here** (AUC 0.438 < 0.5).
  Copying the reference implementation's *words* would make KajiCode **worse**.
- The `current` phrasing is the **best available** of the three (AUC 0.628,
  precision 0.47 at 0.35) — which matches winnow's finding that Jev's ordering is
  modest (their AUC ~0.70, lexical baseline 0.635) and that **the low tail, not
  the ranking, is the value**. On KajiCode data the low tail is not yet clean.

### 2.1 What this proves

The judge is not the bottleneck the prior plan thought. The unresolved problem is
**calibration on KajiCode's workload**, and the code as shipped has already chosen
the best of the three phrasings. Chasing phrasing/threshold constants is not the
lever; at `0.35` the judge keeps 51/400 bodies at 0.47 precision — it *is* doing
meaningful selection. The remaining gap is that its decisions still do not change
the model-visible outcome (§3).

### 2.2 The shipped threshold is a cliff — a real, fixable defect

The probe also shows the current selection is a **hard cliff**: `0.35` keeps
51/400, `0.5` keeps 2/400 (the probabilities cluster in 0.2–0.5). A cliff means a
tiny probability shift flips a body between "kept verbatim" and "pruned", with no
middle. That is fragile, not calibrated. §5.4 addresses it with a cheap,
shippable change that does not depend on any phrasing transfer.

---

## 3. Root cause (unchanged and confirmed)

The pipeline is four stages; the judge is an **upstream pre-filter for a lossy
downstream summarizer**:

```
maybeCompact (compaction.go:663)
  stage 1  free positional prune       prune.go:66          drops old tool bodies by age/size
  stage 2  judge filter (optional)      compaction_judge.go:102
  stage 3  threshold check              compaction.go:714
  stage 4  LLM summarizer               CompactMessages :379 -> renderTranscript :1020
```

At stage 4 the elided middle (`middle := messages[systemEnd:boundary]`,
`compaction.go:431`) is handed to `opts.Summarize(middle)` (`:440`); the model next
turn reads **only the summary** (`:448-461`). The judge's kept bodies are *input*
to the summarizer, not *output* of it. Three compounding blocks (`JEV_COMPACTION_OUTCOME_PLAN.md`
§2): a ~450k–790k-token summarizer input, a 2000-byte per-body cap
(`summarizeToolResultMaxBytes`, `compaction.go:1057`), and a strict, terse summary
template (`summaryTemplate`, `:95`).

The reference implementations confirm the fix direction by *not doing this*:

- **`tamaratran/fast-jev-compaction`** (`/tmp/jevresearch/.../src/compact.ts`) —
  README: *"replaces the compaction summary with Jev decisions … never rewrites
  anything. It only deletes tool calls and tool results Jev says are no longer
  needed, and it asks Jev while showing it the whole conversation. User and
  assistant text stays verbatim and in order."* Two nouls per call
  (`call_${id}` keep-the-call, `result_${id}` keep-the-result), 3-way decision
  (`decideCall:101`), **no LLM summarizer**. Evidence-gated in the README
  (`compaction.ts`): *"no result is ever left without its call"* — the drop is
  applied to call **and** result together (`applyDecisions:171,190`).
- **`GhalebDweikat/winnow`** — a **live** gate at the tool boundary: block → judge
  → hide confident-no blocks as a stub **with a recall key** (`winnow_recall`),
  an error gate (hide nothing if P(error) ≥ 0.5), a minimum-prune-ratio gate, and
  a **shadow mode**. Its DESIGN states the design principle directly: *"A summary
  is lossy … a calibrated per-block probability lets code make the keep/hide
  decision deterministically, and lets the threshold be tuned against a measurable
  regret rate. The summarizer is only there … on what the judge already decided
  to hide, so it is cheap and its mistakes are recoverable."*
- **`valentynkit/jev-belay`** (completion gating, not compaction) — the pattern
  that matters elsewhere: a **deterministic evidence gate runs before the judge**
  (AUROC 0.976 vs 0.777 wording-only, ablation `README:226`). Judge the
  *evidence*, not the prose.

KajiCode's judge is a blend that falls between these: pre-filter **and**
summarize, with no recall and no live gate. That blend is why per-item
correctness has no model-visible outcome.

---

## 4. Solution options (compared, then chosen)

The outcome only improves if the judgment **drives what survives into the context
the model reads**.

### Option A — Judge keeps verbatim; summarizer only compresses the drops (fast-jev design)
Rebuild the middle: **keep** judged-relevant items verbatim, **drop/truncate** the
judged-stale ones, and run the summarizer on **only the drops** (or not at all).
- Fit: highest — it is the design two independent projects converge on.
- Complexity: **high at the rebuild layer** — dropping a tool call and its result
  together while keeping provider replay valid is exactly what KajiCode's
  `Message`/`ToolCall` model was written *not* to do (`compaction_judge.go:29`,
  "It never drops a message").
- Reliability: the real risk is dangling tool results / role-alternation breaks
  (`safeSuffixBoundary`, `compaction.go:485`, exists for this).
- Verdict: **the correct end state, but not the first step** — it changes
  compaction semantics (keeps a *large* context) and touches replay invariants.

### Option B — Judge-selected artifacts, verbatim (already shipped; flat)
Carry the judged targets into the injected summary. Implemented
(`compaction_artifacts.go`); measured small (`docs/compaction-behavior-report.md`:
0/27 → 3/27) because the channel it fights is already mostly intact. **Keep it;
it is a net positive and free.** It is not the lever.

### Option C — Fix the judge's calibration + make drops recoverable (this plan)
Two changes that are **measurable locally and do not depend on phrasing transfer**:
1. **A two-threshold band** replaces the cliff (§5.4): an explicit "uncertain"
   band that **keeps** the body, so only a confident-no is dropped — winnow's
   proven-safe direction, and a strict improvement over a single cliff.
2. **Recall keys for pruned bodies** (winnow's stub): a dropped body is stored in
   the existing session blob dir and the placeholder names its key, so "dropped"
   ≠ "lost". This makes a *lower* drop threshold safe, which is the only way a
   more aggressive filter can ever be justified.
- Fit: high — reuses the blob store (`internal/sessions/checkpoint.go:177`) and
  the existing placeholder/`prune.go` path; no replay-invariant risk.
- Verdict: **chosen.** It is the smallest change that is both measurable and a
  strict improvement, and it is the prerequisite that makes Option A's more
  aggressive drops safe.

### Option D — Live tool-result gate at the tool boundary (winnow)
Judge each large tool result as it is produced and hide confident-no blocks.
- Fit: architecturally where the leverage is (bloat never accumulates; compaction
  gets cheap).
- Complexity/risk: highest — touches the tool hot path, needs a recall key, an
  error guard, and a finalization hook.
- Verdict: **the right eventual target, a separate phase.** Option C's recall key
  is its prerequisite, so C builds the piece D needs.

**Chosen: C, then re-measure; A and D are later phases that C unblocks.**

---

## 5. Proposed change (detailed)

### 5.1 Recall keys for pruned bodies (the load-bearing change)

Today a judged-irrelevant body becomes `prunedPlaceholder` — a note saying
"re-run the tool if you need it again" (`prune.go:39`). For a non-idempotent read
or a fetched page (`web_fetch`, `code_search`, a deleted file) "re-run" is not
recovery; the bytes are gone.

- Add `Store.WritePrunedBlob(sessionID, body) (key, error)` that reuses the
  content-addressed writer (`checkpoint.go:177`, sha256, non-fatal on failure) and
  returns the hex hash.
- Add a `pruneStaleToolOutputStoring(messages, preserveLast, store, sessionID)`
  variant (beside `prune.go:66`) that, for each recorded replacement, writes the
  body and emits `prunedPlaceholderWithKey(tool, tokens, key)`. Fail-open: if the
  store is nil or the write fails, emit today's `prunedPlaceholder`.
- Thread the store + session id into `maybeCompact` via `Options` (already carries
  `Registry`, `Sandbox`, etc.); nil ⇒ byte-identical to today.
- Register `pruned_blob_*` referenced blobs with the existing orphan lifecycle
  (`pruneOrphanBlobs`, `checkpoint.go:255`) so they are reclaimed correctly — or,
  simpler, write under a dedicated `pruned/` subtree that pruneOrphanBlobs does
  not own, and document its lifecycle. **Decision: write under the existing
  content-addressed dir and extend `referencedBlobs` to include pruned-body keys,
  so there is one lifecycle, not two.**

### 5.2 A recall surface so a drop is reversible

Add one read-only tool `recall_pruned` (name final against the `recall` tool,
`internal/tools/recall.go`, to avoid confusion): given a key, read the blob and
return its body. Register it only when a session store is present (same gating as
other session-scoped tools), and include it in the tool set the loop exposes. Its
description states the contract: "Retrieve a tool result that compaction pruned.
Call this with the key from a pruned placeholder before re-running the tool."

### 5.3 (Deferred) Option A groundwork

Not implemented here. It becomes the next step only if §5.1/§5.2 measured outcome
is still weak; the drop-a-call-with-its-result rebuild is its own change with its
own replay-validity tests.

### 5.4 Remove the calibration cliff (cheap, local, measurable)

Replace the single `keepThreshold` comparison with a three-way band over the one
existing noul:

```
p >= keepHigh        -> keep the body verbatim in history
dropLow <= p < high  -> KEEP (uncertain: keep, it is cheap and safe)
p < dropLow          -> prune (now recoverable via §5.1)
```

- Defaults: `keepHigh = 0.5`, `dropLow = 0.2`. Rationale: the probe shows the mass
  sits in 0.2–0.5 with only 17–25% needed there, so **keeping the whole uncertain
  band is the safe default** — it can only *raise* retention, and the recall key
  makes the rest recoverable.
- This is a **strict superset of the current keep set** (everything ≥0.35 was
  already kept; 0.2–0.35 is now kept too), so it cannot regress on lost
  information and cannot make the summarizer run more often.
- Expose `dropLow` / `keepHigh` under `classifier.features.compaction` on the
  existing 4-place config rule (`internal/config/{types,validate,resolver}.go`,
  `classifier_key`). Keep `DefaultKeepResultThreshold` as the `keepHigh` default
  so an existing config is unchanged.

### 5.5 Make the judge cover the whole requested scope

`judgeCandidates` (`compaction_judge.go:291`) collects stale tool-result bodies,
tool-call arguments, and assistant narration — matching the user's original ask
("every user / assistant / tool call / thinking block"). It does **not** yet:
- ask a *task-level* question (the user's idea 3: "are we on track / is this
  relevant to the plan"). Add a second, low-priority question set that is only
  enabled by a feature flag, reusing the completion/reference-implementation
  pattern (jev-belay's evidence-gate-first rule, §3). **Deferred to a later phase**
  — it is a different consumer, not compaction.

### 5.6 Do NOT change the phrasing

§2 refutes the transferable-phrasing hypothesis on KajiCode data. Keep the shipped
question. If a future pass re-tests phrasing, it must re-run the §7 probe and
report AUC **per session**, because this run shows the per-session AUC varies
0.45–0.75 and a single global number hides that.

---

## 6. Implementation steps

> Order matters: 5.1 → 5.2 → 5.4, verifying after each. 5.1/5.2 change nothing
> observable alone (they add a safety net); 5.4 is the one that changes behavior,
> and it is only safe because 5.1 landed first.

1. **`checkpoint.go` — pruned-blob writer.**
   Add `WritePrunedBlob` reusing `writeBlob` (`:177`) and extend
   `referencedBlobs` (`:297`) to include pruned keys. *Why:* the recall net.
   Test: round-trip a blob; `pruneOrphanBlobs` keeps a referenced pruned key.

2. **`prune.go` — storing variant + keyed placeholder.**
   Add `pruneStaleToolOutputStoring(...)` and `prunedPlaceholderWithKey(...)`,
   keeping the old functions for the no-store path. *Why:* every pruned body
   recoverable. Test: with a store, the placeholder contains the key and the blob
   round-trips; without a store, output is byte-identical to today.

3. **`compaction.go` / `types.go` — thread the store.**
   Add `Options.PrunedBlobStore` + `Options.SessionID`; `maybeCompact` picks the
   storing variant when non-nil. *Why:* wire the capability without changing the
   default path. Test: nil store ⇒ byte-identical `maybeCompact` output.

4. **`internal/tools` — `recall_pruned` tool.**
   Read-only, session-scoped, registered only with a store. Test: round-trip and
   missing-key error.

5. **`classifier/profile.go` + config — the two-threshold band.**
   Add `DropResultThreshold`/`KeepResultThreshold` (keep the latter's default
   0.35 → used as `keepHigh`), 4-place config plumbing, `Options` fields.
   *Why:* remove the cliff. Test: band semantics at the boundaries; an unset
   config yields the current behavior minus the cliff (retention can only go up).

6. **`compaction_judge.go` — apply the band.**
   Replace `>= keepThreshold` with the three-way decision; the uncertain band
   keeps. *Why:* the calibrated, safe filter. Test: p in the band is kept; p below
   `dropLow` is pruned and the placeholder names a key.

7. **Measure (the acceptance gate).**
   Re-run the four-session A/B (`docs/compaction-judge-outcome-report.md`) +
   the loop-level behavior probe (`docs/compaction-behavior-report.md`). Report
   **per session**: kept-body differentiated-precision, retained-context delta,
   and — the new one — **recall round-trips** (prune N bodies, ask the model via
   `recall_pruned` to name a dropped target, verify it can). Success criterion: the
   uncertain band raises retained differentiation at `+≤3%` context, and recall
   round-trips deterministically. **If the retained-context differentiation is
   still ~0, stop and re-diagnose before Option A.**

---

## 7. Files affected

**Modified**
- `internal/sessions/checkpoint.go` — `WritePrunedBlob`; `referencedBlobs` includes
  pruned keys.
- `internal/agent/prune.go` — storing variant + keyed placeholder.
- `internal/agent/compaction.go` — store threading; band in the keep decision path.
- `internal/agent/compaction_judge.go` — three-way band; (no phrasing change).
- `internal/agent/types.go` — `Options` plumbing (`PrunedBlobStore`, `SessionID`,
  threshold pair).
- `internal/classifier/profile.go` — threshold pair + defaults.
- `internal/config/{types,validate,resolver}.go` — the 4-place rule.
- `internal/cli/{app,exec}.go`, `internal/acp/agent.go`,
  `internal/cli/acp_runtime.go` — pass the store + thresholds.
- `docs/architecture.md` — compaction stage note.

**Created**
- `internal/tools/recall_pruned.go` (+ test).
- `internal/agent/compaction_prune_blob_test.go` (or fold into the existing
  compaction tests) — band + store tests.

**Removed**
- Nothing in this phase. (If Option A later lands, the duplicated "restore
  verbatim into history" branch in `judgeStaleHistory`, `compaction_judge.go:129`,
  becomes redundant and goes then.)

No new dependencies. Nothing outside the workspace is touched.

---

## 8. Testing and verification

**Reproduce the current state (before any change).** With a configured classifier:
```bash
go test ./internal/agent -run 'TestJudge|TestMaybeCompact|TestCompact|TestArtifact'
```
and re-run the four-session A/B — confirm the flat outcome (final context within
~0.1%, differentiated retention ~0). That is the "before" number.

**After the change, the same harness must show:**
- the uncertain band raises **differentiated** retention (kept-needed bodies /
  dropped-needed bodies) versus the single-cliff baseline, deterministically;
- retained context grows by **≤ ~3%**, never unbounded;
- **recall round-trips**: for a body pruned below `dropLow`, its key resolves and
  returns the exact bytes;
- a nil store ⇒ `maybeCompact` output byte-identical to today.

**Unit/regression tests (per step):** blob round-trip; placeholder key present;
band boundaries (just below `dropLow` pruned, inside the band kept, above
`keepHigh` kept); preserved-state/reasoning invariants untouched; idempotence.

**Edge cases:** nil store (byte-identical); blob write failure (fail-open to the
old placeholder); a summary written by the previous build (parse must not error);
an already-pruned body (idempotent); `todo_write`/`tool_search`/`skill`/
`write_file`/`edit_file` arguments never dropped; a message carrying `Reasoning`
never touched.

**Build/type/lint/security:**
```bash
make fmt-check
go vet ./...
go test ./...
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```
`go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./...` — the pruned-blob path
writes files under the session dir, so it is release/security-sensitive.

---

## 9. Cleanup

- Delete the §2 throwaway probe and its `/tmp` scratch (the key file
  `/tmp/.jevkey_probe`) after the numbers are folded into this doc.
- Keep the outcome/dryrun/behavior reports; update their "unmeasured"/lever
  caveats to point at §2's refutation so the phrasing/threshold hypothesis is not
  re-derived by a later session.
- If the band makes the single-threshold path unused, delete the dead branch and
  the now-unused `DefaultKeepResultThreshold` alias (or keep it strictly as the
  `keepHigh` default).
- No comment churn; no dead code left in `prune.go` if the non-storing path is only
  the nil-store branch.

---

## 10. Risks

- **The recall net adds disk usage + a read path.** Stored under the session dir
  and wired into `pruneOrphanBlobs`; verify the lifecycle so keys are not orphaned.
  This is the one genuinely new production surface.
- **The band is a retention *raise*, not a shrink.** By design (relevance feature);
  capped by the same estimator and reported. It cannot lose information.
- **§2's ground truth is a string-reuse proxy** (precision ceiling by
  construction), and the probe used the four largest sessions + one model + one
  classifier. Marked: the phrasing conclusion is solid *for these sessions*; the
  band recommendation is directionally safe regardless.
- **Per-session AUC variance is real** (0.45–0.75). Any phrasing re-test must
  report per session, not one global AUC.
- **Still unmeasured:** whether the model *behaves* better (fewer re-reads). The
  recall key makes the boundary measurable (the model can be *asked* to recall),
  but the re-read comparison over a long continuation remains the only true
  downstream test and is not built here.
- **Option A remains the higher-ceiling design.** §5.1/§5.2 are its prerequisites;
  if the band + recall measurement is still flat, A is the next step and its real
  risk (dropping a call with its result without breaking replay) is the thing this
  plan deliberately defers.
