package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/tools"
)

func registerResourceRuntime(t *testing.T, client ToolClient) *Runtime {
	t.Helper()
	registry := tools.NewRegistry()
	runtime, err := RegisterTools(context.Background(), registry, config.MCPConfig{
		Servers: map[string]config.MCPServerConfig{
			"docs": {Type: "stdio", Command: "docs"},
		},
	}, RegisterOptions{
		ClientFactory: func(context.Context, Server) (ToolClient, error) {
			return client, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func TestResourceToolsAppearOnlyWhenServerSupportsResources(t *testing.T) {
	plain := registerResourceRuntime(t, &fakeToolClient{})
	for _, name := range []string{listResourcesToolName, listTemplatesToolName, readResourceToolName} {
		if _, ok := plain.registry.Get(name); ok {
			t.Fatalf("expected %s absent when no server supports resources", name)
		}
	}

	fake := &fakeToolClient{resourcesCapable: true}
	capable := registerResourceRuntime(t, fake)
	for _, name := range []string{listResourcesToolName, listTemplatesToolName, readResourceToolName} {
		if _, ok := capable.registry.Get(name); !ok {
			t.Fatalf("expected %s registered when a server supports resources", name)
		}
	}
}

func TestListResourcesRendersCatalog(t *testing.T) {
	fake := &fakeToolClient{
		resourcesCapable: true,
		resources:        []RemoteResource{{URI: "file:///a.md", Name: "a", MIMEType: "text/markdown"}},
		templates:        []RemoteResourceTemplate{{URITemplate: "db://{id}", Name: "row"}},
	}
	runtime := registerResourceRuntime(t, fake)

	result := runtime.runListResources(context.Background(), map[string]any{})
	if result.Status != tools.StatusOK {
		t.Fatalf("status = %v, output = %q", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "file:///a.md") || !strings.Contains(result.Output, "## docs") {
		t.Fatalf("unexpected output: %q", result.Output)
	}

	templates := runtime.runListResourceTemplates(context.Background(), map[string]any{})
	if !strings.Contains(templates.Output, "db://{id}") {
		t.Fatalf("unexpected template output: %q", templates.Output)
	}
}

func TestListResourcesUnknownServerErrors(t *testing.T) {
	fake := &fakeToolClient{resourcesCapable: true}
	runtime := registerResourceRuntime(t, fake)
	result := runtime.runListResources(context.Background(), map[string]any{"server": "nope"})
	if result.Status != tools.StatusError || !strings.Contains(result.Output, "does not support resources") {
		t.Fatalf("expected actionable error, got %v / %q", result.Status, result.Output)
	}
}

func TestReadResourceReturnsText(t *testing.T) {
	fake := &fakeToolClient{
		resourcesCapable: true,
		contents:         []RemoteResourceContent{{URI: "file:///a.md", Text: "hello world"}},
	}
	runtime := registerResourceRuntime(t, fake)
	result := runtime.runReadResource(context.Background(), map[string]any{"server": "docs", "uri": "file:///a.md"})
	if result.Status != tools.StatusOK || result.Output != "hello world" {
		t.Fatalf("unexpected result: %v / %q", result.Status, result.Output)
	}
}

func TestReadResourceBlobIsSummarized(t *testing.T) {
	fake := &fakeToolClient{
		resourcesCapable: true,
		contents:         []RemoteResourceContent{{URI: "bin://x", MIMEType: "image/png", Blob: "AAAA"}},
	}
	runtime := registerResourceRuntime(t, fake)
	result := runtime.runReadResource(context.Background(), map[string]any{"server": "docs", "uri": "bin://x"})
	if strings.Contains(result.Output, "AAAA") {
		t.Fatalf("blob should not be inlined: %q", result.Output)
	}
	if !strings.Contains(result.Output, "image/png") {
		t.Fatalf("expected mime in summary: %q", result.Output)
	}
}

func TestReadResourceRequiresArgs(t *testing.T) {
	fake := &fakeToolClient{resourcesCapable: true}
	runtime := registerResourceRuntime(t, fake)
	result := runtime.runReadResource(context.Background(), map[string]any{"server": "docs"})
	if result.Status != tools.StatusError {
		t.Fatalf("expected error for missing uri, got %v", result.Status)
	}
}

func TestPromptToolsAppearOnlyWhenServerSupportsPrompts(t *testing.T) {
	for _, name := range []string{listPromptsToolName, getPromptToolName} {
		if _, ok := registerResourceRuntime(t, &fakeToolClient{}).registry.Get(name); ok {
			t.Fatalf("expected %s absent without prompt support", name)
		}
	}
	capable := registerResourceRuntime(t, &fakeToolClient{promptsCapable: true})
	for _, name := range []string{listPromptsToolName, getPromptToolName} {
		if _, ok := capable.registry.Get(name); !ok {
			t.Fatalf("expected %s registered", name)
		}
	}
}

func TestListAndGetPrompt(t *testing.T) {
	fake := &fakeToolClient{
		promptsCapable: true,
		prompts:        []RemotePrompt{{Name: "summarize", Description: "Summarize text", Arguments: []PromptArgument{{Name: "text", Required: true}}}},
		promptMessages: []RemotePromptMessage{{Role: "user"}},
	}
	fake.promptMessages[0].Content.Type = "text"
	fake.promptMessages[0].Content.Text = "Please summarize: hello"

	runtime := registerResourceRuntime(t, fake)

	listed := runtime.runListPrompts(context.Background(), map[string]any{})
	if listed.Status != tools.StatusOK || !strings.Contains(listed.Output, "summarize") || !strings.Contains(listed.Output, "text*") {
		t.Fatalf("unexpected list output: %q", listed.Output)
	}

	got := runtime.runGetPrompt(context.Background(), map[string]any{"server": "docs", "name": "summarize"})
	if got.Status != tools.StatusOK || !strings.Contains(got.Output, "Please summarize: hello") {
		t.Fatalf("unexpected get output: %v / %q", got.Status, got.Output)
	}
}

func TestGetPromptRequiresArgs(t *testing.T) {
	runtime := registerResourceRuntime(t, &fakeToolClient{promptsCapable: true})
	if result := runtime.runGetPrompt(context.Background(), map[string]any{"server": "docs"}); result.Status != tools.StatusError {
		t.Fatalf("expected error for missing name, got %v", result.Status)
	}
}
