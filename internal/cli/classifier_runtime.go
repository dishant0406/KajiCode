package cli

import (
	"github.com/dishant0406/KajiCode/internal/agent"
	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// connectedClassifier returns the classifier when one is connected (enabled
// with a usable profile), or nil. Memory search uses it whenever it is
// connected; the compaction judge and tool gate also need their own feature on.
func connectedClassifier(cfg classifier.Config, deps appDeps) classifier.Classifier {
	if !cfg.Enabled {
		return nil
	}
	client, err := classifier.New(cfg, classifierKeyResolver(deps))
	if err != nil {
		return nil
	}
	return client
}

// registerRecall (re)registers the recall tool so it searches with memory, the
// connected classifier, or with keywords when memory is nil. Registering
// replaces the tool by name, so ACP can call it every turn to follow a
// classifier that was switched on or off mid-session.
func registerRecall(registry *tools.Registry, workspaceRoot string, memory classifier.Classifier) {
	registry.Register(tools.NewRecallTool(harness.ProjectDir(workspaceRoot), harness.GlobalDir(nil), memory))
}

// acpRegisterRecall adapts registerRecall for the ACP surface, which resolves
// config per turn.
func acpRegisterRecall(deps appDeps) func(*tools.Registry, string, config.ResolvedConfig) {
	return func(registry *tools.Registry, workspaceRoot string, resolved config.ResolvedConfig) {
		registerRecall(registry, workspaceRoot, connectedClassifier(resolved.Classifier, deps))
	}
}

// classifierForRun builds the fast classifier used by the compaction judge for a
// headless run, or nil when the capability or the compaction feature is off. A
// nil return is the signal to the agent loop that its behavior is unchanged.
func classifierForRun(cfg classifier.Config, deps appDeps) classifier.Classifier {
	if !cfg.Features.Compaction.Enabled {
		return nil
	}
	return connectedClassifier(cfg, deps)
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
// gate only when the feature is enabled means connecting a classifier alone
// leaves the tool path unchanged.
func toolResultGateForRun(cfg classifier.Config, deps appDeps) *agent.ToolResultGate {
	if !cfg.Features.ToolResult.Enabled {
		return nil
	}
	client := connectedClassifier(cfg, deps)
	if client == nil {
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
