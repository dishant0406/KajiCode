package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func runHarness(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 || args[0] == "list" || args[0] == "show" {
		return runHarnessList(args, stdout, stderr, deps)
	}
	switch args[0] {
	case "prompt":
		return runHarnessPrompt(args[1:], stdout, stderr, deps)
	case "rule", "rules", "permission", "permissions":
		return runHarnessRule(args[1:], stdout, stderr, deps)
	case "-h", "--help", "help":
		return writeHarnessHelp(stdout)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown harness command %q", args[0]))
	}
}

func runHarnessList(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "list", "show":
		case "--json":
			jsonOut = true
		case "-h", "--help", "help":
			return writeHarnessHelp(stdout)
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown harness list flag %q", arg))
		}
	}
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	if jsonOut {
		if err := writePrettyJSON(stdout, resolved.Harness); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatHarness(resolved.Harness)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runHarnessPrompt(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return writeHarnessHelp(stdout)
	}
	switch args[0] {
	case "add", "set":
		id, addendum, project, err := parseHarnessPromptAdd(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		path, err := harnessConfigPath(project, deps)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		addendum.ID = id
		if _, err := config.SetHarnessPromptAddendum(path, addendum); err != nil {
			return writeAppError(stderr, err.Error(), exitProvider)
		}
		_, _ = fmt.Fprintf(stdout, "Saved harness prompt addendum %s in %s\n", id, path)
		return exitSuccess
	case "remove", "rm", "delete":
		id, project, err := parseHarnessRemove(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		path, err := harnessConfigPath(project, deps)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if _, err := config.RemoveHarnessPromptAddendum(path, id); err != nil {
			return writeAppError(stderr, err.Error(), exitProvider)
		}
		_, _ = fmt.Fprintf(stdout, "Removed harness prompt addendum %s from %s\n", id, path)
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown harness prompt command %q", args[0]))
	}
}

func runHarnessRule(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return writeHarnessHelp(stdout)
	}
	switch args[0] {
	case "add", "set":
		rule, project, err := parseHarnessRuleAdd(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if project && strings.EqualFold(rule.Action, config.HarnessRuleAllow) {
			return writeExecUsageError(stderr, "project harness permission rules cannot use action allow")
		}
		path, err := harnessConfigPath(project, deps)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if _, err := config.SetHarnessPermissionRule(path, rule); err != nil {
			return writeAppError(stderr, err.Error(), exitProvider)
		}
		_, _ = fmt.Fprintf(stdout, "Saved harness permission rule %s in %s\n", rule.ID, path)
		return exitSuccess
	case "remove", "rm", "delete":
		id, project, err := parseHarnessRemove(args[1:])
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		path, err := harnessConfigPath(project, deps)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if _, err := config.RemoveHarnessPermissionRule(path, id); err != nil {
			return writeAppError(stderr, err.Error(), exitProvider)
		}
		_, _ = fmt.Fprintf(stdout, "Removed harness permission rule %s from %s\n", id, path)
		return exitSuccess
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown harness rule command %q", args[0]))
	}
}

func harnessConfigPath(project bool, deps appDeps) (string, error) {
	if project {
		workspaceRoot, err := resolveWorkspaceRoot("", deps)
		if err != nil {
			return "", err
		}
		return filepath.Join(workspaceRoot, ".kajicode", "config.json"), nil
	}
	return deps.userConfigPath()
}

func formatHarness(harness config.HarnessConfig) string {
	lines := []string{"Harness"}
	lines = append(lines, fmt.Sprintf("prompt addenda: %d", len(harness.PromptAddenda)))
	for _, addendum := range harness.PromptAddenda {
		state := "enabled"
		if !addendum.IsEnabled() {
			state = "disabled"
		}
		lines = append(lines, fmt.Sprintf("  %s [%s]", harnessDisplay(addendum.ID, "<unnamed>"), state))
	}
	lines = append(lines, fmt.Sprintf("permission rules: %d", len(harness.PermissionRules)))
	for _, rule := range harness.PermissionRules {
		state := "enabled"
		if !rule.IsEnabled() {
			state = "disabled"
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] %s %s", harnessDisplay(rule.ID, "<unnamed>"), state, rule.Action, rule.Match))
	}
	return strings.Join(lines, "\n")
}

func writeHarnessHelp(w io.Writer) int {
	_, err := fmt.Fprint(w, `Usage:
  kajicode harness list [--json]
  kajicode harness prompt add <id> --text <text> [--project] [--disabled]
  kajicode harness prompt remove <id> [--project]
  kajicode harness rule add <id> --match <pattern> --action <allow|ask|deny> [flags]
  kajicode harness rule remove <id> [--project]

Flags for rule add:
      --side-effect <read|write|shell|network|...>
      --min-risk <low|medium|high|critical>
      --command-contains <text>
      --reason <text>
      --project
      --disabled
`)
	if err != nil {
		return exitCrash
	}
	return exitSuccess
}
