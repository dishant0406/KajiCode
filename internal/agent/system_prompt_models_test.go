package agent

import (
	"strings"
	"testing"
)

func TestModelFamilyClassification(t *testing.T) {
	cases := map[string]string{
		"gpt-5":                  familyOpenAI,
		"gpt-4o":                 familyOpenAI,
		"o3-mini":                familyOpenAI,
		"gemini-2.5-pro":         familyGemini,
		"claude-opus-4-6":        familyAnthropic,
		"anthropic/claude-haiku": familyAnthropic,
		"qwen3-coder":            familyQwen,
		"kimi-k2-thinking":       familyKimi,
		"deepseek-v3.2":          familyDeepSeek,
		"MiniMax-M3":             familyMiniMax,
		"zai-org/glm-5-maas":     familyGLM,
		"some-unknown-model":     "",
		"":                       "",
	}
	for model, want := range cases {
		if got := modelFamily(model); got != want {
			t.Errorf("modelFamily(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestProviderNameContributesToHarnessFamily(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     string
	}{
		{provider: "azure-openai", model: "deployment-name", want: familyOpenAI},
		{provider: "openai-compatible", model: "house-model", want: familyGeneric},
		{provider: "google-genai", model: "custom", want: familyGemini},
	}
	for _, tc := range cases {
		if got := modelFamilyFromProvider(tc.provider, tc.model); got != tc.want {
			t.Fatalf("modelFamilyFromProvider(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestBuildSystemPromptAppendsHarnessProfile(t *testing.T) {
	if got := buildSystemPrompt(Options{Model: "gpt-5"}); !strings.Contains(got, openAIPromptAddendum) {
		t.Fatalf("expected the OpenAI addendum in the gpt-5 prompt")
	}
	claude := buildSystemPrompt(Options{Model: "claude-opus-4-6"})
	if !strings.Contains(claude, "Model family: Claude") {
		t.Fatalf("expected Claude harness profile in prompt")
	}
	if strings.Contains(claude, openAIPromptAddendum) {
		t.Fatalf("the claude prompt must not contain the OpenAI addendum")
	}
	openWeight := buildSystemPrompt(Options{Model: "qwen3-coder"})
	if !strings.Contains(openWeight, openWeightPromptAddendum) {
		t.Fatalf("expected open-weight guidance for qwen")
	}
	if got := modelPromptAddendum(""); got != "" {
		t.Fatalf("expected no addendum without a model, got %q", got)
	}
	if strings.Contains(buildSystemPrompt(Options{}), "<harness_profile>") {
		t.Fatalf("expected no harness profile block without a provider/model")
	}
}

func TestHarnessProfileCarriesProviderStrategies(t *testing.T) {
	cases := []struct {
		name    string
		options Options
		want    []string
	}{
		{
			name:    "openai",
			options: Options{Model: "gpt-5"},
			want: []string{
				"Model family: OpenAI/Codex",
				"Planning strategy: Keep a short live checklist",
				"Validation strategy: Run the narrow validator first",
				"Provider posture: OpenAI/Codex.",
			},
		},
		{
			name:    "gemini",
			options: Options{ProviderName: "google-genai", Model: "house-model"},
			want: []string{
				"Model family: Gemini",
				"Planning strategy: State the current hypothesis",
				"Provider posture: Gemini.",
			},
		},
		{
			name:    "generic",
			options: Options{ProviderName: "openai-compatible", Model: "house-model"},
			want: []string{
				"Model family: generic OpenAI-compatible",
				"Use conservative step-by-step tool use",
				"Provider posture: generic OpenAI-compatible model.",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := buildSystemPrompt(tc.options)
			for _, want := range tc.want {
				if !strings.Contains(prompt, want) {
					t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
				}
			}
		})
	}
}

func TestBuildSystemPromptIncludesActiveSessionRuntime(t *testing.T) {
	prompt := buildSystemPrompt(Options{
		ProviderName: "ollama-cloud",
		Model:        "glm-5.1",
	})

	for _, want := range []string{
		"<session>",
		"Active provider: ollama-cloud",
		"Active model: glm-5.1",
		"Persisted config commands may show saved defaults",
		"</session>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}
