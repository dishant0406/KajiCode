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
	for _, want := range []string{"enabled: on", "turnInterval: 10", "compact: on", "cooldownMs: 1200000"} {
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
	if !strings.Contains(stdout, `"turnInterval"`) {
		t.Fatalf("JSON output missing turnInterval:\n%s", stdout)
	}
}

func TestRunLearningSetPersists(t *testing.T) {
	deps, userConfig, _ := harnessCommandDeps(t)

	code, stdout, stderr := runCLICommand([]string{"learning", "set", "turnInterval", "3"}, deps)
	if code != exitSuccess {
		t.Fatalf("set exit = %d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "turnInterval=3") {
		t.Fatalf("set output missing turnInterval=3:\n%s", stdout)
	}

	cfg := readHarnessCLIConfig(t, userConfig)
	if cfg.Learning.TurnInterval != 3 {
		t.Fatalf("persisted turnInterval = %d, want 3", cfg.Learning.TurnInterval)
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
		for _, want := range []string{"learning set", "turnInterval", "cooldownMs"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%v help missing %q:\n%s", args, want, stdout)
			}
		}
	}
}
