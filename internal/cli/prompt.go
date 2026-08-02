package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

type promptInspectOptions struct {
	json bool
	full bool
}

func runPrompt(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "inspect"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	switch command {
	case "inspect", "show":
		return runPromptInspect(args, stdout, stderr, deps)
	case "edit":
		return runPromptEdit(args, stdout, stderr, deps)
	case "help":
		return writePromptHelp(stdout)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown prompt command %q", command))
	}
}

func runPromptInspect(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parsePromptInspectArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writePromptHelp(stdout)
	}
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	modelRegistry, _ := modelregistry.DefaultRegistry()
	mode := agent.PermissionMode(strings.TrimSpace(resolved.Preferences.PermissionProfile))
	if mode == "" {
		mode = agent.PermissionModeAskAll
	}
	report := agent.BuildSystemPromptReport(agent.Options{
		Cwd:            workspaceRoot,
		ProviderName:   resolved.Provider.Name,
		Model:          resolved.Provider.Model,
		ContextWindow:  modelContextWindow(modelRegistry, resolved.Provider.Model),
		PermissionMode: mode,
		Harness:        resolved.Harness,
	}, options.full)
	if options.json {
		if err := writePrettyJSON(stdout, report); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatPromptReport(report)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parsePromptInspectArgs(args []string) (promptInspectOptions, bool, error) {
	options := promptInspectOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		case "--full":
			options.full = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown prompt inspect flag %q", arg)}
		}
	}
	return options, false, nil
}

func runPromptEdit(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	scope, help, err := parsePromptEditScope(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writePromptHelp(stdout)
	}
	path, err := promptEditPath(scope, deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if err := ensureEditablePromptFile(path); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if err := deps.runEditor(path); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if _, err := fmt.Fprintf(stdout, "Edited prompt instructions: %s\n", path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parsePromptEditScope(args []string) (string, bool, error) {
	scope := "user"
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return scope, true, nil
		case "--user":
			scope = "user"
		case "--project":
			scope = "project"
		default:
			return "", false, execUsageError{fmt.Sprintf("unknown prompt edit flag %q", arg)}
		}
	}
	return scope, false, nil
}

func promptEditPath(scope string, deps appDeps) (string, error) {
	if scope == "project" {
		workspaceRoot, err := resolveWorkspaceRoot("", deps)
		if err != nil {
			return "", err
		}
		return filepath.Join(workspaceRoot, ".kajicode", "KAJICODE.md"), nil
	}
	userConfigPath, err := deps.userConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(userConfigPath), "KAJICODE.md"), nil
}

func ensureEditablePromptFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create prompt directory: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect prompt file: %w", err)
	}
	return os.WriteFile(path, []byte("# KajiCode Instructions\n\n"), 0o600)
}

func formatPromptReport(report agent.SystemPromptReport) string {
	lines := []string{
		"Prompt inspect",
		fmt.Sprintf("total: %d tokens estimated, %d bytes", report.EstimatedTokens, report.TotalBytes),
	}
	for _, section := range report.Sections {
		lines = append(lines, fmt.Sprintf("  %s: %d tokens, %d bytes", section.Role, section.EstimatedTokens, section.Bytes))
	}
	if strings.TrimSpace(report.Prompt) != "" {
		lines = append(lines, "", "Full prompt:", report.Prompt)
	}
	return strings.Join(lines, "\n")
}

func writePromptHelp(w io.Writer) int {
	_, err := fmt.Fprint(w, `Usage:
  kajicode prompt inspect [--json] [--full]
  kajicode prompt edit [--user|--project]

Inspect or edit the instructions that feed the runtime system prompt.
`)
	if err != nil {
		return exitCrash
	}
	return exitSuccess
}
