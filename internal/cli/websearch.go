package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// `kajicode web-search` configures the web-search backend headlessly — the CLI
// analogue of the TUI's /web-search form, so the same setup is reachable from a
// terminal (and therefore over ACP). The API key is written to the env fallback
// file that every surface loads at startup; it is never printed back.
func runWebSearch(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		if err := writeWebSearchHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	command := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	switch command {
	case "status", "show":
		return runWebSearchStatus(stdout, stderr, deps)
	case "set", "add":
		return runWebSearchSet(args, stdout, stderr, deps)
	case "remove", "clear", "reset":
		return runWebSearchRemove(stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown web-search command %q", command))
	}
}

func writeWebSearchHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  kajicode web-search status
  kajicode web-search set <provider> [--key <api-key>] [--key-env <ENV_VAR>] [--base-url <url>]
  kajicode web-search remove

Providers:
  exa, tavily, searxng, custom

`+webSearchProvidersText())
	return err
}

func webSearchProvidersText() string {
	var b strings.Builder
	for _, p := range tools.WebSearchProviders() {
		key := p.EnvVar
		if key == "" {
			key = "(keyless)"
		}
		b.WriteString(fmt.Sprintf("  %-10s env=%s\n", p.ID, key))
	}
	return b.String()
}

func runWebSearchStatus(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	// Stored values live in the env fallback file; load it first so a fresh
	// process reports what is actually configured rather than an empty env.
	if dir := webSearchConfigDir(deps); dir != "" {
		_ = config.LoadEnvFile(dir)
	}
	active := strings.TrimSpace(os.Getenv("KAJICODE_WEBSEARCH_PROVIDER"))
	lines := []string{"Web search configuration"}
	for _, p := range tools.WebSearchProviders() {
		set := "—"
		if p.EnvVar != "" && strings.TrimSpace(os.Getenv(p.EnvVar)) != "" {
			set = "set"
		}
		base := tools.WebSearchProviderDefaultBaseURL(p.ID)
		if p.BaseURLEnv != "" {
			if v := strings.TrimSpace(os.Getenv(p.BaseURLEnv)); v != "" {
				base = v
			}
		}
		lines = append(lines, fmt.Sprintf("  %-10s key: %-5s base: %s", p.ID, set, base))
	}
	if active == "" {
		lines = append(lines, "active provider: (failover chain)")
	} else {
		lines = append(lines, "active provider: "+active)
	}
	if _, err := fmt.Fprintln(stdout, strings.Join(lines, "\n")); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runWebSearchSet(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	providerID := ""
	key := ""
	keyEnv := ""
	baseURL := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "--key":
			v, ok := next()
			if !ok {
				return writeExecUsageError(stderr, "--key requires a value")
			}
			key = v
		case strings.HasPrefix(arg, "--key="):
			key = strings.TrimSpace(strings.TrimPrefix(arg, "--key="))
		case arg == "--key-env":
			v, ok := next()
			if !ok {
				return writeExecUsageError(stderr, "--key-env requires a value")
			}
			keyEnv = v
		case strings.HasPrefix(arg, "--key-env="):
			keyEnv = strings.TrimSpace(strings.TrimPrefix(arg, "--key-env="))
		case arg == "--base-url":
			v, ok := next()
			if !ok {
				return writeExecUsageError(stderr, "--base-url requires a value")
			}
			baseURL = v
		case strings.HasPrefix(arg, "--base-url="):
			baseURL = strings.TrimSpace(strings.TrimPrefix(arg, "--base-url="))
		case strings.HasPrefix(arg, "-"):
			return writeExecUsageError(stderr, fmt.Sprintf("unknown web-search flag %q", arg))
		default:
			if providerID != "" {
				return writeExecUsageError(stderr, fmt.Sprintf("unexpected argument %q", arg))
			}
			providerID = strings.ToLower(strings.TrimSpace(arg))
		}
	}
	if providerID == "" {
		return writeExecUsageError(stderr, "web-search set requires a provider (exa, tavily, searxng, custom)")
	}
	provider, ok := webSearchProviderByID(providerID)
	if !ok {
		return writeExecUsageError(stderr, fmt.Sprintf("unknown web-search provider %q", providerID))
	}
	// Resolve the key: --key wins, else --key-env names the variable to read.
	if key == "" && keyEnv != "" {
		key = strings.TrimSpace(os.Getenv(keyEnv))
		if key == "" {
			return writeExecUsageError(stderr, fmt.Sprintf("environment variable %s is empty", keyEnv))
		}
	}
	if provider.RequiresKey && key == "" {
		return writeExecUsageError(stderr, fmt.Sprintf("provider %q requires a key (--key or --key-env)", provider.ID))
	}

	pairs := map[string]string{"KAJICODE_WEBSEARCH_PROVIDER": provider.ID}
	if provider.EnvVar != "" && key != "" {
		pairs[provider.EnvVar] = key
	}
	switch {
	case baseURL != "" && provider.BaseURLEnv != "":
		pairs[provider.BaseURLEnv] = baseURL
	case provider.DefaultBaseURL != "":
		pairs["KAJICODE_WEBSEARCH_BASE_URL"] = provider.DefaultBaseURL
	}

	configDir := webSearchConfigDir(deps)
	if configDir == "" {
		return writeAppError(stderr, "could not resolve the KajiCode config directory", exitCrash)
	}
	path, err := config.WriteEnvFile(configDir, pairs)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	// Make the new values live in THIS process too, so a tool call later in the
	// same process (e.g. the long-lived ACP server) sees them without a restart.
	for k, v := range pairs {
		_ = os.Setenv(k, v)
	}
	if _, err := fmt.Fprintf(stdout, "Web search configured for %s.\nWrote %s\n", provider.ID, path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runWebSearchRemove(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	keys := []string{"KAJICODE_WEBSEARCH_PROVIDER", "KAJICODE_WEBSEARCH_BASE_URL"}
	for _, p := range tools.WebSearchProviders() {
		if p.EnvVar != "" {
			keys = append(keys, p.EnvVar)
		}
		if p.BaseURLEnv != "" {
			keys = append(keys, p.BaseURLEnv)
		}
	}
	configDir := webSearchConfigDir(deps)
	if configDir == "" {
		return writeAppError(stderr, "could not resolve the KajiCode config directory", exitCrash)
	}
	removed, err := config.RemoveEnvFileKeys(configDir, keys)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if removed {
		// Also unset in this process so a later tool call this process makes sees it.
		for _, k := range keys {
			_ = os.Unsetenv(k)
		}
	}
	if _, err := fmt.Fprintln(stdout, "Web search credentials removed."); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func webSearchProviderByID(id string) (tools.WebSearchProvider, bool) {
	for _, p := range tools.WebSearchProviders() {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return tools.WebSearchProvider{}, false
}

// webSearchConfigDir is the directory holding config.json (and thus the env
// fallback file). Reuses the resolved user config path, falling back to the
// OS config dir.
func webSearchConfigDir(deps appDeps) string {
	if deps.userConfigPath != nil {
		if p, err := deps.userConfigPath(); err == nil && strings.TrimSpace(p) != "" {
			return filepath.Dir(p)
		}
	}
	if dir, err := config.UserConfigDir(); err == nil {
		return filepath.Join(dir, "kajicode")
	}
	return ""
}
