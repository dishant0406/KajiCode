package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

// learningStatusJSON is the resolved learning config for `status --json`. It
// uses explicit bools so the effective defaults always print (unlike
// LearningConfig.MarshalJSON, which omits fields that read as their defaults).
type learningStatusJSON struct {
	Enabled      bool  `json:"enabled"`
	TurnInterval int   `json:"turnInterval"`
	Compact      bool  `json:"compact"`
	CooldownMs   int64 `json:"cooldownMs"`
}

// runLearningConfig controls KajiCode's self-learning (perpetual memory)
// auto-review config. Subcommands: status (default), set <key> <value>, and the
// on/off shorthands that map to `set enabled <value>`.
func runLearningConfig(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return runLearningStatus(nil, stdout, stderr, deps)
	}
	switch args[0] {
	case "-h", "--help", "help":
		return writeLearningHelp(stdout)
	case "status", "show":
		return runLearningStatus(args[1:], stdout, stderr, deps)
	case "set":
		return runLearningSet(args[1:], stdout, stderr, deps)
	case "on", "off":
		return runLearningSet([]string{"enabled", args[0]}, stdout, stderr, deps)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown learning command %q", args[0]))
	}
}

func runLearningStatus(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		case "-h", "--help", "help":
			return writeLearningHelp(stdout)
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown learning status flag %q", arg))
		}
	}
	learning := config.DefaultLearningConfig()
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err == nil {
		learning = resolved.Learning.Effective()
	} else if !errors.Is(err, os.ErrNotExist) {
		// A real config error (malformed file, etc.) is surfaced; a missing
		// user config falls through to the production defaults.
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	if jsonOut {
		// Emit explicit resolved values rather than the raw struct, whose
		// MarshalJSON omits fields their defaults make indistinguishable from
		// "unset" when nothing was explicitly configured.
		out := learningStatusJSON{
			Enabled:      learning.IsEnabled(),
			TurnInterval: learning.TurnInterval,
			Compact:      learning.IsCompactEnabled(),
			CooldownMs:   learning.CooldownMs,
		}
		if err := writePrettyJSON(stdout, out); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatLearning(learning)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runLearningSet(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeLearningHelp(stdout)
	}
	if len(args) != 2 {
		return writeExecUsageError(stderr, "usage: kajicode learning set <key> <value> (keys: enabled, turnInterval, compact, cooldownMs)")
	}
	key := strings.TrimSpace(args[0])
	value := strings.TrimSpace(args[1])
	path, err := deps.userConfigPath()
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	cfg, err := config.SetLearningConfig(path, key, value)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	learning := cfg.Learning.Effective()
	if _, err := fmt.Fprintf(stdout, "Saved %s=%s. Learning: %s\n", key, displayLearningValue(key, value), formatLearningInline(learning)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// displayLearningValue renders the accepted shorthand verbatim for enabled/compact
// so an `on`/`off` input round-trips in the confirmation line.
func displayLearningValue(key, value string) string {
	if key == "enabled" || key == "compact" {
		switch strings.ToLower(value) {
		case "on", "true", "1", "yes":
			return "on"
		case "off", "false", "0", "no":
			return "off"
		}
	}
	return value
}

func formatLearning(learning config.LearningConfig) string {
	lines := []string{"Learning"}
	lines = append(lines, fmt.Sprintf("enabled: %s", onOff(learning.IsEnabled())))
	lines = append(lines, fmt.Sprintf("turnInterval: %d", learning.TurnInterval))
	lines = append(lines, fmt.Sprintf("compact: %s", onOff(learning.IsCompactEnabled())))
	lines = append(lines, fmt.Sprintf("cooldownMs: %d", learning.CooldownMs))
	return strings.Join(lines, "\n")
}

func formatLearningInline(learning config.LearningConfig) string {
	return fmt.Sprintf("enabled=%s turnInterval=%d compact=%s cooldownMs=%d",
		onOff(learning.IsEnabled()),
		learning.TurnInterval,
		onOff(learning.IsCompactEnabled()),
		learning.CooldownMs,
	)
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func writeLearningHelp(w io.Writer) int {
	_, err := fmt.Fprint(w, `Usage:
  kajicode learning [status] [--json]
  kajicode learning set <key> <value>
  kajicode learning on|off

Inspect and configure KajiCode's self-learning (perpetual memory) auto-review.

Subcommands:
  status     Show the effective learning config (default)
  set        Set one learning key:
               enabled      on|off
               turnInterval <int>   (>= 0; 0 resets to default)
               compact      on|off
               cooldownMs   <int>   (>= 0; 0 resets to default)
  on|off     Shorthand for `+"`set enabled on|off`"+`

Flags:
      --json      Print JSON summary (status)
  -h, --help      Show this help
`)
	if err != nil {
		return exitCrash
	}
	return exitSuccess
}
