package cli

import (
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/config"
)

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

// toolResultGateForRun builds the live tool-result relevance gate, or nil when
// the capability or the feature is off. It reuses the same classifier the
// compaction judge uses (classifier.New is cheap and stateless), so a nil return
// is the signal to the agent loop that the tool path is unchanged. Returning a
// gate only when the feature is enabled means connecting a classifier still
// changes no behavior.
func toolResultGateForRun(cfg classifier.Config, deps appDeps) *agent.ToolResultGate {
	if !cfg.Enabled || !cfg.Features.ToolResult.Enabled {
		return nil
	}
	client, err := classifier.New(cfg, classifierKeyResolver(deps))
	if err != nil {
		return nil
	}
	feature := cfg.Features.ToolResult
	return &agent.ToolResultGate{
		Classifier:        client,
		KeepThreshold:     feature.EffectiveKeepThreshold(),
		DropThreshold:     feature.EffectiveDropThreshold(),
		MinPruneRatio:     feature.EffectiveMinPruneRatio(),
		MaxHiddenRatio:    feature.EffectiveMaxHiddenRatio(),
		MinBytes:          feature.EffectiveMinBytes(),
		CoverageThreshold: feature.EffectiveCoverageThreshold(),
		Requery:           feature.Requery,
		MaxRequery:        feature.EffectiveMaxRequery(),
		Shadow:            feature.ShadowMode,
	}
}

// acpBuildToolResultGate adapts toolResultGateForRun for the ACP surface, which
// builds its options per turn from the resolved config.
func acpBuildToolResultGate(deps appDeps) func(config.ResolvedConfig) *agent.ToolResultGate {
	return func(resolved config.ResolvedConfig) *agent.ToolResultGate {
		return toolResultGateForRun(resolved.Classifier, deps)
	}
}
