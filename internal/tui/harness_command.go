package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func (m model) handleHarnessCommand(input string) (model, string) {
	args, err := splitMCPCommandArgs(input)
	if err != nil {
		return m, harnessUsageText(commandStatusBlocked, strings.Replace(err.Error(), "MCP command", "harness command", 1))
	}
	if len(args) == 0 || harnessListCommand(args[0]) {
		return m, m.harnessText()
	}
	if harnessHelpCommand(args[0]) {
		return m, harnessUsageText(commandStatusInfo, "Manage harness prompt addenda and permission rules.")
	}

	switch strings.ToLower(args[0]) {
	case "prompt":
		return m.handleHarnessPromptCommand(args[1:])
	case "rule", "rules", "permission", "permissions":
		return m.handleHarnessRuleCommand(args[1:])
	default:
		return m, harnessUsageText(commandStatusBlocked, fmt.Sprintf("unknown harness command %q", args[0]))
	}
}

func (m model) handleHarnessPromptCommand(args []string) (model, string) {
	if len(args) == 0 || harnessHelpCommand(args[0]) {
		return m, harnessUsageText(commandStatusInfo, "Usage: /harness prompt add <id> --text \"...\" [--project]")
	}
	switch strings.ToLower(args[0]) {
	case "add", "set":
		id, addendum, project, err := parseTUIHarnessPromptAdd(args[1:])
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		path, err := m.harnessConfigPath(project)
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		addendum.ID = id
		if _, err := config.SetHarnessPromptAddendum(path, addendum); err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		return m.reloadHarnessAndReport(path, project, "Saved harness prompt addendum "+id)
	case "remove", "rm", "delete":
		id, project, err := parseTUIHarnessRemove(args[1:])
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		path, err := m.harnessConfigPath(project)
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		if _, err := config.RemoveHarnessPromptAddendum(path, id); err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		return m.reloadHarnessAndReport(path, project, "Removed harness prompt addendum "+id)
	default:
		return m, harnessUsageText(commandStatusBlocked, fmt.Sprintf("unknown harness prompt command %q", args[0]))
	}
}

func (m model) handleHarnessRuleCommand(args []string) (model, string) {
	if len(args) == 0 || harnessHelpCommand(args[0]) {
		return m, harnessUsageText(commandStatusInfo, "Usage: /harness rule add <id> --match <tool> --action <allow|ask|deny>")
	}
	switch strings.ToLower(args[0]) {
	case "add", "set":
		rule, project, err := parseTUIHarnessRuleAdd(args[1:])
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		if project && strings.EqualFold(rule.Action, config.HarnessRuleAllow) {
			return m, harnessUsageText(commandStatusBlocked, "project harness permission rules cannot use action allow")
		}
		path, err := m.harnessConfigPath(project)
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		if _, err := config.SetHarnessPermissionRule(path, rule); err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		return m.reloadHarnessAndReport(path, project, "Saved harness permission rule "+rule.ID)
	case "remove", "rm", "delete":
		id, project, err := parseTUIHarnessRemove(args[1:])
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		path, err := m.harnessConfigPath(project)
		if err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		if _, err := config.RemoveHarnessPermissionRule(path, id); err != nil {
			return m, harnessUsageText(commandStatusBlocked, err.Error())
		}
		return m.reloadHarnessAndReport(path, project, "Removed harness permission rule "+id)
	default:
		return m, harnessUsageText(commandStatusBlocked, fmt.Sprintf("unknown harness rule command %q", args[0]))
	}
}

func (m model) harnessConfigPath(project bool) (string, error) {
	if project {
		if strings.TrimSpace(m.projectConfigPath) != "" {
			return m.projectConfigPath, nil
		}
		if strings.TrimSpace(m.cwd) == "" {
			return "", fmt.Errorf("project config path is unavailable")
		}
		return filepath.Join(m.cwd, ".kajicode", "config.json"), nil
	}
	if strings.TrimSpace(m.userConfigPath) == "" {
		return "", fmt.Errorf("user config path is unavailable")
	}
	return m.userConfigPath, nil
}

func (m model) reloadHarnessAndReport(path string, project bool, message string) (model, string) {
	next, err := m.reloadHarness(path, project)
	if err != nil {
		return m, renderCommandOutput(commandOutput{
			Title:  "Harness",
			Status: commandStatusWarning,
			Sections: []commandSection{{
				Title: "Saved",
				Lines: []string{message, "path: " + path, "reload failed: " + err.Error()},
			}},
			Hints: []string{"restart KajiCode if the live session should pick up the saved harness config"},
		})
	}
	return next, renderCommandOutput(commandOutput{
		Title:  "Harness",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Saved",
			Lines: []string{message, "path: " + path},
		}},
		Hints: []string{"run /harness to inspect the effective live policy"},
	})
}

func (m model) reloadHarness(path string, project bool) (model, error) {
	userPath, err := existingHarnessConfigPath(m.userConfigPath)
	if err != nil {
		return m, err
	}
	projectPath := m.projectConfigPath
	if project {
		projectPath = path
	}
	projectPath, err = existingHarnessConfigPath(projectPath)
	if err != nil {
		return m, err
	}
	resolved, err := config.Resolve(config.ResolveOptions{UserConfigPath: userPath, ProjectConfigPath: projectPath})
	if err != nil {
		return m, err
	}
	m.agentOptions.Harness = resolved.Harness
	if project && path != "" {
		m.projectConfigPath = path
	}
	return m, nil
}

func existingHarnessConfigPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if os.IsNotExist(err) {
		return "", nil
	} else {
		return "", fmt.Errorf("inspect config %s: %w", path, err)
	}
}
