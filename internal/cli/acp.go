package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dishant0406/KajiCode/internal/acp"
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
)

const acpUsage = `kajicode acp — serve the Agent Client Protocol (ACP) over stdio

Editors that speak ACP (Zed, JetBrains, Neovim, ...) spawn this command and drive
KAJICODE as a backend over JSON-RPC 2.0 on stdin/stdout. KAJICODE keeps your provider,
model, and API keys (BYOK); the editor only hosts the conversation thread.

Usage:
  kajicode acp

Not meant to be run interactively — point your editor's ACP / external-agent
setting at "kajicode acp".`

// runACP serves ACP over stdio so an editor can drive KAJICODE's agent core. It
// speaks JSON-RPC 2.0 (newline-delimited JSON) on stdin/stdout; stderr stays free
// for human-readable diagnostics. The session lifecycle maps onto KAJICODE's own
// session store, and provider/model/keys remain owned by KAJICODE.
func runACP(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			fmt.Fprintln(stdout, acpUsage)
			return exitSuccess
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown acp flag %q", arg))
		}
	}

	conn := acp.NewConn(deps.stdin, stdout)
	acp.NewAgent(conn, acp.Deps{
		ResolveConfig: deps.resolveConfig,
		// deps.newProvider is wrapped in fillAppDeps to apply the stored API key,
		// so ACP is authenticated for apiKeyStored profiles like every other
		// surface — no ACP-specific credential handling needed.
		NewProvider: deps.newProvider,
		RunAgent:    agent.Run,
		// Build the SCOPED registry + sandbox engine per workspace, exactly like the
		// exec surface, so ACP shell/file tools are confined — never run unconfined.
		// Plugin activation overlays the multi-root skill tool and its roots, and the
		// same roots render the skill catalog, so ACP's advertised skills match what
		// the skill tool can resolve.
		BuildWorkspace: func(workspaceRoot, sessionID string, resolved config.ResolvedConfig) (acp.Workspace, error) {
			return buildACPWorkspace(workspaceRoot, sessionID, resolved, deps, stderr)
		},
		BuildLearning:        acpBuildLearning(deps),
		ResolveWorkspaceRoot: acpWorkspaceRootResolver(deps),
		Store:                deps.newSessionStore(),
		AgentInfo:            acp.Implementation{Name: "kajicode", Version: version},
		Commands:             acpCommands(deps),
		RunCommand:           acpRunCommand(deps, acpSessionCommand(deps)),
		Retitle:              acpRetitle(deps),
		AuthMethods:          acpAuthMethods,
		Authenticate:         acpAuthenticate(),
		Logout:               acpLogout(deps),
		ProviderAdd:          acpProviderAdd(deps),
		DiscoverModels:       acpDiscoverModels(deps),
		Providers:            acpUsableProviders(deps),
		ResolveContextWindow: acpResolveContextWindow(),
		SavePromptSnippet:    acpSavePromptSnippet(deps),
		ExpandPromptSnippet:  acpExpandPromptSnippet(deps),
		ExpandSkill:          acpExpandSkill(deps),
		SuggestNes:           acpSuggestNes(deps),
		ListProviders:        acpListProviders(deps),
		SetProvider:          acpSetProvider(deps),
		DisableProvider:      acpDisableProvider(deps),
	})

	ctx, stop := signalContext()
	defer stop()
	if err := conn.Serve(ctx); err != nil && ctx.Err() == nil {
		return writeAppError(stderr, "acp: "+err.Error(), exitCrash)
	}
	return exitSuccess
}

// acpWorkspaceRootResolver validates a client-supplied cwd into a confinement
// root. It reuses exec's resolveWorkspaceRoot (abs+clean, must be an existing
// dir) and additionally rejects the filesystem root and the home directory — an
// editor must not be able to point KAJICODE's file/shell tools at the whole disk.
func acpWorkspaceRootResolver(deps appDeps) func(string) (string, error) {
	return func(cwd string) (string, error) {
		root, err := resolveWorkspaceRoot(cwd, deps)
		if err != nil {
			return "", err
		}
		if root == filepath.Dir(root) {
			return "", fmt.Errorf("cwd must not be the filesystem root: %s", root)
		}
		if home, herr := os.UserHomeDir(); herr == nil && home != "" && filepath.Clean(home) == root {
			return "", fmt.Errorf("cwd must not be the home directory: %s", root)
		}
		return root, nil
	}
}
