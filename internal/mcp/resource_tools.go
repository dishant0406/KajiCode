package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// resourceToolName is the model-facing name for each of the three MCP resource
// tools. They mirror opencode's list_mcp_resources / list_mcp_resource_templates
// / read_mcp_resource so a familiar model recognizes them at once.
const (
	listResourcesToolName   = "list_mcp_resources"
	listTemplatesToolName   = "list_mcp_resource_templates"
	readResourceToolName    = "read_mcp_resource"
	listPromptsToolName     = "list_mcp_prompts"
	getPromptToolName       = "get_mcp_prompt"
	maxResourceBlobBytes    = 10 * 1024 * 1024
	resourceToolDescription = "MCP resources are data a connected server exposes (documents, records, blobs). Use these tools before assuming a server has no readable data."
	promptToolDescription   = "MCP prompts are reusable templates a server exposes; render one to get ready-made instructions."
)

// catalogTool is one of the three resource tools. It looks servers up on the
// live runtime each call, so it always sees the current connection set.
type catalogTool struct {
	runtime     *Runtime
	name        string
	description string
	parameters  tools.Schema
}

func (tool catalogTool) Name() string             { return tool.name }
func (tool catalogTool) Description() string      { return tool.description }
func (tool catalogTool) Parameters() tools.Schema { return tool.parameters }
func (tool catalogTool) Safety() tools.Safety     { return catalogToolSafety() }
func (tool catalogTool) Run(ctx context.Context, args map[string]any) tools.Result {
	switch tool.name {
	case listResourcesToolName:
		return tool.runtime.runListResources(ctx, args)
	case listTemplatesToolName:
		return tool.runtime.runListResourceTemplates(ctx, args)
	case listPromptsToolName:
		return tool.runtime.runListPrompts(ctx, args)
	case getPromptToolName:
		return tool.runtime.runGetPrompt(ctx, args)
	default:
		return tool.runtime.runReadResource(ctx, args)
	}
}

func catalogToolSafety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectNetwork,
		Permission:      tools.PermissionPrompt,
		Reason:          "Reads data from a configured MCP server.",
		AdvertiseInAuto: true,
	}
}

// resourceTools builds the three resource tools, or nil when no connected server
// supports resources (so an ordinary MCP setup does not grow an unused toolbelt).
func (runtime *Runtime) resourceTools() []tools.Tool {
	if runtime == nil || len(runtime.resourceServerNames()) == 0 {
		return nil
	}
	serverArg := tools.PropertySchema{
		Type:        "string",
		Description: "Optional MCP server name. When omitted, covers every connected server that supports resources.",
	}
	return []tools.Tool{
		catalogTool{
			runtime:     runtime,
			name:        listResourcesToolName,
			description: "Lists resources provided by connected MCP servers. " + resourceToolDescription,
			parameters: tools.Schema{
				Type:                 "object",
				Properties:           map[string]tools.PropertySchema{"server": serverArg},
				AdditionalProperties: false,
			},
		},
		catalogTool{
			runtime:     runtime,
			name:        listTemplatesToolName,
			description: "Lists resource templates provided by connected MCP servers. A template is a parameterized resource you read after filling in its URI template. " + resourceToolDescription,
			parameters: tools.Schema{
				Type:                 "object",
				Properties:           map[string]tools.PropertySchema{"server": serverArg},
				AdditionalProperties: false,
			},
		},
		catalogTool{
			runtime:     runtime,
			name:        readResourceToolName,
			description: "Reads a specific resource from an MCP server by name and URI. The URI is an MCP identifier, not necessarily a file URL. " + resourceToolDescription,
			parameters: tools.Schema{
				Type: "object",
				Properties: map[string]tools.PropertySchema{
					"server": {Type: "string", Description: "MCP server name exactly as returned by " + listResourcesToolName + "."},
					"uri":    {Type: "string", Description: "Resource URI to read. Use the exact URI string returned by " + listResourcesToolName + "."},
				},
				Required:             []string{"server", "uri"},
				AdditionalProperties: false,
			},
		},
	}
}

// promptTools builds the two prompt tools, or nil when no connected server
// supports prompts.
func (runtime *Runtime) promptTools() []tools.Tool {
	if runtime == nil || len(runtime.promptServerNames()) == 0 {
		return nil
	}
	serverArg := tools.PropertySchema{
		Type:        "string",
		Description: "Optional MCP server name. When omitted, covers every connected server that supports prompts.",
	}
	return []tools.Tool{
		catalogTool{
			runtime:     runtime,
			name:        listPromptsToolName,
			description: "Lists reusable prompt templates provided by connected MCP servers, with their required arguments. " + promptToolDescription,
			parameters: tools.Schema{
				Type:                 "object",
				Properties:           map[string]tools.PropertySchema{"server": serverArg},
				AdditionalProperties: false,
			},
		},
		catalogTool{
			runtime:     runtime,
			name:        getPromptToolName,
			description: "Renders a prompt template from an MCP server into messages. Pass the template name and its arguments. " + promptToolDescription,
			parameters: tools.Schema{
				Type: "object",
				Properties: map[string]tools.PropertySchema{
					"server":    {Type: "string", Description: "MCP server name exactly as returned by " + listPromptsToolName + "."},
					"name":      {Type: "string", Description: "Prompt template name returned by " + listPromptsToolName + "."},
					"arguments": {Type: "object", Description: "Template argument values keyed by argument name."},
				},
				Required:             []string{"server", "name"},
				AdditionalProperties: false,
			},
		},
	}
}

// resourceServerNames returns the names of live servers advertising resources.
func (runtime *Runtime) resourceServerNames() []string {
	return runtime.serverNamesWith(serverSupportsResources)
}

// promptServerNames returns the names of live servers advertising prompts.
func (runtime *Runtime) promptServerNames() []string {
	return runtime.serverNamesWith(serverSupportsPrompts)
}

// serverNamesWith returns the sorted names of live servers whose client satisfies
// the capability predicate.
func (runtime *Runtime) serverNamesWith(supports func(ToolClient) bool) []string {
	runtime.refreshMu.Lock()
	defer runtime.refreshMu.Unlock()
	names := make([]string, 0, len(runtime.live))
	for _, entry := range runtime.live {
		if supports(entry.client) {
			names = append(names, entry.server.Name)
		}
	}
	sort.Strings(names)
	return names
}

// resourceEntry returns the live server with the given name, or false.
func (runtime *Runtime) resourceEntry(name string) (liveServer, bool) {
	runtime.refreshMu.Lock()
	defer runtime.refreshMu.Unlock()
	for _, entry := range runtime.live {
		if entry.server.Name == name {
			return entry, true
		}
	}
	return liveServer{}, false
}

func (runtime *Runtime) runListResources(ctx context.Context, args map[string]any) tools.Result {
	return runtime.runListCatalog(ctx, args, listResourcesToolName, func(ctx context.Context, client ToolClient) ([]string, error) {
		resourceClient, ok := client.(ResourceClient)
		if !ok {
			return nil, nil
		}
		resources, err := resourceClient.ListResources(ctx)
		if err != nil {
			return nil, err
		}
		lines := make([]string, 0, len(resources))
		for _, resource := range resources {
			lines = append(lines, formatResource(resource))
		}
		return lines, nil
	}, runtime.resourceServerNames)
}

func (runtime *Runtime) runListResourceTemplates(ctx context.Context, args map[string]any) tools.Result {
	return runtime.runListCatalog(ctx, args, listTemplatesToolName, func(ctx context.Context, client ToolClient) ([]string, error) {
		resourceClient, ok := client.(ResourceClient)
		if !ok {
			return nil, nil
		}
		templates, err := resourceClient.ListResourceTemplates(ctx)
		if err != nil {
			return nil, err
		}
		lines := make([]string, 0, len(templates))
		for _, template := range templates {
			lines = append(lines, formatResourceTemplate(template))
		}
		return lines, nil
	}, runtime.resourceServerNames)
}

// runListCatalog is the shared list handler: resolve the target servers, call the
// per-server lister, and render one labeled block. A per-server failure is
// reported inline so one broken server does not hide the others.
func (runtime *Runtime) runListCatalog(ctx context.Context, args map[string]any, toolName string, list func(context.Context, ToolClient) ([]string, error), serverNames func() []string) tools.Result {
	targets, err := runtime.targetCatalogServers(args, serverNames, toolName)
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: "Error: " + err.Error()}
	}
	var builder strings.Builder
	for _, name := range targets {
		entry, ok := runtime.resourceEntry(name)
		if !ok {
			continue
		}
		lines, listErr := list(ctx, entry.client)
		if listErr != nil {
			builder.WriteString(fmt.Sprintf("## %s\n(error listing: %s)\n\n", name, listErr.Error()))
			continue
		}
		builder.WriteString(fmt.Sprintf("## %s\n", name))
		if len(lines) == 0 {
			builder.WriteString("(none)\n\n")
			continue
		}
		builder.WriteString(strings.Join(lines, "\n"))
		builder.WriteString("\n\n")
	}
	output := strings.TrimSpace(builder.String())
	if output == "" {
		output = "No MCP servers support this catalog."
	}
	return tools.Result{Status: tools.StatusOK, Output: output, Meta: map[string]string{"mcp.tool": toolName, "mcp.servers": strings.Join(targets, ",")}}
}

// targetCatalogServers resolves the requested subset of capability servers. An
// explicit server must exist AND support the capability; an unknown or
// unsupported server is an error so the model gets an actionable message.
func (runtime *Runtime) targetCatalogServers(args map[string]any, serverNames func() []string, toolName string) ([]string, error) {
	supported := serverNames()
	kind := "resources"
	if toolName == listPromptsToolName {
		kind = "prompts"
	}
	if len(supported) == 0 {
		return nil, fmt.Errorf("no connected MCP server supports %s", kind)
	}
	requested, _ := args["server"].(string)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return supported, nil
	}
	for _, name := range supported {
		if name == requested {
			return []string{requested}, nil
		}
	}
	return nil, fmt.Errorf("MCP server %q does not support %s. Available: %s", requested, kind, strings.Join(supported, ", "))
}

func (runtime *Runtime) runReadResource(ctx context.Context, args map[string]any) tools.Result {
	server, _ := args["server"].(string)
	uri, _ := args["uri"].(string)
	server = strings.TrimSpace(server)
	uri = strings.TrimSpace(uri)
	if server == "" || uri == "" {
		return tools.Result{Status: tools.StatusError, Output: "Error: read_mcp_resource requires both 'server' and 'uri'."}
	}
	entry, ok := runtime.resourceEntry(server)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q is not connected.", server)}
	}
	if !serverSupportsResources(entry.client) {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q does not support resources.", server)}
	}
	resourceClient, ok := entry.client.(ResourceClient)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q cannot read resources.", server)}
	}
	contents, err := resourceClient.ReadResource(ctx, uri)
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: reading MCP resource %s/%s failed: %s", server, uri, err.Error())}
	}
	if len(contents) == 0 {
		return tools.Result{Status: tools.StatusOK, Output: "(empty MCP resource)", Meta: map[string]string{"mcp.server": server, "mcp.uri": uri}}
	}
	var builder strings.Builder
	for _, content := range contents {
		builder.WriteString(formatResourceContent(content))
		builder.WriteString("\n")
	}
	return tools.Result{
		Status: tools.StatusOK,
		Output: strings.TrimSpace(builder.String()),
		Meta:   map[string]string{"mcp.server": server, "mcp.uri": uri},
	}
}

func formatResource(resource RemoteResource) string {
	line := "- " + resource.URI
	if resource.Name != "" {
		line += " (" + resource.Name + ")"
	}
	if resource.MIMEType != "" {
		line += " [" + resource.MIMEType + "]"
	}
	if resource.Description != "" {
		line += ": " + resource.Description
	}
	return line
}

func formatResourceTemplate(template RemoteResourceTemplate) string {
	line := "- " + template.URITemplate
	if template.Name != "" {
		line += " (" + template.Name + ")"
	}
	if template.Description != "" {
		line += ": " + template.Description
	}
	return line
}

// formatResourceContent renders one resources/read item. Text is returned as-is;
// a blob is summarized (never dumped as base64, which would blow the budget and
// carry no meaning to the model).
func formatResourceContent(content RemoteResourceContent) string {
	if content.Text != "" {
		return content.Text
	}
	if content.Blob != "" {
		size := len(content.Blob)
		decoded := (size / 4) * 3
		if decoded > maxResourceBlobBytes {
			return fmt.Sprintf("(binary resource %s, %s, ~%d bytes — too large to inline)", content.URI, contentType(content.MIMEType), decoded)
		}
		return fmt.Sprintf("(binary resource %s, %s, ~%d bytes)", content.URI, contentType(content.MIMEType), decoded)
	}
	return "(empty resource content)"
}

func contentType(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return "unknown type"
	}
	return mimeType
}

// --- prompts ---

func (runtime *Runtime) runListPrompts(ctx context.Context, args map[string]any) tools.Result {
	return runtime.runListCatalog(ctx, args, listPromptsToolName, func(ctx context.Context, client ToolClient) ([]string, error) {
		promptClient, ok := client.(PromptClient)
		if !ok {
			return nil, nil
		}
		prompts, err := promptClient.ListPrompts(ctx)
		if err != nil {
			return nil, err
		}
		lines := make([]string, 0, len(prompts))
		for _, prompt := range prompts {
			lines = append(lines, formatPrompt(prompt))
		}
		return lines, nil
	}, runtime.promptServerNames)
}

func (runtime *Runtime) runGetPrompt(ctx context.Context, args map[string]any) tools.Result {
	server, _ := args["server"].(string)
	name, _ := args["name"].(string)
	server = strings.TrimSpace(server)
	name = strings.TrimSpace(name)
	if server == "" || name == "" {
		return tools.Result{Status: tools.StatusError, Output: "Error: get_mcp_prompt requires both 'server' and 'name'."}
	}
	entry, ok := runtime.resourceEntry(server)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q is not connected.", server)}
	}
	if !serverSupportsPrompts(entry.client) {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q does not support prompts.", server)}
	}
	promptClient, ok := entry.client.(PromptClient)
	if !ok {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: MCP server %q cannot render prompts.", server)}
	}
	arguments, _ := args["arguments"].(map[string]any)
	messages, err := promptClient.GetPrompt(ctx, name, arguments)
	if err != nil {
		return tools.Result{Status: tools.StatusError, Output: fmt.Sprintf("Error: rendering MCP prompt %s/%s failed: %s", server, name, err.Error())}
	}
	output := strings.TrimSpace(promptText(messages))
	if output == "" {
		output = "(empty MCP prompt)"
	}
	return tools.Result{Status: tools.StatusOK, Output: output, Meta: map[string]string{"mcp.server": server, "mcp.prompt": name}}
}

func formatPrompt(prompt RemotePrompt) string {
	line := "- " + prompt.Name
	if prompt.Description != "" {
		line += ": " + prompt.Description
	}
	if len(prompt.Arguments) > 0 {
		names := make([]string, 0, len(prompt.Arguments))
		for _, argument := range prompt.Arguments {
			label := argument.Name
			if argument.Required {
				label += "*"
			}
			names = append(names, label)
		}
		line += " [" + strings.Join(names, ", ") + "]"
	}
	return line
}
