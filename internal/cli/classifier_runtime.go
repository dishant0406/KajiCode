package cli

import "github.com/dishant0406/KajiCode/internal/classifier"

import "github.com/dishant0406/KajiCode/internal/config"

// classifierForRun builds the fast classifier used by the compaction judge for a
// headless run, or nil when the capability or the compaction feature is off. A
// nil return is the signal to the agent loop that its behavior is unchanged.
func classifierForRun(cfg classifier.Config, deps appDeps) classifier.Classifier {
	if !cfg.Enabled || !cfg.Features.Compaction.Enabled {
		return nil
	}
	client, err := classifier.New(cfg, classifierKeyResolver(deps))
	if err != nil {
		return nil
	}
	return client
}

// acpBuildCompactionJudge adapts classifierForRun for the ACP surface, which
// builds its options per turn from the resolved config.
func acpBuildCompactionJudge(deps appDeps) func(config.ResolvedConfig) classifier.Classifier {
	return func(resolved config.ResolvedConfig) classifier.Classifier {
		return classifierForRun(resolved.Classifier, deps)
	}
}
