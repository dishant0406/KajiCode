package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dishant0406/KajiCode/internal/redaction"
)

func (m model) mcpAddWizardOverlay(width int) string {
	if m.mcpAddWizard == nil {
		return ""
	}
	return m.mcpAddWizard.render(width)
}

func (wizard *mcpAddWizardState) render(width int) string {
	if wizard == nil {
		return ""
	}
	overlayWidth := mcpAddWizardOverlayWidth(width)
	innerWidth := maxInt(20, overlayWidth-4)
	lines := []string{
		kajicodeTheme.faint.Render(mcpAddWizardStepLine(wizard.step)),
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
	}
	if wizard.err != "" {
		lines = append(lines, kajicodeTheme.red.Render("error: "+wizard.err), "")
	}
	switch wizard.step {
	case mcpAddWizardStepName:
		lines = append(lines, wizard.renderNameStep(innerWidth)...)
	case mcpAddWizardStepType:
		lines = append(lines, wizard.renderTypeStep(innerWidth)...)
	case mcpAddWizardStepEndpoint:
		lines = append(lines, wizard.renderEndpointStep(innerWidth)...)
	case mcpAddWizardStepHeader:
		lines = append(lines, wizard.renderHeaderStep(innerWidth)...)
	case mcpAddWizardStepConfirm:
		lines = append(lines, wizard.renderConfirmStep(innerWidth)...)
	case mcpAddWizardStepResult:
		lines = append(lines, wizard.renderResultStep(innerWidth)...)
	}
	lines = append(lines,
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
		kajicodeTheme.faint.Render(wizard.footer()),
	)
	block := styledBlockFillTitle(overlayWidth, "Add MCP Server", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle())
	return centerRenderedBlock(block, width)
}

func (wizard *mcpAddWizardState) renderNameStep(width int) []string {
	value := displayValue(strings.TrimSpace(wizard.serverName), "type a stable name")
	return []string{
		kajicodeTheme.accent.Render("Server Name"),
		fitStyledLine(kajicodeTheme.ink.Render("> "+value), width),
		kajicodeTheme.faint.Render("Default: " + mcpAddWizardTypes[clampInt(wizard.selectedType, 0, len(mcpAddWizardTypes)-1)].Label),
		kajicodeTheme.faint.Render("Use lowercase letters, numbers, dashes, or underscores."),
	}
}

func (wizard *mcpAddWizardState) renderTypeStep(width int) []string {
	lines := []string{kajicodeTheme.accent.Render("Server Type")}
	for index, item := range mcpAddWizardTypes {
		marker := "  "
		surface := transparentSurface
		if index == wizard.selectedType {
			marker = "> "
			surface = kajicodeTheme.onSel
		}
		line := marker + item.Label
		if item.Meta != "" {
			line += "  " + item.Meta
		}
		lines = append(lines, fillPaletteLine(surface(kajicodeTheme.ink).Render(line), width, surface))
	}
	return lines
}

func (wizard *mcpAddWizardState) renderEndpointStep(width int) []string {
	title := "Endpoint URL"
	placeholder := "https://example.com/mcp"
	if !wizard.isRemote() {
		title = "Command"
		placeholder = "npx -y @modelcontextprotocol/server-filesystem ."
	}
	value := displayValue(strings.TrimSpace(wizard.endpoint), placeholder)
	return []string{
		kajicodeTheme.accent.Render(title),
		fitStyledLine(kajicodeTheme.ink.Render("> "+value), width),
	}
}

func (wizard *mcpAddWizardState) renderHeaderStep(width int) []string {
	value := displayValue(strings.TrimSpace(wizard.headerInput), "press Enter to skip")
	return []string{
		kajicodeTheme.accent.Render("Add header"),
		fitStyledLine(kajicodeTheme.ink.Render("> "+redaction.RedactString(value, redaction.Options{})), width),
		kajicodeTheme.faint.Render(`Paste "Key: Value" or "Key=Value".`),
	}
}

func (wizard *mcpAddWizardState) renderConfirmStep(width int) []string {
	lines := []string{
		kajicodeTheme.accent.Render("Review setup"),
		"server: " + kajicodeTheme.ink.Render(wizard.serverName),
		"type: " + kajicodeTheme.ink.Render(strings.ToUpper(wizard.serverType)),
	}
	if source := strings.TrimSpace(wizard.sourceLabel); source != "" {
		lines = append(lines, "source: "+kajicodeTheme.ink.Render(source))
	}
	if sourceURL := strings.TrimSpace(wizard.sourceURL); sourceURL != "" {
		lines = append(lines, "docs: "+kajicodeTheme.ink.Render(redactMCPWizardDisplayValue(sourceURL)))
	}
	if wizard.isRemote() {
		lines = append(lines, "url: "+kajicodeTheme.ink.Render(redactMCPWizardDisplayValue(wizard.endpoint)))
		if wizard.headerKey != "" {
			lines = append(lines, "header: "+kajicodeTheme.ink.Render(wizard.headerKey+"=[REDACTED]"))
		}
	} else {
		lines = append(lines, "command: "+kajicodeTheme.ink.Render(redactMCPWizardCommand(wizard.endpoint)))
	}
	if len(wizard.prerequisites) > 0 {
		lines = append(lines, "", kajicodeTheme.accent.Render("Needs"))
		for _, item := range wizard.prerequisites {
			lines = append(lines, "  - "+kajicodeTheme.ink.Render(item))
		}
	}
	lines = append(lines, "", kajicodeTheme.faint.Render("Enter saves and tests the server."))
	for index, line := range lines {
		lines[index] = fitStyledLine(line, width)
	}
	return lines
}

func (wizard *mcpAddWizardState) renderResultStep(width int) []string {
	result := wizard.result
	title := result.Title
	if title == "" {
		title = "MCP setup issue"
	}
	state := displayValue(result.State, "not saved")
	transport := "HTTP remote"
	if !wizard.isRemote() {
		transport = "Local stdio"
	} else if wizard.serverType == "sse" {
		transport = "SSE remote"
	}
	lines := []string{
		kajicodeTheme.accent.Render(title),
		kajicodeTheme.ink.Bold(true).Render(displayValue(wizard.serverName, "unnamed")) + "  " + kajicodeTheme.faint.Render(state),
		kajicodeTheme.faint.Render(transport),
	}
	if result.Message != "" {
		lines = append(lines, fitStyledLine(kajicodeTheme.red.Render(result.Message), width))
	}
	if !result.Saved {
		lines = append(lines, kajicodeTheme.faint.Render("No config was saved yet."))
	} else {
		lines = append(lines, fmt.Sprintf("Tools: %d discovered", maxInt(0, result.ToolCount)))
	}
	lines = append(lines, "")
	for index, action := range wizard.resultActionLabels() {
		marker := "  "
		if index == clampInt(wizard.resultSelected, 0, len(wizard.resultActionLabels())-1) {
			marker = "> "
		}
		lines = append(lines, marker+action)
	}
	for index, line := range lines {
		lines[index] = fitStyledLine(line, width)
	}
	return lines
}

func redactMCPWizardDisplayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if looksLikeMCPDisplayURLValue(value) {
		return redactMCPDisplayURL(value)
	}
	return redaction.RedactString(value, redaction.Options{})
}

func redactMCPWizardCommand(value string) string {
	parts, err := splitMCPCommandArgs(value)
	if err != nil || len(parts) == 0 {
		return redactMCPWizardDisplayValue(value)
	}
	redacted := make([]string, 0, len(parts))
	head := strings.TrimSpace(parts[0])
	if looksLikeMCPDisplayURLValue(head) {
		head = redactMCPDisplayURL(head)
	} else {
		head = redaction.RedactString(head, redaction.Options{})
	}
	redacted = append(redacted, head)
	redacted = append(redacted, redactedCommandArgs(parts[1:])...)
	return strings.Join(redacted, " ")
}

func (wizard *mcpAddWizardState) resultActionLabels() []string {
	switch {
	case wizard == nil:
		return nil
	case wizard.result.Connected:
		return []string{"Use server", "Manage tools", "Edit config", "Disable server"}
	case wizard.result.Saved:
		return []string{"Retry connection", "Edit config", "Disable server", "Remove server"}
	default:
		return []string{"Edit URL", "Save disabled", "Discard"}
	}
}

func (wizard *mcpAddWizardState) footer() string {
	switch wizard.step {
	case mcpAddWizardStepType:
		return "up/down select   Enter continue   Esc close"
	case mcpAddWizardStepResult:
		return "Enter select   r retry   s save disabled   d discard"
	default:
		return "Enter continue   left back   Esc close"
	}
}

func mcpAddWizardOverlayWidth(width int) int {
	if width <= 0 {
		return mcpAddWizardMaxWidth
	}
	target := minInt(width, mcpAddWizardMaxWidth)
	if target < mcpAddWizardMinWidth {
		return width
	}
	return target
}

func mcpAddWizardStepLine(step mcpAddWizardStep) string {
	steps := []struct {
		step  mcpAddWizardStep
		label string
	}{
		{mcpAddWizardStepName, "1 name"},
		{mcpAddWizardStepType, "2 type"},
		{mcpAddWizardStepEndpoint, "3 endpoint"},
		{mcpAddWizardStepHeader, "4 auth"},
		{mcpAddWizardStepConfirm, "5 confirm"},
	}
	parts := make([]string, 0, len(steps))
	for _, item := range steps {
		if item.step == step {
			parts = append(parts, "["+item.label+"]")
		} else {
			parts = append(parts, item.label)
		}
	}
	return strings.Join(parts, "  ")
}
