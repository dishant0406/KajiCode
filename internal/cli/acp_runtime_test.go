package cli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/hooks"
	"github.com/dishant0406/KajiCode/internal/plugins"
	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/sessions"
)

// acpRuntimeDeps builds the minimal appDeps buildACPWorkspace touches: an empty
// scoped core registry, a grant store in a temp dir, a fail-open plugin/hook
// loader, and a skills dir that never touches the host. It deliberately leaves
// Agents.Depth unset so the test exercises the real default-config gate.
func acpRuntimeDeps(t *testing.T, configRoot string) appDeps {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return appDeps{
		getwd: func() (string, error) { return t.TempDir(), nil },
		loadPlugins: func(plugins.LoadOptions) (plugins.LoadResult, error) {
			return plugins.LoadResult{}, nil
		},
		loadHooks: func(hooks.LoadOptions) (hooks.LoadResult, error) { return hooks.LoadResult{}, nil },
		skillsDir: func() string { return t.TempDir() },
		// A zero Backend keeps the sandbox engine construction offline and
		// deterministic (no PATH scan for a native sandbox executable).
		selectSandboxBackend: func(sandbox.BackendOptions) sandbox.Backend { return sandbox.Backend{} },
		// No MCP servers configured: the workspace gets no MCP runtime.
		resolveMCPConfig: func(string, bool) (config.MCPConfig, error) {
			return config.MCPConfig{}, nil
		},
		newSessionStore: func() *sessions.Store {
			return sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
		},
		newSandboxStore: func() (*sandbox.GrantStore, error) {
			return sandbox.NewGrantStore(sandbox.StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
		},
	}
}

// TestBuildACPWorkspaceRegistersAgentToolsByDefault pins the fix for "ACP cannot
// use sub-agents": buildACPWorkspace historically gated registerAgents on
// resolved.Agents.Depth > 0, but depth 0 means "use the built-in default", so a
// default config silently dropped Task/TaskOutput/TaskStop/GenerateAgent. This
// test drives the REAL buildACPWorkspace gate (not a stubbed
// acp.Deps.BuildWorkspace) with the default zero depth and asserts the agent
// tools are present.
func TestBuildACPWorkspaceRegistersAgentToolsByDefault(t *testing.T) {
	deps := acpRuntimeDeps(t, t.TempDir())
	resolved := config.ResolvedConfig{
		ActiveProvider: "echo",
		Provider: config.ProviderProfile{
			Name:         "echo",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "http://127.0.0.1/v1",
			Model:        "echo-model",
		},
	}

	var stderr bytes.Buffer
	ws, err := buildACPWorkspace(t.TempDir(), "sess_under_test", resolved, deps, &stderr)
	if err != nil {
		t.Fatalf("buildACPWorkspace: %v", err)
	}
	defer ws.Close()

	for _, name := range []string{"Task", "TaskOutput", "TaskStop", "GenerateAgent"} {
		if _, ok := ws.Registry.Get(name); !ok {
			t.Fatalf("default-config ACP workspace is missing %q; stderr=%q", name, stderr.String())
		}
	}
	if len(ws.Agents) == 0 {
		t.Fatalf("Agents infos empty, want the builtin roster; stderr=%q", stderr.String())
	}
	if ws.TaskCompletions == nil {
		t.Fatal("TaskCompletions not wired")
	}
	if ws.Rebase == nil {
		t.Fatal("Rebase not wired")
	}
}
