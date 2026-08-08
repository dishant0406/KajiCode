package cli

import (
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// learningEngine builds the self-learning engine for a run, or returns nil so
// the agent loop stays byte-identical to a no-learning run. The engine is only
// constructed when the resolved config has auto-learning enabled AND a provider
// is available; otherwise it is inert.
//
// globalDir and localDir are the harness store roots for each scope. For
// headless exec, localDir is the per-session learning root
// (<sessionsRoot>/<sessionID>/learning); for the TUI it may be "" (session
// identity is negotiated inside the TUI run), in which case only the global
// store backs the engine — matching how the learn tool already resolves to
// harness.GlobalDir.
func learningEngine(cfg config.LearningConfig, provider kajicoderuntime.Provider, globalDir, localDir string) *agent.LearningEngine {
	if provider == nil || !cfg.IsEnabled() {
		return nil
	}
	global := harness.NewStore(harness.StoreOptions{Dir: globalDir, Scope: harness.ScopeGlobal})
	var local *harness.Store
	if localDir != "" {
		local = harness.NewStore(harness.StoreOptions{Dir: localDir, Scope: harness.ScopeLocal})
	}
	return agent.NewLearningEngine(cfg, provider, global, local)
}
