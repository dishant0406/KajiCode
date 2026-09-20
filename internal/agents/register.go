package agents

import (
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// Register wires the agent tools (Task, TaskOutput, TaskStop, GenerateAgent)
// into a run's registry and returns the supervisor backing them. The base
// context supplies the parent's runtime handles; base.Registry is set to the
// registry being registered into so child runs filter from the exact tool set
// the parent has.
func Register(registry *tools.Registry, runner *Runner, base ChildRunContext) *Supervisor {
	base.Registry = registry
	supervisor := NewSupervisor(runner, base)
	registry.Register(NewTaskTool(supervisor))
	registry.Register(NewOutputTool(supervisor))
	registry.Register(NewStopTool(supervisor))
	if runner != nil && runner.Paths.ProjectDir != "" {
		registry.Register(NewGenerateTool(runner.Paths.ProjectDir))
	}
	return supervisor
}

// Infos summarizes the delegatable agents for the orchestrator's system prompt.
// It returns plain agent-package data so the agent package needs no import of
// this package.
func Infos(supervisor *Supervisor) []agent.AgentInfo {
	if supervisor == nil {
		return nil
	}
	list := supervisor.List()
	infos := make([]agent.AgentInfo, 0, len(list))
	for _, item := range list {
		infos = append(infos, agent.AgentInfo{Name: item.Name, WhenToUse: item.Description})
	}
	return infos
}
