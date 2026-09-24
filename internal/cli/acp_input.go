package cli

import (
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/usercommands"
)

// Wiring for the elicitation-backed input modals (/prompt snippets).

// acpSavePromptSnippet writes a reusable prompt snippet to the user command dir,
// reusing usercommands.Save (the same path the TUI /prompt editor uses).
func acpSavePromptSnippet(deps appDeps) func(workspaceRoot, slug, body string) (string, error) {
	return func(_, slug, body string) (string, error) {
		dir := acpUserCommandDir(deps)
		if dir == "" {
			return "", errFromWriter("could not resolve the user command directory")
		}
		cmd, err := usercommands.Save(dir, slug, body, false)
		if err != nil {
			return "", err
		}
		return cmd.Name, nil
	}
}

// acpExpandPromptSnippet resolves "/name args" against the saved snippets,
// expanding the template placeholders. ok=false when no snippet matches.
func acpExpandPromptSnippet(deps appDeps) func(workspaceRoot, name, args string) (string, bool) {
	return func(workspaceRoot, name, args string) (string, bool) {
		paths := usercommands.Paths{}
		if dir := acpUserCommandDir(deps); dir != "" {
			paths.UserDir = dir
		}
		if strings.TrimSpace(workspaceRoot) != "" {
			paths.ProjectDir = filepath.Join(workspaceRoot, ".kajicode", "commands")
		}
		want := strings.ToLower(strings.TrimSpace(name))
		if _, isBuiltin := lookupACPCommand(want); isBuiltin || isSessionCommand(want) {
			return "", false
		}
		for _, cmd := range usercommands.Load(paths) {
			if strings.EqualFold(cmd.Name, want) {
				expanded := usercommands.Expand(cmd.Template, args)
				if strings.TrimSpace(expanded) == "" {
					return "", false
				}
				return expanded, true
			}
		}
		return "", false
	}
}

// acpUserCommandDir is the user's personal commands directory.
func acpUserCommandDir(deps appDeps) string {
	if deps.userConfigPath != nil {
		if p, err := deps.userConfigPath(); err == nil && strings.TrimSpace(p) != "" {
			return filepath.Join(filepath.Dir(p), "commands")
		}
	}
	return ""
}
