package cli

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/agents"
)

func formatAgentList(result agents.LoadResult) string {
	lines := []string{"Agents:"}
	if len(result.Agents) == 0 {
		lines = append(lines, "  (none)")
	}
	for _, agent := range result.Agents {
		marker := ""
		if agent.Hidden {
			marker = " (hidden)"
		}
		description := strings.TrimSpace(agent.Description)
		if description == "" {
			description = "(no description)"
		}
		lines = append(lines, fmt.Sprintf("  %s [%s]%s - %s", agent.Name, agent.Location, marker, description))
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warnings:")
		for _, warning := range result.Warnings {
			lines = append(lines, "  "+warning)
		}
	}
	return strings.Join(lines, "\n")
}

func formatAgentShow(agent agents.Agent) string {
	lines := []string{
		"name: " + agent.Name,
	}
	if agent.Description != "" {
		lines = append(lines, "description: "+agent.Description)
	}
	lines = append(lines, "location: "+string(agent.Location))
	if agent.FilePath != "" {
		lines = append(lines, "file: "+agent.FilePath)
	}
	if agent.Mode != "" {
		lines = append(lines, "mode: "+string(agent.Mode))
	}
	if agent.Hidden {
		lines = append(lines, "hidden: true")
	}
	if agent.Extends != "" {
		lines = append(lines, "extends: "+agent.Extends)
	}
	if agent.Model != "" {
		lines = append(lines, "model: "+agent.Model)
	}
	if agent.Thinking != "" {
		lines = append(lines, "thinking: "+agent.Thinking)
	}
	if len(agent.Tools) > 0 {
		lines = append(lines, "tools: "+strings.Join(agent.Tools, ", "))
	}
	if len(agent.ExcludeTools) > 0 {
		lines = append(lines, "excludeTools: "+strings.Join(agent.ExcludeTools, ", "))
	}
	lines = append(lines, "", "system prompt:", strings.TrimSpace(agent.SystemPrompt))
	if len(agent.Warnings) > 0 {
		lines = append(lines, "", "Warnings:")
		for _, warning := range agent.Warnings {
			lines = append(lines, "  "+warning)
		}
	}
	return strings.Join(lines, "\n")
}

func formatAgentPaths(paths agents.Paths) string {
	lines := []string{"user: " + paths.UserDir}
	if strings.TrimSpace(paths.ProjectDir) != "" {
		lines = append(lines, "project: "+paths.ProjectDir)
	}
	return strings.Join(lines, "\n")
}
