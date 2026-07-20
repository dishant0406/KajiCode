package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstallMethodStandaloneByDefault(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "kajicode")
	if err := os.WriteFile(exePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if method := DetectInstallMethod(exePath); method != InstallMethodStandalone {
		t.Fatalf("DetectInstallMethod = %q, want standalone", method)
	}
}

func TestDetectInstallMethodNpmViaMarkerFile(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "kajicode")
	if err := os.WriteFile(exePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kajicode-binary-version"), []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile marker: %v", err)
	}
	if method := DetectInstallMethod(exePath); method != InstallMethodNpm {
		t.Fatalf("DetectInstallMethod = %q, want npm", method)
	}
}

func TestDetectInstallMethodNpmViaPackageJSON(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "kajicode")
	if err := os.WriteFile(exePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@dishant0406/kajicode"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package.json: %v", err)
	}
	if method := DetectInstallMethod(exePath); method != InstallMethodNpm {
		t.Fatalf("DetectInstallMethod = %q, want npm", method)
	}
}

func TestDetectInstallMethodIgnoresUnrelatedPackageJSON(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "kajicode")
	if err := os.WriteFile(exePath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"some-other-package"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package.json: %v", err)
	}
	if method := DetectInstallMethod(exePath); method != InstallMethodStandalone {
		t.Fatalf("DetectInstallMethod = %q, want standalone", method)
	}
}
