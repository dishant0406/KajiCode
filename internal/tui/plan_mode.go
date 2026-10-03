package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// planExecutionPrompt is the synthetic user turn submitted after the user
// approves a plan. It carries the model's own plan (recorded with todo_write)
// into a fresh, non-plan-mode run so execution starts without the user retyping.
const planExecutionPrompt = "The plan above was approved. Execute it now, following the recorded todo list step by step."

// planModeNotice is the transcript line shown when the user toggles plan mode.
func planModeNotice(on bool) string {
	if on {
		return "Plan mode ON — read-only. KajiCode will investigate and record a plan; writes and commands are disabled until you approve. Ctrl+G or /plan off to exit."
	}
	return "Plan mode OFF — KajiCode can edit and run commands again."
}

// togglePlanMode flips the read-only planning phase and reports the new state.
// The flag applies to the next run, so a toggle mid-run takes effect afterwards.
func (m model) togglePlanMode() (model, tea.Cmd) {
	m.planMode = !m.planMode
	m.transcript = reduceTranscript(m.transcript, transcriptAction{
		kind: actionAppendSystem,
		text: planModeNotice(m.planMode),
	})
	return m, nil
}

// setPlanMode applies an explicit on/off (the /plan on|off form) and reports the
// change; a no-op request still echoes the current state so /plan status works.
func (m model) setPlanMode(on bool, report bool) model {
	changed := m.planMode != on
	m.planMode = on
	if changed || report {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: planModeNotice(m.planMode),
		})
	}
	return m
}

// handlePlanCommand implements /plan [on|off|status]. Bare /plan toggles, matching
// the Ctrl+G chord.
func (m model) handlePlanCommand(arg string) (model, tea.Cmd) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "":
		return m.togglePlanMode()
	case "on":
		return m.setPlanMode(true, false), nil
	case "off":
		return m.setPlanMode(false, false), nil
	case "status":
		return m.setPlanMode(m.planMode, true), nil
	default:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Plan\nusage: /plan [on|off|status]",
		})
		return m, nil
	}
}
