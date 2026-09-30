package tui

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

const (
	classifierAddWizardMinWidth = 58
	classifierAddWizardMaxWidth = 92
)

type classifierAddWizardStep int

const (
	classifierAddWizardStepName classifierAddWizardStep = iota
	classifierAddWizardStepEndpoint
	classifierAddWizardStepModel
	classifierAddWizardStepKey
	classifierAddWizardStepConfirm
	classifierAddWizardStepResult
)

type classifierAddWizardState struct {
	step       classifierAddWizardStep
	name       string
	endpoint   string
	model      string
	apiKey     string
	err        string
	resultText string
	resultOK   bool
}

func newClassifierAddWizard() *classifierAddWizardState {
	return &classifierAddWizardState{step: classifierAddWizardStepName}
}

func (m model) openClassifierAddWizard() model {
	m.cancelClassifierCommand()
	m.classifierAddWizard = newClassifierAddWizard()
	m.clearSuggestions()
	return m
}

func (m model) handleClassifierAddWizardKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.classifierAddWizard == nil {
		return m, nil
	}
	wizard := m.classifierAddWizard
	switch {
	case keyIs(msg, tea.KeyEsc):
		m.classifierAddWizard = nil
		// An in-flight save keeps running; only the closed wizard is dropped.
		return m, nil
	case keyBackspace(msg):
		wizard.deleteRune()
		return m, nil
	case keyCtrl(msg, 'u'):
		wizard.clearCurrentInput()
		return m, nil
	case keyIs(msg, tea.KeyLeft):
		wizard.back()
		return m, nil
	case keyText(msg) != "":
		if wizard.step == classifierAddWizardStepResult {
			m.classifierAddWizard = nil
			return m, nil
		}
		wizard.appendRunes(keyRunes(msg))
		return m, nil
	case keyIs(msg, tea.KeyEnter) || keyIs(msg, tea.KeyRight):
		if wizard.step == classifierAddWizardStepResult {
			m.classifierAddWizard = nil
			return m, nil
		}
		return m.advanceClassifierAddWizard()
	}
	return m, nil
}

func (m model) advanceClassifierAddWizard() (model, tea.Cmd) {
	wizard := m.classifierAddWizard
	if wizard == nil {
		return m, nil
	}
	wizard.err = ""
	switch wizard.step {
	case classifierAddWizardStepName:
		name := strings.TrimSpace(wizard.name)
		if name == "" {
			wizard.err = "a name is required"
			return m, nil
		}
		wizard.name = name
		wizard.step = classifierAddWizardStepEndpoint
	case classifierAddWizardStepEndpoint:
		endpoint := strings.TrimSpace(wizard.endpoint)
		if endpoint == "" {
			wizard.err = "an endpoint is required"
			return m, nil
		}
		if parsed, err := url.Parse(endpoint); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			wizard.err = "endpoint must be an http(s) URL"
			return m, nil
		}
		wizard.endpoint = endpoint
		wizard.step = classifierAddWizardStepModel
	case classifierAddWizardStepModel:
		wizard.model = strings.TrimSpace(wizard.model)
		wizard.step = classifierAddWizardStepKey
	case classifierAddWizardStepKey:
		wizard.apiKey = strings.TrimSpace(wizard.apiKey)
		wizard.step = classifierAddWizardStepConfirm
	case classifierAddWizardStepConfirm:
		return m.saveClassifierAddWizard()
	}
	return m, nil
}

func (m model) saveClassifierAddWizard() (model, tea.Cmd) {
	wizard := m.classifierAddWizard
	if wizard == nil {
		return m, nil
	}
	args, stdin := wizard.commandArgs()
	return m.startClassifierCommand(classifierCommandRequest{
		origin: classifierCommandOriginWizard,
		args:   args,
		stdin:  stdin,
		raw:    "classifier " + strings.Join(args, " "),
	})
}

// commandArgs builds the CLI add arguments and the stdin secret. The API key is
// passed on stdin (never in argv) so it does not leak into shell history,
// process listings, or the transcript.
func (wizard *classifierAddWizardState) commandArgs() ([]string, string) {
	args := []string{"add", strings.TrimSpace(wizard.name), "--endpoint", strings.TrimSpace(wizard.endpoint)}
	if model := strings.TrimSpace(wizard.model); model != "" {
		args = append(args, "--model", model)
	}
	stdin := ""
	if key := strings.TrimSpace(wizard.apiKey); key != "" {
		args = append(args, "--api-key-stdin")
		stdin = key
	}
	return args, stdin
}

func (wizard *classifierAddWizardState) appendRunes(runes []rune) {
	if wizard == nil {
		return
	}
	for _, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			continue
		}
		switch wizard.step {
		case classifierAddWizardStepName:
			wizard.name += string(r)
		case classifierAddWizardStepEndpoint:
			wizard.endpoint += string(r)
		case classifierAddWizardStepModel:
			wizard.model += string(r)
		case classifierAddWizardStepKey:
			wizard.apiKey += string(r)
		}
	}
	wizard.err = ""
}

func (wizard *classifierAddWizardState) deleteRune() {
	if wizard == nil {
		return
	}
	switch wizard.step {
	case classifierAddWizardStepName:
		wizard.name = trimLastRune(wizard.name)
	case classifierAddWizardStepEndpoint:
		wizard.endpoint = trimLastRune(wizard.endpoint)
	case classifierAddWizardStepModel:
		wizard.model = trimLastRune(wizard.model)
	case classifierAddWizardStepKey:
		wizard.apiKey = trimLastRune(wizard.apiKey)
	}
	wizard.err = ""
}

func (wizard *classifierAddWizardState) clearCurrentInput() {
	if wizard == nil {
		return
	}
	switch wizard.step {
	case classifierAddWizardStepName:
		wizard.name = ""
	case classifierAddWizardStepEndpoint:
		wizard.endpoint = ""
	case classifierAddWizardStepModel:
		wizard.model = ""
	case classifierAddWizardStepKey:
		wizard.apiKey = ""
	}
	wizard.err = ""
}

func (wizard *classifierAddWizardState) back() {
	if wizard == nil {
		return
	}
	if wizard.step > classifierAddWizardStepName {
		wizard.step--
	}
	wizard.err = ""
}

func (wizard *classifierAddWizardState) canBack() bool {
	return wizard != nil && wizard.step > classifierAddWizardStepName && wizard.step < classifierAddWizardStepResult
}

// applySaveResult records the outcome of the CLI save and moves to the result step.
func (m model) applyClassifierAddWizardSaveResult(result ClassifierCommandResult) model {
	wizard := m.classifierAddWizard
	if wizard == nil {
		return m
	}
	output := strings.TrimSpace(result.Output)
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		wizard.resultOK = false
		wizard.resultText = strings.TrimSpace(result.Error)
		if wizard.resultText == "" {
			wizard.resultText = output
		}
		if wizard.resultText == "" {
			wizard.resultText = "the classifier could not be saved"
		}
	} else {
		wizard.resultOK = true
		if output == "" {
			output = fmt.Sprintf("Saved classifier %s.", strings.TrimSpace(wizard.name))
		}
		wizard.resultText = output
	}
	wizard.step = classifierAddWizardStepResult
	return m
}
