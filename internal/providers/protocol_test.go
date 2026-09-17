package providers

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/providercatalog"
)

func TestProtocolFromNPM(t *testing.T) {
	cases := map[string]providercatalog.APIFormat{
		"@ai-sdk/anthropic":         providercatalog.APIFormatAnthropicMessages,
		"@ai-sdk/openai":            providercatalog.APIFormatOpenAIResponses,
		"@ai-sdk/openai-compatible": "",
		"@ai-sdk/google":            "",
		"@ai-sdk/amazon-bedrock":    "",
		"":                          "",
		"  @ai-sdk/anthropic  ":     providercatalog.APIFormatAnthropicMessages,
	}
	for npm, want := range cases {
		if got := protocolFromNPM(npm); got != want {
			t.Errorf("protocolFromNPM(%q) = %q, want %q", npm, got, want)
		}
	}
}

func TestMessagesBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://opencode.ai/zen/v1":       "https://opencode.ai/zen",
		"https://opencode.ai/zen/v1/":      "https://opencode.ai/zen",
		"https://api.anthropic.com":        "https://api.anthropic.com",
		"https://api.example/anthropic/v2": "https://api.example/anthropic/v2",
	}
	for in, want := range cases {
		if got := messagesBaseURL(in); got != want {
			t.Errorf("messagesBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}
