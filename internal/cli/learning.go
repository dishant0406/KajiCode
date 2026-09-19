package cli

import (
	"strings"

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
// globalDir is the cross-project store, projectDir the per-repository store
// (<workspace>/.kajicode/learning), and sessionDir the per-session root
// (<sessionsRoot>/<sessionID>/learning), which may be empty for the TUI where
// session identity is negotiated inside the run.
func learningEngine(cfg config.LearningConfig, provider kajicoderuntime.Provider, globalDir, projectDir, sessionDir string) *agent.LearningEngine {
	if provider == nil || !cfg.IsEnabled() {
		return nil
	}
	global := harness.NewStore(harness.StoreOptions{Dir: globalDir, Scope: harness.ScopeGlobal})
	project := harness.NewStore(harness.StoreOptions{Dir: projectDir, Scope: harness.ScopeProject})
	var session *harness.Store
	if strings.TrimSpace(sessionDir) != "" {
		session = harness.NewStore(harness.StoreOptions{Dir: sessionDir, Scope: harness.ScopeSession})
	}
	return agent.NewLearningEngine(cfg, provider, global, project, session)
}
