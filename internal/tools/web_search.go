package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/redaction"
	zeroSandbox "github.com/dishant0406/KajiCode/internal/sandbox"
)

const (
	defaultWebSearchLimit      = 5
	maxWebSearchLimit          = 10
	webSearchTimeout           = 10 * time.Second
	webSearchBodyLimit         = 256 * 1024
	webSearchRedirectLimit     = 5
	defaultWebSearchContextMax = 10000
	maxWebSearchContextMax     = 30000
	minWebSearchRelevanceScore = 0.15
	snippetProviderLabel       = "snippet"
)

// webSearchDescription builds the tool description with the current year baked
// in (mirrors opencode's websearch.txt). This is what stops stale-cutoff answers
// ("AI news 2025") by forcing the model to anchor recent-queries on the real year.
func webSearchDescription() string {
	year := time.Now().Year()
	return fmt.Sprintf(
		"Search the web and return ranked results. When a content provider (Exa/Tavily) is configured, results include page content excerpts; otherwise snippets (SearXNG/generic) are returned, in which case prefer web_fetch to retrieve a full page. The current year is %d: ALWAYS include it when searching for recent information. Example: instead of \"latest AI news\", search \"AI news %d\", NEVER \"AI news %d\".",
		year, year, year-1,
	)
}

// searchResult is one hit returned by a search backend.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
	// Content is the fetched page text when the provider returns full content
	// (Exa/Tavily/Parallel), empty for snippet-only backends.
	Content string
	// Score is an optional provider-supplied relevance signal in the [0, 1]
	// range. Zero (the zero value) is treated as "absent" by the renderer.
	Score float64
}

// SearchOptions carries the per-call knobs a backend needs to return rich,
// content-bearing results (mirrors opencode's websearch parameters).
type SearchOptions struct {
	// Limit caps the number of results returned.
	Limit int
	// Type selects a search mode: auto | fast | deep.
	Type string
	// LiveCrawl is fallback | preferred and only affects content providers.
	LiveCrawl string
	// ContextMax caps the per-result content excerpt budget in characters.
	ContextMax int
	// CurrentYear is injected so providers/query guidance stay current.
	CurrentYear int
	// PreferredProvider forces a specific provider when set (exa|tavily|searxng);
	// empty selects automatically with fallback.
	PreferredProvider string
}

// searchBackend discovers URLs (and optionally full content) for a query. It is
// an interface so any hosted search API (or a fake, in tests) can be dropped in
// without touching the tool. nil means no backend is configured.
type searchBackend interface {
	Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error)
}

type webSearchTool struct {
	baseTool
	backend searchBackend
}

// NewWebSearchTool builds the web_search tool with the env-configured backend.
func NewWebSearchTool() Tool {
	return newWebSearchToolWithBackend(nil)
}

func newWebSearchToolWithBackend(backend searchBackend) Tool {
	return webSearchTool{
		baseTool: baseTool{
			name:        "web_search",
			description: webSearchDescription(),
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {
						Type:        "string",
						Description: "Search query.",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of results to return.",
						Default:     defaultWebSearchLimit,
						Minimum:     intPtr(1),
						Maximum:     intPtr(maxWebSearchLimit),
					},
					"domains": {
						Type:        "array",
						Description: "Optional list of allowed hostnames; results outside these hosts are filtered out before they reach the model. Useful as a prompt-injection defense when you only want results from a known set of domains.",
						Items:       &PropertySchema{Type: "string"},
					},
					"type": {
						Type:        "string",
						Description: "Search type: auto, fast, or deep.",
						Enum:        []string{"auto", "fast", "deep"},
						Default:     "auto",
					},
					"livecrawl": {
						Type:        "string",
						Description: "Live-crawl mode for content providers: fallback (default) or preferred.",
						Enum:        []string{"fallback", "preferred"},
						Default:     "fallback",
					},
					"context_max_characters": {
						Type:        "integer",
						Description: "Per-result content budget in characters.",
						Default:     defaultWebSearchContextMax,
						Minimum:     intPtr(1),
						Maximum:     intPtr(maxWebSearchContextMax),
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: false,
			},
			// Hosted search sends model-provided query text to a configured network
			// backend. Keep it visible in auto mode, but guard execution through the
			// normal permission flow like web_fetch.
			safety: Safety{
				SideEffect:      SideEffectNetwork,
				Permission:      PermissionPrompt,
				Reason:          "Sends model-provided search query text to the configured web search backend.",
				AdvertiseInAuto: true,
			},
			// Search backends may share non-thread-safe clients.
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false},
		},
		backend: backend,
	}
}

// RunWithSandbox follows the normal web_search path. The sandbox network policy
// gates sandboxed shell egress; this in-process hosted search tool is guarded by
// the permission flow plus backend and result-domain safeguards.
func (tool webSearchTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	return tool.Run(ctx, args)
}

func (tool webSearchTool) Run(ctx context.Context, args map[string]any) Result {
	// Resolve the backend lazily. NewWebSearchTool is constructed without a
	// backend; defaultSearchBackend() reads the process env, so api keys
	// configured at runtime (e.g. via /web-search, which sets env vars) take
	// effect on the next call even though the tool was built earlier.
	backend := tool.backend
	if backend == nil {
		backend = defaultSearchBackend()
	}

	query, err := stringArg(args, "query", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	// max=0 disables intArg's upper bound so an over-cap limit clamps here rather
	// than erroring; min=1 still rejects non-positive limits.
	limit, err := intArg(args, "limit", defaultWebSearchLimit, 1, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	if limit > maxWebSearchLimit {
		limit = maxWebSearchLimit
	}
	domains, domainsProvided, err := stringListArgWebSearch(args, "domains")
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	// Fail closed: the caller asked for an allowlist, but every entry was
	// invalid (or the list was empty). Silently dropping the filter would
	// turn a constrained search into an unconstrained one, defeating the
	// prompt-injection defense the parameter exists to provide.
	if domainsProvided && len(domains) == 0 {
		return errorResult("Error: web_search 'domains' argument was provided but contained no valid hostnames; remove the argument or pass valid hostnames")
	}

	searchType, err := enumArg(args, "type", "auto", []string{"auto", "fast", "deep"})
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	livecrawl, err := enumArg(args, "livecrawl", "fallback", []string{"fallback", "preferred"})
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}
	contextMax, err := intArg(args, "context_max_characters", defaultWebSearchContextMax, 1, maxWebSearchContextMax)
	if err != nil {
		return errorResult("Error: Invalid arguments for web_search: " + err.Error())
	}

	if backend == nil {
		// Always-visible tool: return a helpful one-line setup hint instead of a
		// hard error so the model can either proceed without search or ask the
		// user to configure a provider. code_search stays gated separately.
		return okResult("web_search is not configured. To enable it, set KAJICODE_WEBSEARCH_PROVIDER (auto|exa|tavily|searxng|snippet|tinyfish) plus: EXA_API_KEY and/or TAVILY_API_KEY and/or TINYFISH_API_KEY for content, or KAJICODE_WEBSEARCH_BASE_URL for a self-hosted SearXNG/snippet backend.")
	}

	opts := SearchOptions{
		Limit:             limit,
		Type:              searchType,
		LiveCrawl:         livecrawl,
		ContextMax:        contextMax,
		CurrentYear:       time.Now().Year(),
		PreferredProvider: "",
	}

	runCtx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	results, err := backend.Search(runCtx, query, opts)
	if err != nil {
		return errorResult("Error performing web search: " + redactWebSearchText(err.Error()))
	}
	// Filter by domains BEFORE the empty-result short-circuit so a non-empty
	// allowlist that ate every hit surfaces as a clear "no results matched
	// domains" error rather than a misleading "no results" message.
	if len(results) > 0 && len(domains) > 0 {
		filtered, err := filterWebSearchByDomains(results, domains)
		if err != nil {
			return errorResult("Error: " + redactWebSearchText(err.Error()))
		}
		results = filtered
	}
	results = rankAndTrimWebSearchResults(results, limit)
	if len(results) == 0 {
		return okResult("No results for query: " + redactWebSearchText(query))
	}
	return okResult(redactWebSearchText(formatSearchResults(results)))
}

// rankAndTrimWebSearchResults sorts by relevance (highest score first) and then
// applies a score floor so off-topic hits don't crowd out good ones. When no
// result carries a score, the existing order is preserved (limit still applies).
func rankAndTrimWebSearchResults(results []searchResult, limit int) []searchResult {
	hasScore := false
	for _, r := range results {
		if r.Score > 0 {
			hasScore = true
			break
		}
	}
	if hasScore {
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
		kept := results[:0]
		for _, r := range results {
			if r.Score >= minWebSearchRelevanceScore {
				kept = append(kept, r)
			}
		}
		results = kept
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// formatSearchResults renders results as a compact numbered list:
// "1. Title — URL" with the snippet indented on the next line.
// If a result carries a score that rounds to at least 0.01, it is shown as
// " — score 0.91" after the title. Absent (zero), negative, and sub-0.005 scores
// are omitted so the common case stays tidy and never prints a noisy "score 0.00".
func formatSearchResults(results []searchResult) string {
	lines := make([]string, 0, len(results)*3)
	for index, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "(untitled)"
		}
		header := fmt.Sprintf("%d. %s — %s", index+1, title, strings.TrimSpace(result.URL))
		// Gate on the *rounded* value: a raw "> 0" check would still render
		// "score 0.00" for tiny positives like 0.0001, the exact noise the
		// score suffix is meant to avoid.
		if rounded := math.Round(result.Score*100) / 100; rounded >= 0.01 {
			header += fmt.Sprintf(" — score %.2f", rounded)
		}
		lines = append(lines, header)
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			lines = append(lines, "   "+snippet)
		}
		// Full page content from a content provider is the upgrade over snippet-only
		// search: the model reads real text without a round-trip to web_fetch.
		if content := strings.TrimSpace(result.Content); content != "" {
			lines = append(lines, "   <content> "+content+" </content>")
		}
	}
	return strings.Join(lines, "\n")
}

// defaultSearchBackend returns the env-configured backend chain, or nil when
// none of the supported knobs (Exa/Tavily keys, a base URL) are set. It honors
// an explicit KAJICODE_WEBSEARCH_PROVIDER and otherwise auto-selects with
// failover.
func defaultSearchBackend() searchBackend {
	return buildSearchBackend(envToSearchEnv())
}

// sameHostRedirectPolicy confines the search backend to redirects that stay on
// the originally-requested host, so a configured hosted backend cannot silently
// redirect the request to a different network origin.
func sameHostRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= webSearchRedirectLimit {
		return fmt.Errorf("stopped after %d redirects", webSearchRedirectLimit)
	}
	origin := via[0].URL
	if !strings.EqualFold(req.URL.Hostname(), origin.Hostname()) {
		return fmt.Errorf("refusing cross-host redirect to %q", req.URL.Hostname())
	}
	// Refuse a scheme change too — a same-host https→http downgrade would send the
	// query (and the bearer token Go keeps on same-host redirects) over plaintext.
	if !strings.EqualFold(req.URL.Scheme, origin.Scheme) {
		return fmt.Errorf("refusing redirect that changes scheme %q→%q", origin.Scheme, req.URL.Scheme)
	}
	return nil
}

// httpSearchBackend is the generic JSON backend: POST {query,limit} to a
// configured endpoint and parse an array of {title,url,snippet}. Its shape
// matches common hosted search APIs without copying any of their code; swap in a
// backend-specific implementation by implementing searchBackend.
type httpSearchBackend struct {
	client   *http.Client
	baseURL  string
	apiKey   string
	provider string
}

func (backend *httpSearchBackend) Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	// SearXNG speaks a different shape (GET /search?q=&format=json, keyless), so a
	// self-hosted instance works as a real engine with no API key.
	if strings.EqualFold(backend.provider, "searxng") {
		return backend.searchSearxng(ctx, query, opts)
	}
	requestBody := map[string]any{"query": query, "limit": opts.Limit}
	// Forward the configured provider so an aggregating endpoint can route the
	// query; without this the KAJICODE_WEBSEARCH_PROVIDER knob would be inert.
	if backend.provider != "" {
		requestBody["provider"] = backend.provider
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode search request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, backend.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "kajicode-web-search/0.1")
	if backend.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+backend.apiKey)
	}

	response, err := backend.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read search response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Status only; the body may echo the request (incl. auth) so it is not surfaced.
		return nil, fmt.Errorf("search backend returned HTTP %d", response.StatusCode)
	}
	return parseSearchResults(body)
}

// searchSearxng queries a SearXNG instance: GET {baseURL}/search?q=&format=json,
// no API key. SearXNG returns {results:[{title,url,content,score}]}, which differs
// from the generic POST backend, so it has its own request + parser.
func (backend *httpSearchBackend) searchSearxng(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	endpoint, err := url.Parse(strings.TrimRight(backend.baseURL, "/") + "/search")
	if err != nil {
		return nil, fmt.Errorf("build searxng request: %w", err)
	}
	params := endpoint.Query()
	params.Set("q", query)
	params.Set("format", "json")
	endpoint.RawQuery = params.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build searxng request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "kajicode-web-search/0.1")
	if backend.apiKey != "" { // optional, e.g. a reverse-proxy in front of SearXNG
		request.Header.Set("Authorization", "Bearer "+backend.apiKey)
	}

	response, err := backend.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("searxng request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read searxng response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("searxng backend returned HTTP %d", response.StatusCode)
	}
	return parseSearxngResults(body, opts.Limit)
}

func parseSearxngResults(body []byte, limit int) ([]searchResult, error) {
	var payload struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}
	out := make([]searchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Content, Score: r.Score})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// lenientScore decodes a provider "score" that may arrive as a JSON number, a
// numeric string ("0.91"), null, or some other shape, never failing the decode.
// A plain float64 field would make json.Unmarshal reject the *entire* response
// when a single provider sends a stringified or malformed score, dropping every
// result; here an unparseable score degrades to the zero value (treated as
// absent) instead.
type lenientScore float64

func (s *lenientScore) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	// Accept both 0.91 and "0.91"; ignore anything else (object, bool, NaN/Inf,
	// out-of-range) by leaving the score at zero. ParseFloat accepts "NaN"/"Inf",
	// so reject non-finite results explicitly to honor the documented filter.
	unquoted := strings.TrimSpace(strings.Trim(string(trimmed), `"`))
	if f, parseErr := strconv.ParseFloat(unquoted, 64); parseErr == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		*s = lenientScore(f)
	}
	return nil
}

// parseSearchResults accepts either a bare array [{title,url,snippet,score}] or
// a wrapped object {"results":[...]}, the two shapes common across providers.
// The optional "score" field is forwarded when present; providers that omit
// it leave searchResult.Score at the zero value and the renderer skips it.
func parseSearchResults(body []byte) ([]searchResult, error) {
	type rawResult struct {
		Title   string       `json:"title"`
		URL     string       `json:"url"`
		Snippet string       `json:"snippet"`
		Score   lenientScore `json:"score,omitempty"`
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty search backend response")
	}

	convert := func(raw []rawResult) []searchResult {
		out := make([]searchResult, 0, len(raw))
		for _, item := range raw {
			out = append(out, searchResult{
				Title:   item.Title,
				URL:     item.URL,
				Snippet: item.Snippet,
				Score:   float64(item.Score),
			})
		}
		return out
	}

	if trimmed[0] == '[' {
		var bare []rawResult
		if err := json.Unmarshal(trimmed, &bare); err != nil {
			return nil, fmt.Errorf("parse search results: %w", err)
		}
		return convert(bare), nil
	}
	var wrapped struct {
		Results []rawResult `json:"results"`
	}
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}
	return convert(wrapped.Results), nil
}

func redactWebSearchText(value string) string {
	return redaction.RedactString(value, redaction.Options{})
}

// stringListArgWebSearch reads an optional []string argument. The conventional
// `[]any` shape is preferred (matches the rest of the tools package) but some
// providers' tool-calling shapes emit `[]string` directly; tolerate both.
//
// The returned provided bool is true when the caller passed a "domains" key at
// all (even with an empty array or all-invalid entries). That lets Run
// distinguish "no allowlist" from "allowlist that normalized to empty" — the
// latter is a fail-closed error, not a silent permission to skip filtering.
func stringListArgWebSearch(args map[string]any, key string) (domains []string, provided bool, err error) {
	value, ok := args[key]
	if !ok || value == nil {
		return nil, false, nil
	}
	raw, ok := value.([]any)
	if !ok {
		if asString, ok2 := value.([]string); ok2 {
			return normalizeWebSearchDomainList(asString), true, nil
		}
		return nil, true, fmt.Errorf("%s must be a list of strings", key)
	}
	out := make([]string, 0, len(raw))
	for index, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s[%d] must be a string", key, index)
		}
		out = append(out, text)
	}
	return normalizeWebSearchDomainList(out), true, nil
}

// normalizeWebSearchDomainList lowercases, strips scheme/www/path fragments,
// drops empty entries, and de-duplicates while preserving input order.
func normalizeWebSearchDomainList(domains []string) []string {
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, len(domains))
	for _, raw := range domains {
		host := canonicalizeWebSearchHost(raw)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

// canonicalizeWebSearchHost extracts a bare lowercase hostname from a domain
// string, accepting "react.dev", "https://react.dev/x", and "www.React.dev"
// forms. Returns "" on empty/whitespace input. The "www." prefix is stripped
// before lowercasing so case-insensitive matches like "WWW.react.dev" work.
func canonicalizeWebSearchHost(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return ""
		}
		// Hostname() strips the port so allowlist entries like
		// "https://react.dev:443/path" normalize the same as "react.dev".
		// Using parsed.Host here would leave ":443" in the string and
		// silently break every match against a result URL on port 443.
		trimmed = parsed.Hostname()
	}
	trimmed = strings.TrimSpace(trimmed)
	if i := strings.IndexAny(trimmed, "/?#"); i >= 0 {
		trimmed = trimmed[:i]
	}
	trimmed = strings.ToLower(trimmed)
	// Drop a trailing FQDN dot ("react.dev." == "react.dev") so an allowlist
	// entry and a result host normalize the same way; hostFromWebSearchURL does
	// the same on the result side.
	trimmed = strings.TrimSuffix(trimmed, ".")
	trimmed = strings.TrimPrefix(trimmed, "www.")
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\n") {
		return ""
	}
	return trimmed
}

// filterWebSearchByDomains drops results whose host is not in the allowlist.
// A result whose URL fails to parse is dropped (fail-closed). When the filter
// eats every result, the error names the failing allowlist so the caller knows
// the constraint, not the provider, is the cause.
func filterWebSearchByDomains(results []searchResult, allowed []string) ([]searchResult, error) {
	allowSet := make(map[string]struct{}, len(allowed))
	for _, d := range allowed {
		allowSet[d] = struct{}{}
	}
	out := make([]searchResult, 0, len(results))
	for _, row := range results {
		host := hostFromWebSearchURL(row.URL)
		if host == "" {
			// Unparseable URL: never let it through when an allowlist is set.
			continue
		}
		if _, ok := allowSet[host]; !ok {
			continue
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no web_search results matched domains=%v; loosen the allowlist or remove the domains argument", allowed)
	}
	return out, nil
}

func hostFromWebSearchURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimSuffix(host, ".") // match canonicalizeWebSearchHost (FQDN dot)
	host = strings.TrimPrefix(host, "www.")
	return host
}
