# models.dev as the single source of truth for model facts

Status: **implemented.** Original analysis 2026-09-14; phases 0–5 landed via
`internal/modelsource` (snapshot + Resolve + Facts + embedded seed), the registry
bridge (`internal/modelregistry/modelsource_bridge.go`), `Registry.Get` synthesis
for models the curated catalog does not know, and CLI/TUI session binding. Phase 4
(hardcoding retirement) is complete: `VisionCapableByName` and
`reasoningEffortsForModelName` are deleted; vision and effort tiers now resolve
only through curated facts → models.dev (live snapshot, then the embedded seed).
Goal: stop hardcoding model capabilities/limits/cost; resolve a model the user
selected against models.dev at runtime and make that resolved record the authority
for the whole session (compaction window, capabilities, reasoning, cost, picker
labels).

## 1. What we verified about models.dev (live probe)

Three endpoints, all JSON:

| Endpoint | Size | Shape | Use for |
|---|---|---|---|
| `https://models.dev/models.json` | 313 KB | `{"<lab>/<slug>": <ModelRecord>}` — 395 canonical, **provider-agnostic** models | **Primary lookup index** |
| `https://models.dev/api.json` | 4.5 MB | `{"<provider-slug>": {id,env,npm,name,doc,models:{<key>:<ModelRecord>}}}` — 213 providers, 7619 rows | Provider-qualified lookup / provider list |
| `https://models.dev/catalog.json` | 4.95 MB | `{"models": {...}, "providers": {...}}` | Whole catalog in one fetch |

`ModelRecord` keys (union across data): `id, name, description, family,
attachment, reasoning, reasoning_options, tool_call, structured_output,
temperature, knowledge, release_date, last_updated, modalities{input[],output[]},
open_weights, limit{context,output}, cost{input,output,cache_read,cache_write},
status (absent | "deprecated" | "beta"), experimental, interleaved, provider`.

Vision signal = `"image" ∈ modalities.input` (also `attachment` boolean, `pdf`,
`video`, `audio`). Reasoning = `reasoning` bool + `reasoning_options`
(`[{"type":"toggle"}]` / `[{"type":"effort","values":["low","medium","high",...]}]`
/ `budget_tokens`) — this is exactly the effort-tier list we currently hardcode.

Named-model checks (canonical index):

| id | canonical key | modalities.input | attachment | reasoning | ctx |
|---|---|---|---|---|---|
| kimi-k3 | `moonshotai/kimi-k3` | text,image,video | true | true | 1048576 |
| muse-spark-1.3 | `meta/muse-spark-1.3` | text,image,video,pdf,audio | true | true | 1048576 |
| claude-fable-5 | `anthropic/claude-fable-5` | text,image,pdf | true | true | 1000000 |
| glm-5 | `zhipuai/glm-5.x` | text | false | true | 1000000 |
| deepseek-v4-flash | `deepseek/deepseek-v4-flash*` | text | false | true | 1000000 |
| gpt-6-astra | `openai/gpt-6-astra` | text,image,pdf | true | true | 1050000 |
| qwen3.5-plus | `alibaba/qwen3.5-plus` | text,image,video | false | true | 1000000 |

i.e. models.dev already answers every question the curated registry answers for
these models — including the vision yes/no that we were patching by hand.

Caveat: provider proxies rename models (`modalities` differ per provider row:
`tokengo/glm-5` = text-only, `tokengo/z-ai/glm-5.3-flash` = text,image,video,pdf).
So resolution must try the **provider-qualified** row first, then the canonical
row, then fuzzy.

## 2. State of hardcoding before this work (analysis snapshot)

All model knowledge lives in `internal/modelregistry`:

- **Curated literals** `catalog.go:49-64` — 12 entries with hardcoded context,
  output, per-million cost, aliases, capabilities.
- **`decorateModelDepth`** `catalog.go:77-151` — hardcoded per-id effort
  defaults, regex match patterns, deprecation + upgrade chains.
- **`reasoningEffortsForModelName`** `catalog.go:212-249` — name-prefix effort
  tables.
- **`VisionCapableByName`** `vision.go:33-90` — hardcoded vision families
  (the table we keep appending to).
- **Default roles** `default_roles.go:46-98`, **modes** `modes.go:30-63` —
  hardwired model ids + efforts.
- **Provider defaults** `internal/providercatalog/catalog.go` — per-provider
  `DefaultModel` strings (gpt-4.1, claude-sonnet-4.5, gemini-2.5-pro, …).
- Constants: `DefaultModelID`, `FallbackContextWindow=200_000`,
  `sourceLastVerified="2026-06-04"`.

**Existing partial overlay** `internal/modelregistry/modelsdev.go` already
fetches `api.json` and overrides **only** context window, max output, and
non-tiered base pricing — and only for entries already in the curated catalog
(join key `APIModel`, 3 providers: anthropic/openai/google). Cache:
`~/.cache/kajicode/modelsdev.json`, refresh >24h, ignore >7d, kill-switch
`KAJICODE_DISABLE_MODELS_FETCH`. It never carries modalities or efforts.

**Discovery layer** `internal/providermodelcatalog/remote.go` already maps
`modalities.input` into picker rows, and `DiscoverCatalog` merges it into
`modelPickerLiveByProvider` — but the TUI vision gate still consults the curated
registry first (`image_attach.go:122`) and only falls back to the discovered map.

### Fact → consumers (what a single source must feed)

- **Context window → compaction:** `internal/agent/loop.go:490/551/645`
  (proactive 0.7 + reactive), fed via `cli/exec.go:1430`, `tui/model.go:5358`,
  `command_center.go:436` through `AgentContextWindow`. Display: `view.go:392/418`,
  `tui/model_catalog.go:64`, `contextreport.go:194`.
- **Max output:** `providers/factory.go:200`, `kajicodecommands/contracts.go:288`.
- **Cost:** `usage/tracker.go:103-111`, `usage/report.go:145-149`,
  `cli/usage.go:268`, statusline `view.go:353/375`.
- **Capabilities/vision:** `cli/exec.go:431/1227`, `agent/rolerouter.go:93-95/151`,
  `tui/role_command.go:404/503`, `tui/image_attach.go:116-157`,
  `providers/factory.go:206-217`.
- **Reasoning efforts:** `tui/session_controls.go:237`, `command_center.go:475/714`,
  `cli/exec.go:1481-1501`, `providers/factory.go:215`.
- **Aliases/deprecation/upgrade:** `cli/exec.go:1401`, `command_center.go:708`,
  `agent/rolerouter.go:37/254`, `tools/escalate_model.go:86`,
  `specialist/manifest.go:313`.

## 3. Target design

Introduce a **model fact resolver** that, given the selected model id (+ optional
provider), returns a fully-populated record sourced from models.dev, and make all
reads above flow through it.

```
internal/modelsource            (new package)
  Snapshot            // parsed models.json + api.json index, cached
  Resolve(provider, id) (ModelFacts, Score, ok)   // exact → qualified → fuzzy
  facts: Modalities, Attachment, Reasoning, ReasoningOptions,
         ToolCall, Limits{Context,Output}, Cost{...}, Status, Family
```

Resolution precedence for a user slug `S` with provider `P`:
1. exact key in `api.json[P].models` (`S` and `P/S`, `P:S`, `P-S` spellings);
2. exact key in canonical `models.json` (`S`, `<lab>/S`, then any key ending `/S`);
3. **fuzzy**: normalized token/prefix match over canonical keys, take the
   highest score; require score ≥ threshold else `ok=false` (never guess wildly).

Cache: reuse the existing 24h/7d TTL + `KAJICODE_DISABLE_MODELS_FETCH` and cache
path; extend the stored subset to keep the full record fields above (schema bump,
old caches ignored by version).

**Session binding (getter/setter).** At model selection (and `--model` / `/model`
switch / role route), resolve once and store the `ModelFacts` on the session
context:
- `SessionModelFacts` getter on the agent options + TUI model.
- All fact reads (`context window`, `vision`, `reasoning efforts`, `cost`,
  `max output`) read the bound facts first, not the curated registry.
- Setter invalidates on model/provider/role change and re-resolves.

This makes compaction, gauges, capability gates, effort pickers, cost math, and
picker labels all agree on one record.

## 4. Phased plan

- **Phase 0 — parity harness.** Record a fixture of `models.json` + `api.json`
  (the embedded seed `internal/modelsource/modelsdev_seed.json.gz` now plays this
  role). Add golden tests asserting `Resolve` yields the table in §1.
- **Phase 1 — package + resolver.** `internal/modelsource` with Snapshot,
  Resolve (exact/qualified/fuzzy), and fact struct. No callers yet. Unit tests
  for precedence, qualifier stripping, fuzzy threshold, offline fallback.
- **Phase 2 — bind into session.** Add getter/setter; resolve at model select.
  Keep curated registry as the **offline fallback only**.
- **Phase 3 — migrate readers by fact**, one PR each, tests per area:
  1. vision gates (`SupportsVision`, `modelSupportsVisionTUI`) → facts.Modalities;
  2. context window/compaction (`AgentContextWindow` call sites) → facts.Limits;
  3. reasoning efforts (`ReasoningEfforts`/`EffectiveReasoningEffort`) →
     facts.ReasoningOptions;
  4. cost (`CalculateCost`) → facts.Cost;
  5. picker labels.
- **Phase 4 — retire hardcoding.** ✅ Done: `VisionCapableByName` family table and
  `reasoningEffortsForModelName` are deleted (a name-unknown model is simply "not
  vision-capable"; efforts come from the entry's models.dev tiers). `catalog.go` is
  reduced to identity/aliases + offline seed; the embedded seed replaces the need
  for name inference offline.
- **Phase 5 — docs.** Update `docs/architecture.md`, `docs/HOW_KAJICODE_WORKS.md`.

## 5. Risks / open questions

- **Network dependency:** offline/first-run must degrade to the cached snapshot
  or curated seed — never refuse every model. Needs explicit fallback ordering.
- **Provider aliasing:** the same model resolves differently per provider row;
  must prefer the active provider's row, else canonical. Ties → canonical.
- **Fuzzy false positives:** keep a score threshold and prefer exact/prefix over
  fuzzy; log the chosen key so mis-resolutions are diagnosable.
- **Cache size:** keeping full records raises cache size (models.json ~313 KB,
  api.json ~4.5 MB). Consider caching only canonical + active providers'
  records, or the 313 KB `models.json` as the primary index and `api.json` only
  when the provider is known.
- **Status fields:** models.dev has no `preview`; only `deprecated`/`beta`
  (mostly absent) — replacing `ModelStatus` needs a mapping decision.
- **Cost tiers:** models.dev has flat `cost{}` (no tier ceilings); tiered
  pricing (e.g. Gemini >200k) would lose fidelity — decide whether to keep the
  tiered curated model as an override or drop tiers.

## Live refresh (implemented)

The snapshot is memoized once per process for hot-path speed. `/model refresh`
and the model picker's "Refresh models" row now also refresh models.dev
in-session, not just at startup:

- `modelsource.RefreshAndReload(ctx)` fetches a new catalog (when enabled),
  writes the cache, then `Reload()`s.
- `modelsource.Reload()` drops the once-guarded snapshot under `reloadMu`, so the
  next lookup re-reads cache/seed. It never errors; a failed fetch leaves the
  previous snapshot intact and surfaces the error to the caller.
- The TUI runs the refresh off-thread (`modelSourceRefreshCmd`) and, on the
  resulting message, resets `modelregistry`'s synthesized-entry memo and rebuilds
  the registry from the fresh catalog before re-binding the session model.

This keeps one source of truth (models.dev, with the embedded seed as offline
fallback) while letting an updated catalog take effect without restarting.
