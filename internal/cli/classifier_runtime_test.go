package cli

import (
	"testing"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

func connectedClassifierConfig() classifier.Config {
	return classifier.Config{
		Enabled:  true,
		Active:   "jev",
		Profiles: []classifier.Profile{{Name: "jev", Endpoint: "http://127.0.0.1:1/classify", APIKey: "test-key"}},
	}
}

func TestConnectedClassifierNeedsAnEnabledProfile(t *testing.T) {
	if connectedClassifier(classifier.Config{}, appDeps{}) != nil {
		t.Fatal("no classifier config should mean no classifier")
	}
	disabled := connectedClassifierConfig()
	disabled.Enabled = false
	if connectedClassifier(disabled, appDeps{}) != nil {
		t.Fatal("a disabled classifier must not be used")
	}
	if connectedClassifier(classifier.Config{Enabled: true}, appDeps{}) != nil {
		t.Fatal("enabled with no profile should mean no classifier")
	}
	if connectedClassifier(connectedClassifierConfig(), appDeps{}) == nil {
		t.Fatal("a connected classifier should be returned with no feature switch")
	}
}

// The compaction judge and tool gate keep their own feature switches; only
// memory search uses the classifier as soon as it is connected.
func TestFeatureBuildersStillNeedTheirSwitch(t *testing.T) {
	cfg := connectedClassifierConfig()
	if classifierForRun(cfg, appDeps{}) != nil || toolResultGateForRun(cfg, appDeps{}) != nil {
		t.Fatal("compaction judge and tool gate must stay off until their feature is enabled")
	}
	cfg.Features.Compaction.Enabled = true
	cfg.Features.ToolResult.Enabled = true
	if classifierForRun(cfg, appDeps{}) == nil || toolResultGateForRun(cfg, appDeps{}) == nil {
		t.Fatal("enabled features should get the classifier")
	}
}

func TestRegisterRecallFollowsTheClassifier(t *testing.T) {
	workspace := t.TempDir()
	registry := newCoreRegistry(workspace)
	keyword, _ := registry.Get("recall")

	registerRecall(registry, workspace, connectedClassifier(connectedClassifierConfig(), appDeps{}))
	withClassifier, _ := registry.Get("recall")
	if withClassifier == keyword {
		t.Fatal("a connected classifier should replace the recall tool")
	}
	registerRecall(registry, workspace, nil)
	if back, _ := registry.Get("recall"); back == withClassifier {
		t.Fatal("disconnecting should swap recall back to keyword search")
	}
}
