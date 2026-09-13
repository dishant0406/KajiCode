package mcp

import (
	"context"
)

// RemoteResource is a resource advertised by a server via resources/list.
type RemoteResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// RemoteResourceTemplate is a parameterized resource advertised via
// resources/templates/list.
type RemoteResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// RemoteResourceContent is one item from resources/read.
type RemoteResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ResourceClient is implemented by a ToolClient that can read a server's
// resources. Optional: a client that does not implement it simply cannot serve
// the resource tools' requests (they report the server as unsupported).
type ResourceClient interface {
	ListResources(ctx context.Context) ([]RemoteResource, error)
	ListResourceTemplates(ctx context.Context) ([]RemoteResourceTemplate, error)
	ReadResource(ctx context.Context, uri string) ([]RemoteResourceContent, error)
}

// RemotePrompt is a prompt template advertised by a server via prompts/list.
type RemotePrompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// RemotePromptMessage is one rendered message from prompts/get.
type RemotePromptMessage struct {
	Role    string `json:"role"`
	Content struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
}

// PromptClient is implemented by a ToolClient that can read a server's prompts.
// Optional, like ResourceClient.
type PromptClient interface {
	ListPrompts(ctx context.Context) ([]RemotePrompt, error)
	GetPrompt(ctx context.Context, name string, args map[string]any) ([]RemotePromptMessage, error)
}

// requestFunc issues one JSON-RPC request and decodes the result into target.
type requestFunc func(ctx context.Context, method string, params any, target any) error

// maxCatalogPages bounds pagination over list endpoints so a misbehaving server
// that never returns a cursor (or repeats one) cannot loop forever.
const maxCatalogPages = 100

// clientListResources implements resources/list with cursor pagination.
func clientListResources(ctx context.Context, request requestFunc) ([]RemoteResource, error) {
	var resources []RemoteResource
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Resources  []RemoteResource `json:"resources"`
			NextCursor string           `json:"nextCursor"`
		}
		if err := request(ctx, "resources/list", params, &result); err != nil {
			return nil, err
		}
		resources = append(resources, result.Resources...)
		if result.NextCursor == "" {
			return resources, nil
		}
		if _, repeated := seen[result.NextCursor]; repeated {
			return resources, nil // guard against a server that repeats a cursor
		}
		seen[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
	return resources, nil
}

func clientListResourceTemplates(ctx context.Context, request requestFunc) ([]RemoteResourceTemplate, error) {
	var templates []RemoteResourceTemplate
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			ResourceTemplates []RemoteResourceTemplate `json:"resourceTemplates"`
			NextCursor        string                   `json:"nextCursor"`
		}
		if err := request(ctx, "resources/templates/list", params, &result); err != nil {
			return nil, err
		}
		templates = append(templates, result.ResourceTemplates...)
		if result.NextCursor == "" {
			return templates, nil
		}
		if _, repeated := seen[result.NextCursor]; repeated {
			return templates, nil
		}
		seen[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
	return templates, nil
}

func clientReadResource(ctx context.Context, request requestFunc, uri string) ([]RemoteResourceContent, error) {
	var result struct {
		Contents []RemoteResourceContent `json:"contents"`
	}
	if err := request(ctx, "resources/read", map[string]any{"uri": uri}, &result); err != nil {
		return nil, err
	}
	return result.Contents, nil
}

// clientListPrompts implements prompts/list with cursor pagination.
func clientListPrompts(ctx context.Context, request requestFunc) ([]RemotePrompt, error) {
	var prompts []RemotePrompt
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Prompts    []RemotePrompt `json:"prompts"`
			NextCursor string         `json:"nextCursor"`
		}
		if err := request(ctx, "prompts/list", params, &result); err != nil {
			return nil, err
		}
		prompts = append(prompts, result.Prompts...)
		if result.NextCursor == "" {
			return prompts, nil
		}
		if _, repeated := seen[result.NextCursor]; repeated {
			return prompts, nil
		}
		seen[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
	return prompts, nil
}

func clientGetPrompt(ctx context.Context, request requestFunc, name string, args map[string]any) ([]RemotePromptMessage, error) {
	if args == nil {
		args = map[string]any{}
	}
	var result struct {
		Messages []RemotePromptMessage `json:"messages"`
	}
	if err := request(ctx, "prompts/get", map[string]any{"name": name, "arguments": args}, &result); err != nil {
		return nil, err
	}
	return result.Messages, nil
}

// promptText joins a rendered prompt's text messages into one string.
func promptText(messages []RemotePromptMessage) string {
	text := ""
	for _, message := range messages {
		if message.Content.Type != "text" {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += message.Content.Text
	}
	return text
}

// --- stdio Client ---

func (client *Client) ListResources(ctx context.Context) ([]RemoteResource, error) {
	return clientListResources(ctx, client.request)
}

func (client *Client) ListResourceTemplates(ctx context.Context) ([]RemoteResourceTemplate, error) {
	return clientListResourceTemplates(ctx, client.request)
}

func (client *Client) ReadResource(ctx context.Context, uri string) ([]RemoteResourceContent, error) {
	return clientReadResource(ctx, client.request, uri)
}

func (client *Client) ListPrompts(ctx context.Context) ([]RemotePrompt, error) {
	return clientListPrompts(ctx, client.request)
}

func (client *Client) GetPrompt(ctx context.Context, name string, args map[string]any) ([]RemotePromptMessage, error) {
	return clientGetPrompt(ctx, client.request, name, args)
}

// --- network (streamable HTTP) client ---

func (client *networkClient) ListResources(ctx context.Context) ([]RemoteResource, error) {
	return clientListResources(ctx, client.request)
}

func (client *networkClient) ListResourceTemplates(ctx context.Context) ([]RemoteResourceTemplate, error) {
	return clientListResourceTemplates(ctx, client.request)
}

func (client *networkClient) ReadResource(ctx context.Context, uri string) ([]RemoteResourceContent, error) {
	return clientReadResource(ctx, client.request, uri)
}

func (client *networkClient) ListPrompts(ctx context.Context) ([]RemotePrompt, error) {
	return clientListPrompts(ctx, client.request)
}

func (client *networkClient) GetPrompt(ctx context.Context, name string, args map[string]any) ([]RemotePromptMessage, error) {
	return clientGetPrompt(ctx, client.request, name, args)
}

// --- remote SSE client ---

func (client *remoteSSEClient) ListResources(ctx context.Context) ([]RemoteResource, error) {
	return clientListResources(ctx, client.request)
}

func (client *remoteSSEClient) ListResourceTemplates(ctx context.Context) ([]RemoteResourceTemplate, error) {
	return clientListResourceTemplates(ctx, client.request)
}

func (client *remoteSSEClient) ReadResource(ctx context.Context, uri string) ([]RemoteResourceContent, error) {
	return clientReadResource(ctx, client.request, uri)
}

func (client *remoteSSEClient) ListPrompts(ctx context.Context) ([]RemotePrompt, error) {
	return clientListPrompts(ctx, client.request)
}

func (client *remoteSSEClient) GetPrompt(ctx context.Context, name string, args map[string]any) ([]RemotePromptMessage, error) {
	return clientGetPrompt(ctx, client.request, name, args)
}
