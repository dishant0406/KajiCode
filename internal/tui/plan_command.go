package tui

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

type currentPlanReader interface {
	CurrentTodos() []tools.PlanItem
}

func (m model) planText() string {
	tool, ok := m.registry.Get("todo_write")
	if !ok {
		return "No plan is active."
	}

	reader, ok := tool.(currentPlanReader)
	if !ok {
		return "No plan is active."
	}

	plan := reader.CurrentTodos()
	if len(plan) == 0 {
		return "No plan is active."
	}

	lines := make([]string, 0, len(plan)+1)
	lines = append(lines, "Current Plan")
	for index, item := range plan {
		line := fmt.Sprintf("%d. [%s] %s", index+1, item.Status, item.Content)
		if item.Notes != "" {
			line += "\n   Notes: " + item.Notes
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
