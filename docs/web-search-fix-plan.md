# Improving Web Search in KajiCode — Exhaustive Fix Plan

Status: Implemented. Exa + Tavily content providers, auto-selection with failover,
always-visible registration, and the full argument surface are wired and tested.
Upstream-open (Brave/Bing/Firecrawl-native) follow the same interface.
Reference: opencode web search (verified from source) + Exa/Tavily/SearXNG/Parallel API specs (verified from docs).

---

## 1. Problem statement — why current web search is "bad"

Today `web_search` (`internal/tools/web_search.go`) only returns **search-result
snippets** (title, URL, one-line snippet) and:

1. Requires an env-var backend (`KAJICODE_WEBSEARCH_BASE_URL`) that most users
   never set, so the tool errors out or is hidden entirely.
2. Has **no page content** — the model sees 5 titles + blurbs and must guess
   which URL to `web_fetch`. Answers are thin and need many round trips.
3. Adds **no relevance ranking** — `Score` is only *printed*, never used to
   filter or sort, so off-topic hits crowd out good ones.
4. Supports **one generic backend + SearXNG** only; no out-of-the-box hosted
   provider for content.
5. Has **no current-year anchoring**, causing stale-cutoff answers ("AI news
   2025" instead of "AI news 2026").
6. Abruptly **hides the tool** when unconfigured (`CoreNetworkTools()` gating),
   which is confusing.

## 2. Reference architecture — how opencode does it (verified)

From `packages/opencode/src/tool/websearch.ts` + `mcp-websearch.ts`:

- **Providers**: Exa (`api.exa.ai/search`) and Parallel (`search.parallel.ai/mcp`),
  selected deterministically per session (`checksum(sessionID) % 2`), with an env
  override `OPENCODE_WEBSEARCH_PROVIDER` and feature flags.
- Both are **MCP `tools/call` POSTs** returning **full content**.
- **Arguments**: `query`, `numResults` (default 8), `livecrawl`
  (`fallback`/`preferred`), `type` (`auto`/`fast`/`deep`),
  `contextMaxCharacters` (default 10000). The `contextMaxCharacters` knob is what
  keeps page content within the LLM token budget.
- **Description template** (`websearch.txt`) forces current-year anchoring in the
  query.
- Takeaways for KajiCode: **content comes from the provider in the search call**,
  and **the search tool stays visible** even when a provider errors.

KajiCode already ships a **keyless Firecrawl MCP** default
(`internal/config/mcp_defaults.go`) for scrape/rich search. That unblocks
content retrieval **with zero setup**, so the plan can (a) use Firecrawl MCP as
the zero-config content fallback and (b) add optional Exa/Tavily native providers
for quality/control.

## 3. Design

### 3.1 Provider registry (new `internal/tools/web_search_providers.go`)

Extend the existing `searchBackend` interface and `searchResult` struct to
carry content, and add per-call options:

```go
type SearchOptions struct {
    Limit             int
    Type              string   // auto | fast | deep
    LiveCrawl         string   // fallback | preferred
    ContextMax        int      // content char budget (default 10000)
    CurrentYear       int      // injected for query guidance
    PreferredProvider string   // "" = auto
}

type searchResult struct {
    Title, URL, Snippet string
    Content string       // page text ("" for snippet-only backends)
    Score float64
}

type searchBackend interface {
    Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error)
}
```

Providers (each implements `searchBackend`):
- **`exa`** — `POST https://api.exa.ai/search`, header `x-api-key`,
  body `{query, numResults, type, livecrawl, contents:{includeText:true, maxCharacters:ContextMax}}`
  → results `[{title,url,text,score}]`. `score` present → relevance sort.
- **`tavily`** — `POST https://api.tavily.com/search`, header `Authorization: Bearer`,
  body `{query, max_results, search_depth, include_raw_content:true, include_answer:false, include_domains, exclude_domains}`
  → results `[{title,url,content,score}]`. `content` is rich; `score` is relevance.
- **`searxng`** — existing self-host path, keyless GET with `format=json`.
- **`snippet`/generic** — current `httpSearchBackend` (POST JSON), labeled
  snippet-only so the model knows to call `web_fetch`.
- New optional **`firecrawl`** — reuse the MCP client as the zero-config content
  fallback for snippets.

### 3.2 Provider selection & failover

`selectWebSearchProvider(opts.SearchOptions.PreferredProvider, env)`:
1. Explicit `KAJICODE_WEBSEARCH_PROVIDER` (auto|exa|tavily|searxng|firecrawl) wins.
2. Else `auto`: if `EXA_API_KEY` set → exa; else `TAVILY_API_KEY` set → tavily;
   else if base URL → searxng/snippet; else default keyless Firecrawl MCP.
3. Provider list for failover: on a provider error/empty result, try the next
   enabled provider in order (`[exa, tavily, searxng, firecrawl, snippet]`
   filtered to configured ones).

### 3.3 `web_search.go` upgrades

- **Content in results**: render `<content> ... </content>` per result when the
  provider returned page text, budgeted by `context_max_characters`.
- **Relevance**: `rankAndTrimWebSearchResults` sorts by `Score` desc and drops
  rows below a floor (`minWebSearchRelevanceScore = 0.15`) only when any provider
  supplies scores; otherwise preserves order and just truncates to `limit`.
- **New args** (backward-compatible): `type`, `livecrawl`,
  `context_max_characters`. Use `enumArg` (added in `internal/tools/args.go`) and
  `intArg`.
- **Current-year guidance**: `webSearchDescription()` embeds the current year
  into the tool description (mirrors opencode `websearch.txt`).
- **Metadata**: report per-result provider (`Meta["provider"]`) and whether it
  returned full content (`Meta["full_content"]`).
- **Fail-closed stays**: `domains` allowlist + redaction + `sameHostRedirectPolicy`
  unchanged.

### 3.4 Registration policy

- `web_search` **always visible** in `CoreNetworkTools()` (matches opencode).
  When no provider/backend is configured, return `okResult` with short setup
  instructions (single line), NOT a hard error — so the model doesn't stall.
  `domains` validation remains fail-closed.
- `code_search` stays gated on `defaultSearchBackend() != nil` (existing).

### 3.5 Config surface (env, no config-file requirement)

| Env var | Purpose | Default |
|---------|---------|---------|
| `KAJICODE_WEBSEARCH_PROVIDER` | auto/exa/tavily/searxng/firecrawl | `auto` |
| `EXA_API_KEY` | Exa key | — |
| `TAVILY_API_KEY` | Tavily key | — |
| `PARALLEL_API_KEY` | Parallel key (future) | — |
| `KAJICODE_WEBSEARCH_BASE_URL` | SearXNG/generic base | — |
| `KAJICODE_WEBSEARCH_API_KEY` | key for generic/SearXNG-proxy | — |

Add `EXA_API_KEY`, `TAVILY_API_KEY`, `PARALLEL_API_KEY` to the sandbox scrub
allowlist (`internal/sandbox/runner.go` `sensitiveKeys`) so shell children can't
leak them.

### 3.6 Security invariants (keep)

- Prompt-injection: content from providers is untrusted text; keep `domains`
  allowlist + redaction. Add per-result `[source: <url>]`.
- SSRF: keep `sameHostRedirectPolicy` + `webFetchSafeTransport`/resolver for any
  new provider path; never follow cross-host redirects.
- Permissions: web_search stays `PermissionPrompt` + `AdvertiseInAuto` via the
  existing `effectiveToolPermission` flow in `registry.go`. Firecrawl MCP tools
  stay governed by their existing MCP permission handling.

## 4. Files to change

| File | Change |
|------|--------|
| `internal/tools/web_search.go` | Provider interface + `SearchOptions`; `enumArg` wiring; `type`/`livecrawl`/`context_max_characters`; relevance sort + threshold; content rendering; `webSearchDescription()`; always-visible behavior + setup `okResult`; provider metadata |
| `internal/tools/web_search_providers.go` (new) | `exaProvider`, `tavilyProvider`, `searxngProvider`, `snippet` generic; `selectWebSearchProvider`; failover chain; request/response parsing |
| `internal/tools/args.go` | `enumArg` helper (added) |
| `internal/tools/builtin_catalog.go` | Register `web_search` unconditionally; conditional `code_search` |
| `internal/tools/registry.go` | `CoreNetworkTools()` — always include `web_search` |
| `internal/tools/code_search.go` | Reuse provider selection for code query; keep token budget |
| `internal/config/resolver.go` | (optional) document provider env vars; no code change |
| `internal/config/mcp_defaults.go` | (optional) keep Firecrawl default as zero-config content fallback |
| `internal/sandbox/runner.go` | Allowlist `EXA_API_KEY`, `TAVILY_API_KEY`, `PARALLEL_API_KEY` in `sensitiveKeys` |
| `internal/acp/translate.go` | Add `web_search` to `primaryArgHint` (currently only `query`/`url` keys; web_search uses `query` so likely already covered — verify) |
| `internal/tools/web_search_searxng_test.go` | Keep tests; adapt to `SearchOptions` signature |
| `internal/tools/web_search_test.go` | Update `fakeSearchBackend` to `Search(ctx,q,opts)`; add relevance/content/current-year cases |
| `internal/tools/web_search_providers_test.go` (new) | Exa/Tavily/SearXNG fixtures, selection, failover, threshold |
| `docs/architecture.md` | Document provider model + config surface |

## 5. Tests to add

1. **Provider selection**: `auto` prefers Exa when `EXA_API_KEY` set; Tavily when
   only `TAVILY_API_KEY`; SearXNG when only base URL; Firecrawl default when none.
   Explicit override wins regardless of flags.
2. **Relevance**: shuffled results re-ranked by `Score`; sub-0.15 dropped; no-score
   preserves order.
3. **Content excerpt**: `context_max_characters` caps returned text; `Meta`
   reports `full_content=true`.
4. **Failover**: configured first provider 500s → falls back to next; empty result
   chains.
5. **Budget**: rich content still respects tool output budget.
6. **Security**: `domains` allowlist filters content-bearing hits; redaction
   applied; cross-host redirect refused.
7. **Current-year**: description embeds correct year; schema has `type`/`livecrawl`/
   `context_max_characters`.
8. **Backward compat**: old snippet-only generic backend parses old shapes;
   `fakeSearchBackend` updated everywhere.
9. **Enum arg**: invalid `type`/`livecrawl` rejected.

## 6. Sequencing (phased, each phase green)

- **Phase 0 (refactor seam)**: Provider interface + `SearchOptions` + snippet
  backend refactor; update all `fakeSearchBackend` call sites & tests. Behavior
  identical. `make test ./internal/tools`.
- **Phase 1**: `enumArg` helper + new args (`type`/`livecrawl`/`context_max_characters`)
  + `webSearchDescription()` current-year + relevance sort/threshold. Add tests.
- **Phase 2**: Exa provider with `context_max_characters` content + Tavily
  provider. Tests for request/response + budget.
- **Phase 3**: Registration policy change (always-visible, setup `okResult`) +
  env allowlist + provider failover chain + code_search reuse.
- **Phase 4**: (optional) Firecrawl MCP zero-config fallback wiring + docs
  (`docs/architecture.md`).

## 7. Risks & mitigations

- **API cost/dependency**: Exa/Tavily need paid keys for content. Mitigation:
  keep keyless SearXNG + Firecrawl MCP as default/fallback; content is a
  progressive enhancement.
- **Content size**: rich content can blow the token window. Mitigation:
  `context_max_characters` (default 10000, max 30000) + existing output budget.
- **Prompt injection**: fetched content is untrusted. Mitigation: keep domains
  allowlist + redaction + provenance markers; do NOT auto-fetch without model
  intent (web_fetch stays explicit).
- **Registration change**: making web_search always-visible could show a tool
  that errors. Mitigation: return a helpful `okResult` setup hint, never a hard
  error; keep code_search gated.
- **Concurrent edits**: the repo has shown concurrent-writer interference in
  prior sessions. Mitigation: apply small, byte-exact `edit_file` patches and
  re-read before each; run `go build` after each file.

## 8. Not in scope (separate PRs)

- `code_search` full provider split (reuse the seam, defer Exa code-search).
- Adding an `answer`/research agent (query decomposition, iterative fetch) — the
  main loop already orchestrates; a follow-up could add `deep` mode fan-out.
- Brave/Bing providers (same pattern, add later behind the interface).
