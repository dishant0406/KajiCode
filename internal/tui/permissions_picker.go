package tui

import (
	"fmt"

	"github.com/dishant0406/KajiCode/internal/agent"
)

func (m model) setPermissionProfile(mode agent.PermissionMode) (model, error) {
	switch mode {
	case agent.PermissionModeAskAll, agent.PermissionModeReadOnly,
		agent.PermissionModeReadWrite, agent.PermissionModeBypassAll:
	default:
		return m, fmt.Errorf("invalid profile %s", mode)
	}
	previous := m.permissionMode
	m.permissionMode = mode
	if m.savePermissionProfile != nil {
		if err := m.savePermissionProfile(mode); err != nil {
			m.permissionMode = previous
			return m, err
		}
	}
	return m, nil
}

func (m model) newPermissionsPicker() *commandPicker {
	items := []pickerItem{
		{Label: "Ask for every action", Value: string(agent.PermissionModeAskAll), Meta: "reads · writes · shell · network"},
		{Label: "Allow all reads", Value: string(agent.PermissionModeReadOnly), Meta: "ask for writes · shell · network"},
		{Label: "Allow all reads and file writes", Value: string(agent.PermissionModeReadWrite), Meta: "ask for shell · network · local control"},
		{Label: "Bypass all permissions and sandboxing", Value: string(agent.PermissionModeBypassAll), Meta: "dangerous · unrestricted host access"},
	}
	selected := 0
	for index, item := range items {
		if item.Value == string(m.permissionMode) {
			selected = index
			break
		}
	}
	return &commandPicker{
		kind:     pickerPermissions,
		title:    "Permissions",
		items:    append([]pickerItem{}, items...),
		allItems: items,
		selected: selected,
	}
}

func (m model) choosePermissionProfile(value string) (model, string) {
	mode := agent.PermissionMode(value)
	var err error
	m, err = m.setPermissionProfile(mode)
	if err != nil {
		return m, "Permission profile was not changed: " + err.Error()
	}
	warning := ""
	if mode == agent.PermissionModeBypassAll {
		warning = "\nWARNING: tools may access or modify anything the KajiCode process can reach without approval or sandboxing."
	}
	return m, fmt.Sprintf("Permission profile set to %s.%s\n\n%s", mode, warning, m.permissionsText())
}
