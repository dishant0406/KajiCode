package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

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
