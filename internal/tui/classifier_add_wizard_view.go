package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m model) classifierAddWizardOverlay(width int) string {
	if m.classifierAddWizard == nil {
		return ""
	}
	return m.classifierAddWizard.render(width)
}

func (wizard *classifierAddWizardState) render(width int) string {
	if wizard == nil {
		return ""
	}
	overlayWidth := classifierAddWizardOverlayWidth(width)
	innerWidth := maxInt(20, overlayWidth-4)
	lines := []string{
		kajicodeTheme.faint.Render(classifierAddWizardStepLine(wizard.step)),
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
	}
	if wizard.err != "" {
		lines = append(lines, kajicodeTheme.red.Render("error: "+wizard.err), "")
	}
	switch wizard.step {
	case classifierAddWizardStepName:
		lines = append(lines, wizard.renderFieldLine(innerWidth, "Classifier Name", wizard.name, "type a stable name (e.g. jev)")...)
	case classifierAddWizardStepEndpoint:
		lines = append(lines, wizard.renderFieldLine(innerWidth, "Endpoint", wizard.endpoint, "https://openrouter.ai/api/v1/systemone")...)
	case classifierAddWizardStepModel:
		lines = append(lines, wizard.renderFieldLine(innerWidth, "Model", wizard.model, "jev-1.13 (optional)")...)
	case classifierAddWizardStepKey:
		lines = append(lines, wizard.renderFieldLine(innerWidth, "API Key", strings.Repeat("*", len(wizard.apiKey)), "stored encrypted; Enter to skip")...)
	case classifierAddWizardStepConfirm:
		lines = append(lines, wizard.renderConfirmStep(innerWidth)...)
	case classifierAddWizardStepResult:
		lines = append(lines, wizard.renderResultStep(innerWidth)...)
	}
	lines = append(lines,
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
		kajicodeTheme.faint.Render(wizard.footer()),
	)
	block := styledBlockFillTitle(overlayWidth, "Add Classifier", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle())
	return centerRenderedBlock(block, width)
}

func (wizard *classifierAddWizardState) renderFieldLine(width int, label, value, placeholder string) []string {
	display := displayValue(strings.TrimSpace(value), placeholder)
	keyHint := "The key is stored in the encrypted credential store, never in config.json."
	if label != "API Key" {
		keyHint = "Enter to continue, Left to go back, Esc to cancel."
	}
	return []string{
		kajicodeTheme.accent.Render(label),
		fitStyledLine(kajicodeTheme.ink.Render("> "+display), width),
		kajicodeTheme.faint.Render(keyHint),
	}
}

func (wizard *classifierAddWizardState) renderConfirmStep(width int) []string {
	keyState := "none (env var or unauthenticated endpoint)"
	if strings.TrimSpace(wizard.apiKey) != "" {
		keyState = "provided (stored encrypted)"
	}
	fields := []string{
		"Name:      " + displayValue(strings.TrimSpace(wizard.name), "-"),
		"Endpoint:  " + displayValue(strings.TrimSpace(wizard.endpoint), "-"),
		"Model:     " + displayValue(strings.TrimSpace(wizard.model), "(none)"),
		"API key:   " + keyState,
	}
	lines := []string{kajicodeTheme.accent.Render("Confirm")}
	for _, field := range fields {
		lines = append(lines, fitStyledLine(kajicodeTheme.ink.Render(field), width))
	}
	lines = append(lines, kajicodeTheme.faint.Render("Enter to save, Left to go back, Esc to cancel."))
	return lines
}

func (wizard *classifierAddWizardState) renderResultStep(width int) []string {
	title := kajicodeTheme.red.Render("Classifier not saved")
	if wizard.resultOK {
		title = kajicodeTheme.accent.Render("Classifier saved")
	}
	lines := []string{title}
	for _, line := range strings.Split(strings.TrimSpace(wizard.resultText), "\n") {
		lines = append(lines, fitStyledLine(kajicodeTheme.ink.Render(line), width))
	}
	lines = append(lines, kajicodeTheme.faint.Render("Enable a feature in the classifier.features config block. Enter or Esc to close."))
	return lines
}

func (wizard *classifierAddWizardState) footer() string {
	parts := []string{"Enter next"}
	if wizard.canBack() {
		parts = append(parts, "Left back")
	}
	parts = append(parts, "Esc close")
	return strings.Join(parts, "   ")
}

func classifierAddWizardOverlayWidth(width int) int {
	if width <= 0 {
		width = defaultStartupWidth
	}
	overlayWidth := minInt(width, classifierAddWizardMaxWidth)
	if overlayWidth < classifierAddWizardMinWidth {
		overlayWidth = width
	}
	return overlayWidth
}

func classifierAddWizardStepLine(step classifierAddWizardStep) string {
	switch step {
	case classifierAddWizardStepName:
		return "Step 1/5 - Name"
	case classifierAddWizardStepEndpoint:
		return "Step 2/5 - Endpoint"
	case classifierAddWizardStepModel:
		return "Step 3/5 - Model"
	case classifierAddWizardStepKey:
		return "Step 4/5 - API Key"
	case classifierAddWizardStepConfirm:
		return "Step 5/5 - Confirm"
	default:
		return "Result"
	}
}
