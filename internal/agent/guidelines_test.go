package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// writeTestFile writes content to path after creating parent dirs.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// makeGitRoot marks dir as a git root (a .git/HEAD is enough for HasGitMetadata).
func makeGitRoot(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("git mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("git head: %v", err)
	}
}

func TestGuidelineTrackerObservePathFindsSubdirectoryRule(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "a", "b")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "subdirectory rule")

	tracker := newGuidelineTracker(root)
	tracker.ObservePath(sub)

	blocks := tracker.drain()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 discovered block, got %d: %v", len(blocks), blocks)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "subdirectory rule") {
		t.Errorf("discovered block missing content: %q", joined)
	}
	// The block must be compaction-preserved <INSTRUCTIONS> format: a "# "
	// heading containing " instructions for " plus wrapped tags, exactly what
	// compaction_preserve.projectInstructionBlock recognizes.
	if !strings.HasPrefix(strings.TrimSpace(joined), "# ") ||
		!strings.Contains(joined, projectInstructionsHeadingMarker) ||
		!strings.Contains(joined, projectInstructionsOpenTag) ||
		!strings.Contains(joined, projectInstructionsCloseTag) {
		t.Errorf("drained block is not in compaction-preserved <INSTRUCTIONS> format: %q", joined)
	}
}

func TestGuidelineTrackerDoesNotResendAlreadyKnown(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	writeTestFile(t, filepath.Join(root, "AGENTS.md"), "root rule")

	tracker := newGuidelineTracker(root)
	// A file already in the boot system prompt (seeded) is never re-injected.
	tracker.seedStartup(root)
	tracker.ObservePath(root)

	if blocks := tracker.drain(); len(blocks) != 0 {
		t.Fatalf("seeded file was re-injected: %v", blocks)
	}
}

func TestGuidelineTrackerOnlyReportsOneLevelPerNearestRule(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "sub")
	writeTestFile(t, filepath.Join(root, "AGENTS.md"), "root rule")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "sub rule")

	tracker := newGuidelineTracker(root)
	tracker.ObservePath(sub)

	blocks := tracker.drain()
	// Both root and sub rules are discovered because findProjectContextFile
	// matches one file per directory level, and both govern the touched dir.
	if len(blocks) != 2 {
		t.Fatalf("expected 2 discovered blocks (root + sub), got %d: %v", len(blocks), blocks)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "root rule") || !strings.Contains(joined, "sub rule") {
		t.Errorf("blocks missing expected rules: %q", joined)
	}
}

func TestGuidelineTrackerDrainIsIdempotentForRepeatTouches(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "sub")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "sub rule")

	tracker := newGuidelineTracker(root)
	tracker.ObservePath(sub)
	if blocks := tracker.drain(); len(blocks) != 1 {
		t.Fatalf("expected 1 block first drain, got %d", len(blocks))
	}
	// A second touch of the same directory must not re-queue the file.
	tracker.ObservePath(sub)
	if blocks := tracker.drain(); len(blocks) != 0 {
		t.Fatalf("repeat touch re-injected a file: %v", blocks)
	}
}

func TestGuidelineTrackerIgnoresEmptyRuleFile(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "sub")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "   \n  ")

	tracker := newGuidelineTracker(root)
	tracker.ObservePath(sub)
	if blocks := tracker.drain(); len(blocks) != 0 {
		t.Fatalf("empty rule file was injected: %v", blocks)
	}
}

func TestGuidelineObserverWiring(t *testing.T) {
	root := t.TempDir()
	tracker := newGuidelineTracker(root)
	observer := tracker.observer()
	if observer == nil {
		t.Fatal("expected a non-nil observer from a live tracker")
	}
	// The observer and tracker share state; nil-tracker observer stays nil and
	// tools no-op on it.
	if tracker.observer() == nil {
		t.Fatal("observer should be non-nil")
	}
	var nilTracker *guidelineTracker
	if nilTracker.observer() != nil {
		t.Fatal("expected nil observer for nil tracker")
	}
}

func TestGuidelineTrackerReassertAfterCompaction(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "sub")
	writeTestFile(t, filepath.Join(root, "AGENTS.md"), "root rule")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "sub rule")

	tracker := newGuidelineTracker(root)
	// Working directory deep in the tree; both root and sub rules govern it.
	blocks := tracker.reassertGuidelines(sub)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 reasserted blocks (root + sub), got %d: %v", len(blocks), blocks)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, "root rule") || !strings.Contains(joined, "sub rule") {
		t.Errorf("reasserted blocks missing rules: %q", joined)
	}
	// Rebuild must not consume tracker state (reassert is a pure view, unlike drain).
	if again := tracker.reassertGuidelines(sub); len(again) != 2 {
		t.Errorf("reassert is not idempotent: %d blocks on second call", len(again))
	}
}

func TestGuidelineTrackerReassertNilSafe(t *testing.T) {
	var nilTracker *guidelineTracker
	if blocks := nilTracker.reassertGuidelines(""); len(blocks) != 0 {
		t.Fatalf("nil tracker reassert returned %d blocks: %v", len(blocks), blocks)
	}
}

// compactionPreservesInjectedGuideline proves the mid-run injected <INSTRUCTIONS>
// block survives compaction verbatim via the existing preserved-state machinery.
// It reproduces the real flow: a fresh guideline block is injected as a user
// message, then a compaction summarizes an older middle that includes it; the
// block must be carried into the preserved-state JSON, not dropped.
func TestCompactionPreservesInjectedGuideline(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	makeGitRoot(t, root)
	sub := filepath.Join(root, "sub")
	writeTestFile(t, filepath.Join(sub, "AGENTS.md"), "critical sub rule: never touch the vendor dir")

	tracker := newGuidelineTracker(root)
	blocks := tracker.reassertGuidelines(sub)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 guideline block, got %d", len(blocks))
	}

	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "do the task"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "first tool round"},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "1", Content: "ok"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "second tool round"},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "2", Content: "done"},
		// The injected guideline middle user messages.
		{Role: kajicoderuntime.MessageRoleUser, Content: blocks[0]},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "final answer"},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "3", Content: "seen"},
	}

	summarized := make(map[string]string)
	result, err := CompactMessages(messages, CompactionOptions{
		Summarize: func(middle []kajicoderuntime.Message) (string, error) {
			summarized["middle"] = renderTranscript(middle)
			return "the assistant did the work", nil
		},
	})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !result.Compacted {
		t.Fatal("expected compaction to run")
	}
	// The injected instruction body must appear in the summarizer-visible middle
	// OR be preserved in the compacted summary's preserved-state block. Reject a
	// summarizer that would have paraphrased it away by asserting the preserved
	// state carries the instruction verbatim.
	compactedJoined := renderTranscript(result.Messages)
	if !strings.Contains(compactedJoined, "never touch the vendor dir") {
		t.Errorf("injected guideline lost across compaction. Compacted:\n%s", compactedJoined)
	}
}
