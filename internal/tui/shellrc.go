package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Shell profile (rc) integration for the /web-search command. KajiCode persists
// provider API keys as literal `export KEY=…` lines inside a guarded block in the
// user's shell rc file so they survive restarts and are picked up by future
// shells. The equivalent env-fallback file (internal/config envfile) covers the
// case where the rc is never sourced for the launching process; both write sites
// are on by default.

// Shell rc block guards. Everything between the two markers is KajiCode-owned and
// rewritten idempotently; surrounding user content is left untouched.
const (
	rcGoGuard   = "### >>> kajicode admin start >>>"
	rcStopGuard = "### <<< kajicode admin end <<<"
)

// ErrNoShellRC is returned when no shell rc could be identified for the user.
var ErrNoShellRC = errors.New("no shell rc file detected")

// detectShellRC returns the absolute path of the current user's shell startup
// file based on $SHELL and HOME, or ErrNoShellRC. It prefers zsh/bash profile
// files and falls back through common candidates so non-interactive login shells
// still pick up exports.
func detectShellRC() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", ErrNoShellRC
	}
	shell := strings.ToLower(filepath.Base(strings.TrimSpace(os.Getenv("SHELL"))))
	var candidates []string
	switch shell {
	case "zsh":
		candidates = []string{".zshrc", ".zprofile"}
	case "bash":
		candidates = []string{".bashrc", ".bash_profile"}
	case "sh", "dash":
		candidates = []string{".profile"}
	case "fish":
		candidates = []string{".config/fish/config.fish"}
	default:
		// Unknown shell: try the union of common names, in a sensible priority.
		candidates = []string{".zshrc", ".bashrc", ".bash_profile", ".profile"}
	}
	for _, c := range candidates {
		p := filepath.Join(home, c)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNoShellRC
}

// writeEnvToRC inserts the given KEY=VALUE exports (as export literals) into the
// shell rc at rcPath, inside the guarded region, creating the file if needed.
// Values with special characters are single-quoted for shell-safe sourcing.
func writeEnvToRC(rcPath string, pairs map[string]string) error {
	if rcPath == "" {
		return ErrNoShellRC
	}
	data := ""
	if b, err := os.ReadFile(rcPath); err == nil {
		data = string(b)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read shell rc %s: %w", rcPath, err)
	}
	block := buildRCBlock(pairs)
	out, err := replaceRCBlock(data, block)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o700); err != nil {
		return fmt.Errorf("create shell rc dir: %w", err)
	}
	if err := os.WriteFile(rcPath, []byte(out), 0o600); err != nil {
		return fmt.Errorf("write shell rc %s: %w", rcPath, err)
	}
	return nil
}

// buildRCBlock renders the guarded export block. Keys are sorted for
// determinism. A nil/empty map returns the empty string (removal case).
func buildRCBlock(pairs map[string]string) string {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	// sort.Strings(keys) — deterministic output.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	var b strings.Builder
	b.WriteString(rcGoGuard + "\n")
	for _, k := range keys {
		v := strings.TrimSpace(pairs[k])
		if v == "" {
			continue
		}
		b.WriteString("export " + k + "=" + rcValueQuoted(v) + "\n")
	}
	b.WriteString(rcStopGuard + "\n")
	return b.String()
}

// rcValueQuoted single-quotes a value for shell sourcing, escaping embedded
// single quotes.
func rcValueQuoted(value string) string {
	escaped := strings.ReplaceAll(value, "'", "'\"'\"'")
	return "'" + escaped + "'"
}

// replaceRCBlock swaps the KajiCode guarded region of src with block, preserving
// surrounding content. Mirrors config.envfile.replaceEnvBlock semantics.
func replaceRCBlock(src string, block string) (string, error) {
	start := strings.Index(src, rcGoGuard)
	stop := strings.Index(src, rcStopGuard)
	if start >= 0 && stop >= 0 && stop > start {
		before := src[:start]
		after := src[stop+len(rcStopGuard):]
		out := strings.TrimRight(before, "\n")
		remainder := strings.TrimLeft(after, "\n")
		if block == "" {
			if remainder == "" {
				return strings.TrimRight(out, "\n") + "\n", nil
			}
			return out + "\n" + remainder, nil
		}
		return out + "\n" + strings.TrimRight(block, "\n") + remainder, nil
	}
	trimmed := strings.TrimRight(src, "\n")
	switch {
	case trimmed == "":
		return block, nil
	case block == "":
		return trimmed + "\n", nil
	default:
		return trimmed + "\n\n" + strings.TrimRight(block, "\n") + "\n", nil
	}
}

// sourceRC best-effort sources the rc file in the current shell so newly
// exported vars are live immediately. It is non-fatal: if sourcing fails
// (interactive prompts, missing shell), the env-fallback file still covers the
// process. Fish is only sourced when the shell is fish.
func sourceRC(rcPath string) error {
	if rcPath == "" {
		return ErrNoShellRC
	}
	shell := strings.ToLower(strings.TrimSpace(os.Getenv("SHELL")))
	var cmd *exec.Cmd
	switch {
	case strings.HasSuffix(shell, "fish"):
		cmd = exec.Command("fish", "-c", "source "+quotePath(rcPath))
	default:
		cmd = exec.Command("sh", "-c", ". "+quotePath(rcPath))
	}
	return cmd.Run()
}

// quotePath shell-quotes a path for embedding in a source command.
func quotePath(p string) string {
	return "'" + strings.ReplaceAll(p, "'", "'\"'\"'") + "'"
}
