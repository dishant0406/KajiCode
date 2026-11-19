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

// npm exits 0 when it installs the old version again, so the upgrade must check
// what the installed binary reports before it claims success.
func TestVerifyNpmUpdateChecksInstalledVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell binary stub")
	}
	binary := filepath.Join(t.TempDir(), "kajicode")
	script := "#!/bin/sh\necho 'BANNER ART'\necho\necho 'kajicode 0.8.5'\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile binary stub: %v", err)
	}

	if err := verifyNpmUpdate(context.Background(), binary, "0.8.5"); err != nil {
		t.Fatalf("matching version returned error: %v", err)
	}
	if err := verifyNpmUpdate(context.Background(), binary, "v0.8.5"); err != nil {
		t.Fatalf("matching version with a v prefix returned error: %v", err)
	}
	err := verifyNpmUpdate(context.Background(), binary, "0.8.6")
	if err == nil || !strings.Contains(err.Error(), "npm installed 0.8.5, not 0.8.6") {
		t.Fatalf("stale install error = %v, want it to name both versions", err)
	}
	if err := verifyNpmUpdate(context.Background(), binary+"-missing", "0.8.6"); err == nil {
		t.Fatal("a missing binary must be an error, not a success")
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// npmStub writes a POSIX-shell npm into a fake global prefix and a fake
// installed kajicode binary that prints installedVersion. It returns the path
// of the installed binary and of a file that logs every npm call. An empty
// viewOutput makes `npm view` fail.
func npmStub(t *testing.T, viewOutput string, installedVersion string) (executable string, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell stubs")
	}
	prefix := t.TempDir()
	logPath = filepath.Join(t.TempDir(), "npm-calls.txt")
	viewBranch := "exit 1"
	if viewOutput != "" {
		viewBranch = "echo " + shellQuote(viewOutput)
	}
	script := "#!/bin/sh\necho \"$@\" >> " + shellQuote(logPath) + "\n" +
		"case \"$1\" in\n  view) " + viewBranch + " ;;\n  *) ;;\nesac\n"
	npmPath := filepath.Join(prefix, "bin", "npm")
	if err := os.MkdirAll(filepath.Dir(npmPath), 0o755); err != nil {
		t.Fatalf("MkdirAll npm bin: %v", err)
	}
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile npm stub: %v", err)
	}
	executable = filepath.Join(prefix, "lib", "node_modules", "@dishant0406", "kajicode", "kajicode")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll install dir: %v", err)
	}
	binary := "#!/bin/sh\necho 'kajicode " + installedVersion + "'\n"
	if err := os.WriteFile(executable, []byte(binary), 0o755); err != nil {
		t.Fatalf("WriteFile binary stub: %v", err)
	}
	return executable, logPath
}

func npmCalls(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile npm call log: %v", err)
	}
	return string(data)
}

// An npm install asks npm which version is newest. It must not need the GitHub
// API, which allows only 60 requests an hour and blocked `kajicode upgrade`.
func TestApplyNpmUpdatesWithoutGitHub(t *testing.T) {
	executable, logPath := npmStub(t, "0.8.7", "0.8.7")

	result, err := applyNpm(context.Background(), Options{CurrentVersion: "0.8.6"}, executable)
	if err != nil {
		t.Fatalf("applyNpm returned error: %v", err)
	}
	if !result.Applied || result.LatestVersion != "0.8.7" || result.CurrentVersion != "0.8.6" ||
		result.Message != "updated via npm to 0.8.7" || result.InstallMethod != InstallMethodNpm {
		t.Fatalf("unexpected result: %#v", result)
	}
	calls := npmCalls(t, logPath)
	if !strings.Contains(calls, "view "+npmPackageName+" version --prefer-online") || !strings.Contains(calls, "install -g") {
		t.Fatalf("npm calls = %q, want a fresh view and then an install", calls)
	}
}

func TestApplyNpmDoesNotInstallWhenAlreadyUpToDate(t *testing.T) {
	executable, logPath := npmStub(t, "0.8.6", "0.8.6")

	result, err := applyNpm(context.Background(), Options{CurrentVersion: "0.8.6"}, executable)
	if err != nil {
		t.Fatalf("applyNpm returned error: %v", err)
	}
	if result.Applied || result.UpdateAvailable || result.Message != "already up to date" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if calls := npmCalls(t, logPath); strings.Contains(calls, "install") {
		t.Fatalf("npm calls = %q, want no install", calls)
	}
}

func TestApplyNpmTreatsASourceBuildAsOlderThanAnyRelease(t *testing.T) {
	executable, _ := npmStub(t, "0.8.7", "0.8.7")

	result, err := applyNpm(context.Background(), Options{CurrentVersion: "dev"}, executable)
	if err != nil || !result.Applied || result.CurrentVersion != "0.0.0" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestApplyNpmFailsClearlyWhenNpmCannotAnswer(t *testing.T) {
	executable, logPath := npmStub(t, "", "0.8.6")

	_, err := applyNpm(context.Background(), Options{CurrentVersion: "0.8.6"}, executable)
	if err == nil || !strings.Contains(err.Error(), "npm view "+npmPackageName+" version") {
		t.Fatalf("error = %v, want it to name the failed npm view", err)
	}
	if calls := npmCalls(t, logPath); strings.Contains(calls, "install") {
		t.Fatalf("npm calls = %q, want no install after a failed lookup", calls)
	}
}

// The check after the install still catches an install that did not change.
func TestApplyNpmReportsAnInstallThatDidNotChangeTheVersion(t *testing.T) {
	executable, _ := npmStub(t, "0.8.7", "0.8.6")

	_, err := applyNpm(context.Background(), Options{CurrentVersion: "0.8.6"}, executable)
	if err == nil || !strings.Contains(err.Error(), "npm installed 0.8.6, not 0.8.7") {
		t.Fatalf("error = %v, want it to name both versions", err)
	}
}

// Only a plain npm install skips GitHub. A caller that points the update at its
// own release source keeps the GitHub path.
func TestUsesNpmRegistry(t *testing.T) {
	fetch := func(context.Context, string) (Release, error) { return Release{}, nil }
	cases := []struct {
		name    string
		method  InstallMethod
		options Options
		want    bool
	}{
		{"plain npm install", InstallMethodNpm, Options{}, true},
		{"standalone install", InstallMethodStandalone, Options{}, false},
		{"custom endpoint", InstallMethodNpm, Options{Endpoint: "https://example.test/latest"}, false},
		{"custom repository", InstallMethodNpm, Options{Repository: "someone/fork"}, false},
		{"custom fetcher", InstallMethodNpm, Options{Fetch: fetch}, false},
	}
	for _, tc := range cases {
		if got := usesNpmRegistry(tc.method, tc.options); got != tc.want {
			t.Fatalf("%s: usesNpmRegistry = %v, want %v", tc.name, got, tc.want)
		}
	}
	t.Setenv("KAJICODE_UPDATE_RELEASE_URL", "https://example.test/latest")
	if usesNpmRegistry(InstallMethodNpm, Options{}) {
		t.Fatal("KAJICODE_UPDATE_RELEASE_URL must keep the GitHub path")
	}
}
