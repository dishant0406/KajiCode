package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

// TestClassifierFormPrefillsFromConfig guards that the /classifier form shows the
// active profile and current feature switches rather than starting blank.
func TestClassifierFormPrefillsFromConfig(t *testing.T) {
	cfg := classifier.Config{
		Enabled: true,
		Active:  "jev",
		Profiles: []classifier.Profile{{
			Name:         "jev",
			Endpoint:     "https://openrouter.ai/api/v1/systemone",
			Model:        "jev-1.13",
			APIKeyStored: true,
			Headers:      map[string]string{"X-Tier": "pro"},
			TimeoutMS:    15000,
		}},
		Features: classifier.Features{
			Compaction: classifier.CompactionFeature{Enabled: true, KeepResultThreshold: 0.4},
			ToolResult: classifier.ToolResultFeature{Enabled: true, DropThreshold: 0.15, KeepThreshold: 0.55, ShadowMode: true},
		},
	}
	form := newClassifierForm(cfg)
	if form.name != "jev" || form.endpoint != "https://openrouter.ai/api/v1/systemone" || form.model != "jev-1.13" {
		t.Fatalf("profile not prefilled: %+v", form)
	}
	if form.header != "X-Tier=pro" || form.timeout != "15000" {
		t.Fatalf("header/timeout not prefilled: %+v", form)
	}
	if !form.capability || !form.compaction || !form.toolResult || !form.toolShadow {
		t.Fatalf("feature switches not prefilled: %+v", form)
	}
	if form.compactionKeep != "0.4" || form.toolDrop != "0.15" || form.toolKeep != "0.55" {
		t.Fatalf("thresholds not prefilled: %+v", form)
	}
}

// TestClassifierFormCommandArgsBuildsConfigure guards the CLI arguments the form
// produces: explicit bools, omitted blanks, and a key on stdin.
func TestClassifierFormCommandArgsBuildsConfigure(t *testing.T) {
	form := &classifierFormState{
		name: "jev", endpoint: "https://x.test/v1", model: "jev-1.13",
		capability: true, compaction: true, compactionKeep: "0.35",
		toolResult: true, toolDrop: "0.2", toolKeep: "0.5", toolShadow: true,
		apiKey: "sk-secret",
	}
	args, stdin := form.commandArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{"configure jev", "--endpoint https://x.test/v1", "--enable=true", "--compaction=true",
		"--tool-result=true", "--tool-shadow=true", "--compaction-keep 0.35", "--api-key-stdin"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
	if stdin != "sk-secret" {
		t.Fatalf("key not routed to stdin: %q", stdin)
	}
}

// TestClassifierFormBlankFieldsAreOmitted guards that leaving a text field blank
// leaves the stored value alone rather than clearing it.
func TestClassifierFormBlankFieldsAreOmitted(t *testing.T) {
	form := &classifierFormState{name: "jev", endpoint: "https://x.test/v1", capability: false}
	args, stdin := form.commandArgs()
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--model") || strings.Contains(joined, "--header") || strings.Contains(joined, "--timeout") {
		t.Fatalf("blank fields must be omitted: %q", joined)
	}
	if stdin != "" {
		t.Fatalf("no key expected on stdin: %q", stdin)
	}
	if !strings.Contains(joined, "--enable=false") {
		t.Fatalf("capability must be sent explicitly: %q", joined)
	}
}

// TestClassifierFormAdvanceValidates guards the per-step validation so a bad
// endpoint or threshold is rejected before the CLI is called.
func TestClassifierFormAdvanceValidates(t *testing.T) {
	form := &classifierFormState{step: classifierFormStepEndpoint, name: "jev", endpoint: "not-a-url"}
	if err := form.advance(); err == "" {
		t.Fatal("expected an invalid-endpoint error")
	}
	for _, bad := range []string{"9", "NaN", "-0.1"} {
		if form := (&classifierFormState{step: classifierFormStepToolDrop, toolDrop: bad}); form.advance() == "" {
			t.Fatalf("expected %q to be rejected as a drop threshold", bad)
		}
		if form := (&classifierFormState{step: classifierFormStepCompactionKeep, compactionKeep: bad}); form.advance() == "" {
			t.Fatalf("expected %q to be rejected as a compaction keep threshold", bad)
		}
	}
	form = &classifierFormState{step: classifierFormStepName, name: "ok"}
	if err := form.advance(); err != "" {
		t.Fatalf("unexpected error: %s", err)
	}
	if form.step != classifierFormStepEndpoint {
		t.Fatalf("did not advance: %v", form.step)
	}
}

// TestClassifierFormKeyFlow drives handleClassifierFormKey through the whole form
// so the dispatcher, Esc/Left/Up/Enter handling, and the confirm→save path are
// covered rather than only the pure state helpers.
func TestClassifierFormKeyFlow(t *testing.T) {
	m := model{classifierForm: &classifierFormState{step: classifierFormStepName, name: "jev"}}
	var runnerArgs []string
	m.classifierCommand = func(_ context.Context, args []string, _ string) ClassifierCommandResult {
		runnerArgs = args
		return ClassifierCommandResult{ExitCode: 0, Output: "ok"}
	}

	// Esc closes the form.
	m, _ = m.handleClassifierFormKey(testKey(tea.KeyEsc))
	if m.classifierForm != nil {
		t.Fatal("Esc must close the form")
	}

	// Walk to the confirm step with Enter/Right/Tab, then Left goes back.
	m.classifierForm = &classifierFormState{step: classifierFormStepEndpoint, name: "jev", endpoint: "https://x.test/v1"}
	m, _ = m.handleClassifierFormKey(testKey(tea.KeyRight))
	if m.classifierForm.step != classifierFormStepModel {
		t.Fatalf("Right must advance a text step, got %v", m.classifierForm.step)
	}
	m, _ = m.handleClassifierFormKey(testKey(tea.KeyLeft))
	if m.classifierForm.step != classifierFormStepEndpoint {
		t.Fatalf("Left must go back a step, got %v", m.classifierForm.step)
	}
	// Up toggles a yes/no step.
	m.classifierForm.step = classifierFormStepCapability
	m.classifierForm.capability = false
	m, _ = m.handleClassifierFormKey(testKey(tea.KeyUp))
	if !m.classifierForm.capability {
		t.Fatal("Up must toggle the capability switch")
	}

	// Enter on the confirm step saves by invoking the CLI bridge.
	m.classifierForm.step = classifierFormStepConfirm
	m, cmd := m.handleClassifierFormKey(testKey(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Enter on confirm must start the save command")
	}
	msg := cmd()
	if result, ok := msg.(classifierCommandResultMsg); ok {
		m = m.applyClassifierCommandResultMessage(result)
	}
	if m.classifierForm == nil || m.classifierForm.step != classifierFormStepResult {
		t.Fatalf("save did not reach the result step: %+v", m.classifierForm)
	}
	if len(runnerArgs) == 0 || runnerArgs[0] != "configure" {
		t.Fatalf("save must call `classifier configure`, got %v", runnerArgs)
	}
}
