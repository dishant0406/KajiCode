package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectShellRCZsh(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("export FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	got, err := detectShellRC()
	if err != nil {
		t.Fatalf("detectShellRC: %v", err)
	}
	if got != rc {
		t.Fatalf("got %q, want %q", got, rc)
	}
}

func TestDetectShellRCFallback(t *testing.T) {
	home := t.TempDir()
	// No zshrc but a .profile exists.
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("#x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/weird")
	got, err := detectShellRC()
	if err != nil {
		t.Fatalf("detectShellRC: %v", err)
	}
	if filepath.Base(got) != ".profile" {
		t.Fatalf("got %q, want .profile", got)
	}
}

func TestDetectShellRCNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	if _, err := detectShellRC(); err != ErrNoShellRC {
		t.Fatalf("err = %v, want ErrNoShellRC", err)
	}
}

func TestWriteEnvToRCIdempotent(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".zshrc")
	if err := writeEnvToRC(rc, map[string]string{"EXA_API_KEY": "abc'}123", "TAVILY_API_KEY": "t"}); err != nil {
		t.Fatalf("writeEnvToRC: %v", err)
	}
	if err := writeEnvToRC(rc, map[string]string{"EXA_API_KEY": "new", "TAVILY_API_KEY": "t"}); err != nil {
		t.Fatalf("writeEnvToRC 2: %v", err)
	}
	b, _ := os.ReadFile(rc)
	s := string(b)
	if strings.Count(s, "EXA_API_KEY=") != 1 {
		t.Errorf("duplicate EXA_API_KEY lines:\n%s", s)
	}
	if !strings.Contains(s, rcGoGuard) || !strings.Contains(s, rcStopGuard) {
		t.Errorf("guard markers missing:\n%s", s)
	}
	// Idempotence: the rewritten value "new" must appear exactly once and the
	// tricky single-quote value from the first write must be gone.
	if strings.Count(s, "export EXA_API_KEY='new'") != 1 {
		t.Errorf("expected exactly one EXA_API_KEY='new', got:\n%s", s)
	}
	if strings.Contains(s, "'\"'\"'") {
		t.Errorf("stale single-quoted value survived rewrite:\n%s", s)
	}
}

func TestWriteEnvToRCPreservesUserLines(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rc, []byte("export MYSTUFF=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnvToRC(rc, map[string]string{"EXA_API_KEY": "k"}); err != nil {
		t.Fatalf("writeEnvToRC: %v", err)
	}
	b, _ := os.ReadFile(rc)
	s := string(b)
	if !strings.Contains(s, "MYSTUFF=x") {
		t.Errorf("user line lost:\n%s", s)
	}
	if !strings.Contains(s, "export EXA_API_KEY=") {
		t.Errorf("export missing:\n%s", s)
	}
}

func TestReplaceRCBlockRemoval(t *testing.T) {
	block := buildRCBlock(map[string]string{"EXA_API_KEY": "k"})
	src := "pre\n" + block + "post\n"
	out, err := replaceRCBlock(src, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, rcGoGuard) || strings.Contains(out, "EXA_API_KEY") {
		t.Errorf("block not removed:\n%s", out)
	}
	if !strings.Contains(out, "pre") || !strings.Contains(out, "post") {
		t.Errorf("surrounding content lost:\n%s", out)
	}
}

func TestBuildRCBlockEmpty(t *testing.T) {
	if got := buildRCBlock(nil); got != "" {
		t.Fatalf("empty block = %q, want empty", got)
	}
}

func TestRCValueQuoting(t *testing.T) {
	got := rcValueQuoted("a{b}c")
	if got != "'a{b}c'" {
		t.Errorf("got %q", got)
	}
}

func TestWriteEnvToRCEscapesSingleQuotes(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".zshrc")
	val := "ab'c}d"
	if err := writeEnvToRC(rc, map[string]string{"EXA_API_KEY": val}); err != nil {
		t.Fatalf("writeEnvToRC: %v", err)
	}
	b, _ := os.ReadFile(rc)
	want := rcGoGuard + "\nexport EXA_API_KEY='" + strings.ReplaceAll(val, "'", "'\"'\"'") + "'\n" + rcStopGuard + "\n"
	if string(b) != want {
		t.Fatalf("got=%q want=%q", string(b), want)
	}
}

func TestQuotePath(t *testing.T) {
	if got := quotePath("/a/b c"); got != "'/a/b c'" {
		t.Errorf("got %q", got)
	}
}
