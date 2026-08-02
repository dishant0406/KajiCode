package agent

import "strings"

const (
	familyOpenAI    = "openai"
	familyGemini    = "gemini"
	familyAnthropic = "anthropic"
	familyQwen      = "qwen"
	familyKimi      = "kimi"
	familyDeepSeek  = "deepseek"
	familyMiniMax   = "minimax"
	familyGLM       = "glm"
	familyGeneric   = "generic"
)

const (
	editStrategyPatch      = "patch-first"
	editStrategyStructured = "structured-edit"
	editStrategyCareful    = "careful-verified-edit"
)

// harnessProfile is the model/provider-specific operating contract KajiCode
// applies around the common agent loop. It intentionally stays small: the
// provider adapters own wire details, while this profile selects prompt framing,
// context pressure, and tool-use posture.
type harnessProfile struct {
	Family                 string
	DisplayName            string
	PromptAddendum         string
	EditStrategy           string
	ToolUseStrategy        string
	CompactionTriggerRatio float64
	PreserveRecentMessages int
}

func resolveHarnessProfile(options Options) harnessProfile {
	family := modelFamilyFromProvider(options.ProviderName, options.Model)
	profile := baseHarnessProfile(family)
	if profile.Family == "" {
		return harnessProfile{}
	}
	return profile
}

func baseHarnessProfile(family string) harnessProfile {
	switch family {
	case familyOpenAI:
		return harnessProfile{
			Family:                 familyOpenAI,
			DisplayName:            "OpenAI/Codex",
			PromptAddendum:         openAIPromptAddendum,
			EditStrategy:           editStrategyPatch,
			ToolUseStrategy:        "Use exact tool schemas, prefer apply_patch/edit_file for edits, and keep tool calls small enough to stream cleanly.",
			CompactionTriggerRatio: 0.68,
			PreserveRecentMessages: 8,
		}
	case familyGemini:
		return harnessProfile{
			Family:                 familyGemini,
			DisplayName:            "Gemini",
			PromptAddendum:         geminiPromptAddendum,
			EditStrategy:           editStrategyStructured,
			ToolUseStrategy:        "Prefer explicit read/search before mutation, use tool_search for deferred schemas, and keep each tool request independently answerable.",
			CompactionTriggerRatio: 0.66,
			PreserveRecentMessages: 8,
		}
	case familyAnthropic:
		return harnessProfile{
			Family:                 familyAnthropic,
			DisplayName:            "Claude",
			PromptAddendum:         anthropicPromptAddendum,
			EditStrategy:           editStrategyStructured,
			ToolUseStrategy:        "Keep preambles short, ground claims in tool results, and avoid re-reading large files already summarized in context.",
			CompactionTriggerRatio: 0.7,
			PreserveRecentMessages: 8,
		}
	case familyQwen, familyKimi, familyDeepSeek, familyMiniMax, familyGLM:
		return harnessProfile{
			Family:                 family,
			DisplayName:            displayNameForFamily(family),
			PromptAddendum:         openWeightPromptAddendum,
			EditStrategy:           editStrategyCareful,
			ToolUseStrategy:        "Use exact tool names and JSON fields, repair uncertainty with tool_search instead of inventing calls, and verify after edits.",
			CompactionTriggerRatio: 0.64,
			PreserveRecentMessages: 10,
		}
	case familyGeneric:
		return harnessProfile{
			Family:                 familyGeneric,
			DisplayName:            "generic OpenAI-compatible",
			EditStrategy:           editStrategyCareful,
			ToolUseStrategy:        "Use exact advertised tool names and prefer dedicated file tools over shell for file reads and edits.",
			CompactionTriggerRatio: compactionTriggerRatio,
			PreserveRecentMessages: defaultCompactionPreserveLast,
		}
	default:
		return harnessProfile{}
	}
}

func displayNameForFamily(family string) string {
	switch family {
	case familyQwen:
		return "Qwen"
	case familyKimi:
		return "Kimi"
	case familyDeepSeek:
		return "DeepSeek"
	case familyMiniMax:
		return "MiniMax"
	case familyGLM:
		return "GLM"
	default:
		return family
	}
}

func modelFamily(model string) string {
	return modelFamilyFromProvider("", model)
}

func modelFamilyFromProvider(provider string, model string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	haystack := strings.TrimSpace(provider + " " + model)
	openAICompatible := strings.Contains(provider, "openai-compatible")
	switch {
	case haystack == "":
		return ""
	case isOpenAIModel(model):
		return familyOpenAI
	case containsModelMarker(model, "gemini", "google", "genai"):
		return familyGemini
	case containsModelMarker(model, "claude", "anthropic"):
		return familyAnthropic
	case strings.Contains(model, "qwen"):
		return familyQwen
	case strings.Contains(model, "kimi") || strings.Contains(model, "moonshot"):
		return familyKimi
	case strings.Contains(model, "deepseek"):
		return familyDeepSeek
	case strings.Contains(model, "minimax") || strings.Contains(model, "mini-max"):
		return familyMiniMax
	case strings.Contains(model, "glm") || strings.Contains(model, "zai-org"):
		return familyGLM
	case openAICompatible:
		return familyGeneric
	case containsModelMarker(provider, "azure", "openai", "chatgpt", "codex"):
		return familyOpenAI
	case containsModelMarker(provider, "gemini", "google", "genai"):
		return familyGemini
	case containsModelMarker(provider, "claude", "anthropic"):
		return familyAnthropic
	default:
		return ""
	}
}

func isOpenAIModel(model string) bool {
	return strings.HasPrefix(model, "gpt") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") ||
		strings.Contains(model, "openai") ||
		strings.Contains(model, "codex")
}

func containsModelMarker(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func modelPromptAddendum(model string) string {
	return resolveHarnessProfile(Options{Model: model}).PromptAddendum
}

func harnessProfileContext(options Options) string {
	profile := resolveHarnessProfile(options)
	if profile.Family == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<harness_profile>\n")
	b.WriteString("Model family: " + profile.DisplayName + "\n")
	b.WriteString("Edit strategy: " + profile.EditStrategy + "\n")
	b.WriteString("Tool strategy: " + profile.ToolUseStrategy + "\n")
	b.WriteString("Context strategy: preserve the recent working set, prune stale tool output before summarizing, and load deferred tools with tool_search only when needed.\n")
	b.WriteString("</harness_profile>")
	if profile.PromptAddendum != "" {
		b.WriteString("\n\n")
		b.WriteString(profile.PromptAddendum)
	}
	return b.String()
}
