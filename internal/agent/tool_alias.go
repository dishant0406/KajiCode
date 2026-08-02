package agent

import "github.com/dishant0406/KajiCode/internal/tools"

var toolNameAliases = map[string]string{
	"cat":              "read_file",
	"read":             "read_file",
	"ls":               "list_directory",
	"list_files":       "list_directory",
	"rg":               "grep",
	"ripgrep":          "grep",
	"search":           "grep",
	"find_files":       "glob",
	"glob_files":       "glob",
	"shell":            tools.ExecCommandToolName,
	"terminal":         tools.ExecCommandToolName,
	"exec":             tools.ExecCommandToolName,
	"patch":            "apply_patch",
	"apply_patch_tool": "apply_patch",
	"edit":             "edit_file",
	"write":            "write_file",
}

func canonicalizeToolCalls(registry *tools.Registry, calls []ToolCall) []ToolCall {
	if len(calls) == 0 || registry == nil {
		return calls
	}
	var out []ToolCall
	for i, call := range calls {
		canonical, ok := canonicalToolName(registry, call.Name)
		if !ok || canonical == call.Name {
			continue
		}
		if out == nil {
			out = append([]ToolCall(nil), calls...)
		}
		out[i].Name = canonical
	}
	if out == nil {
		return calls
	}
	return out
}

func canonicalToolName(registry *tools.Registry, name string) (string, bool) {
	if registry == nil || name == "" {
		return name, false
	}
	if _, found := registry.Get(name); found {
		return name, true
	}
	alias, found := toolNameAliases[name]
	if !found {
		return name, false
	}
	if _, targetFound := registry.Get(alias); !targetFound {
		return name, false
	}
	return alias, true
}
