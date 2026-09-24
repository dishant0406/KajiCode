package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func webSearchTestDeps(t *testing.T) appDeps {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	return appDeps{
		userConfigPath: func() (string, error) { return cfg, nil },
		getwd:          func() (string, error) { return dir, nil },
	}
}

func TestWebSearchSetStatusRemove(t *testing.T) {
	deps := webSearchTestDeps(t)
	var out bytes.Buffer

	if code := runWebSearch([]string{"set", "exa", "--key", "sk-secret"}, &out, &out, deps); code != exitSuccess {
		t.Fatalf("set: code=%d out=%s", code, out.String())
	}
	if strings.Contains(out.String(), "sk-secret") {
		t.Fatal("the API key must never be printed")
	}
	// The key is now live in this process for a later tool call.
	if os.Getenv("EXA_API_KEY") != "sk-secret" {
		t.Fatalf("set did not make the key live (EXA_API_KEY=%q)", os.Getenv("EXA_API_KEY"))
	}

	// A fresh process (unset env) reads status from the stored env file.
	_ = os.Unsetenv("EXA_API_KEY")
	_ = os.Unsetenv("KAJICODE_WEBSEARCH_PROVIDER")
	out.Reset()
	if code := runWebSearch([]string{"status"}, &out, &out, deps); code != exitSuccess {
		t.Fatalf("status: code=%d", code)
	}
	if !strings.Contains(out.String(), "exa") || !strings.Contains(out.String(), "set") {
		t.Fatalf("status did not report the stored key: %s", out.String())
	}
	if strings.Contains(out.String(), "sk-secret") {
		t.Fatal("status leaked the key")
	}

	out.Reset()
	if code := runWebSearch([]string{"remove"}, &out, &out, deps); code != exitSuccess {
		t.Fatalf("remove: code=%d", code)
	}
	if os.Getenv("KAJICODE_WEBSEARCH_PROVIDER") != "" {
		t.Fatal("remove did not clear the in-process provider")
	}
}

func TestWebSearchHelpAndUnknown(t *testing.T) {
	deps := webSearchTestDeps(t)
	var out bytes.Buffer
	// -h must print help, not run status.
	if code := runWebSearch([]string{"-h"}, &out, &out, deps); code != exitSuccess {
		t.Fatalf("-h: code=%d", code)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("-h did not print help: %s", out.String())
	}
	out.Reset()
	if _, ok := webSearchProviderByID("nope"); ok {
		t.Fatal("unknown provider resolved")
	}
}
