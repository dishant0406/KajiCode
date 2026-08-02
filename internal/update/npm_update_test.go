package update

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNpmGlobalPrefixForExecutableHandlesPlatformPayload(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "node-v22.20.0")
	executable := filepath.Join(prefix, "lib", "node_modules", "@dishant0406", "kajicode", "node_modules", "@dishant0406", "kajicode-darwin-arm64", "kajicode")

	got, ok := npmGlobalPrefixForExecutable(executable)
	if !ok {
		t.Fatal("expected npm prefix to be detected")
	}
	if got != prefix {
		t.Fatalf("prefix = %q, want %q", got, prefix)
	}
}

func TestNpmGlobalPrefixForExecutableHandlesDownloadedWrapperBinary(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "node-v22.20.0")
	executable := filepath.Join(prefix, "lib", "node_modules", "@dishant0406", "kajicode", "kajicode")

	got, ok := npmGlobalPrefixForExecutable(executable)
	if !ok {
		t.Fatal("expected npm prefix to be detected")
	}
	if got != prefix {
		t.Fatalf("prefix = %q, want %q", got, prefix)
	}
}

func TestApplyNpmUpdateUsesOwningPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell npm stub")
	}
	prefix := t.TempDir()
	npmPath := filepath.Join(prefix, "bin", "npm")
	argsPath := filepath.Join(t.TempDir(), "npm-args.txt")
	if err := os.MkdirAll(filepath.Dir(npmPath), 0o755); err != nil {
		t.Fatalf("MkdirAll npm bin: %v", err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile npm stub: %v", err)
	}

	executable := filepath.Join(prefix, "lib", "node_modules", "@dishant0406", "kajicode", "node_modules", "@dishant0406", "kajicode-darwin-arm64", "kajicode")
	if err := applyNpmUpdate(context.Background(), executable); err != nil {
		t.Fatalf("applyNpmUpdate returned error: %v", err)
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("ReadFile npm args: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"install", "-g", "--prefix", prefix, npmPackageName + "@latest"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("npm args = %#v, want %#v", got, want)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
