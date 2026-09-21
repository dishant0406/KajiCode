package agents

import "github.com/dishant0406/KajiCode/internal/tools"

// FilterRegistry returns a new registry containing only the tools the agent's
// ruleset explicitly allows AND that already exist in the parent registry. The
// base registry is the ceiling: because a child is filtered from the same
// registry its parent runs with, a child can never gain a tool the parent did
// not have. Filtering is permission-based rather than a hardcoded tool-name
// allowlist, so MCP tools ("mcp_<server>_<tool>"), plugin tools, and skill tools
// survive exactly when the ruleset allows them.
func FilterRegistry(base *tools.Registry, rules Ruleset) *tools.Registry {
	filtered := tools.NewRegistry()
	if base == nil {
		return filtered
	}
	for _, tool := range base.All() {
		if Evaluate(tool.Name(), rules) == ActionAllow {
			filtered.Register(tool)
		}
	}
	return filtered
}

// delegationIsReadOnly reports whether an agent restricted to rules can only
// read the workspace: every tool rules allows is a pure read-only effect in the
// parent registry. It is the safety gate the agent loop's parallel batcher
// consults before running several delegations at once, so it fails closed — an
// unknown, mutating, interactive, or process-reading tool in the allowed set
// makes the delegation sequential.
func delegationIsReadOnly(registry *tools.Registry, rules Ruleset) bool {
	if registry == nil {
		return false
	}
	for _, tool := range registry.All() {
		if Evaluate(tool.Name(), rules) != ActionAllow {
			continue
		}
		if tools.CapabilitiesOf(tool).Effect != tools.EffectReadOnly {
			return false
		}
	}
	return true
}

// Spawnable returns the agents a Task caller may invoke: subagent/all mode and
// not hidden. Primary-mode agents are reserved for top-level selection and
// never appear as delegation targets.
func Spawnable(result LoadResult) []Agent {
	agents := []Agent{}
	for _, agent := range result.Agents {
		if agent.Hidden {
			continue
		}
		if NormalizeMode(agent.Mode) == ModePrimary {
			continue
		}
		agents = append(agents, agent)
	}
	return agents
}
