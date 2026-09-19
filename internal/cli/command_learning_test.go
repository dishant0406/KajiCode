package cli

import (
	"strings"
	"testing"
)

func TestRunLearningStatusShowsDefaults(t *testing.T) {
	deps, _, _ := harnessCommandDeps(t)

	code, stdout, stderr := runCLICommand([]string{"learning", "status"}, deps)
	if code != exitSuccess {
		t.Fatalf("status exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	for _, want := range []string{"enabled: on", "debounceMs: 30000", "compact: on", "pruneAfterDays: 90", "maxEntries: 200"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunLearningStatusJSON(t *testing.T) {
	deps, _, _ := harnessCommandDeps(t)

	code, stdout, _ := runCLICommand([]string{"learning", "status", "--json"}, deps)
	if code != exitSuccess {
		t.Fatalf("status --json exit = %d\nstdout:\n%s", code, stdout)
	}
	for _, want := range []string{`"debounceMs"`, `"pruneAfterDays"`, `"maxEntries"`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("JSON output missing %s:\n%s", want, stdout)
		}
	}
}

func TestRunLearningSetPersists(t *testing.T) {
	deps, userConfig, _ := harnessCommandDeps(t)

	code, stdout, stderr := runCLICommand([]string{"learning", "set", "debounceMs", "3000"}, deps)
	if code != exitSuccess {
		t.Fatalf("set exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "debounceMs=3000") {
		t.Fatalf("set output missing debounceMs=3000:\n%s", stdout)
	}

	cfg := readHarnessCLIConfig(t, userConfig)
	if cfg.Learning.DebounceMs != 3000 {
		t.Fatalf("persisted debounceMs = %d, want 3000", cfg.Learning.DebounceMs)
	}
}

func TestRunLearningOffShorthand(t *testing.T) {
	deps, _, _ := harnessCommandDeps(t)

	code, stdout, stderr := runCLICommand([]string{"learning", "off"}, deps)
	if code != exitSuccess {
		t.Fatalf("off exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "enabled=off") {
		t.Fatalf("off output missing enabled=off:\n%s", stdout)
	}
}

func TestRunLearningUnknownKeyAndSubcommand(t *testing.T) {
	deps, _, _ := harnessCommandDeps(t)

	code, _, stderr := runCLICommand([]string{"learning", "set", "bogus", "1"}, deps)
	if code == exitSuccess {
		t.Fatalf("set bogus should fail, stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "unknown learning key") {
		t.Fatalf("stderr = %q", stderr)
	}

	code, _, stderr = runCLICommand([]string{"learning", "nope"}, deps)
	if code != exitUsage {
		t.Fatalf("unknown subcommand exit = %d, want usage; stderr=%q", code, stderr)
	}
}

func TestRunLearningHelp(t *testing.T) {
	deps, _, _ := harnessCommandDeps(t)

	for _, args := range [][]string{
		{"learning", "--help"},
		{"learning", "help"},
	} {
		code, stdout, _ := runCLICommand(args, deps)
		if code != exitSuccess {
			t.Fatalf("%v exit = %d", args, code)
		}
		for _, want := range []string{"learning set", "debounceMs", "pruneAfterDays", "maxEntries"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v help missing %q:\n%s", args, want, stdout)
			}
		}
	}
}
