package providers

import (
	"strings"

	"github.com/dishant0406/KajiCode/internal/providercatalog"
)

// Wire protocols a model can be served on, keyed off the models.dev `provider.npm`
// API-SDK package. A provider that serves all its models on one endpoint leaves
// npm empty; the factory then falls back to the provider's kind default. Only
// models.dev rows that state an explicit protocol override change the target —
// an empty or unrecognized npm keeps the existing chat-completions path.
const (
	// protocolFromNPM maps a models.dev npm package to a providercatalog APIFormat.
	// The empty string means "no decision" — the caller keeps its default. Note
	// that @ai-sdk/google (Gemini generateContent) and vendor SDKs (bedrock,
	// vertex, …) intentionally map to "" : their native endpoints are not
	// implemented for a generic base URL, so those models stay on the provider's
	// default chat-completions path rather than being sent to a path that 404s.
	anthropicNPM = "@ai-sdk/anthropic"
	openaiNPM    = "@ai-sdk/openai"
)

// protocolFromNPM resolves a models.dev npm package to the wire protocol the
// provider factory should use. Empty when the package implies no override.
func protocolFromNPM(npm string) providercatalog.APIFormat {
	switch strings.TrimSpace(npm) {
	case anthropicNPM:
		return providercatalog.APIFormatAnthropicMessages
	case openaiNPM:
		return providercatalog.APIFormatOpenAIResponses
	default:
		return ""
	}
}
