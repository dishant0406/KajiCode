package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/lsp"
)

// lspNavigateTool exposes LSP code navigation (jump-to-definition, find-all-
// references, find-implementations, workspace symbol search) as a read-only,
// model-callable tool. It answers "where is X defined / who calls X / what
// implements this interface" semantically, where grep collides on names or
// misses indirect references. Degrades to a clear "unavailable" message when no
// language server is installed for the file's type — LSP is opportunistic.
type lspNavigateTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
	manager       *lsp.Manager
}

// NewLSPNavigateTool builds the tool with workspace-only path confinement.
func NewLSPNavigateTool(workspaceRoot string) Tool {
	return NewScopedLSPNavigateTool(workspaceRoot, nil)
}

// NewScopedLSPNavigateTool builds the tool with its own lazily-started LSP
// manager (servers spin up on first use and are reused across calls within a
// session). The model-supplied path is resolved through the same scoped
// confinement the sibling read-only tools use, so a `..` or absolute path
// cannot read or open a file outside the workspace (or an extra granted root).
func NewScopedLSPNavigateTool(workspaceRoot string, scope PathScope) Tool {
	root := normalizeWorkspaceRoot(workspaceRoot)
	return lspNavigateTool{
		baseTool: baseTool{
			name: "lsp_navigate",
			description: "Navigate code semantically via the language server: jump to a symbol's " +
				"definition, find all references, find implementations of an interface/method, search " +
				"workspace symbols by name, read hover documentation, outline a file's symbols, or trace " +
				"call-hierarchy (incoming/outgoing calls). Use this instead of grep when you need precise " +
				"definition/reference/implementation results. Returns file:line:col locations.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"op": {
						Type:        "string",
						Description: "Operation to perform.",
						Enum:        []string{"definition", "references", "implementation", "workspace_symbol", "hover", "document_symbol", "prepare_call_hierarchy", "incoming_calls", "outgoing_calls"},
					},
					"path":      {Type: "string", Description: "File to anchor the query (required for all ops; for workspace_symbol it selects the language server)."},
					"line":      {Type: "integer", Description: "1-based line of the symbol (position-based ops: definition/references/implementation/hover/call hierarchy).", Minimum: intPtr(1)},
					"character": {Type: "integer", Description: "1-based column of the symbol (position-based ops).", Minimum: intPtr(1)},
					"query":     {Type: "string", Description: "Symbol name to search for (workspace_symbol)."},
				},
				Required:             []string{"op", "path"},
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Queries the language server for code navigation; reads files, modifies nothing."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: false, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: root,
		scope:         scope,
		manager:       lsp.NewManager(root),
	}
}

func (tool lspNavigateTool) Run(ctx context.Context, args map[string]any) Result {
	opRaw, err := stringArg(args, "op", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for lsp_navigate: " + err.Error())
	}
	op := lsp.NavOp(strings.ToLower(strings.TrimSpace(opRaw)))

	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for lsp_navigate: " + err.Error())
	}

	known := map[lsp.NavOp]bool{
		lsp.NavDefinition: true, lsp.NavReferences: true, lsp.NavImplementation: true,
		lsp.NavWorkspaceSymbol: true, lsp.NavHover: true, lsp.NavDocumentSymbol: true,
		lsp.NavPrepareCallHierarchy: true, lsp.NavIncomingCalls: true, lsp.NavOutgoingCalls: true,
	}
	if !known[op] {
		return errorResult("Error: lsp_navigate unknown op " + string(op) + " (use definition, references, implementation, workspace_symbol, hover, document_symbol, prepare_call_hierarchy, incoming_calls, or outgoing_calls).")
	}

	// Confine the model-supplied path to the workspace (or an extra granted root)
	// BEFORE reading it or handing it to the language server — a `..` or absolute
	// path must not read/open a file outside the boundary the sibling read-only
	// tools enforce. Errors echo only the relative path, never an absolute one.
	absPath, relPath, scopeErr := resolveScopedReadPath(tool.workspaceRoot, tool.scope, requestedPath)
	if scopeErr != nil {
		return errorResult("Error: lsp_navigate " + scopeErr.Error())
	}

	text, readErr := readWorkspaceFile(absPath)
	if readErr != nil && op != lsp.NavWorkspaceSymbol {
		return errorResult("Error: lsp_navigate could not read " + relPath + ": " + readErr.Error())
	}

	// Hand the LSP manager the resolved absolute path so it opens only the
	// confined file (not the raw, possibly-escaping, model input).
	req := lsp.NavRequest{Op: op, Path: absPath, Text: text}
	switch op {
	case lsp.NavWorkspaceSymbol:
		query, qErr := stringArg(args, "query", "", true)
		if qErr != nil {
			return errorResult("Error: lsp_navigate workspace_symbol requires a query.")
		}
		req.Query = strings.TrimSpace(query)
	case lsp.NavDocumentSymbol:
		// No position needed; the whole file is outlined.
	default:
		line, lErr := intArg(args, "line", 0, 1, 0)
		col, cErr := intArg(args, "character", 1, 1, 0)
		if lErr != nil || cErr != nil || line == 0 {
			return errorResult("Error: lsp_navigate " + string(op) + " requires 1-based line and character.")
		}
		req.Line = line
		req.Character = col
		req.IncludeDeclaration = true
	}

	result, ok, navErr := tool.manager.Navigate(ctx, req)
	if navErr != nil {
		return errorResult("Error: lsp_navigate failed: " + navErr.Error())
	}
	if !ok {
		return okResult(fmt.Sprintf("No language server available for %s — lsp_navigate is unavailable for this file type. Fall back to grep/read_file.", filepath.Base(relPath)))
	}

	return okResult(tool.formatResult(op, result))
}

// readWorkspaceFile reads an already-confined absolute path (resolved by
// resolveScopedReadPath). Returns "" + error when the file can't be read.
func readWorkspaceFile(absPath string) (string, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (tool lspNavigateTool) formatResult(op lsp.NavOp, result lsp.NavResult) string {
	switch op {
	case lsp.NavWorkspaceSymbol:
		if len(result.Symbols) == 0 {
			return "No matching symbols."
		}
		lines := []string{fmt.Sprintf("%d symbol(s):", len(result.Symbols))}
		for _, s := range result.Symbols {
			lines = append(lines, fmt.Sprintf("  %s %s — %s", s.Kind, s.Name, tool.formatLocation(s.Location)))
		}
		return strings.Join(lines, "\n")
	case lsp.NavHover:
		if result.Hover == nil || result.Hover.Contents == "" {
			return "No hover information available at this position."
		}
		return result.Hover.Contents
	case lsp.NavDocumentSymbol:
		if len(result.Outline) == 0 {
			return "No symbols in file."
		}
		lines := []string{fmt.Sprintf("%d document symbol(s):", len(result.Outline))}
		lines = append(lines, tool.formatOutline(result.Outline, 0)...)
		return strings.Join(lines, "\n")
	case lsp.NavPrepareCallHierarchy:
		if len(result.CallItems) == 0 {
			return "No call hierarchy available at this position."
		}
		lines := []string{fmt.Sprintf("%d call hierarchy item(s):", len(result.CallItems))}
		for _, it := range result.CallItems {
			lines = append(lines, fmt.Sprintf("  %s %s — %s", it.Kind, it.Name, tool.formatURI(it.URI)))
		}
		return strings.Join(lines, "\n")
	case lsp.NavIncomingCalls, lsp.NavOutgoingCalls:
		if len(result.CallEdges) == 0 {
			return "No " + string(op) + " found."
		}
		lines := []string{fmt.Sprintf("%d %s:", len(result.CallEdges), string(op))}
		for _, edge := range result.CallEdges {
			lines = append(lines, fmt.Sprintf("  %s %s — %s (%d reference(s))", edge.Item.Kind, edge.Item.Name, tool.formatURI(edge.Item.URI), len(edge.FromRanges)))
		}
		return strings.Join(lines, "\n")
	default:
		if len(result.Locations) == 0 {
			return "No " + string(op) + " results."
		}
		lines := []string{fmt.Sprintf("%d %s result(s):", len(result.Locations), op)}
		for _, l := range result.Locations {
			lines = append(lines, "  "+tool.formatLocation(l))
		}
		return strings.Join(lines, "\n")
	}
}

func (tool lspNavigateTool) formatOutline(outline []lsp.SymbolOutline, depth int) []string {
	var lines []string
	indent := strings.Repeat("  ", depth)
	for _, s := range outline {
		loc := fmt.Sprintf("%d:%d", s.Range.Start.Line+1, s.Range.Start.Character+1)
		lines = append(lines, fmt.Sprintf("%s%s %s — %s", indent, s.Kind, s.Name, loc))
		lines = append(lines, tool.formatOutline(s.Children, depth+1)...)
	}
	return lines
}

// formatURI renders an absolute file URI path workspace-relative when possible.
func (tool lspNavigateTool) formatURI(uri string) string {
	path := lsp.URIToPath(uri)
	if rel, err := filepath.Rel(tool.workspaceRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// formatLocation renders a location as a workspace-relative file:line:col
// (1-based for the user, converting from LSP's 0-based positions).
func (tool lspNavigateTool) formatLocation(l lsp.Location) string {
	path := lsp.URIToPath(l.URI)
	if rel, err := filepath.Rel(tool.workspaceRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
		path = filepath.ToSlash(rel)
	}
	return fmt.Sprintf("%s:%d:%d", path, l.Range.Start.Line+1, l.Range.Start.Character+1)
}
