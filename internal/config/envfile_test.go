package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileWriteLoadRemove(t *testing.T) {
	dir := t.TempDir()
	path := EnvFilePath(dir)
	if path == "" {
		t.Fatal("EnvFilePath returned empty")
	}

	// Write initial pairs.
	written, err := WriteEnvFile(dir, map[string]string{
		"EXA_API_KEY": "exa-123",
		"FOO":         "bar",
		"EMPTY":       "", // should be dropped
	})
	if err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	if written != path {
		t.Fatalf("written = %q, want %q", written, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	// Idempotent rewrite: replacing one key must not duplicate lines.
	if _, err := WriteEnvFile(dir, map[string]string{"EXA_API_KEY": "exa-456", "FOO": "bar"}); err != nil {
		t.Fatalf("WriteEnvFile round2: %v", err)
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "EXA_API_KEY=") != 1 {
		t.Fatalf("expected single EXA_API_KEY line, got:\n%s", data)
	}

	// Load into a temp-free env: use unset keys.
	os.Unsetenv("EXA_API_KEY_LOAD_TEST")
	os.Unsetenv("FOO_LOAD_TEST")
	defer os.Unsetenv("EXA_API_KEY_LOAD_TEST")
	defer os.Unsetenv("FOO_LOAD_TEST")
	if _, err := WriteEnvFile(dir, map[string]string{
		"EXA_API_KEY_LOAD_TEST": "k",
		"FOO_LOAD_TEST":         "v",
	}); err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if os.Getenv("EXA_API_KEY_LOAD_TEST") != "k" {
		t.Errorf("EXA_API_KEY_LOAD_TEST = %q, want k", os.Getenv("EXA_API_KEY_LOAD_TEST"))
	}
	if os.Getenv("FOO_LOAD_TEST") != "v" {
		t.Errorf("FOO_LOAD_TEST = %q, want v", os.Getenv("FOO_LOAD_TEST"))
	}

	// Live env wins over stored fallback.
	os.Setenv("EXA_API_KEY_LOAD_TEST", "live")
	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if os.Getenv("EXA_API_KEY_LOAD_TEST") != "live" {
		t.Errorf("live env should win, got %q", os.Getenv("EXA_API_KEY_LOAD_TEST"))
	}

	// Remove keys.
	removed, err := RemoveEnvFileKeys(dir, []string{"EXA_API_KEY_LOAD_TEST"})
	if err != nil {
		t.Fatalf("RemoveEnvFileKeys: %v", err)
	}
	if !removed {
		t.Fatal("RemoveEnvFileKeys should report removal")
	}
	os.Unsetenv("EXA_API_KEY_LOAD_TEST")
	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("LoadEnvFile after remove: %v", err)
	}
	if os.Getenv("EXA_API_KEY_LOAD_TEST") != "" {
		t.Errorf("removed key still loaded: %q", os.Getenv("EXA_API_KEY_LOAD_TEST"))
	}
	// Second remove is a no-op.
	again, err := RemoveEnvFileKeys(dir, []string{"EXA_API_KEY_LOAD_TEST"})
	if err != nil || again {
		t.Fatalf("second remove: again=%v err=%v, want false/nil", again, err)
	}
}

func TestLoadEnvFileAbsentIsNoop(t *testing.T) {
	dir := t.TempDir() // no envfile
	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("absent file should not error, got %v", err)
	}
	if _, err := RemoveEnvFileKeys(dir, []string{"EXA_API_KEY"}); err != nil {
		t.Fatalf("absent remove should not error, got %v", err)
	}
}

func TestEnvFilePathEmptyDir(t *testing.T) {
	if got := EnvFilePath(""); got != "" {
		t.Fatalf("EnvFilePath('') = %q, want empty", got)
	}
	// WriteEnvFile with empty dir is a no-op.
	if got, err := WriteEnvFile("", map[string]string{"X": "y"}); err != nil || got != "" {
		t.Fatalf("WriteEnvFile('') = %q, err %v; want empty/nil", got, err)
	}
}

func TestEnvFilePreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := EnvFilePath(dir)
	// Pre-seed the file with a user comment + a non-managed line.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# user note\nexport FOO=keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteEnvFile(dir, map[string]string{"EXA_API_KEY": "k"}); err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "user note") || !strings.Contains(s, "FOO=keep") {
		t.Errorf("user content lost:\n%s", s)
	}
	if !strings.Contains(s, envGoGuard) || !strings.Contains(s, envStopGuard) {
		t.Errorf("guard region missing:\n%s", s)
	}
}

func TestEnvValueQuotingPreservesSingleQuotes(t *testing.T) {
	got := envValueQuoted("a'b")
	// We can't fully assert shell behavior here, only that quotes are escaped.
	if !strings.Contains(got, "'\"'\"'") {
		t.Errorf("single quote not escaped: %q", got)
	}
}
