package cli

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/redaction"
)

// The `kajicode classifier configure` command wires a registered profile and the
// feature switches in one step: it can update a profile's routing fields
// (endpoint, model, auth, headers, timeout) and turn the capability and each
// consumer on or off. It is the write path the TUI and ACP configuration forms
// call, so every surface saves through exactly one implementation. Values that
// are not passed are left exactly as they were.

type classifierConfigureOptions struct {
	json        bool
	apiKeyStdin bool
	setActive   bool
	profile     classifier.Profile

	// enable is the explicit classifier.enabled write; nil means "leave it".
	enable     *bool
	compaction *bool
	toolResult *bool

	compactionKeep *float64

	toolKeep     *float64
	toolDrop     *float64
	toolMinPrune *float64
	toolMinBytes *int
	toolShadow   *bool
}

func runClassifierConfigure(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseClassifierConfigureArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writeClassifierHelp(stdout)
	}
	if options.apiKeyStdin {
		key, err := readProviderKeyStdin(deps)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		options.profile.APIKey = key
	}

	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, "failed to resolve user config: "+err.Error(), exitCrash)
	}
	cfg, err := readClassifierWritableConfig(configPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// configure upserts: it updates a registered profile or registers a new one,
	// so the TUI and ACP forms can save a complete classifier in one call.
	if _, err := storeAndUpsertClassifier(configPath, &cfg, options.profile); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// The first registered profile becomes active automatically so `check` works
	// immediately; --set-active forces it.
	if options.setActive || strings.TrimSpace(cfg.file.Classifier.Active) == "" {
		cfg.file.Classifier.SetActive(options.profile.Name)
	}

	// Decide the capability switch: an explicit --enable/--disable always wins;
	// otherwise turning on a consumer enables the capability, because an enabled
	// feature under a disabled capability would be silently inert.
	switch {
	case options.enable != nil:
		cfg.setEnabled(*options.enable)
	case featureTrue(options.compaction) || featureTrue(options.toolResult):
		cfg.setEnabled(true)
	}
	if err := applyClassifierFeatureSwitches(&cfg, options); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if err := writeClassifierWritableConfig(configPath, cfg); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// Re-read the file so the report reflects exactly what was persisted,
	// including the feature keys this command did not touch.
	if saved, readErr := readClassifierWritableConfig(configPath); readErr == nil {
		cfg = saved
	}
	return reportClassifierConfigure(stdout, configPath, cfg, options)
}

// applyClassifierFeatureSwitches writes every feature flag the caller passed,
// preserving feature keys this version does not model.
func applyClassifierFeatureSwitches(cfg *classifierWritableConfig, options classifierConfigureOptions) error {
	if options.compaction != nil {
		if err := cfg.setFeatureKey("compaction", "enabled", *options.compaction); err != nil {
			return err
		}
	}
	if options.compactionKeep != nil {
		if err := cfg.setFeatureKey("compaction", "keepResultThreshold", *options.compactionKeep); err != nil {
			return err
		}
	}
	if options.toolResult != nil {
		if err := cfg.setFeatureKey("toolResult", "enabled", *options.toolResult); err != nil {
			return err
		}
	}
	toolKnobs := []struct {
		set   bool
		key   string
		value any
	}{
		{options.toolKeep != nil, "keepThreshold", derefFloat(options.toolKeep)},
		{options.toolDrop != nil, "dropThreshold", derefFloat(options.toolDrop)},
		{options.toolMinPrune != nil, "minPruneRatio", derefFloat(options.toolMinPrune)},
		{options.toolMinBytes != nil, "minBytes", derefInt(options.toolMinBytes)},
		{options.toolShadow != nil, "shadowMode", derefBool(options.toolShadow)},
	}
	for _, knob := range toolKnobs {
		if !knob.set {
			continue
		}
		if err := cfg.setFeatureKey("toolResult", knob.key, knob.value); err != nil {
			return err
		}
	}
	return nil
}

func reportClassifierConfigure(stdout io.Writer, configPath string, cfg classifierWritableConfig, options classifierConfigureOptions) int {
	active := strings.TrimSpace(cfg.file.Classifier.Active)
	profile, _ := cfg.file.Classifier.Profile(options.profile.Name)
	features := cfg.file.Classifier.Features
	if options.json {
		payload := map[string]any{
			"configPath": configPath,
			"profile":    redactedClassifierProfile(profile),
			"active":     cfg.file.Classifier.Active,
			"features": map[string]any{
				"compaction": features.Compaction,
				"toolResult": features.ToolResult,
			},
		}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	lines := []string{fmt.Sprintf("Configured classifier %s.", options.profile.Name)}
	if profile.URL() != "" {
		lines = append(lines, "  endpoint:    "+profile.URL())
	}
	if strings.TrimSpace(profile.Model) != "" {
		lines = append(lines, "  model:       "+strings.TrimSpace(profile.Model))
	}
	if strings.TrimSpace(active) != "" {
		lines = append(lines, "  active:      "+active)
	}
	// Read the enabled state back from the file so the report matches what was
	// persisted, not only what this command passed.
	lines = append(lines,
		"  classifier:  "+onOff(cfg.classifierEnabled()),
		"  compaction:  "+featureSummary(features.Compaction.Enabled, fmt.Sprintf("keep >= %.2f", features.Compaction.EffectiveKeepResultThreshold())),
		"  toolResult:  "+featureSummary(features.ToolResult.Enabled, fmt.Sprintf("drop < %.2f", features.ToolResult.EffectiveDropThreshold())),
	)
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

// featureSummary renders "enabled (knob)" / "disabled".
func featureSummary(enabled bool, detail string) string {
	if !enabled {
		return "disabled"
	}
	if strings.TrimSpace(detail) == "" {
		return "enabled"
	}
	return "enabled (" + detail + ")"
}

// classifierEnabled reads the capability switch back from the writable config,
// which `configure` re-reads after writing so it matches the persisted file.
func (cfg classifierWritableConfig) classifierEnabled() bool {
	return cfg.file.Classifier.Enabled
}

func parseClassifierConfigureArgs(args []string) (classifierConfigureOptions, bool, error) {
	options := classifierConfigureOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		name, inline, hasInline := strings.Cut(arg, "=")
		switch name {
		case "-h", "--help":
			return options, true, nil
		case "help":
			if !hasInline {
				return options, true, nil
			}
		case "--json":
			if hasInline {
				return options, false, execUsageError{fmt.Sprintf("%s takes no value", name)}
			}
			options.json = true
		case "--set-active":
			if hasInline {
				return options, false, execUsageError{fmt.Sprintf("%s takes no value", name)}
			}
			options.setActive = true
		case "--api-key-stdin":
			if hasInline {
				return options, false, execUsageError{fmt.Sprintf("%s takes no value", name)}
			}
			options.apiKeyStdin = true
		case "--enable", "--compaction", "--tool-result", "--tool-shadow":
			value, err := classifierInlineBool(name, inline, hasInline)
			if err != nil {
				return options, false, err
			}
			switch name {
			case "--enable":
				options.enable = &value
			case "--compaction":
				options.compaction = &value
			case "--tool-result":
				options.toolResult = &value
			case "--tool-shadow":
				options.toolShadow = &value
			}
		case "--disable":
			value := false
			options.enable = &value
		case "--kind", "--endpoint", "--url", "--model", "--auth-header", "--auth-scheme", "--api-key-env", "--header", "--timeout",
			"--compaction-keep", "--tool-keep", "--tool-drop", "--tool-min-prune-ratio", "--tool-min-bytes":
			value, err := classifierValueArg(args, &index, name, inline, hasInline)
			if err != nil {
				return options, false, err
			}
			if err := applyClassifierConfigureValue(&options, name, value); err != nil {
				return options, false, err
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return options, false, execUsageError{fmt.Sprintf("unknown classifier configure flag %q", arg)}
			}
			if options.profile.Name != "" {
				return options, false, execUsageError{"classifier configure takes a single classifier name"}
			}
			options.profile.Name = strings.TrimSpace(arg)
		}
	}
	if options.profile.Name == "" {
		return options, false, execUsageError{"classifier configure requires a name"}
	}
	return options, false, nil
}

// applyClassifierConfigureValue routes one value flag into the options, parsing
// the numeric knobs and the profile's routing fields.
func applyClassifierConfigureValue(options *classifierConfigureOptions, name, value string) error {
	switch name {
	case "--kind":
		options.profile.Kind = value
	case "--endpoint", "--url":
		options.profile.Endpoint = value
	case "--model":
		options.profile.Model = value
	case "--auth-header":
		options.profile.AuthHeader = value
	case "--auth-scheme":
		options.profile.AuthScheme = value
	case "--api-key-env":
		options.profile.APIKeyEnv = value
	case "--header":
		if err := addClassifierHeader(&options.profile, value); err != nil {
			return err
		}
	case "--timeout":
		timeout, err := strconv.Atoi(value)
		if err != nil || timeout <= 0 {
			return execUsageError{fmt.Sprintf("invalid --timeout %q: want a positive number of milliseconds", value)}
		}
		options.profile.TimeoutMS = timeout
	case "--compaction-keep":
		parsed, err := classifierProbabilityFlag(name, value)
		if err != nil {
			return err
		}
		options.compactionKeep = &parsed
	case "--tool-keep":
		parsed, err := classifierProbabilityFlag(name, value)
		if err != nil {
			return err
		}
		options.toolKeep = &parsed
	case "--tool-drop":
		parsed, err := classifierProbabilityFlag(name, value)
		if err != nil {
			return err
		}
		options.toolDrop = &parsed
	case "--tool-min-prune-ratio":
		parsed, err := classifierProbabilityFlag(name, value)
		if err != nil {
			return err
		}
		options.toolMinPrune = &parsed
	case "--tool-min-bytes":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return execUsageError{fmt.Sprintf("invalid --tool-min-bytes %q: want a non-negative number of bytes", value)}
		}
		options.toolMinBytes = &parsed
	}
	return nil
}

// classifierProbabilityFlag parses a threshold in [0,1]. NaN is rejected
// explicitly: ParseFloat accepts it and both range comparisons are false.
func classifierProbabilityFlag(flag, value string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
		return 0, execUsageError{fmt.Sprintf("invalid %s %q: want a number in [0,1]", flag, value)}
	}
	return parsed, nil
}

// classifierInlineBool parses a bool flag that accepts a bare form (true) or an
// explicit `=true|false`. A following token is never consumed, so a bool flag can
// never swallow the positional name.
func classifierInlineBool(name, inline string, hasInline bool) (bool, error) {
	if !hasInline {
		return true, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(inline))
	if err != nil {
		return false, execUsageError{fmt.Sprintf("invalid %s value %q: want true or false", name, inline)}
	}
	return parsed, nil
}

// classifierValueArg resolves a value flag from its inline `=value` form or the
// next token.
func classifierValueArg(args []string, index *int, name, inline string, hasInline bool) (string, error) {
	if hasInline {
		return strings.TrimSpace(inline), nil
	}
	return classifierFlagValue(args, index, name)
}

func featureTrue(value *bool) bool { return value != nil && *value }
func derefFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
func derefBool(value *bool) bool { return value != nil && *value }
