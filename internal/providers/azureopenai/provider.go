package azureopenai

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/providers/openai"
	"github.com/dishant0406/KajiCode/internal/providers/providerio"
)

type Options struct {
	APIKey            string
	BaseURL           string
	Model             string
	AuthHeader        string
	AuthScheme        string
	AuthHeaderValue   string
	CustomHeaders     map[string]string
	HTTPClient        *http.Client
	UserAgent         string
	OAuthResolver     providerio.TokenResolver
	MaxTokens         int
	StreamIdleTimeout time.Duration
	ParseThinkTags    bool
}

func New(options Options) (kajicoderuntime.Provider, error) {
	baseURL, endpoint, err := ChatCompletionsEndpoint(options.BaseURL)
	if err != nil {
		return nil, err
	}
	authHeader := strings.TrimSpace(options.AuthHeader)
	authScheme := strings.TrimSpace(options.AuthScheme)
	if authHeader == "" {
		authHeader = "api-key"
	}
	if strings.EqualFold(authHeader, "api-key") && authScheme == "" {
		authScheme = "raw"
	}
	return openai.New(openai.Options{
		APIKey:                options.APIKey,
		BaseURL:               baseURL,
		Endpoint:              endpoint,
		Model:                 options.Model,
		AuthHeader:            authHeader,
		AuthScheme:            authScheme,
		AuthHeaderValue:       options.AuthHeaderValue,
		CustomHeaders:         providerio.CopyHeaders(options.CustomHeaders),
		HTTPClient:            options.HTTPClient,
		UserAgent:             options.UserAgent,
		OAuthResolver:         options.OAuthResolver,
		MaxTokens:             options.MaxTokens,
		StreamIdleTimeout:     options.StreamIdleTimeout,
		ParseThinkTags:        options.ParseThinkTags,
		DisablePromptCacheKey: true,
	})
}

func ChatCompletionsEndpoint(baseURL string) (string, string, error) {
	root, endpoint, err := endpoints(baseURL)
	if err != nil {
		return "", "", err
	}
	return root, endpoint, nil
}

func ModelsEndpoint(baseURL string) (string, error) {
	parsed, err := parseBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	if index := strings.Index(strings.ToLower(path), "/openai/deployments/"); index >= 0 {
		parsed.Path = strings.TrimRight(path[:index], "/") + "/openai/v1"
		parsed.RawQuery = ""
		return strings.TrimRight(parsed.String(), "/") + "/models", nil
	}
	root, _, err := endpoints(baseURL)
	if err != nil {
		return "", err
	}
	return root + "/models", nil
}

func endpoints(baseURL string) (string, string, error) {
	parsed, err := parseBaseURL(baseURL)
	if err != nil {
		return "", "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, "/chat/completions") {
		root := *parsed
		root.Path = strings.TrimSuffix(path, "/chat/completions")
		root.RawQuery = ""
		return strings.TrimRight(root.String(), "/"), parsed.String(), nil
	}
	if strings.Contains(lowerPath, "/openai/deployments/") {
		root := *parsed
		root.RawQuery = ""
		parsed.Path = path + "/chat/completions"
		return strings.TrimRight(root.String(), "/"), parsed.String(), nil
	}
	parsed.RawQuery = ""
	switch {
	case lowerPath == "" || lowerPath == "/":
		parsed.Path = "/openai/v1"
	case strings.HasSuffix(lowerPath, "/openai/v1"):
		parsed.Path = path
	case strings.HasSuffix(lowerPath, "/openai"):
		parsed.Path = path + "/v1"
	default:
		parsed.Path = path + "/openai/v1"
	}
	root := strings.TrimRight(parsed.String(), "/")
	return root, root + "/chat/completions", nil
}

func parseBaseURL(baseURL string) (*url.URL, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("azure-openai provider requires baseURL")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Azure OpenAI base URL %q", baseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("Azure OpenAI base URL must use http or https")
	}
	parsed.Fragment = ""
	return parsed, nil
}
