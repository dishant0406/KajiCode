package cli

import (
	"strings"
	"testing"
)

func TestParseExecRoleFlag(t *testing.T) {
	options, help, err := parseExecArgs([]string{"--role", "design", "build it"})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.role != "design" {
		t.Fatalf("role = %q, want design", options.role)
	}
	if strings.Join(options.promptParts, " ") != "build it" {
		t.Fatalf("promptParts = %#v", options.promptParts)
	}
}

func TestParseExecRoleFlagInline(t *testing.T) {
	options, help, err := parseExecArgs([]string{"--role=implement", "build it"})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.role != "implement" {
		t.Fatalf("role = %q, want implement", options.role)
	}
}

func TestParseExecRoleFlagTrimsWhitespace(t *testing.T) {
	options, help, err := parseExecArgs([]string{"--role", "  plan  ", "do it"})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.role != "plan" {
		t.Fatalf("role = %q, want trimmed plan", options.role)
	}
}

func TestParseExecRoleFlagDefaultsEmpty(t *testing.T) {
	options, help, err := parseExecArgs([]string{"do it"})
	if err != nil {
		t.Fatalf("parseExecArgs returned error: %v", err)
	}
	if help {
		t.Fatal("help = true, want false")
	}
	if options.role != "" {
		t.Fatalf("role = %q, want empty default", options.role)
	}
}
