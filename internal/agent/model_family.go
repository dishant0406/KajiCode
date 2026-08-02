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
