package tui

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/modelregistry"
)

func (m model) promptInspectText(input string) string {
	options, err := parsePromptInspectCommandArgs(input)
	if err != nil {
		return promptInspectUsageText(commandStatusBlocked, err.Error())
	}
	if options.help {
		return promptInspectUsageText(commandStatusInfo, "Inspect the runtime system prompt assembled for the next agent turn.")
	}

	agentOptions := m.agentOptions
	agentOptions.Registry = m.registry
	agentOptions.PermissionMode = m.permissionMode
	agentOptions.ProviderName = m.providerName
	agentOptions.Model = m.modelName
	agentOptions.ReasoningEffort = string(m.reasoningEffort)
	agentOptions.ResponseStyle = m.responseStyle
	agentOptions.Cwd = m.cwd
	agentOptions.ContextWindow = modelregistry.AgentContextWindow(m.modelContextWindow(m.modelName))

	report := agent.BuildSystemPromptReport(agentOptions, options.full)
	if options.full {
		return promptInspectFullText(report)
	}
	sections := []commandSection{
		{
			Title: "Totals",
			Fields: []commandField{
				{Key: "estimated tokens", Value: fmt.Sprintf("%d", report.EstimatedTokens)},
				{Key: "bytes", Value: fmt.Sprintf("%d", report.TotalBytes)},
				{Key: "runes", Value: fmt.Sprintf("%d", report.TotalRunes)},
			},
		},
		{
			Title: "Sections",
			Rows:  promptInspectSectionRows(report.Sections),
		},
		{
			Title: "Editable layers",
			Rows:  promptInspectEditableRows(),
		},
	}
	return renderCommandOutput(commandOutput{
		Title:    "Prompt inspect",
		Status:   commandStatusOK,
		Sections: sections,
		Hints:    promptInspectHints(),
	})
}

type promptInspectCommandOptions struct {
	full bool
	help bool
}

func parsePromptInspectCommandArgs(input string) (promptInspectCommandOptions, error) {
	options := promptInspectCommandOptions{full: true}
	args, err := splitMCPCommandArgs(input)
	if err != nil {
		return options, fmt.Errorf("%s", strings.Replace(err.Error(), "MCP command", "prompt inspect command", 1))
	}
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "-h", "--help", "help":
			options.help = true
		case "--full", "full", "--raw", "raw":
			options.full = true
		case "--summary", "summary":
			options.full = false
		default:
			return options, fmt.Errorf("unknown prompt inspect flag %q", arg)
		}
	}
	return options, nil
}

func promptInspectSectionRows(sections []agent.SystemPromptSectionReport) []commandRow {
	rows := make([]commandRow, 0, len(sections))
	for _, section := range sections {
		text := fmt.Sprintf("%s: %d tokens, %d bytes", section.Role, section.EstimatedTokens, section.Bytes)
		rows = append(rows, commandRow{Text: text})
	}
	return rows
}

func promptInspectEditableRows() []commandRow {
	return []commandRow{
		{Text: "user/project guidelines: kajicode prompt edit --user | kajicode prompt edit --project"},
		{Text: "harness addendum: /harness prompt add <id> --text \"...\" [--project]"},
		{Text: "permission policy: /harness rule add <id> --match <tool> --action <allow|ask|deny>"},
	}
}

func promptInspectHints() []string {
	return []string{"/prompt-inspect prints the complete prompt", "/harness edits the configurable harness layers live"}
}

func promptInspectFullText(report agent.SystemPromptReport) string {
	lines := []string{
		"Prompt inspect",
		fmt.Sprintf("estimated tokens: %d", report.EstimatedTokens),
		fmt.Sprintf("bytes: %d", report.TotalBytes),
		fmt.Sprintf("runes: %d", report.TotalRunes),
		"summary: /prompt-inspect --summary",
		"",
		"Editable layers:",
		"  user/project guidelines: kajicode prompt edit --user | kajicode prompt edit --project",
		"  harness addendum: /harness prompt add <id> --text \"...\" [--project]",
		"  permission policy: /harness rule add <id> --match <tool> --action <allow|ask|deny>",
		"",
		"Full prompt:",
		strings.TrimSpace(report.Prompt),
	}
	return strings.Join(lines, "\n")
}

func promptInspectUsageText(status commandStatus, message string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Prompt inspect",
		Status: status,
		Sections: []commandSection{{
			Title: "Usage",
			Lines: []string{
				message,
				"/prompt-inspect",
				"/prompt-inspect --full",
				"/prompt-inspect --summary",
			},
		}},
	})
}
