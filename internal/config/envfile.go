package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// envFileName is the KajiCode-owned environment fallback file stored under the
// user config directory (~/.config/kajicode on macOS/Linux, via UserConfigDir).
// It holds `KEY=VALUE` lines that are loaded at process startup so web-search
// (and any other) env vars configured through /web-search are present even when
// the user's shell never sourced them. Values are plaintext, matching the
// user-chosen fallback model (see docs/web-search-command-plan.md).
const envFileName = "envfile"

// envGoGuard and envStopGuard delimit the KajiCode-managed region of the file.
// Keeping a delimited block lets /web-search replace keys idempotently without
// rewriting user-authored lines in the same file.
const (
	envGoGuard   = "# >>> kajicode env >>>"
	envStopGuard = "# <<< kajicode env <<<"
)

// EnvFilePath returns the path of the env fallback file for the given config
// directory (dir is usually the directory containing config.json / the user
// config dir). Empty on error so callers can skip gracefully.
func EnvFilePath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, envFileName)
}

// LoadEnvFile reads the KajiCode env fallback file under dir (if present) and
// calls os.Setenv for each KEY=VALUE, but ONLY for keys not already set in the
// process environment — a live env var always beats the stored fallback. It is a
// no-op when the file is absent. Intended to be called once at startup.
func LoadEnvFile(dir string) error {
	path := EnvFilePath(dir)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read env fallback %s: %w", path, err)
	}
	pairs, err := parseEnvFile(data)
	if err != nil {
		return fmt.Errorf("parse env fallback %s: %w", path, err)
	}
	for key, value := range pairs {
		if os.Getenv(key) != "" {
			continue // live env wins
		}
		_ = os.Setenv(key, value)
	}
	return nil
}

// WriteEnvFile persists the given KEY=VALUE pairs into the KajiCode env fallback
// file under dir, replacing only the KajiCode-managed region and preserving any
// surrounding content. Empty values drop the key. Returns the written path.
//
// An empty dir (config not yet resolved) is a documented no-op returning ("", nil)
// so callers don't need to special-case first-run.
func WriteEnvFile(dir string, pairs map[string]string) (string, error) {
	path := EnvFilePath(dir)
	if path == "" {
		return "", nil
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read env fallback %s: %w", path, err)
	}
	block := buildEnvBlock(pairs)
	out, err := replaceEnvBlock(string(existing), block)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create env fallback dir: %w", err)
	}
	// 0600: the file holds plaintext secrets.
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return "", fmt.Errorf("write env fallback %s: %w", path, err)
	}
	return path, nil
}

// RemoveEnvFileKeys removes the given keys from the env fallback file under dir,
// reporting whether any line was removed. Absent file is a no-op.
func RemoveEnvFileKeys(dir string, keys []string) (bool, error) {
	path := EnvFilePath(dir)
	if path == "" {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read env fallback %s: %w", path, err)
	}
	pairs, err := parseEnvFile(data)
	if err != nil {
		return false, fmt.Errorf("parse env fallback %s: %w", path, err)
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[strings.TrimSpace(k)] = true
	}
	changed := false
	for k := range pairs {
		if want[k] {
			delete(pairs, k)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	block := buildEnvBlock(pairs)
	out, err := replaceEnvBlock(string(data), block)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return false, fmt.Errorf("write env fallback %s: %w", path, err)
	}
	return true, nil
}

// buildEnvBlock renders the guarded, idempotent env block for the given pairs.
// Keys are sorted for deterministic output.
func buildEnvBlock(pairs map[string]string) string {
	if len(pairs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(envGoGuard + "\n")
	for _, k := range keys {
		v := strings.TrimSpace(pairs[k])
		if v == "" {
			continue
		}
		b.WriteString(k + "=" + envValueQuoted(v) + "\n")
	}
	b.WriteString(envStopGuard + "\n")
	return b.String()
}

// envValueQuoted single-quotes a value for shell-safe sourcing, escaping any
// single quotes the value itself contains.
func envValueQuoted(value string) string {
	escaped := strings.ReplaceAll(value, "'", "'\"'\"'")
	return "'" + escaped + "'"
}

// replaceEnvBlock swaps the KajiCode-managed region of src with block. The first
// region (from envGoGuard to envStopGuard inclusive) is replaced; content before
// and after is preserved. When no region exists, block is appended after a blank
// line keep-alive at the end.
func replaceEnvBlock(src string, block string) (string, error) {
	start := strings.Index(src, envGoGuard)
	stop := strings.Index(src, envStopGuard)
	if start >= 0 && stop >= 0 && stop > start {
		before := src[:start]
		after := src[stop+len(envStopGuard):]
		// Collapse to a single trailing newline before the block whose content
		// follows the guard lines, and drop leading blank lines after the block.
		out := strings.TrimRight(before, "\n")
		if block == "" {
			// Removing the whole region: rejoin remaining content tidily.
			remainder := strings.TrimLeft(after, "\n")
			if remainder == "" {
				return strings.TrimRight(out, "\n") + "\n", nil
			}
			return out + "\n" + remainder, nil
		}
		return out + "\n" + strings.TrimRight(block, "\n") + strings.TrimLeft(after, "\n"), nil
	}
	// No existing region: append (with a separating newline if the file isn't empty).
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

// parseEnvFile parses KEY=VALUE lines within the guarded region (or the whole
// file when no guard region exists). Comment lines (starting with #) are ignored.
func parseEnvFile(data []byte) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(string(data), "\n")
	inBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == envGoGuard {
			inBlock = true
			continue
		}
		if line == envStopGuard {
			inBlock = false
			continue
		}
		if !inBlock {
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("malformed env line: %q", line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("env line with empty key: %q", line)
		}
		out[key] = unquoteEnvValue(value)
	}
	return out, nil
}

// unquoteEnvValue strips surrounding single/double quotes from a value.
func unquoteEnvValue(value string) string {
	v := strings.TrimSpace(value)
	if len(v) >= 2 {
		if (v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
