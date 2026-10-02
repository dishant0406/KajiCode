# Tool-Result Gate — Coverage Plan (pre-truncated + short-but-large results)

Status: **implemented** (see `docs/tool-gate-report.md` §1.1/§3 for the measured
result). Written 2026-09-30 against the working tree (the tool gate was
uncommitted work from the previous session: `internal/agent/tool_gate.go` + its
wiring). Both guards described below were removed; the drop threshold was
re-checked on the newly covered tools and kept at 0.2 (§7).

The goal of this plan: make the classifier relevance gate actually reach the
results that flood the context — the **pre-truncated** bodies (`web_fetch`,
large `read_file`/`grep`, `browser_snapshot`) and the **short-but-large** bodies
(`web_search`, most `bash` dumps) — without weakening any safety guard.

---

## 1. Symptom

A live run of `go run ./cmd/kajicode` with the gate enabled showed the gate
judging only a minority of results on a normal turn. The prior report
(`docs/tool-gate-report.md`) measured **3 of 240 results gated (1.2%)** at the
shipped threshold, and the per-tool run showed `web_search` and the largest
`web_fetch` were never even considered.

## 2. Root cause

`maybeGateToolResult` (`internal/agent/tool_gate.go:93`) has two pre-screen
guards, both copied from winnow's per-block design and applied at KajiCode's
**post-budget** stage, where they do not fit:

**(a) A hard 50-line minimum** — `if len(lines) < gateBlockLines*gateMinBlocks`
(`tool_gate.go:121`, duplicated in `gateBlocks`, `tool_gate.go:220`). With
`gateBlockLines = 25` and `gateMinBlocks = 2`, any body under **50 lines** is
skipped. Line count is not context cost: a 17-line/2.6 KB `web_search` and a
30-line/2.4 KB `bash` result are skipped even though they are large in bytes.
This is the sole reason `web_search` is never gated (see §3).

**(b) A blanket skip of already-truncated results** — `if result.Truncated {
return result }` (`tool_gate.go:107`). The output budget
(`internal/tools/output_boundary.go`, `output_ceiling.go:58`) runs inside
`registry.RunWithOptions` **before** the gate, so every result the budget
truncated is skipped — and those are exactly the **largest** bodies: a 64 KB
`web_fetch`, a 128 KB `read_file`, the `web_fetch` body over the 64 KB ceiling,
and `browser_snapshot`.

The `Truncated` skip's stated rationale (`tool_gate.go:104-106`) is *"gating it
would compound the loss and overwrite its spill pointer"*. That rationale holds
only for the minority of pre-truncated results that actually carry a spill —
measured, **63 of 69 carry none** (§3). For those 63 the guard protects a spill
that does not exist.

**Nothing above is a safety guard.** The safety guards are the error belt
(`tool_gate.go:67`), the tool denylist (`gateableTool`, `:205`), the
image/non-OK skips (`:101`), the uncertain band (`:142`), `MinPruneRatio`
(`:160`), and fail-open (`:96`, `:171`). None of them change here.

## 3. Evidence (measured on this machine)

Method: parsed every `tool_result` event in the four largest sessions under
`~/.local/share/kajicode/sessions` and replayed the gate's pre-screen order
(denylist → status → `Truncated` → `MinBytes` → error belt → 50-line minimum),
attributing bytes to the first guard that skips the result.

### 3.1 Coverage — eligible vs each skip (share of result bytes)

| session | ELIGIBLE | below_minlines | pre_truncated | below_minbytes | deny_tool |
|---|---|---|---|---|---|
| `…09101146_1` | 65.1% | 12.2% | 8.1% | 9.0% | 5.0% |
| `…08140107_336` | 49.1% | 12.5% | 3.8% | 25.6% | 6.4% |
| `…14101521_35` | 59.0% | 14.8% | 0.0% | 18.4% | 6.5% |
| `…03195423_39` | 58.7% | 11.9% | 2.3% | 19.1% | 7.2% |

The two guards remove **12–15% (below_minlines)** and **0–8%
(pre_truncated)** of all result bytes from gating. `below_minbytes` is 9–26%
but is the *small* results (< 1500 B) — not worth a classifier call (§4, ruled
out).

### 3.2 What the two blind spots actually are

**below_minlines, by tool (4 sessions):** `bash` 707 results / 1.68 MB
(avg 2.4 KB, avg 32.7 lines), `read_file` 97 / 200 KB, `web_search` 28 / 72 KB
(avg 2.6 KB, 17.5 lines), `grep` 18 / 51 KB.

**pre_truncated, by tool:** `web_fetch` 8 / 183 KB (avg **22.9 KB**),
`read_file` 26 / 162 KB (6.2 KB), `grep` 30 / 156 KB (5.2 KB),
`bash` 2 / 131 KB (**65 KB**), `browser_snapshot` 1 / 64 KB.

**pre_truncated spill presence (4 sessions):** 6 with a `spill_path`, **63
without** (reasons: `head_limit` 28, `max_lines` 26, `upstream_tool_budget` 7,
`limit` 2). The largest bodies — `web_fetch` and `browser_snapshot` — are
exactly the ones truncated by the ceiling with **no** usable spill pointer in
the recorded meta.

### 3.3 Why `web_search` is special

`web_search` (`internal/tools/web_search.go:246`) returns
`formatSearchResults` — 5 results, each a header/snippet/`<content>` line, ~17
lines / 2.6 KB. It passes `MinBytes` (1500), carries no error signal, and is not
denylisted: **the 50-line minimum is the only reason it is never gated.** Its
bytes are inherently multi-line and ≤ 300 B per line, so byte-bounded blocking
($4.1) splits it cleanly into ≥ 2 blocks.

### 3.4 External check (primary sources)

- **winnow** (`GhalebDweikat/winnow`) gates at the tool result with a
  **byte** threshold (`WINNOW_MIN_CHARS = 1500`) plus `len(blocks) < 2 →
  skip`. It does **not** skip pre-truncated results (they fall under its char
  threshold incidentally). Its block size is also 25 lines.
- **fast-jev-compaction** (`tamaratran/fast-jev-compaction`) **re-truncates**
  already-truncated results; it has no "already truncated" skip and no
  line-count minimum.
- **pi-warden** (`DevMortimer/pi-warden`): its relevance compaction "kept whole
  only 1 of the 34 files the agent read again" and ships off — the cautionary
  case on coverage vs regret.

Conclusion: the two guards are **KajiCode-specific conservative additions**, not
shared reference behavior. winnow gates on bytes and needs ≥ 2 blocks; KajiCode
should do the same — the "≥ 2 blocks" granularity requirement is real, the
"50 lines" proxy for it is not.

## 4. Proposed solution

Keep the gate exactly where it is (`loop.go:902`, before persist/display/
observe) and fix the two pre-screen rules. Do **not** move the gate before the
budget: that would require the gate to live in `internal/tools` (it needs the
classifier + task goal, which live in `internal/agent`) — a large, unnecessary
refactor.

### 4.1 Byte-bounded blocks (fixes `web_search` and short-but-large bash)

Change block splitting so a block ends when **adding the next line would push it
past 1500 bytes**, or after **25 lines**, whichever comes first. Closing
*before* the line that would overflow (not after) is what guarantees the split:
any body with ≥ 2 lines and more than `gateBlockBytes` total bytes then yields
≥ 2 blocks.

- `gateBlockBytes = 1500` (new const, next to `gateBlockLines`).
- Replace the guard at `tool_gate.go:121` — `if len(lines) <
  gateBlockLines*gateMinBlocks` — with a plain `blocks := gateBlocks(lines); if
  len(blocks) < gateMinBlocks { return result }`.
- Rewrite `gateBlocks` (`tool_gate.go:219`) to accumulate whole lines and close
  a block before adding a line that would exceed `gateBlockBytes`, or once it
  holds 25 lines. Remove the now-redundant internal 50-line check.
- The one residual case is a genuinely **single-line** body (no `\n` at all):
  its first line already exceeds the cap, so it is one block and is skipped.
  This is rare (a one-line result is almost never worth gating) and is
  documented, not hidden.

### 4.2 Gate pre-truncated results, preserving their spill (fixes `web_fetch`, big reads/greps, `browser_snapshot`)

Delete the blanket `if result.Truncated { return result }` (`tool_gate.go:107`).
Gate pre-truncated results like any other, with one rule for the recall net:

- If `result.Meta["spill_path"]` is set, **reuse it** in the stub and do not
  spill again — that file holds the *pre-budget* (fuller) body, which is
  strictly better than a new spill of the budgeted body.
- Otherwise, spill the body the model sees (`tools.SpillOutput`) and point the
  stub at it.

This makes the existing `spill_path`-preservation test
(`tool_gate_test.go:293`, `TestGateSkipsAlreadyTruncated`) obsolete: its intent
(never clobber a spill pointer) is preserved, but its behavior ("never gated")
is inverted.

Wording: change the stub from *"full output saved to"* to *"output saved to"*,
because for a pre-truncated body with no prior spill the saved file is the
budgeted view, not the original.

### 4.3 Keep everything else

- **Keep `MinBytes = 1500`.** `below_minbytes` is 9–26% of bytes but every
  member is < 1500 B; a stub is ~150 B of overhead plus a classifier round trip,
  so gating one is a net loss. Not worth it.
- **Keep** the error belt, denylist, image/non-OK skips, uncertain band,
  `MinPruneRatio`, and fail-open. Unchanged.
- **Keep** the gate off by default; a nil gate stays byte-identical.

## 5. Implementation steps

1. **Add `gateBlockBytes`** in `internal/agent/tool_gate.go` next to
   `gateBlockLines` (value `1500`), with a comment stating it is the "≥ 2 blocks
   for any gateable body" guarantee.
2. **Rewrite `gateBlocks`** (`tool_gate.go:219`) to break a block at 25 lines
   *or* 1500 bytes accumulated, whichever first, preserving line boundaries;
   return `nil` when fewer than `gateMinBlocks` blocks result. Remove its
   internal 50-line check.
3. **Simplify the call site** (`tool_gate.go:120-124`): delete the
   50-line guard; call `gateBlocks` and `if len(blocks) < gateMinBlocks { return
   result }`.
4. **Delete the pre-truncated short-circuit** (`tool_gate.go:107-109`).
5. **Spill reuse** in the drop path (`tool_gate.go:166-172`): read
   `result.Meta["spill_path"]` first; only call `tools.SpillOutput` when it is
   empty; keep `result.Truncated = true`.
6. **Reword the stub** (`renderGated`, `tool_gate.go:340`): "output saved to"
   instead of "full output saved to".
7. **Tests** (`internal/agent/tool_gate_test.go`): replace
   `TestGateSkipsAlreadyTruncated` with a test proving a pre-truncated body is
   gated **and** keeps its `spill_path`; add a byte-bounded-block test (a
   17-line/3 KB body yields ≥ 2 blocks and is gated); add a "single-line body is
   still skipped" test; keep `TestGateNilClassifierIsByteIdentical`.
8. **Docs**: update `docs/tool-gate-report.md` (coverage section) and the
   pre-screen list in `docs/JEV_TOOL_GATE_PLAN.md` §5.2 to match the new rules.

## 6. Files affected

| File | Change |
|---|---|
| `internal/agent/tool_gate.go` | `gateBlockBytes`; byte-bounded `gateBlocks`; drop the 50-line guard; drop the `Truncated` skip; spill reuse; stub wording |
| `internal/agent/tool_gate_test.go` | invert the pre-truncated test; add byte-block + single-line tests |
| `docs/tool-gate-report.md` | updated coverage numbers |
| `docs/JEV_TOOL_GATE_PLAN.md` | §5.2 pre-screen rules |

No new files, no new config knobs, no new dependency, no move of the gate.

## 7. Testing and verification

**Reproduce the gap first** (already done; repeat after the change to compare):
re-run the session parser used in §3 and confirm `below_minlines` and
`pre_truncated` drop to ~0 and `ELIGIBLE` rises from 49–65% to the high 70s.

**Unit** (`go test ./internal/agent`):
- a 17-line/3 KB `web_search`-shaped body is split into ≥ 2 blocks and, with the
  stub classifier, is gated;
- a pre-truncated body with `spill_path` set is gated and the pointer is
  byte-identical afterward; a pre-truncated body without one gets a new spill;
- a true single-line body is skipped;
- nil gate / classifier error / error belt / denylist / image / non-OK remain
  byte-identical (existing tests).

**Live** (the real binary, as in the last run): `go run ./cmd/kajicode` with a
test config naming `web_search`, a large `web_fetch`, a big `read_file`, a
`grep`, and a `bash` dump; read `gate_decision`/`spill_path` from the `-o json`
`meta` and confirm each is `pruned`, and that `read_file` on the spill round-trips.

**Regression + hygiene**: `make fmt-check` · `go vet ./...` · `go test ./...` ·
release build + smoke · `go vet`/`govulncheck` on the touched surface ·
`git diff --check`. Restore any test config afterward.

## 8. Cleanup

- Remove the duplicated 50-line check inside `gateBlocks` (now the single
  `len(blocks) < gateMinBlocks` rule).
- Delete `TestGateSkipsAlreadyTruncated` (superseded) — do **not** leave it as a
  skipped/XFAIL test.
- No temp harness, `/tmp` scratch, or spill test files left behind.

## 9. Risks

- **Latency (real, measured).** More bodies are judged and blocks are smaller,
  so a large `read_file` costs more classifier requests than before. Measured
  live on the newly covered set: **p50 421 ms, p90 534 ms, p99 731 ms, max
  2.34 s** per result — the plan's p90 ≤ 300 ms gate is **missed**. The call is
  fail-open with a 15 s timeout, so the cost is latency, not correctness; measure
  it in the shadow run before enabling by default.
- **Regret on small results.** `web_search` and short `bash` bodies are now
  judged; hiding a relevant snippet is possible. Bounded by the uncertain band,
  `MinPruneRatio`, and the recall stub. The `0.2` drop threshold was re-checked
  on the newly-covered tools (569 blocks): **0 blocks below 0.20**, 1.6% hidden
  at 0.30, both with **zero regret** on that sample — so 0.2 is kept. The
  zero-regret result is a 569-block sample, not the full 975-block one; the 0.5
  floor still costs on the larger sample.
- **Pre-truncated recall.** For the pre-truncated results with no prior spill,
  the new spill holds only the budgeted body — the elided middle stays gone (it
  was already gone before the change; this does not make it worse, but it does
  not recover it either).
- **Net behavior is still context-*quality*, not size.** The prior report's
  verdict stands: the classifier works, the win is relevance, not tokens.
