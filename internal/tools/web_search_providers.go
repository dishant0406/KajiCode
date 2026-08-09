package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Web search provider names. Kept as exported constants so selection logic and
// tests can reference them without magic strings.
const (
	providerExa       = "exa"
	providerTavily    = "tavily"
	providerSearxng   = "searxng"
	providerSnippet   = "snippet"
	providerFirecrawl = "firecrawl"
	providerTinyFish  = "tinyfish"
	providerAuto      = "auto"
)

// env keys read to build provider backends.
const (
	envWebSearchProvider = "KAJICODE_WEBSEARCH_PROVIDER"
	envExaAPIKey         = "EXA_API_KEY"
	envTavilyAPIKey      = "TAVILY_API_KEY"
	envParallelAPIKey    = "PARALLEL_API_KEY"
	envTinyFishAPIKey    = "TINYFISH_API_KEY"
	envWebSearchBaseURL  = "KAJICODE_WEBSEARCH_BASE_URL"
	envWebSearchAPIKey   = "KAJICODE_WEBSEARCH_API_KEY"
)

const (
	exaDefaultURL      = "https://api.exa.ai/search"
	tavilyDefaultURL   = "https://api.tavily.com/search"
	tinyFishDefaultURL = "https://api.search.tinyfish.ai"
)

// exaProvider searches the Exa API and returns full page text (excerpt-budgeted
// by ContextMax). It keeps its own HTTP client so redirect policy and auth are
// entirely under KajiCode's control.
type exaProvider struct {
	client *http.Client
	apiKey string
	// baseURL is almost always exaDefaultURL; overridable for tests/proxies.
	baseURL string
}

func newExaProvider(apiKey string) *exaProvider {
	return &exaProvider{
		client: &http.Client{
			Timeout:       webSearchTimeout,
			CheckRedirect: sameHostRedirectPolicy,
		},
		apiKey:  apiKey,
		baseURL: exaDefaultURL,
	}
}

func (p *exaProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	// Exa "type" is auto|fast|deep|deep-lite|deep-reasoning|instant; our args
	// surface a smaller auto|fast|deep set, so map deep-lite/deep-reasoning via a
	// helper and leave unknown values at auto.
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultWebSearchLimit
	}
	exaType := exaTypeForSearchType(opts.Type)
	body := map[string]any{
		"query":      query,
		"numResults": limit,
		"type":       exaType,
		"contents":   map[string]any{"includeText": true, "maxCharacters": opts.maxContext()},
	}
	if opts.LiveCrawl == "preferred" {
		// Exa livecrawl: true|false|"fallback". "fallback" (the default) uses
		// cached content with live crawling as a fallback; "preferred" biases
		// toward live crawling.
		body["livecrawl"] = "fallback"
	} else {
		body["livecrawl"] = true
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode exa request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build exa request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", p.apiKey)
	request.Header.Set("User-Agent", "kajicode-web-search/0.1")

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("exa search failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read exa response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("exa search returned HTTP %d", response.StatusCode)
	}
	return parseExaResults(raw)
}

// exaTypeForSearchType maps the web_search arg (auto|fast|deep) to an Exa type.
func exaTypeForSearchType(t string) string {
	switch strings.ToLower(t) {
	case "fast":
		return "fast"
	case "deep":
		return "deep"
	default:
		return "auto"
	}
}

type exaResult struct {
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Text      string  `json:"text"`
	Score     float64 `json:"score"`
	Published string  `json:"publishedDate"`
}

func parseExaResults(body []byte) ([]searchResult, error) {
	var payload struct {
		Results []exaResult `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode exa response: %w", err)
	}
	out := make([]searchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, searchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: firstNonEmptyWebSearch(summarizeContent(r.Text), r.Title),
			Content: r.Text,
			Score:   r.Score,
		})
	}
	return out, nil
}

// firstNonEmptyWebSearch returns the first non-empty slice element ("" default).
func firstNonEmptyWebSearch(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// summarizeContent derives a short snippet (first meaningful sentence up to ~200
// chars) from full content so snippet-only consumers (and the renderer) have text
// even when Content isn't shown.
func summarizeContent(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[:i]
	}
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

// tavilyProvider searches the Tavily API and returns rich content.
type tavilyProvider struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

func newTavilyProvider(apiKey string) *tavilyProvider {
	return &tavilyProvider{
		client: &http.Client{
			Timeout:       webSearchTimeout,
			CheckRedirect: sameHostRedirectPolicy,
		},
		apiKey:  apiKey,
		baseURL: tavilyDefaultURL,
	}
}

func (p *tavilyProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultWebSearchLimit
	}
	body := map[string]any{
		"query":               query,
		"max_results":         limit,
		"search_depth":        tavilyDepthForSearchType(opts.Type),
		"include_raw_content": "markdown",
		"include_answer":      false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode tavily request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build tavily request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("User-Agent", "kajicode-web-search/0.1")

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("tavily search failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read tavily response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily search returned HTTP %d", response.StatusCode)
	}
	return parseTavilyResults(raw, opts.maxContext())
}

// tavilyDepthForSearchType maps auto|fast|deep to Tavily search_depth.
func tavilyDepthForSearchType(t string) string {
	switch strings.ToLower(t) {
	case "fast":
		return "fast"
	case "deep":
		return "advanced"
	default:
		return "basic"
	}
}

type tavilyResult struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	RawContent string  `json:"raw_content"`
	Score      float64 `json:"score"`
	ID         string  `json:"id"`
}

func parseTavilyResults(body []byte, contextMax int) ([]searchResult, error) {
	var payload struct {
		Results []tavilyResult `json:"results"`
		Answer  string         `json:"answer"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode tavily response: %w", err)
	}
	out := make([]searchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		// Prefer rich raw markdown, fall back to the provider content.
		content := r.Content
		if r.RawContent != "" {
			content = r.RawContent
		}
		out = append(out, searchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: summarizeContent(content),
			Content: truncateWebSearchContent(content, contextMax),
			Score:   r.Score,
		})
	}
	return out, nil
}

// tinyFishProvider searches the TinyFish Search API, which returns ranked
// snippet-only results (title, snippet, url) for LLM consumption. It's a GET
// endpoint with an X-API-Key header; the free tier does not consume credits.
type tinyFishProvider struct {
	client  *http.Client
	apiKey  string
	baseURL string
}

func newTinyFishProvider(apiKey string) *tinyFishProvider {
	return &tinyFishProvider{
		client: &http.Client{
			Timeout:       webSearchTimeout,
			CheckRedirect: sameHostRedirectPolicy,
		},
		apiKey:  apiKey,
		baseURL: tinyFishDefaultURL,
	}
}

func (p *tinyFishProvider) Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultWebSearchLimit
	}
	u, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse tinyfish base url: %w", err)
	}
	q := u.Query()
	q.Set("query", query)
	// Start at the first page; TinyFish's free tier returns a small fixed set.
	q.Set("page", strconv.Itoa(0))
	u.RawQuery = q.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build tinyfish request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-API-Key", p.apiKey)
	request.Header.Set("User-Agent", "kajicode-web-search/0.1")

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("tinyfish search failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, webSearchBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("read tinyfish response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("tinyfish search returned HTTP %d", response.StatusCode)
	}
	return parseTinyFishResults(raw)
}

type tinyFishResult struct {
	Position int    `json:"position"`
	SiteName string `json:"site_name"`
	Title    string `json:"title"`
	Snippet  string `json:"snippet"`
	URL      string `json:"url"`
}

func parseTinyFishResults(body []byte) ([]searchResult, error) {
	var payload struct {
		Results []tinyFishResult `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode tinyfish response: %w", err)
	}
	out := make([]searchResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, searchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: firstNonEmptyWebSearch(r.Snippet, r.Title),
			Content: r.Snippet, // snippet-only; no full text from this provider
			Score:   0,
		})
	}
	return out, nil
}

// truncateWebSearchContent clips content to contextMax characters (default
// applied by maxContext) at a safe boundary. Since the renderer writes
// "<content> ... </content>", we hard-cap far below the body limit to keep the
// model's input token window happy.
func truncateWebSearchContent(content string, contextMax int) string {
	t := strings.TrimSpace(content)
	if contextMax <= 0 || len(t) <= contextMax {
		return t
	}
	return t[:contextMax] + "\n…[truncated]"
}

// maxContext returns the per-result content budget, defaulting when unset.
func (o SearchOptions) maxContext() int {
	if o.ContextMax <= 0 {
		return defaultWebSearchContextMax
	}
	return o.ContextMax
}

// searchBackendChain tries each configured backend in order, returning the first
// non-error, non-empty result set. It never masks an explicit single-provider
// request (that path builds a one-element chain) and stops failover at the
// earliest success so a slow/broken primary cannot degrade good results.
type searchBackendChain struct {
	backends []searchBackend
}

func (c *searchBackendChain) Search(ctx context.Context, query string, opts SearchOptions) ([]searchResult, error) {
	var lastErr error
	for _, backend := range c.backends {
		results, err := backend.Search(ctx, query, opts)
		if err != nil {
			lastErr = err
			continue
		}
		if len(results) == 0 {
			lastErr = fmt.Errorf("provider returned no results")
			continue
		}
		return results, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no search provider configured")
	}
	return nil, lastErr
}

// buildSearchBackend resolves which backend(s) to use for web_search. It honors
// an explicit KAJICODE_WEBSEARCH_PROVIDER (a one-element chain) and otherwise
// auto-selects with failover across every configured provider. Returns nil when
// nothing is configured so the tool reports unconfigured.
func buildSearchBackend(env searchEnv) searchBackend {
	explicit := strings.ToLower(env.provider)
	if explicit != "" && explicit != providerAuto {
		switch explicit {
		case providerExa:
			if env.exaKey != "" {
				return newExaProvider(env.exaKey)
			}
		case providerTavily:
			if env.tavilyKey != "" {
				return newTavilyProvider(env.tavilyKey)
			}
		case providerSearxng:
			if env.baseURL != "" {
				return buildSnippetBackend(env.baseURL, env.genericKey, providerSearxng)
			}
		case providerTinyFish:
			if env.tinyFishKey != "" {
				return newTinyFishProvider(env.tinyFishKey)
			}
		case providerSnippet:
			if env.baseURL != "" {
				return buildSnippetBackend(env.baseURL, env.genericKey, providerSnippet)
			}
		}
		return nil
	}

	// auto (or unset): chain everything that is configured, preferring richer
	// content providers first, ending at the keyless snippet/SearXNG fallback.
	var chain []searchBackend
	if env.exaKey != "" {
		chain = append(chain, newExaProvider(env.exaKey))
	}
	if env.tavilyKey != "" {
		chain = append(chain, newTavilyProvider(env.tavilyKey))
	}
	if env.tinyFishKey != "" {
		chain = append(chain, newTinyFishProvider(env.tinyFishKey))
	}
	if env.baseURL != "" {
		chain = append(chain, buildSnippetBackend(env.baseURL, env.genericKey, providerSearxng))
	}
	switch len(chain) {
	case 0:
		return nil
	case 1:
		return chain[0]
	default:
		return &searchBackendChain{backends: chain}
	}
}

// searchEnv is the resolved set of environment knobs for building search
// backends; kept as a struct so selection/failover logic is testable without env
// mutation.
type searchEnv struct {
	exaKey      string
	tavilyKey   string
	parallelKey string
	tinyFishKey string
	baseURL     string
	genericKey  string
	provider    string // KAJICODE_WEBSEARCH_PROVIDER
}

// envToSearchEnv reads the process environment into a searchEnv.
func envToSearchEnv() searchEnv {
	return searchEnv{
		exaKey:      strings.TrimSpace(os.Getenv(envExaAPIKey)),
		tavilyKey:   strings.TrimSpace(os.Getenv(envTavilyAPIKey)),
		parallelKey: strings.TrimSpace(os.Getenv(envParallelAPIKey)),
		tinyFishKey: strings.TrimSpace(os.Getenv(envTinyFishAPIKey)),
		baseURL:     strings.TrimSpace(os.Getenv(envWebSearchBaseURL)),
		genericKey:  strings.TrimSpace(os.Getenv(envWebSearchAPIKey)),
		provider:    strings.TrimSpace(os.Getenv(envWebSearchProvider)),
	}
}

// buildSnippetBackend constructs the generic/SearXNG httpSearchBackend for the
// given base URL.
func buildSnippetBackend(baseURL, apiKey, provider string) *httpSearchBackend {
	return &httpSearchBackend{
		client: &http.Client{
			Timeout:       webSearchTimeout,
			CheckRedirect: sameHostRedirectPolicy,
		},
		baseURL:  baseURL,
		apiKey:   apiKey,
		provider: provider,
	}
}

// WebSearchProvider describes one web-search provider for the /web-search TUI
// setup form: the env var to set, its auth base URL, and whether an API key is
// required. The base URLs mirror the constants the providers themselves use, so
// the form prefills a real, working endpoint instead of a placeholder.
type WebSearchProvider struct {
	ID             string // "exa" | "tavily" | "searxng" | "custom"
	Label          string // display name in the picker
	EnvVar         string // env var holding the API key ("" if keyless)
	BaseURLEnv     string // env var holding the base URL, if configurable
	DefaultBaseURL string // endpoint the tool uses when BaseURLEnv is unset
	RequiresKey    bool
}

// webSearchProviders is the ordered table backing WebSearchProviders. Kept
// private; callers use the exported slice.
var webSearchProviders = []WebSearchProvider{
	{
		ID:             providerExa,
		Label:          "Exa (content search)",
		EnvVar:         envExaAPIKey,
		DefaultBaseURL: exaDefaultURL,
		RequiresKey:    true,
	},
	{
		ID:             providerTavily,
		Label:          "Tavily (content search)",
		EnvVar:         envTavilyAPIKey,
		DefaultBaseURL: tavilyDefaultURL,
		RequiresKey:    true,
	},
	{
		ID:             providerTinyFish,
		Label:          "TinyFish (free, ranked snippets)",
		EnvVar:         envTinyFishAPIKey,
		DefaultBaseURL: tinyFishDefaultURL,
		RequiresKey:    true,
	},
	{
		ID:          providerSearxng,
		Label:       "SearXNG (self-hosted, keyless)",
		BaseURLEnv:  envWebSearchBaseURL,
		RequiresKey: false,
	},
	{
		ID:          providerSnippet,
		Label:       "Custom / snippet (generic JSON)",
		EnvVar:      envWebSearchAPIKey,
		BaseURLEnv:  envWebSearchBaseURL,
		RequiresKey: false,
	},
}

// WebSearchProviders returns the provider metadata slice used by the /web-search
// form. The returned slice is a copy so callers cannot mutate the table.
func WebSearchProviders() []WebSearchProvider {
	out := make([]WebSearchProvider, len(webSearchProviders))
	copy(out, webSearchProviders)
	return out
}

// WebSearchProviderDefaultBaseURL returns the auth base URL a provider uses when
// no override is configured, or "" for keyless/proxy providers. Used to prefill
// the form's base-URL field.
func WebSearchProviderDefaultBaseURL(id string) string {
	for _, p := range webSearchProviders {
		if p.ID == id {
			return p.DefaultBaseURL
		}
	}
	return ""
}
