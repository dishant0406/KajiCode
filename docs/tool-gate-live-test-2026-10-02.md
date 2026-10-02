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
- **The drop set sits on the threshold edge.** Every `gate_max_hidden_relevance`
  observed is 0.08–0.19 — just under the 0.2 floor. The gate works, but its
  decisions are threshold-sensitive, as earlier reports flagged.
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
