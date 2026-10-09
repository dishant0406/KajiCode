package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func (m model) classifierFormOverlay(width int) string {
	if m.classifierForm == nil {
		return ""
	}
	return m.classifierForm.render(width)
}

func (form *classifierFormState) render(width int) string {
	if form == nil {
		return ""
	}
	overlayWidth := classifierFormOverlayWidth(width)
	innerWidth := maxInt(20, overlayWidth-4)
	lines := []string{
		kajicodeTheme.faint.Render(classifierFormStepLine(form.step)),
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
	}
	if form.err != "" {
		lines = append(lines, kajicodeTheme.red.Render("error: "+form.err), "")
	}
	lines = append(lines, form.renderStep(innerWidth)...)
	lines = append(lines,
		kajicodeTheme.line.Render(strings.Repeat("-", innerWidth)),
		kajicodeTheme.faint.Render(form.footer()),
	)
	block := styledBlockFillTitle(overlayWidth, "Configure Classifier", lines, kajicodeTheme.lineStrong, lipgloss.NewStyle())
	return centerRenderedBlock(block, width)
}

func (form *classifierFormState) renderStep(width int) []string {
	switch form.step {
	case classifierFormStepName:
		return form.fieldLine(width, "Classifier Name", form.name, "je v (a stable name)")
	case classifierFormStepEndpoint:
		return form.fieldLine(width, "Endpoint", form.endpoint, "https://openrouter.ai/api/v1/systemone")
	case classifierFormStepModel:
		return form.fieldLine(width, "Model", form.model, "jev-1.13 (optional)")
	case classifierFormStepAuthHeader:
		return form.fieldLine(width, "Auth Header", form.authHeader, "Authorization (optional)")
	case classifierFormStepAuthScheme:
		return form.fieldLine(width, "Auth Scheme", form.authScheme, "Bearer (optional; \"raw\" sends the key bare)")
	case classifierFormStepHeader:
		return form.fieldLine(width, "Extra Header", form.header, "X-Router-Tier=pro (optional)")
	case classifierFormStepTimeout:
		return form.fieldLine(width, "Timeout (ms)", form.timeout, "8000 (optional)")
	case classifierFormStepKey:
		return form.fieldLine(width, "API Key", strings.Repeat("*", len(form.apiKey)), form.keyPlaceholder())
	case classifierFormStepCapability:
		return form.boolLine(width, "Enable Classifier", form.capability, "memory search uses it as soon as it is on; the features below also need it")
	case classifierFormStepCompaction:
		return form.boolLine(width, "Compaction Judge", form.compaction, "keep/drop stale tool bodies before summarizing")
	case classifierFormStepCompactionKeep:
		return form.fieldLine(width, "Compaction Keep", form.compactionKeep, "0.35 (results at or above this stay verbatim)")
	case classifierFormStepToolResult:
		return form.boolLine(width, "Tool-Result Gate", form.toolResult, "hide irrelevant blocks of a large tool result")
	case classifierFormStepToolDrop:
		return form.fieldLine(width, "Gate Drop", form.toolDrop, "0.2 (below this a block is hidden)")
	case classifierFormStepToolKeep:
		return form.fieldLine(width, "Gate Keep", form.toolKeep, "0.5 (at or above this a block is kept)")
	case classifierFormStepToolShadow:
		return form.boolLine(width, "Gate Shadow Mode", form.toolShadow, "record decisions without rewriting (safe first run)")
	case classifierFormStepConfirm:
		return form.renderConfirmStep(width)
	case classifierFormStepResult:
		return form.renderResultStep(width)
	}
	return nil
}

func (form *classifierFormState) fieldLine(width int, label, value, placeholder string) []string {
	display := displayValue(strings.TrimSpace(value), placeholder)
	hint := "Enter to continue, Left to go back, Esc to cancel. Blank keeps the stored value."
	if label == "API Key" {
		hint = "Stored in the encrypted credential store, never in config.json. Enter to skip."
	}
	return []string{
		kajicodeTheme.accent.Render(label),
		fitStyledLine(kajicodeTheme.ink.Render("> "+display), width),
		kajicodeTheme.faint.Render(hint),
	}
}

func (form *classifierFormState) boolLine(width int, label string, value bool, hint string) []string {
	state := "off"
	if value {
		state = "on"
	}
	return []string{
		kajicodeTheme.accent.Render(label),
		fitStyledLine(kajicodeTheme.ink.Render("> "+state), width),
		kajicodeTheme.faint.Render("Up/Down or y/n toggles it. " + hint),
	}
}

func (form *classifierFormState) keyPlaceholder() string {
	if form.keyStored {
		return "already stored (Enter to keep)"
	}
	return "optional; Enter to skip"
}

func (form *classifierFormState) renderConfirmStep(width int) []string {
	keyState := "unchanged"
	if strings.TrimSpace(form.apiKey) != "" {
		keyState = "provided (stored encrypted)"
	}
	fields := []string{
		"Name:        " + displayValue(strings.TrimSpace(form.name), "-"),
		"Endpoint:    " + displayValue(strings.TrimSpace(form.endpoint), "-"),
		"Model:       " + displayValue(strings.TrimSpace(form.model), "(unchanged)"),
		"Auth header: " + displayValue(strings.TrimSpace(form.authHeader), "(default)"),
		"Auth scheme: " + displayValue(strings.TrimSpace(form.authScheme), "(default)"),
		"Header:      " + displayValue(strings.TrimSpace(form.header), "(none)"),
		"Timeout:     " + displayValue(strings.TrimSpace(form.timeout), "(default)"),
		"API key:     " + keyState,
		"Classifier:  " + onOffLabel(form.capability) + " (memory search follows this)",
		"Compaction:  " + onOffLabel(form.compaction) + " (keep " + displayValue(strings.TrimSpace(form.compactionKeep), "default") + ")",
		"Tool gate:   " + onOffLabel(form.toolResult) + " (drop " + displayValue(strings.TrimSpace(form.toolDrop), "default") +
			", keep " + displayValue(strings.TrimSpace(form.toolKeep), "default") + ", shadow " + onOffLabel(form.toolShadow) + ")",
	}
	lines := []string{kajicodeTheme.accent.Render("Confirm")}
	for _, field := range fields {
		lines = append(lines, fitStyledLine(kajicodeTheme.ink.Render(field), width))
	}
	lines = append(lines, kajicodeTheme.faint.Render("Enter to save, Left to go back, Esc to cancel."))
	return lines
}

func (form *classifierFormState) renderResultStep(width int) []string {
	title := kajicodeTheme.red.Render("Classifier not saved")
	if form.resultOK {
		title = kajicodeTheme.accent.Render("Classifier saved")
	}
	lines := []string{title}
	for _, line := range strings.Split(strings.TrimSpace(form.resultText), "\n") {
		lines = append(lines, fitStyledLine(kajicodeTheme.ink.Render(line), width))
	}
	lines = append(lines, kajicodeTheme.faint.Render("Enter or Esc to close."))
	return lines
}

func (form *classifierFormState) footer() string {
	parts := []string{"Enter next"}
	if form.canBack() {
		parts = append(parts, "Left back")
	}
	parts = append(parts, "Esc close")
	return strings.Join(parts, "   ")
}

func onOffLabel(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func classifierFormOverlayWidth(width int) int {
	if width <= 0 {
		width = defaultStartupWidth
	}
	overlayWidth := minInt(width, classifierFormMaxWidth)
	if overlayWidth < classifierFormMinWidth {
		overlayWidth = width
	}
	return overlayWidth
}

func classifierFormStepLine(step classifierFormStep) string {
	switch step {
	case classifierFormStepName:
		return "Step 1/16 - Name"
	case classifierFormStepEndpoint:
		return "Step 2/16 - Endpoint"
	case classifierFormStepModel:
		return "Step 3/16 - Model"
	case classifierFormStepAuthHeader:
		return "Step 4/16 - Auth header"
	case classifierFormStepAuthScheme:
		return "Step 5/16 - Auth scheme"
	case classifierFormStepHeader:
		return "Step 6/16 - Extra header"
	case classifierFormStepTimeout:
		return "Step 7/16 - Timeout"
	case classifierFormStepKey:
		return "Step 8/16 - API key"
	case classifierFormStepCapability:
		return "Step 9/16 - Enable classifier"
	case classifierFormStepCompaction:
		return "Step 10/16 - Compaction feature"
	case classifierFormStepCompactionKeep:
		return "Step 11/16 - Compaction keep threshold"
	case classifierFormStepToolResult:
		return "Step 12/16 - Tool-result gate"
	case classifierFormStepToolDrop:
		return "Step 13/16 - Gate drop threshold"
	case classifierFormStepToolKeep:
		return "Step 14/16 - Gate keep threshold"
	case classifierFormStepToolShadow:
		return "Step 15/16 - Gate shadow mode"
	case classifierFormStepConfirm:
		return "Step 16/16 - Confirm"
	default:
		return "Result"
	}
}
