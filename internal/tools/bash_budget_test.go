package tools

import (
	"strconv"
	"strings"
	"testing"
)

// Small output passes through untouched and records raw==emitted, no truncated flag.
func TestBudgetBashOutputSmallPassesThrough(t *testing.T) {
	meta := map[string]string{}
	out, errStr := budgetBashOutput("hello\n", "warn\n", meta)
	if out != "hello\n" || errStr != "warn\n" {
		t.Fatalf("small output altered: out=%q err=%q", out, errStr)
	}
	if meta["truncated"] == "true" {
		t.Fatalf("small output must not be flagged truncated: %v", meta)
	}
	if meta["raw_bytes"] != strconv.Itoa(len("hello\n")+len("warn\n")) {
		t.Fatalf("raw_bytes wrong: %v", meta)
	}
}

// Oversized stdout is truncated head+tail: both the first and last lines survive,
// the middle is dropped behind a marker, and meta is flagged.
func TestBudgetBashOutputTruncatesHeadAndTail(t *testing.T) {
	head := "FIRST_LINE_MARKER\n"
	tail := "\nLAST_LINE_MARKER"
	big := head + strings.Repeat("x", bashOutputBudgetBytes) + tail

	meta := map[string]string{}
	out, _ := budgetBashOutput(big, "", meta)

	if !strings.Contains(out, "FIRST_LINE_MARKER") {
		t.Fatalf("head lost after truncation")
	}
	if !strings.Contains(out, "LAST_LINE_MARKER") {
		t.Fatalf("tail lost after truncation (failures live at the tail)")
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("expected a truncation marker, got:\n%s", out[:200])
	}
	if len(out) > bashOutputBudgetBytes {
		t.Fatalf("emitted %d bytes exceeds budget %d", len(out), bashOutputBudgetBytes)
	}
	if meta["truncated"] != "true" {
		t.Fatalf("expected truncated=true, got %v", meta)
	}
	if meta["raw_bytes"] != strconv.Itoa(len(big)) {
		t.Fatalf("raw_bytes = %s, want %d", meta["raw_bytes"], len(big))
	}
	if got, _ := strconv.Atoi(meta["emitted_bytes"]); got != len(out) {
		t.Fatalf("emitted_bytes = %s, want %d", meta["emitted_bytes"], len(out))
	}
}
