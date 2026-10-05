# Live Test — classifier tool-result gate on a fresh session

Fresh reproduction of the `zero_20261001200650_1790885210670660000_1` task,
run twice through `go run ./cmd/kajicode` from committed `a4838d0`, to answer:
**does a normal run use the classifier?**

- **Query (verbatim):** *"if you check /tmp we have opencode i guess what does
  it have that we dont in kajicode resreach please"*
- **Mode:** `--permissions bypass-all`
- **Provider / model:** `growwcorp` / `fireworks/DeepSeek_V4.1_Flash`
- **Classifier:** profile `jev` → `https://openrouter.ai/api/v1/systemone`,
  model `jev-1.13`, reachable (probe 0.580)
- **Run A** — as configured (`features.compaction` only; `toolResult` unset)
- **Run B** — same, plus `features.toolResult = {enabled: true}`

Config was restored byte-exact after the test (sha256 `2b2b94…`).

---

## Answer

**Classifier usage is wired and works, but it is gated by config.**

- **Run A (shipped config): the gate never fires** — `toolResult` is off, so
  `toolResultGateForRun` returns `nil` and the tool path is byte-identical. This
  matches the original trace exactly.
- **Run B (gate enabled): the gate fires on every large result** — 57 results
  judged across `bash`, `read_file`, `grep`, `web_search`, `web_fetch`.

So: a fresh `go run ./cmd/kajicode` **does not** use the classifier unless you
turn a feature on. The compaction judge stays silent until the context reaches
its threshold (~139k tokens for a 200k window); the gate is off unless enabled.

---

## Run A — as configured (`toolResult` off)

| metric | value |
|---|---|
| tool calls | `bash` 54, `todo_write` 4, `read_file` 1, `web_fetch` 13 |
| results judged by the gate | **0** |
| result meta | `output_budget_*` only — no `gate_*`, no `spill_path` |
| exit code | 0 |

Zero `gate_decision` keys. Because the gate is opt-in, connecting the classifier
changed nothing here — the intended default.

Note the tool mix: **54 bash vs 1 native file tool** — the shell-first shape the
original trace documented, confirming it reproduces.

## Run B — gate enabled (`toolResult` on)

| metric | value |
|---|---|
| tool calls | `bash` 46, `web_fetch` 22, `read_file` 13, `todo_write` 7, `grep` 4, `web_search` 2, `ls` 1, `write_file` 1 |
| results judged | **57** |
| decisions | pruned 37, kept 12, below_min_prune_ratio 8 |
| lines hidden / kept | 6,377 hidden / 8,187 kept |
| spill files written | yes, one per pruned result (round-tripped, e.g. 3,169 B) |
| exit code | 0 |

The gate judged **all four surfaces the user asked about**: `read_file`, `grep`,
`web_search`, `web_fetch` — plus `bash`. `web_search` and `grep` being judged is
the coverage fix working live (they were previously skipped). Pruned results
carry a `spill_path`; the stub names it, so "dropped ≠ lost" holds.

### Honest caveats

- **n=1 per arm.** One task, one model, one classifier, one threshold (0.2).
  Directional, not a distribution.
- **The drop set sits on the threshold edge — but that is arithmetic, not a
  finding.** Every `gate_max_hidden_relevance` observed is 0.08–0.19, "just under
  the 0.2 floor". That key was computed **only over blocks that were already
  hidden** (the max was updated inside the `< DropThreshold` branch), so it is
  *guaranteed* to be below the floor. It measured nothing about the classifier.
  Reading it as "the classifier scores everything ~0.1" was the error it invited.
  It has since been replaced by `gate_relevance_min` / `gate_relevance_max`,
  taken over **every** judged block. Measured with the real binary on the same
  file (`internal/tui/flush.go`):

  | goal | decision | `gate_relevance_min`–`max` |
  |---|---|---|
  | "explain what the flush loop does" | `kept` | **0.31–0.73** |
  | "…do not discuss the file contents" | `above_max_hidden_ratio` | **0.05–0.07** |

  The classifier separates cleanly and in the right direction. The gate works;
  its decisions are threshold-sensitive, which is a real property, but the
  "uniformly low" reading was a metric artifact.
- **Prompt tokens are not a clean comparison.** Run A peaked at 99,712 prompt
  tokens; Run B at 116,363. But Run B also did more/different work (web research
  into opencode's docs), so this is **not** a gated-vs-ungated token delta and
  should not be read as one.
- **Run B wrote a repo file.** Its `write_file` call created
  `docs/OPENCODE_GAP_ANALYSIS.md` — a genuine artifact of the task, not the test
  harness. Left in place; remove it if unwanted.

---

## Reproduce

```bash
# as configured — gate off, expect 0 gate_decision
kajicode exec --permissions bypass-all -o json "<query>" > runA.json

# enable the gate
python3 - <<'PY'
import json
p="$HOME/.config/kajicode/config.json"; d=json.load(open(p))
d["classifier"]["features"]["toolResult"]={"enabled":True}
json.dump(d,open(p,"w"),indent=2,sort_keys=True)
PY
kajicode exec --permissions bypass-all -o json "<query>" > runB.json

# count gate decisions
grep -c '"gate_decision"' runB.json      # → 57
grep -c '"gate_decision"' runA.json      # → 0
```

---

## Follow-up: the no-ceiling blackout (fixed)

The caveat above — "the drop set sits on the threshold edge" — turned out to be
the visible half of a real defect, not just fragility.

**What was wrong.** The only ratio check in the gate was a **minimum**
(`MinPruneRatio`): hide too little and the result is kept whole. There was **no
ceiling**, so when the classifier scored every block low the gate legally hid
**100%** of a result and replaced it with `[…]` markers plus a stub. Reproduced
with the shipped defaults on a real run:

```
[gate] 44 of 44 lines of read_file path="go.sum" hidden (max hidden relevance 0.05)
```

The stub's recovery instruction ("`read_file` it") pointed at the spill file —
but a `read_file` of that file was itself gateable, so the recovery read was
answered with another stub and another pointer. The model was left with no
evidence and looped, re-reading and shelling `echo` until the turn cap.

**The fix.**

- `ToolResultGate.MaxHiddenRatio` (default `classifier.DefaultGateMaxHiddenRatio`
  = 0.5) caps the hidden share. At or above it the result is kept whole
  (`gateDecisionAboveMax`), so no classifier can black out a result.
- A read of a file inside the spill directory (`read_file`,
  `read_minified_file`) is never gated, so the stub's own recovery pointer always
  resolves.
- A repeated-call guard in `internal/agent/guardrails.go` halts a run whose turns
  only re-issue tool calls that change nothing.
- `gate_stubs` / `gate_kept` trace counters make a blackout visible instead of
  silent.

**Verified live.** Same prompt, same classifier, same config:

| binary | `read_file go.sum` | outcome |
| --- | --- | --- |
| before | `44 of 44 lines hidden (max hidden relevance 0.05)` | model re-read, then answered |
| after | full body delivered | answered in one call |

The residual caveat stands: the ceiling makes a uniformly-low classifier **safe**,
not **correct**. If the backend keeps scoring relevant output near 0.1, the gate
hides the maximum allowed share on every call. That calibration question is
unmeasured and remains open.
