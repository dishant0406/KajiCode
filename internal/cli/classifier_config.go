package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/redaction"
)

// The `kajicode classifier` command manages the pluggable fast-classifier
// capability: it registers Jev-shaped classifier profiles and proves one is
// reachable. Registering a profile never turns a classifier-powered feature on;
// that is a separate, explicit config step.

func runClassifier(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	return runClassifierWithContext(context.Background(), args, stdout, stderr, deps)
}

func runClassifierWithContext(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "classifier expects a subcommand: list, add, configure, remove, use, or check")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		return writeClassifierHelp(stdout)
	case "list", "ls":
		return runClassifierList(rest, stdout, stderr, deps)
	case "add":
		return runClassifierAdd(rest, stdout, stderr, deps)
	case "configure", "config", "set":
		return runClassifierConfigure(rest, stdout, stderr, deps)
	case "remove", "rm":
		return runClassifierRemove(rest, stdout, stderr, deps)
	case "use", "activate":
		return runClassifierUse(rest, stdout, stderr, deps)
	case "check", "test":
		return runClassifierCheck(ctx, rest, stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown classifier subcommand %q", sub))
	}
}

func runClassifierList(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut, help, err := parseClassifierListArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writeClassifierHelp(stdout)
	}
	cfg, exitCode := loadClassifierFileConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}
	if jsonOut {
		if err := writePrettyJSON(stdout, redaction.RedactValue(classifierListPayload(cfg.Classifier), redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if len(cfg.Classifier.Profiles) == 0 {
		if _, err := fmt.Fprintln(stdout, "No classifier profiles registered. Add one with `kajicode classifier add <name> --endpoint <url>`."); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	for _, profile := range cfg.Classifier.Profiles {
		marker := " "
		if strings.EqualFold(profile.Name, cfg.Classifier.Active) {
			marker = "*"
		}
		if _, err := fmt.Fprintf(stdout, "%s %s\t%s\t%s\n", marker, profile.Name, profile.URL(), profile.Model); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

func runClassifierAdd(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseClassifierAddArgs(args)
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

	// Never persist a plaintext key to config.json: move it into the encrypted
	// credential store and keep only the APIKeyStored marker. Unlike a provider
	// profile there is no safe inline fallback — a classifier key that cannot be
	// stored is a hard error, so the secret is never written where a plain read
	// would leak it.
	profile := options.profile
	updated, err := storeAndUpsertClassifier(configPath, &cfg, profile)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	stored, ok := cfg.file.Classifier.Profile(profile.Name)
	if !ok {
		return writeAppError(stderr, fmt.Sprintf("classifier profile %q was not stored", profile.Name), exitCrash)
	}
	profile = stored
	// The first registered profile becomes active automatically so `check` works
	// immediately; --set-active forces it for later profiles.
	if options.setActive || strings.TrimSpace(cfg.file.Classifier.Active) == "" {
		cfg.file.Classifier.SetActive(profile.Name)
	}
	if err := writeClassifierWritableConfig(configPath, cfg); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}

	if options.json {
		payload := map[string]any{
			"configPath": configPath,
			"updated":    updated,
			"active":     cfg.file.Classifier.Active,
			"profile":    redactedClassifierProfile(profile),
		}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	action := "Added"
	if updated {
		action = "Updated"
	}
	if _, err := fmt.Fprintf(stdout, "%s classifier %s in %s\nEnable a feature in the classifier.features config block (see docs).\n", action, profile.Name, configPath); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// storeAndUpsertClassifier moves an inline key into the encrypted credential
// store and writes the profile into the writable config, shared by `add` and
// `configure`. It returns whether an existing profile was updated.
func storeAndUpsertClassifier(configPath string, cfg *classifierWritableConfig, profile classifier.Profile) (bool, error) {
	if key := strings.TrimSpace(profile.APIKey); key != "" {
		if err := storeClassifierKey(configPath, profile.Name, key); err != nil {
			return false, err
		}
		profile.APIKey = ""
		profile.APIKeyStored = true
	}
	return cfg.upsertProfile(profile)
}

func runClassifierRemove(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut, name, help, err := parseClassifierPositionalCommand(args, "remove")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writeClassifierHelp(stdout)
	}
	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, "failed to resolve user config: "+err.Error(), exitCrash)
	}
	cfg, err := readClassifierWritableConfig(configPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if !cfg.removeProfile(name) {
		return writeAppError(stderr, fmt.Sprintf("classifier %s is not registered", name), exitCrash)
	}
	if err := writeClassifierWritableConfig(configPath, cfg); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// Best-effort: drop any stored key for the removed profile.
	forgetClassifierKey(configPath, name)

	if jsonOut {
		if err := writePrettyJSON(stdout, map[string]any{"removed": name, "configPath": configPath}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Removed classifier %s.\n", name); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runClassifierUse(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut, name, help, err := parseClassifierPositionalCommand(args, "use")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writeClassifierHelp(stdout)
	}
	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, "failed to resolve user config: "+err.Error(), exitCrash)
	}
	cfg, err := readClassifierWritableConfig(configPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if !cfg.file.Classifier.SetActive(name) {
		return writeAppError(stderr, fmt.Sprintf("classifier %s is not registered", name), exitCrash)
	}
	if err := writeClassifierWritableConfig(configPath, cfg); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if jsonOut {
		if err := writePrettyJSON(stdout, map[string]any{"active": name, "configPath": configPath}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Active classifier is now %s.\n", name); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runClassifierCheck(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut, name, help, err := parseClassifierCheckArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		return writeClassifierHelp(stdout)
	}
	cfg, exitCode := loadClassifierFileConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}
	active, ok := cfg.Classifier.ActiveProfile()
	if !ok {
		return writeAppError(stderr, "no classifier profile registered; run `kajicode classifier add`", exitCrash)
	}
	if strings.TrimSpace(name) != "" {
		profile, found := cfg.Classifier.Profile(name)
		if !found {
			return writeAppError(stderr, fmt.Sprintf("classifier %s is not registered", name), exitCrash)
		}
		active = profile
	}

	client, err := classifier.New(classifier.Config{
		Enabled: true, Active: active.Name, Profiles: []classifier.Profile{active},
	}, classifierKeyResolver(deps))
	if err != nil || client == nil {
		return writeAppError(stderr, "classifier is not configured", exitCrash)
	}

	result, classifyErr := client.Classify(ctx, classifier.Request{
		State: "classifier connectivity probe",
		Questions: map[string]classifier.Question{
			"reachable": classifier.Noul("The classifier endpoint responded to this request.", "", ""),
		},
	})

	if jsonOut {
		payload := map[string]any{
			"profile":     active.Name,
			"endpoint":    active.URL(),
			"reachable":   classifyErr == nil,
			"probability": result.Probability("reachable", 0),
			"features": map[string]any{
				"compaction": cfg.Classifier.Features.Compaction.Enabled,
				"toolResult": cfg.Classifier.Features.ToolResult.Enabled,
			},
		}
		if classifyErr != nil {
			payload["error"] = redaction.ErrorMessage(classifyErr, redaction.Options{})
		}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		if classifyErr != nil {
			return exitCrash
		}
		return exitSuccess
	}

	if classifyErr != nil {
		if _, writeErr := fmt.Fprintf(stdout, "Classifier %s is NOT reachable: %s\n", active.Name, redaction.ErrorMessage(classifyErr, redaction.Options{})); writeErr != nil {
			return exitCrash
		}
		return exitCrash
	}
	if _, err := fmt.Fprintf(stdout, "Classifier %s is reachable (%s). Probe probability: %.3f\n", active.Name, active.URL(), result.Probability("reachable", 0)); err != nil {
		return exitCrash
	}
	// Connecting a classifier enables nothing: each feature is a separate opt-in.
	// Report their state so "reachable" is not mistaken for "in effect".
	if _, err := fmt.Fprintf(stdout, "Features: compaction=%s toolResult=%s (set classifier.features.<name>.enabled in config to turn one on)\n",
		onOff(cfg.Classifier.Features.Compaction.Enabled), onOff(cfg.Classifier.Features.ToolResult.Enabled)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// loadClassifierFileConfig reads the user's config file (typed only; these CLI
// surfaces never need the raw writer) and surfaces validation issues.
func loadClassifierFileConfig(stderr io.Writer, deps appDeps) (config.FileConfig, int) {
	configPath, err := deps.userConfigPath()
	if err != nil {
		return config.FileConfig{}, writeAppError(stderr, "failed to resolve user config: "+err.Error(), exitCrash)
	}
	cfg, issues := config.ValidateFile(configPath)
	if len(issues) > 0 {
		return config.FileConfig{}, writeAppError(stderr, redaction.ErrorMessage(fmt.Errorf("%s", issues[0].Message), redaction.Options{}), exitCrash)
	}
	return cfg, exitSuccess
}

func classifierListPayload(cfgr classifier.Config) map[string]any {
	profiles := make([]map[string]any, 0, len(cfgr.Profiles))
	for _, profile := range cfgr.Profiles {
		profiles = append(profiles, redactedClassifierProfile(profile))
	}
	return map[string]any{
		"enabled":  cfgr.Enabled,
		"active":   cfgr.Active,
		"profiles": profiles,
		"features": cfgr.Features,
	}
}

// redactedClassifierProfile returns the profile with any inline key scrubbed, so
// list/check JSON output never echoes a secret.
func redactedClassifierProfile(profile classifier.Profile) map[string]any {
	return map[string]any{
		"name":         profile.Name,
		"kind":         profile.Adapter(),
		"endpoint":     profile.URL(),
		"model":        profile.Model,
		"authHeader":   profile.AuthHeader,
		"authScheme":   profile.AuthScheme,
		"apiKeyEnv":    profile.APIKeyEnv,
		"apiKeyStored": profile.APIKeyStored,
		"headers":      redaction.RedactValue(profile.Headers, redaction.Options{}),
		"timeoutMs":    profile.TimeoutMS,
	}
}

// classifierKeyNamespace prefixes classifier credentials in the shared
// credential store. Without it a classifier profile named like a model provider
// (`openrouter`, `openai`) would overwrite or delete that provider's stored key.
const classifierKeyNamespace = "classifier:"

func classifierKeyName(name string) string {
	return classifierKeyNamespace + strings.ToLower(strings.TrimSpace(name))
}

// classifierKeyResolver reads a stored classifier key from the encrypted
// credential store beside the user config.
func classifierKeyResolver(deps appDeps) classifier.APIKeyResolver {
	return func(name string) (string, bool) {
		path, err := deps.userConfigPath()
		if err != nil {
			return "", false
		}
		store, err := config.ProviderKeyStoreAt(filepath.Dir(path))
		if err != nil {
			return "", false
		}
		key, ok, err := store.Get(classifierKeyName(name))
		if err != nil || !ok {
			return "", false
		}
		return key, true
	}
}

func storeClassifierKey(configPath, name, key string) error {
	store, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("open credential store: %w", err)
	}
	if err := store.Set(classifierKeyName(name), key); err != nil {
		return fmt.Errorf("store classifier key: %w", err)
	}
	return nil
}

func forgetClassifierKey(configPath, name string) {
	store, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return
	}
	_, _ = store.Delete(classifierKeyName(name))
}

func writeClassifierHelp(w io.Writer) int {
	_, err := fmt.Fprint(w, `Usage:
  kajicode classifier list [--json]
  kajicode classifier add <name> [flags]
  kajicode classifier configure <name> [flags]
  kajicode classifier remove <name> [--json]
  kajicode classifier use <name> [--json]
  kajicode classifier check [name] [--json]

The classifier is a pluggable fast-classifier capability. A profile points at any
Jev-shaped endpoint ({state, questions} -> {answers}) and is stored in config.json.
Registering a profile never enables a feature; enable one explicitly.

Flags for add:
      --kind <kind>           Wire adapter (default: jev)
      --endpoint <url>        Full request URL (OpenRouter Jev router: https://openrouter.ai/api/v1/systemone)
      --model <model>         Model sent as the request "model" (e.g. jev-1.13)
      --header KEY=VALUE      Extra request header (repeatable)
      --auth-header <name>    Header the key goes in (default: Authorization)
      --auth-scheme <scheme>  Scheme prefix (default: Bearer; "" or "raw" sends the key bare)
      --api-key-env <ENV>     Environment variable holding the key
      --api-key-stdin         Read the key from stdin (stored encrypted, never written to config.json)
      --timeout <ms>          Per-request timeout in milliseconds (default: 8000)
      --set-active            Make this the active profile
      --json                  Print the result as JSON
  -h, --help                  Show this help

configure edits a registered profile and its feature switches in one step. It
accepts the add flags above, plus:
      --enable[=true|false]   Turn the classifier capability on or off
      --disable               Turn the classifier capability off
      --compaction[=bool]     Enable/disable the compaction keep/drop judge
      --compaction-keep <p>   Compaction keep threshold in [0,1]
      --tool-result[=bool]    Enable/disable the live tool-result gate
      --tool-keep <p>         Gate keep threshold in [0,1]
      --tool-drop <p>         Gate drop threshold in [0,1]
      --tool-min-prune-ratio <p>  Gate minimum hidden share in [0,1]
      --tool-min-bytes <n>    Gate minimum result size in bytes
      --tool-shadow[=bool]    Gate shadow mode (decide but never rewrite)
Flags are value=`+"`=value`"+` or a following token; a bare bool flag means true.
Unpassed fields are left unchanged.
`)
	if err != nil {
		return exitCrash
	}
	return exitSuccess
}
