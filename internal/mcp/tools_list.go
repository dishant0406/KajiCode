package mcp

import "context"

// clientListTools implements tools/list with cursor pagination, mirroring
// clientListResources. It is shared by all three transports. OutputSchema is not
// required by KajiCode (the model only needs inputSchema), so a server that omits
// or nulls it is tolerated by the plain struct decoding — no fragile schema here.
func clientListTools(ctx context.Context, request requestFunc) ([]RemoteTool, error) {
	var tools []RemoteTool
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; page < maxCatalogPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Tools      []RemoteTool `json:"tools"`
			NextCursor string       `json:"nextCursor"`
		}
		if err := request(ctx, "tools/list", params, &result); err != nil {
			return nil, err
		}
		tools = append(tools, result.Tools...)
		if result.NextCursor == "" {
			return tools, nil
		}
		if _, repeated := seen[result.NextCursor]; repeated {
			return tools, nil // guard against a server that repeats a cursor
		}
		seen[result.NextCursor] = struct{}{}
		cursor = result.NextCursor
	}
	return tools, nil
}
