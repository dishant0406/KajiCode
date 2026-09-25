package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
)

// learningStatusJSON is the resolved learning config for `status --json`. It
// uses explicit bools so the effective defaults always print (unlike
// LearningConfig.MarshalJSON, which omits fields that read as their defaults).
type learningStatusJSON struct {
	Enabled        bool  `json:"enabled"`
	DebounceMs     int64 `json:"debounceMs"`
	Compact        bool  `json:"compact"`
	PruneAfterDays int   `json:"pruneAfterDays"`
	MaxEntries     int   `json:"maxEntries"`
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
	case "revert", "rollback":
		return runLearningRevert(stdout, stderr, deps)
	case "prune":
		return runLearningPrune(stdout, stderr, deps)
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
			Enabled:        learning.IsEnabled(),
			DebounceMs:     learning.DebounceMs,
			Compact:        learning.IsCompactEnabled(),
			PruneAfterDays: learning.PruneAfterDays,
			MaxEntries:     learning.MaxEntries,
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
		return writeExecUsageError(stderr, "usage: kajicode learning set <key> <value> (keys: enabled, debounceMs, compact, pruneAfterDays, maxEntries)")
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
	lines = append(lines, fmt.Sprintf("debounceMs: %d", learning.DebounceMs))
	lines = append(lines, fmt.Sprintf("compact: %s", onOff(learning.IsCompactEnabled())))
	lines = append(lines, fmt.Sprintf("pruneAfterDays: %d", learning.PruneAfterDays))
	lines = append(lines, fmt.Sprintf("maxEntries: %d", learning.MaxEntries))
	return strings.Join(lines, "\n")
}

func formatLearningInline(learning config.LearningConfig) string {
	return fmt.Sprintf("enabled=%s debounceMs=%d compact=%s pruneAfterDays=%d maxEntries=%d",
		onOff(learning.IsEnabled()),
		learning.DebounceMs,
		onOff(learning.IsCompactEnabled()),
		learning.PruneAfterDays,
		learning.MaxEntries,
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

Inspect and configure KajiCode's self-learning (perpetual memory).

Learning is event-driven: a pass runs when the agent does something worth
learning from (a tool failure that was fixed, a user correction, a repeat of a
recorded workflow) or when a run finishes.

Subcommands:
  status     Show the effective learning config (default)
  set        Set one learning key:
               enabled      on|off
               debounceMs      <int>   (>= 0; minimum gap between auto passes)
               compact         on|off  (learn after a context compaction)
               pruneAfterDays  <int>   (>= 0; drop unreinforced entries)
               maxEntries      <int>   (>= 0; cap per scope)
  on|off     Shorthand for `+"`set enabled on|off`"+`
  revert     Undo the most recent automatic learning pass
  prune      Apply decay + caps now, shrinking harness_state.json

Flags:
      --json      Print JSON summary (status)
  -h, --help      Show this help
`)
	if err != nil {
		return exitCrash
	}
	return exitSuccess
}

// runLearningPrune applies decay and caps to the project and global stores on
// demand, so an already-bloated harness_state.json shrinks without waiting for
// the next automatic pass. It uses the effective learning config (pruneAfterDays
// / maxEntries) and also caps the refinement history. Session stores are
// transient and are pruned on every automatic pass, so they are not scanned here.
func runLearningPrune(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	learning := config.DefaultLearningConfig()
	if resolved, rerr := deps.resolveConfig(workspaceRoot, config.Overrides{}); rerr == nil {
		learning = resolved.Learning.Effective()
	}
	now := time.Now()
	removed := harness.NewStore(harness.StoreOptions{Dir: harness.ProjectDir(workspaceRoot), Scope: harness.ScopeProject}).
		PruneStale(learning.PruneAfterDays, learning.MaxEntries, now)
	removed += harness.NewStore(harness.StoreOptions{Dir: harness.GlobalDir(nil), Scope: harness.ScopeGlobal}).
		PruneStale(learning.PruneAfterDays, learning.MaxEntries, now)
	noun := "entries"
	if removed == 1 {
		noun = "entry"
	}
	_, _ = fmt.Fprintf(stdout, "Pruned %d %s; refinement history bounded to %d. Reclaimed project + global harness_state.json.\n",
		removed, noun, harness.MaxRefinements)
	return exitSuccess
}

// runLearningRevert undoes the most recent automatic learning pass by inverting
// the outcomes recorded on its refinement event. It is the recovery path for a
// bad lesson: the store keeps a self-contained rollback record, so no separate
// backup is needed.
func runLearningRevert(stdout io.Writer, stderr io.Writer, deps appDeps) int {
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	root := harness.ProjectDir(workspaceRoot)
	store := harness.NewStore(harness.StoreOptions{Dir: root, Scope: harness.ScopeProject})
	state, err := store.Load()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	outcomes, ok := harness.LatestRollback(state)
	if !ok {
		_, _ = fmt.Fprintln(stdout, "No learning refinement to revert.")
		return exitSuccess
	}
	result := harness.RollbackInverts(store, harness.RollbackOptions{Outcomes: outcomes})
	reverted := 0
	for _, outcome := range outcomes {
		if outcome.Applied {
			reverted++
		}
	}
	if len(result.Errors) > 0 {
		_, _ = fmt.Fprintf(stdout, "Reverted %d change(s) with %d issue(s): %s\n", reverted, len(result.Errors), strings.Join(result.Errors, "; "))
		return exitSuccess
	}
	_, _ = fmt.Fprintf(stdout, "Reverted %d change(s) from refinement %s.\n", reverted, result.RefinementID)
	return exitSuccess
}
