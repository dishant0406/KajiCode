package tui

import (
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

func (m model) harnessText() string {
	harness := m.agentOptions.Harness
	promptRows := harnessPromptRows(harness.PromptAddenda)
	ruleRows := harnessRuleRows(harness.PermissionRules)
	status := commandStatusOK
	if len(promptRows) == 0 && len(ruleRows) == 0 {
		status = commandStatusInfo
	}
	return renderCommandOutput(commandOutput{
		Title:  "Harness",
		Status: status,
		Sections: []commandSection{
			{
				Title: "Effective policy",
				Fields: []commandField{
					{Key: "prompt addenda", Value: fmt.Sprintf("%d", len(harness.PromptAddenda))},
					{Key: "permission rules", Value: fmt.Sprintf("%d", len(harness.PermissionRules))},
				},
			},
			{
				Title: "Prompt addenda",
				Rows:  promptRows,
				Lines: emptyHarnessLines(promptRows, "none"),
			},
			{
				Title: "Permission rules",
				Rows:  ruleRows,
				Lines: emptyHarnessLines(ruleRows, "none"),
			},
		},
		Hints: []string{
			"/harness prompt add <id> --text \"...\"",
			"/harness rule add <id> --match <tool> --action <allow|ask|deny>",
		},
	})
}

func harnessPromptRows(addenda []config.HarnessPromptAddendum) []commandRow {
	rows := make([]commandRow, 0, len(addenda))
	for _, addendum := range addenda {
		state := "enabled"
		if !addendum.IsEnabled() {
			state = "disabled"
		}
		id := harnessValue(addendum.ID, "<unnamed>")
		text := harnessPreview(addendum.Text, 96)
		if text == "" {
			text = "<empty>"
		}
		rows = append(rows, commandRow{Text: id + " [" + state + "] - " + text})
	}
	return rows
}

func harnessRuleRows(rules []config.HarnessPermissionRule) []commandRow {
	rows := make([]commandRow, 0, len(rules))
	for _, rule := range rules {
		state := "enabled"
		if !rule.IsEnabled() {
			state = "disabled"
		}
		parts := []string{
			harnessValue(rule.ID, "<unnamed>") + " [" + state + "]",
			harnessValue(rule.Action, "ask"),
			harnessValue(rule.Match, "*"),
		}
		if side := strings.TrimSpace(rule.SideEffect); side != "" {
			parts = append(parts, "side="+side)
		}
		if risk := strings.TrimSpace(rule.MinRisk); risk != "" {
			parts = append(parts, "risk>="+risk)
		}
		for _, needle := range rule.CommandContains {
			if needle = strings.TrimSpace(needle); needle != "" {
				parts = append(parts, "contains="+needle)
			}
		}
		if reason := harnessPreview(rule.Reason, 80); reason != "" {
			parts = append(parts, "reason="+reason)
		}
		rows = append(rows, commandRow{Text: strings.Join(parts, " ")})
	}
	return rows
}

func emptyHarnessLines(rows []commandRow, text string) []string {
	if len(rows) > 0 {
		return nil
	}
	return []string{text}
}

func harnessUsageText(status commandStatus, message string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Harness",
		Status: status,
		Sections: []commandSection{{
			Title: "Usage",
			Lines: []string{
				message,
				"/harness",
				"/harness prompt add <id> --text \"...\" [--project] [--disabled]",
				"/harness prompt remove <id> [--project]",
				"/harness rule add <id> --match <tool> --action <allow|ask|deny> [--project]",
				"/harness rule remove <id> [--project]",
			},
		}},
	})
}

func harnessListCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "list", "show", "status":
		return true
	default:
		return false
	}
}

func harnessHelpCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "-h", "--help", "help":
		return true
	default:
		return false
	}
}

func harnessValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func harnessPreview(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
