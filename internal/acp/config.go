package acp

import (
	"strconv"

	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/execprofile"
)

// Value catalogs for the session config selectors. KajiCode exposes the knobs
// that change how a turn runs (model, permissions, reasoning effort, turn
// budget, response style) as ACP config options so an editor renders them as
// native dropdowns.

// validPermissionMode accepts only the profiles an editor may set. Both
// `unsafe` and `bypass-all` are excluded: each disables the sandbox and grants
// every tool without prompting, and an editor must not be able to grant itself
// unconfined host access over the wire.
func validPermissionMode(mode string) bool {
	switch agent.PermissionMode(mode) {
	case agent.PermissionModeAuto, agent.PermissionModeAsk,
		agent.PermissionModeAskAll, agent.PermissionModeReadOnly,
		agent.PermissionModeReadWrite:
		return true
	default:
		return false
	}
}

func permissionModeValues() []SessionConfigOptionValue {
	return []SessionConfigOptionValue{
		{Value: string(agent.PermissionModeAuto), Name: "Auto", Description: "Run safe tools automatically; ask before risky ones."},
		{Value: string(agent.PermissionModeAskAll), Name: "Ask", Description: "Ask before every tool that changes state."},
		{Value: string(agent.PermissionModeReadOnly), Name: "Read only", Description: "Allow reads; ask for writes, shell, and network."},
		{Value: string(agent.PermissionModeReadWrite), Name: "Read + write", Description: "Allow reads and file writes; ask for shell and network."},
	}
}

func effortValues() []SessionConfigOptionValue {
	return []SessionConfigOptionValue{
		{Value: "auto", Name: "Auto", Description: "Use the model's default effort."},
		{Value: "low", Name: "Low"},
		{Value: "medium", Name: "Medium"},
		{Value: "high", Name: "High"},
	}
}

func effortValue(current string) string {
	if current == "" {
		return "auto"
	}
	return current
}

// turnValues offers a small set of budgets plus the current value when it is
// not one of them, so the select always contains its current value.
func turnValues(current int) []SessionConfigOptionValue {
	presets := []int{15, 30, 60, 120, 250}
	values := make([]SessionConfigOptionValue, 0, len(presets)+1)
	seen := map[int]bool{}
	add := func(n int) {
		if n <= 0 || seen[n] {
			return
		}
		seen[n] = true
		values = append(values, SessionConfigOptionValue{Value: strconv.Itoa(n), Name: strconv.Itoa(n)})
	}
	add(current)
	for _, p := range presets {
		add(p)
	}
	return values
}

func styleValues() []SessionConfigOptionValue {
	return []SessionConfigOptionValue{
		{Value: "balanced", Name: "Balanced"},
		{Value: "concise", Name: "Concise"},
		{Value: "explanatory", Name: "Explanatory"},
		{Value: "review", Name: "Review"},
	}
}

func styleValue(current string) string {
	if current == "" {
		return "balanced"
	}
	return current
}

// selfCorrectValues lists the post-edit self-correction depths. off (the
// default) leaves the agent loop byte-identical; the others arm the verifier.
func selfCorrectValues() []SessionConfigOptionValue {
	return []SessionConfigOptionValue{
		{Value: "off", Name: "Off", Description: "No post-edit verification."},
		{Value: "on", Name: "On", Description: "Verify and correct after edits."},
		{Value: "tests", Name: "On (tests)", Description: "Also run the project test plan."},
		{Value: "full", Name: "Full", Description: "Tests + language diagnostics."},
	}
}

func selfCorrectValue(current string) string {
	if current == "" {
		return "off"
	}
	return current
}

func validSelfCorrect(depth string) bool {
	switch depth {
	case "off", "on", "tests", "full":
		return true
	default:
		return false
	}
}

// profileValues lists the execution profiles the loop can adopt.
func profileValues() []SessionConfigOptionValue {
	names := execprofile.Names()
	values := make([]SessionConfigOptionValue, 0, len(names)+1)
	values = append(values, SessionConfigOptionValue{Value: "", Name: "Default"})
	for _, name := range names {
		values = append(values, SessionConfigOptionValue{Value: name, Name: name})
	}
	return values
}

func profileValue(current string) string {
	return current
}

func validProfile(name string) bool {
	if name == "" {
		return true
	}
	_, ok := execprofile.Lookup(name)
	return ok
}
