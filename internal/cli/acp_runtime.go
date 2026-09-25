package cli

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/acp"
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/agents"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/mcp"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// buildACPWorkspace builds the per-session workspace for ACP: the same toolkit +
// collaborators exec/TUI assemble (registry, sandbox, plugins, MCP, hooks, file
// tracker, sub-agents, deferred-tool loading), so an ACP session runs the
// identical feature set. It surfaces the trust skips (dropped project hooks/
// plugins/MCP for an untrusted workspace) exactly like exec, and the returned
// Close releases the MCP clients.
func buildACPWorkspace(workspaceRoot, sessionID string, resolved config.ResolvedConfig, deps appDeps, stderr io.Writer) (acp.Workspace, error) {
	scope, err := sandbox.NewScope(workspaceRoot, resolved.Sandbox.AdditionalWriteRoots)
	if err != nil {
		return acp.Workspace{}, err
	}
	engine, err := buildExecSandboxEngine(workspaceRoot, resolved, deps, scope)
	if err != nil {
		return acp.Workspace{}, err
	}
	registry := newCoreRegistryScoped(workspaceRoot, scope)
	registerLocalControlTools(registry, workspaceRoot, resolved.LocalControl)
	activation := activatePlugins(workspaceRoot, registry, deps, stderr, workspaceRoot)

	// MCP: KajiCode owns its server config, so connect the same servers exec does
	// (the editor's own servers stay declined). Autonomy is Low; the session's
	// permission mode governs tool approval.
	mcpRuntime := mcpToolRuntime(noopMCPRuntime{})
	var mcpSkip trustSkip
	if runtime, skip, err := registerMCPToolsForWorkspace(context.Background(), workspaceRoot, registry, deps, mcp.AutonomyLow, workspaceRoot); err == nil {
		mcpRuntime, mcpSkip = runtime, skip
	}

	// Deferred-tool loading + batch, in the same order exec/TUI register them
	// (after MCP/plugin tools so the eligible count matches).
	registerToolSearchIfEligible(registry, resolved.Tools.DeferThreshold, nil, nil)
	registerBatchTool(registry, nil, nil)

	hookDispatcher, hookSkip := newHookDispatcherWithExtra(workspaceRoot, activation.hooks, workspaceRoot)
	emitTrustNotice(stderr, hookSkip, activation.trustSkip, mcpSkip)

	fileTracker := tools.NewFileTracker()
	sessionStore := execSessionStoreFor(deps.newSessionStore(), sessionID)
	store := deps.newSessionStore()

	ws := acp.Workspace{
		Registry:        registry,
		Sandbox:         engine,
		Skills:          activation.skillInfos(deps.skillsDir(), workspaceRoot),
		Harness:         resolved.Harness,
		DeferThreshold:  resolved.Tools.DeferThreshold,
		Hooks:           hookDispatcher,
		FileTracker:     fileTracker,
		SessionStore:    sessionStore,
		MCPInstructions: mcpInstructionInfos(mcpRuntime),
	}

	// Sub-agents: register the Task tool and hydrate its child-run context. The
	// per-turn provider/model/window/mode/resolved config are supplied via Rebase.
	if depth := resolved.Agents.Depth; depth > 0 {
		if runtime, err := registerAgents(registry, workspaceRoot, depth); err == nil {
			ws.Agents = runtime.agentInfos()
			ws.TaskCompletions = runtime.completions()
			ws.Rebase = func(base agents.ChildRunContext, resolved config.ResolvedConfig) {
				base.Sandbox = engine
				base.FileTracker = fileTracker
				base.Hooks = hookDispatcher
				base.Cwd = workspaceRoot
				base.SessionStore = sessionStore
				base.Store = store
				base.ResolveModel = execModelResolver(resolved, deps)
				// Register captured the registry as the child ceiling; leave
				// base.Registry nil so setBase does not overwrite it.
				base.Registry = nil
				runtime.setBase(base)
			}
		}
	}

	ws.Close = func() { closeMCPRuntime(stderr, mcpRuntime) }
	return ws, nil
}

// acpBuildLearning builds the self-learning engine for an ACP session, matching
// exec: the project store backs repository-scoped lessons and a per-session
// store backs session-scoped ones. nil when learning is disabled in config. The
// engine is per-turn because it needs the active provider.
func acpBuildLearning(deps appDeps) func(string, config.ResolvedConfig, kajicoderuntime.Provider, string) *agent.LearningEngine {
	return func(workspaceRoot string, resolved config.ResolvedConfig, provider kajicoderuntime.Provider, sessionID string) *agent.LearningEngine {
		sessionRoot := ""
		if store := deps.newSessionStore(); store != nil && strings.TrimSpace(sessionID) != "" {
			sessionRoot = harness.SessionDir(filepath.Join(store.RootDir, sessionID))
		}
		return learningEngine(resolved.Learning, provider, harness.GlobalDir(nil), harness.ProjectDir(workspaceRoot), sessionRoot)
	}
}
