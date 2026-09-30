# The judge's decisions now reach the model — measured

This implements Option B from `docs/JEV_COMPACTION_OUTCOME_PLAN.md`: making the
compaction judge's decisions a real, model-visible output instead of an upstream
input the summarizer compresses away. It answers the question that plan left
open (`docs/compaction-judge-outcome-report.md`: rescued-path survival **1.5%**).

Status: **implemented and measured.**

## What changed

**The problem.** The judge restores the stale tool bodies it judges relevant,
but they land in the *summarizer's input*; the context the model reads next turn
is only the summary. Measured: **2 of 252** judge-rescued paths (1.5%) survived
into a summary.

**The fix.** The judge pass already knows every stale tool result and what it
acted on. It now also reports those targets as a bounded, **deterministic** list
(`internal/agent/compaction_artifacts.go`), and `CompactMessages` renders it
verbatim into the injected summary:

```
## Carried artifacts (from tool results removed by compaction; re-read a file for its full body)
- read_file internal/tools/file_media.go: …head of the body…
- grep internal/tui/image_attach.go: …head of the body…
```

No extra classifier call and no model in the loop: the same judged input always
renders the same block (`maxDurableArtifacts` 20, `maxArtifactHeadBytes` 300,
`maxArtifactsBytes` 6 KiB).

The block is rendered **before** the preserved-state JSON. The state block is
located by the *last* occurrence of its label, so keeping the single-line JSON
last means a carried tool body that happens to contain that label text cannot
shadow the real state block on the next compaction (a bug a fresh-eyes review
caught; `TestCompactArtifactBodyContainingLabelDoesNotPoisonState` covers it).
The artifacts block is also stripped from the previous-summary carry, so a stale
list is never handed back to the summarizer as authoritative prior state.

Ranking is **path targets first**, then classifier probability, then newest.
A file path is the one target the model can re-read, and a stale `read_file`/
`grep` result is exactly the artifact whose target it needs next.

Three supporting changes:

- **The judge now runs on all three long-session surfaces.** It was wired into
  headless `exec` only; it is now wired into the **TUI** (`internal/cli/app.go`)
  and **ACP** (`internal/acp/agent.go` + `internal/cli/acp_runtime.go`), where
  long compacting sessions actually run. ACP caches the built classifier per
  session so a turn does not reopen the credential store.
- **Fail-open is at least as good as judge-off.** When the summarizer errors,
  `maybeCompact` returns the deterministic prune floor. Previously it returned
  the larger judged slice, so a judge-ON summarizer failure could leave *more*
  context than judge-off.
- **The summarizer prompt points at the block** (`summaryInstructions`), so the
  prose summary's `## Relevant Files` names the carried paths instead of
  inventing its own.

## Measurement

Real sessions from this machine, through the real `maybeCompact`, with
growwcorp/`fireworks/DeepSeek_V4.1_Flash` and the real `jev-1.13` classifier
(`keepResultThreshold` 0.35, window 200k, `PreserveLast` 12). Four largest
sessions, reconstructing the **full raw transcript** (ignoring
`session_compaction` resets), judge OFF vs ON.

**Carried-path survival** = fraction of judge-rescued file paths present in the
final injected context the model reads. OFF is the prose summary alone; ON
includes the artifacts block.

| Session | msgs | raw tok | rescued paths | OFF survival | **ON survival** |
|---|---|---|---|---|---|
| `…101146_1` | 6,103 | 1,794,808 | 20 | 0.50 | **0.80** |
| `…140107_336` | 6,200 | 1,348,087 | 20 | 0.35 | **0.90** |
| `…03195423_39` | 3,385 | 728,814 | 20 | 0.10 | **0.65** |
| `…101521_35` | 4,336 | 1,111,929 | 20 | 0.00 | **0.85** |

Across three independent runs the ON arm held **0.65–0.90** while OFF held
**0.10–0.50**. The prior build's number was **1.5%** (2/252). The acceptance gate
in the plan — "rises from 1.5% to ≫50%" — is met.

The carried targets are the right ones. From `…101146_1` (an image-attachment
task): `internal/imageinput/normalize.go`, `internal/modelregistry/models.go`,
`internal/tools/file_media.go`, `internal/tui/image_attach.go`,
`internal/agent/compaction.go`, `internal/modelregistry/catalog.go`, … — the
files that session actually worked on.

**Context cost.** Final context grows **+2.9% to +14.2%** (~2–4k tokens) on the
sessions that completed. That is the intended trade: the feature is relevance
*preservation*, not shrink, and the block is hard-capped.

## Honest caveats

- **Some arms failed and are excluded, not hidden.** These sessions push ~800k
  tokens into the summarizer, and the DeepSeek summarizer intermittently errors
  on that input. The failing arm returns the prune floor (fail-open), so the
  number is large; it is a **summarizer-input-size** failure, not a classifier
  failure (a separate probe: **20/20** batch round trips to `jev-1.13`
  succeeded). 3 of 4 sessions completed across each run.
- **The bounded block carries the top 20 path targets.** A session with more
  than 20 relevant paths, or relevance in a non-path target, is not fully
  covered. Raising the cap trades context for coverage; the default favours a
  negligible token cost.
- **Four sessions, one model, one classifier, one threshold.** Do not
  over-generalize.
- **Now measured downstream:** `docs/compaction-behavior-report.md` drives the
  real agent loop with the judge OFF vs ON and asks the compacted model (no
  tools) which files are in play. Verdict: the block is real and directionally
  correct but small — it turns 0/27 into 3/27 for paths the OFF context never
  contained, because tool-call arguments already preserve most in-play paths.
  The next-turn re-read comparison over a longer, edit-oriented continuation
  remains unbuilt.

## Files

- **Created:** `internal/agent/compaction_artifacts.go`,
  `internal/agent/compaction_artifacts_test.go`.
- **Modified:** `internal/agent/compaction_judge.go` (report + rank artifacts),
  `internal/agent/compaction.go` (carry the block; fail-open to the prune
  floor; prompt line), `internal/cli/app.go`, `internal/acp/agent.go`,
  `internal/cli/acp_runtime.go`, `internal/cli/classifier_runtime.go`.
- **Removed:** the temporary measurement harness (in-package tests under
  `zz_*_test.go`), as the prior rounds did.

## Verification

```
make fmt-check
go vet ./...
go test ./...
go run ./cmd/kajicode-release build --version 0.0.0-dev
go run ./cmd/kajicode-release smoke --version 0.0.0-dev
git diff --check
```

`go test ./internal/agent -run 'TestJudge|TestMaybeCompact|TestCompact|TestArtifact|TestFormatArtifacts'`
covers: the block renders the targets verbatim; the path-first ranking; the
count/byte caps; a nil artifact set is byte-identical to judge-off; an artifact
still in the verbatim tail is not duplicated; and artifact-target extraction
across `path`/`file_path`/`pattern`/`command`/`query`/`url`.
