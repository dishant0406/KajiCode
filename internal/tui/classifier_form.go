package tui

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/dishant0406/KajiCode/internal/classifier"
)

const (
	classifierFormMinWidth = 58
	classifierFormMaxWidth = 92
)

type classifierFormStep int

const (
	classifierFormStepName classifierFormStep = iota
	classifierFormStepEndpoint
	classifierFormStepModel
	classifierFormStepAuthHeader
	classifierFormStepAuthScheme
	classifierFormStepHeader
	classifierFormStepTimeout
	classifierFormStepKey
	classifierFormStepCapability
	classifierFormStepCompaction
	classifierFormStepCompactionKeep
	classifierFormStepToolResult
	classifierFormStepToolDrop
	classifierFormStepToolKeep
	classifierFormStepToolShadow
	classifierFormStepConfirm
	classifierFormStepResult
)

// classifierFormState is the `/classifier` configuration form. It registers a
// profile and sets its feature switches in one place, so every value the CLI's
// `classifier configure` accepts is reachable from the TUI. It is prefilled from
// the current config; an empty text field leaves the stored value unchanged.
type classifierFormState struct {
	step       classifierFormStep
	name       string
	endpoint   string
	model      string
	authHeader string
	authScheme string
	header     string
	timeout    string
	apiKey     string

	capability     bool
	compaction     bool
	compactionKeep string
	toolResult     bool
	toolDrop       string
	toolKeep       string
	toolShadow     bool

	// keyStored mirrors the profile's APIKeyStored so the key step can say
	// whether a key is already kept without holding the secret.
	keyStored bool

	err        string
	resultText string
	resultOK   bool
}

// newClassifierForm builds the form prefilled from the active profile and the
// current feature switches. With no profile it starts empty (add).
func newClassifierForm(cfg classifier.Config) *classifierFormState {
	form := &classifierFormState{step: classifierFormStepName, capability: cfg.Enabled}
	if profile, ok := cfg.ActiveProfile(); ok {
		form.name = strings.TrimSpace(profile.Name)
		form.endpoint = profile.URL()
		form.model = strings.TrimSpace(profile.Model)
		form.authHeader = strings.TrimSpace(profile.AuthHeader)
		form.authScheme = strings.TrimSpace(profile.AuthScheme)
		form.keyStored = profile.APIKeyStored || strings.TrimSpace(profile.APIKeyEnv) != ""
		if profile.TimeoutMS > 0 {
			form.timeout = strconv.Itoa(profile.TimeoutMS)
		}
		// The form edits one custom header; `--header KEY=VALUE` is merged into the
		// stored map, so any other headers the profile already has are preserved.
		for key, value := range profile.Headers {
			form.header = key + "=" + value
			break
		}
	}
	feature := cfg.Features.Compaction
	form.compaction = feature.Enabled
	form.compactionKeep = trimFloat(feature.EffectiveKeepResultThreshold())
	tool := cfg.Features.ToolResult
	form.toolResult = tool.Enabled
	form.toolDrop = trimFloat(tool.EffectiveDropThreshold())
	form.toolKeep = trimFloat(tool.EffectiveKeepThreshold())
	form.toolShadow = tool.ShadowMode
	return form
}

func (m model) openClassifierForm() model {
	m.cancelClassifierCommand()
	m.classifierForm = newClassifierForm(m.classifierConfig)
	m.clearSuggestions()
	return m
}

func (m model) handleClassifierFormKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.classifierForm == nil {
		return m, nil
	}
	form := m.classifierForm
	switch {
	case keyIs(msg, tea.KeyEsc):
		m.classifierForm = nil
		return m, nil
	case keyBackspace(msg):
		form.deleteRune()
		return m, nil
	case keyCtrl(msg, 'u'):
		form.clearCurrentInput()
		return m, nil
	case keyIs(msg, tea.KeyLeft):
		form.back()
		return m, nil
	case keyIs(msg, tea.KeyUp) || keyIs(msg, tea.KeyDown):
		form.toggleBool()
		return m, nil
	case keyText(msg) != "":
		if form.step == classifierFormStepResult {
			m.classifierForm = nil
			return m, nil
		}
		form.appendRunes(keyRunes(msg))
		return m, nil
	case keyIs(msg, tea.KeyEnter) || keyIs(msg, tea.KeyRight) || keyIs(msg, tea.KeyTab):
		if form.step == classifierFormStepResult {
			m.classifierForm = nil
			return m, nil
		}
		if form.step == classifierFormStepConfirm {
			return m.saveClassifierForm()
		}
		if err := form.advance(); err != "" {
			form.err = err
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

// advance validates the current step and moves to the next, returning an error
// message to display (empty means it advanced).
func (form *classifierFormState) advance() string {
	if form == nil {
		return ""
	}
	switch form.step {
	case classifierFormStepName:
		name := strings.TrimSpace(form.name)
		if name == "" {
			return "a name is required"
		}
		form.name = name
	case classifierFormStepEndpoint:
		endpoint := strings.TrimSpace(form.endpoint)
		if endpoint == "" {
			return "an endpoint is required"
		}
		if parsed, err := url.Parse(endpoint); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "endpoint must be an http(s) URL"
		}
		form.endpoint = endpoint
	case classifierFormStepHeader:
		if raw := strings.TrimSpace(form.header); raw != "" {
			key, value, ok := strings.Cut(raw, "=")
			if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return "header must be KEY=VALUE"
			}
			form.header = strings.TrimSpace(key) + "=" + strings.TrimSpace(value)
		}
	case classifierFormStepTimeout:
		if raw := strings.TrimSpace(form.timeout); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return "timeout must be a positive number of milliseconds"
			}
			form.timeout = strconv.Itoa(value)
		}
	case classifierFormStepCompactionKeep:
		if raw := strings.TrimSpace(form.compactionKeep); raw != "" {
			if !classifierProbability(raw) {
				return "keep threshold must be a number in [0,1]"
			}
		}
	case classifierFormStepToolDrop:
		if raw := strings.TrimSpace(form.toolDrop); raw != "" {
			if !classifierProbability(raw) {
				return "drop threshold must be a number in [0,1]"
			}
		}
	case classifierFormStepToolKeep:
		if raw := strings.TrimSpace(form.toolKeep); raw != "" {
			if !classifierProbability(raw) {
				return "keep threshold must be a number in [0,1]"
			}
		}
	}
	form.err = ""
	if form.step < classifierFormStepResult {
		form.step++
	}
	return ""
}

func (m model) saveClassifierForm() (model, tea.Cmd) {
	form := m.classifierForm
	if form == nil {
		return m, nil
	}
	args, stdin := form.commandArgs()
	return m.startClassifierCommand(classifierCommandRequest{
		origin: classifierCommandOriginWizard,
		args:   args,
		stdin:  stdin,
		raw:    "classifier " + strings.Join(args, " "),
	})
}

// commandArgs builds the `classifier configure` arguments and the stdin secret.
// Bools are always sent explicitly so the form fully controls the switches; text
// fields left blank are omitted so the stored value is preserved. The API key
// travels on stdin (never argv) so it stays out of shell history and the
// transcript.
func (form *classifierFormState) commandArgs() ([]string, string) {
	args := []string{"configure", strings.TrimSpace(form.name), "--endpoint", strings.TrimSpace(form.endpoint)}
	appendValue := func(flag, value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			args = append(args, flag, trimmed)
		}
	}
	appendValue("--model", form.model)
	appendValue("--auth-header", form.authHeader)
	appendValue("--auth-scheme", form.authScheme)
	appendValue("--header", form.header)
	appendValue("--timeout", form.timeout)
	args = append(args, "--enable="+strconv.FormatBool(form.capability))
	args = append(args, "--compaction="+strconv.FormatBool(form.compaction))
	appendValue("--compaction-keep", form.compactionKeep)
	args = append(args, "--tool-result="+strconv.FormatBool(form.toolResult))
	appendValue("--tool-drop", form.toolDrop)
	appendValue("--tool-keep", form.toolKeep)
	args = append(args, "--tool-shadow="+strconv.FormatBool(form.toolShadow))
	stdin := ""
	if key := strings.TrimSpace(form.apiKey); key != "" {
		args = append(args, "--api-key-stdin")
		stdin = key
	}
	return args, stdin
}

// toggleBool flips the current field when it is a yes/no step.
func (form *classifierFormState) toggleBool() {
	if form == nil {
		return
	}
	switch form.step {
	case classifierFormStepCapability:
		form.capability = !form.capability
	case classifierFormStepCompaction:
		form.compaction = !form.compaction
	case classifierFormStepToolResult:
		form.toolResult = !form.toolResult
	case classifierFormStepToolShadow:
		form.toolShadow = !form.toolShadow
	}
}

func (form *classifierFormState) appendRunes(runes []rune) {
	if form == nil {
		return
	}
	for _, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			continue
		}
		switch form.step {
		case classifierFormStepName:
			form.name += string(r)
		case classifierFormStepEndpoint:
			form.endpoint += string(r)
		case classifierFormStepModel:
			form.model += string(r)
		case classifierFormStepAuthHeader:
			form.authHeader += string(r)
		case classifierFormStepAuthScheme:
			form.authScheme += string(r)
		case classifierFormStepHeader:
			form.header += string(r)
		case classifierFormStepTimeout:
			form.timeout += string(r)
		case classifierFormStepKey:
			form.apiKey += string(r)
		case classifierFormStepCompactionKeep:
			form.compactionKeep += string(r)
		case classifierFormStepToolDrop:
			form.toolDrop += string(r)
		case classifierFormStepToolKeep:
			form.toolKeep += string(r)
		case classifierFormStepCapability, classifierFormStepCompaction, classifierFormStepToolResult, classifierFormStepToolShadow:
			switch strings.ToLower(string(r)) {
			case "y":
				form.setBool(true)
			case "n":
				form.setBool(false)
			}
		}
	}
	form.err = ""
}

func (form *classifierFormState) setBool(value bool) {
	switch form.step {
	case classifierFormStepCapability:
		form.capability = value
	case classifierFormStepCompaction:
		form.compaction = value
	case classifierFormStepToolResult:
		form.toolResult = value
	case classifierFormStepToolShadow:
		form.toolShadow = value
	}
}

func (form *classifierFormState) deleteRune() {
	if form == nil {
		return
	}
	switch form.step {
	case classifierFormStepName:
		form.name = trimLastRune(form.name)
	case classifierFormStepEndpoint:
		form.endpoint = trimLastRune(form.endpoint)
	case classifierFormStepModel:
		form.model = trimLastRune(form.model)
	case classifierFormStepAuthHeader:
		form.authHeader = trimLastRune(form.authHeader)
	case classifierFormStepAuthScheme:
		form.authScheme = trimLastRune(form.authScheme)
	case classifierFormStepHeader:
		form.header = trimLastRune(form.header)
	case classifierFormStepTimeout:
		form.timeout = trimLastRune(form.timeout)
	case classifierFormStepKey:
		form.apiKey = ""
	case classifierFormStepCompactionKeep:
		form.compactionKeep = trimLastRune(form.compactionKeep)
	case classifierFormStepToolDrop:
		form.toolDrop = trimLastRune(form.toolDrop)
	case classifierFormStepToolKeep:
		form.toolKeep = trimLastRune(form.toolKeep)
	}
	form.err = ""
}

func (form *classifierFormState) clearCurrentInput() {
	if form == nil {
		return
	}
	switch form.step {
	case classifierFormStepName:
		form.name = ""
	case classifierFormStepEndpoint:
		form.endpoint = ""
	case classifierFormStepModel:
		form.model = ""
	case classifierFormStepAuthHeader:
		form.authHeader = ""
	case classifierFormStepAuthScheme:
		form.authScheme = ""
	case classifierFormStepHeader:
		form.header = ""
	case classifierFormStepTimeout:
		form.timeout = ""
	case classifierFormStepKey:
		form.apiKey = ""
	case classifierFormStepCompactionKeep:
		form.compactionKeep = ""
	case classifierFormStepToolDrop:
		form.toolDrop = ""
	case classifierFormStepToolKeep:
		form.toolKeep = ""
	}
	form.err = ""
}

func (form *classifierFormState) back() {
	if form == nil || form.step == classifierFormStepName {
		return
	}
	form.step--
	form.err = ""
}

func (form *classifierFormState) canBack() bool {
	return form != nil && form.step > classifierFormStepName && form.step < classifierFormStepResult
}

// applyClassifierFormSaveResult records the outcome of the CLI save and moves to
// the result step.
func (m model) applyClassifierFormSaveResult(result ClassifierCommandResult) model {
	form := m.classifierForm
	if form == nil {
		return m
	}
	output := strings.TrimSpace(result.Output)
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		form.resultOK = false
		form.resultText = strings.TrimSpace(result.Error)
		if form.resultText == "" {
			form.resultText = output
		}
		if form.resultText == "" {
			form.resultText = "the classifier could not be saved"
		}
	} else {
		form.resultOK = true
		if output == "" {
			output = fmt.Sprintf("Saved classifier %s.", strings.TrimSpace(form.name))
		}
		form.resultText = output
	}
	form.step = classifierFormStepResult
	return m
}

// classifierProbability reports whether raw parses as a number in [0,1], used to
// validate the form's threshold fields before the CLI is called. NaN is rejected
// explicitly because strconv.ParseFloat accepts it.
func classifierProbability(raw string) bool {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return err == nil && !math.IsNaN(value) && value >= 0 && value <= 1
}

// trimFloat prints a threshold with the shortest round-trippable representation,
// so the form shows 0.2 rather than 0.20000000000000001.
func trimFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
