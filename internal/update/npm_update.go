package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// npmViewTimeout bounds the registry lookup for the newest version when the
// caller sets no timeout. npm starts slower than a plain HTTP request.
const npmViewTimeout = 30 * time.Second

// usesNpmRegistry reports whether an update should ask npm, not the GitHub API,
// which version is newest. An npm install is updated by npm, so npm is the one
// source that agrees with what the install will get, and it has no 60 requests
// an hour limit. A caller that points the update at its own release source
// (endpoint, repository, fetcher, or KAJICODE_UPDATE_RELEASE_URL) keeps GitHub.
func usesNpmRegistry(method InstallMethod, options Options) bool {
	return method == InstallMethodNpm &&
		options.Fetch == nil &&
		strings.TrimSpace(options.Endpoint) == "" &&
		strings.TrimSpace(options.Repository) == "" &&
		strings.TrimSpace(os.Getenv("KAJICODE_UPDATE_RELEASE_URL")) == ""
}

// applyNpm updates an npm install without the GitHub API: it asks npm for the
// newest version, installs it only when it is newer, then checks what was
// installed (see verifyNpmUpdate).
func applyNpm(ctx context.Context, options Options, executablePath string) (ApplyResult, error) {
	prefix, hasPrefix := npmGlobalPrefixForExecutable(executablePath)
	npmPath, err := npmExecutable(prefix, hasPrefix)
	if err != nil {
		return ApplyResult{}, err
	}
	latest, err := npmLatestVersion(ctx, npmPath, options.Timeout)
	if err != nil {
		return ApplyResult{}, err
	}
	current, err := normalizeVersionTag(firstNonEmpty(options.CurrentVersion, "0.0.0"))
	if err != nil {
		current = "0.0.0" // a source build ("dev") counts as older than any release
	}
	newer, err := CompareSemver(latest, current)
	if err != nil {
		return ApplyResult{}, err
	}
	result := Result{
		CurrentVersion:  current,
		LatestVersion:   latest,
		TagName:         "v" + latest,
		ReleaseURL:      fmt.Sprintf("https://github.com/%s/releases/tag/v%s", DefaultRepository, latest),
		UpdateAvailable: newer > 0,
	}
	if !result.UpdateAvailable {
		return ApplyResult{Result: result, Message: "already up to date"}, nil
	}
	if err := applyNpmUpdate(ctx, executablePath); err != nil {
		return ApplyResult{}, err
	}
	if err := verifyNpmUpdate(ctx, executablePath, latest); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{
		Result:        result,
		Applied:       true,
		InstallMethod: InstallMethodNpm,
		BinaryPath:    executablePath,
		Message:       fmt.Sprintf("updated via npm to %s", latest),
	}, nil
}

// npmLatestVersion asks npm for the version the `latest` dist-tag points at.
func npmLatestVersion(ctx context.Context, npmPath string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = npmViewTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// --prefer-online skips npm's cached copy of the package info, which can be a
	// few minutes old right after a release and would hide the new version.
	output, err := exec.CommandContext(ctx, npmPath, "view", npmPackageName, "version", "--prefer-online").Output()
	if err != nil {
		return "", fmt.Errorf("npm view %s version: %w", npmPackageName, err)
	}
	version, err := normalizeVersionTag(strings.TrimSpace(string(output)))
	if err != nil {
		return "", fmt.Errorf("npm view %s version returned %q: %w", npmPackageName, strings.TrimSpace(string(output)), err)
	}
	return version, nil
}

func applyNpmUpdate(ctx context.Context, executablePath string) error {
	prefix, hasPrefix := npmGlobalPrefixForExecutable(executablePath)
	npmPath, err := npmExecutable(prefix, hasPrefix)
	if err != nil {
		return err
	}
	args := []string{"install", "-g"}
	if hasPrefix {
		args = append(args, "--prefix", prefix)
	}
	args = append(args, npmPackageName+"@latest")
	command := exec.CommandContext(ctx, npmPath, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("npm install -g %s@latest: %w", npmPackageName, err)
	}
	return nil
}

// verifyNpmUpdate runs the binary npm just installed and checks that it reports
// wantVersion. `npm install` exits 0 when it installs the previous version
// again, which happens while the registry is not yet serving a new release.
func verifyNpmUpdate(ctx context.Context, executablePath string, wantVersion string) error {
	output, err := exec.CommandContext(ctx, executablePath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm finished, but %s could not be run to check its version: %w", executablePath, err)
	}
	// The version is the last word of the output ("kajicode 0.8.6").
	fields := strings.Fields(string(output))
	got := ""
	if len(fields) > 0 {
		got = strings.TrimPrefix(fields[len(fields)-1], "v")
	}
	want := strings.TrimPrefix(wantVersion, "v")
	if got != want {
		return fmt.Errorf("npm installed %s, not %s: the npm registry may not be serving %s yet, so run `kajicode upgrade` again in a few minutes", got, want, want)
	}
	return nil
}

func npmExecutable(prefix string, hasPrefix bool) (string, error) {
	if hasPrefix {
		for _, candidate := range npmExecutableCandidates(prefix) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found on PATH: reinstall with `npm install -g %s@latest`", npmPackageName)
	}
	return npmPath, nil
}

func npmExecutableCandidates(prefix string) []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(prefix, "npm.cmd"),
			filepath.Join(prefix, "npm.exe"),
			filepath.Join(prefix, "npm"),
		}
	}
	return []string{filepath.Join(prefix, "bin", "npm")}
}

func npmGlobalPrefixForExecutable(executablePath string) (string, bool) {
	clean := filepath.Clean(executablePath)
	separator := string(os.PathSeparator)
	for _, marker := range []string{
		separator + "lib" + separator + "node_modules" + separator,
		separator + "node_modules" + separator,
	} {
		if index := strings.Index(clean, marker); index > 0 {
			return clean[:index], true
		}
	}
	return "", false
}
