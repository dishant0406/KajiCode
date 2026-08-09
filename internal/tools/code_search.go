package tools

import (
	"context"
	"math"
	"strings"
	"time"

	zeroSandbox "github.com/dishant0406/KajiCode/internal/sandbox"
)

const (
	defaultCodeSearchTokens = 5000
	maxCodeSearchTokens     = 50000
	minCodeSearchTokens     = 1000
)

// codeSearchTool performs API/library/SDK-focused searches by reusing the same
// env-configured searchBackend as web_search, but prefixed with a "code" flag so
// the backend treats the query as a code/documentation lookup. The result budget
// is expressed in context tokens (mirroring opencode's code_search) rather than a
// raw result count; more tokens requests more results.
type codeSearchTool struct {
	baseTool
	backend searchBackend
}

// NewCodeSearchTool builds the code_search tool with the env-configured backend.
func NewCodeSearchTool() Tool {
	return newCodeSearchToolWithBackend(defaultSearchBackend())
}

func newCodeSearchToolWithBackend(backend searchBackend) Tool {
	return codeSearchTool{
		baseTool: baseTool{
			name:        "code_search",
			description: "Search the web for API references, library docs, and SDK usage. Use when you need to confirm a function signature, package behavior, or release notes rather than general information. Returns ranked results (title, URL, snippet) with a context budget in tokens.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {
						Type:        "string",
						Description: "The API/library/SDK question to search for.",
					},
					"tokensNum": {
						Type:        "integer",
						Description: "Max context tokens to budget for the search results. More tokens requests more results.",
						Default:     defaultCodeSearchTokens,
						Minimum:     intPtr(minCodeSearchTokens),
						Maximum:     intPtr(maxCodeSearchTokens),
					},
				},
				Required:             []string{"query"},
				AdditionalProperties: false,
			},
			// Hosted search sends model-provided query text to a configured network
			// backend. Keep it visible in auto mode but guard execution through the
			// normal permission flow like web_search.
			safety: Safety{
				SideEffect:      SideEffectNetwork,
				Permission:      PermissionPrompt,
				Reason:          "Sends model-provided code-search query text to the configured search backend.",
				AdvertiseInAuto: true,
			},
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false},
		},
		backend: backend,
	}
}

func (tool codeSearchTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *zeroSandbox.Engine) Result {
	return tool.Run(ctx, args)
}

func (tool codeSearchTool) Run(ctx context.Context, args map[string]any) Result {
	query, err := stringArg(args, "query", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for code_search: " + err.Error())
	}
	// max=0 disables the upper bound; the clamps below enforce the range.
	tokensNum, err := intArg(args, "tokensNum", defaultCodeSearchTokens, minCodeSearchTokens, 0)
	if err != nil {
		return errorResult("Error: Invalid arguments for code_search: " + err.Error())
	}
	if tokensNum > maxCodeSearchTokens {
		tokensNum = maxCodeSearchTokens
	}
	if tokensNum < minCodeSearchTokens {
		tokensNum = minCodeSearchTokens
	}

	if tool.backend == nil {
		return errorResult("Error: no search backend configured. Set KAJICODE_WEBSEARCH_BASE_URL (and KAJICODE_WEBSEARCH_API_KEY) to enable code_search.")
	}

	limit := codeSearchLimitForTokens(tokensNum)
	// Fold the intent into the query the backend sees so API/code-focused results
	// are preferred over general web chatter.
	focusedQuery := "[code] " + strings.TrimSpace(query)

	runCtx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	results, err := tool.backend.Search(runCtx, focusedQuery, SearchOptions{
		Limit:       limit,
		Type:        "auto",
		LiveCrawl:   "fallback",
		ContextMax:  defaultWebSearchContextMax,
		CurrentYear: time.Now().Year(),
	})
	if err != nil {
		return errorResult("Error performing code search: " + redactWebSearchText(err.Error()))
	}
	if len(results) == 0 {
		return okResult("No results for code query: " + redactWebSearchText(query))
	}
	results = rankAndTrimWebSearchResults(results, limit)
	return okResult(redactWebSearchText(formatSearchResults(results)))
}

// codeSearchLimitForTokens maps a token budget to a result count. The relationship
// is deliberately conservative: each hit + snippet averages a few hundred tokens,
// so we divide by a fixed per-result estimate and clamp to a sane ceiling.
func codeSearchLimitForTokens(tokensNum int) int {
	const tokensPerResult = 400
	const minLimit = 3
	const maxLimit = 10
	limit := int(math.Round(float64(tokensNum) / tokensPerResult))
	if limit < minLimit {
		return minLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
