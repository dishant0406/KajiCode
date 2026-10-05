package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// stubClassifier answers every question with a fixed probability per question
// id, so a gate decision is deterministic in a test. A question id absent from
// probs is omitted from the result, exactly like a classifier that answers
// nothing for it.
type stubClassifier struct {
	probs map[string]float64
	err   error
	calls int
}

func (s *stubClassifier) Name() string { return "stub" }

func (s *stubClassifier) Classify(_ context.Context, req classifier.Request) (classifier.Result, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	result := classifier.Result{}
	for id := range req.Questions {
		if p, ok := s.probs[id]; ok {
			result[id] = classifier.Answer{Type: classifier.KindNoul, Probability: p}
		}
	}
	return result, nil
}

// bigBody returns a body of n lines that carries no error signal.
func bigBody(n int) string { return strings.Join(bigBodyLines(n), "\n") }

// bigBodyLines returns n plain lines, each well under gateBlockBytes, so only
// the line cap can split them.
func bigBodyLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line value " + strings.Repeat("x", 20)
	}
	return lines
}

func gateFor(cl classifier.Classifier, shadow bool) *ToolResultGate {
	return &ToolResultGate{Classifier: cl, KeepThreshold: 0.5, DropThreshold: 0.1, MinPruneRatio: 0.2, MinBytes: 1500, Shadow: shadow}
}

func messagesWithGoal() gateRun {
	return gateRun{messages: []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleUser, Content: "refactor the parser"},
	}}
}

func okResult(body string) ToolResult {
	return ToolResult{ToolCallID: "call_1", Name: "bash", Status: tools.StatusOK, Output: body}
}

func TestGateNilClassifierIsByteIdentical(t *testing.T) {
	body := bigBody(200)
	got := maybeGateToolResult(context.Background(), Options{}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("nil gate must not change output")
	}
	if got.Meta != nil {
		t.Fatalf("nil gate must not set meta, got %v", got.Meta)
	}
}

func TestGateErrorsAreNeverGated(t *testing.T) {
	cases := map[string]string{
		"python traceback": "Traceback (most recent call last):\n  File x, line 1\n" + bigBody(200),
		"go panic":         "panic: runtime error: index out of range\n" + bigBody(200),
		"go test FAIL":     "--- FAIL: TestFoo (0.01s)\n" + bigBody(200),
		"fatal":            "fatal error: all goroutines are asleep\n" + bigBody(200),
		"exit code":        "Command failed with exit code 2\n" + bigBody(200),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0}}
			got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
			if got.Output != body {
				t.Fatalf("error result must be untouched")
			}
			if stub.calls != 0 {
				t.Fatalf("error belt must short-circuit before any classifier call, got %d", stub.calls)
			}
		})
	}
}

// TestGateProseWordIsNotAnError guards the belt's precision: a body that merely
// contains the word "failed" (source code, a comment) is not a failure and must
// still be gated — the opposite of the earlier over-broad regex, which silently
// disabled the gate on every code-reading result.
func TestGateProseWordIsNotAnError(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	body := strings.ReplaceAll(bigBody(100), "line value", "failed := row.status")
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "read_file"}, okResult(body))
	if !strings.Contains(got.Output, "hidden") {
		t.Fatalf("a body containing the word 'failed' must still be gatable")
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestGateHidesIrrelevantBlocksKeepsRelevant(t *testing.T) {
	// 8 blocks of 25 lines. b000, b002 and b003 are confidently irrelevant
	// (below drop 0.1); the rest are relevant. Three eighths hidden is inside the
	// ceiling, so the gate prunes and the relevant blocks survive verbatim.
	stub := &stubClassifier{probs: map[string]float64{
		"b000": 0.02, "b001": 0.90, "b002": 0.02, "b003": 0.02,
		"b004": 0.90, "b005": 0.90, "b006": 0.90, "b007": 0.90,
		"gate_error": 0.01,
	}}
	body := bigBody(200)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))

	if got.Meta[gateMetaDecision] != gateDecisionPruned {
		t.Fatalf("expected pruned, got %q", got.Meta[gateMetaDecision])
	}
	if !strings.Contains(got.Output, "[… 25 lines hidden …]") || !strings.Contains(got.Output, "[… 50 lines hidden …]") {
		t.Fatalf("expected hidden-run markers around the relevant block, got:\n%s", got.Output)
	}
	// The relevant block survives verbatim.
	relevant := strings.Join(strings.Split(body, "\n")[25:50], "\n")
	if !strings.Contains(got.Output, relevant) {
		t.Fatalf("relevant block was dropped")
	}
	if !got.Truncated {
		t.Fatalf("gated result must set Truncated")
	}
	path := got.Meta["spill_path"]
	if path == "" {
		t.Fatalf("gated result must name a spill path")
	}
	spilled, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if strings.TrimSpace(string(spilled)) != strings.TrimSpace(body) {
		t.Fatalf("spill must round-trip the body")
	}
	_ = os.Remove(path)
	if got.ToolCallID != "call_1" || got.Status != tools.StatusOK {
		t.Fatalf("gate must not touch identity/status")
	}
}

func TestGateBelowMinRatioKeepsEverything(t *testing.T) {
	// Only one of four blocks is confidently irrelevant: 25% hidden is above the
	// 0.2 floor, so tighten by making only one block's lines smaller is not
	// possible; instead drop the ratio floor to 0.5 to force "below min".
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	gate.MinPruneRatio = 0.5
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("below-min-ratio must keep everything byte-identical")
	}
	if got.Meta[gateMetaDecision] != gateDecisionBelowMin {
		t.Fatalf("expected below_min_prune_ratio, got %q", got.Meta[gateMetaDecision])
	}
}

func TestGateUncertainBandKeeps(t *testing.T) {
	// Probabilities inside [drop, keep) must KEEP, never hide.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.3, "b001": 0.2, "b002": 0.4, "b003": 0.25, "gate_error": 0.01}}
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("uncertain band must keep everything")
	}
	if got.Meta[gateMetaDecision] != gateDecisionKept {
		t.Fatalf("expected kept, got %q", got.Meta[gateMetaDecision])
	}
}

func TestGateNoAnswerKeeps(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{}} // answers nothing
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("no answers must keep everything")
	}
}

func TestGateClassifierErrorFailsOpen(t *testing.T) {
	stub := &stubClassifier{err: classifier.ErrUnavailable}
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("classifier error must fail open")
	}
}

func TestGateSemanticErrorQuestionKeepsWhole(t *testing.T) {
	// Regex belt misses a prose failure; the shared error question catches it.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.02, "b002": 0.02, "b003": 0.02, "gate_error": 0.9}}
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("semantic error signal must keep the whole result")
	}
	if got.Meta[gateMetaDecision] != gateDecisionError {
		t.Fatalf("expected error_present, got %q", got.Meta[gateMetaDecision])
	}
}

func TestGateShadowModeNeverRewrites(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, true)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("shadow mode must not rewrite")
	}
	if got.Meta[gateMetaDecision] != gateDecisionShadow {
		t.Fatalf("expected shadow decision, got %q", got.Meta[gateMetaDecision])
	}
}

func TestGateSkipsDenylistedTools(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "todo_write", "Task", "ask_user", "skill"} {
		stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0, "b002": 0.0, "b003": 0.0}}
		body := bigBody(100)
		got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: name}, okResult(body))
		if got.Output != body {
			t.Fatalf("%s must never be gated", name)
		}
		if stub.calls != 0 {
			t.Fatalf("%s must not reach the classifier", name)
		}
	}
}

func TestGateSkipsSmallAndPlaceholderBodies(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0}}
	small := "just a short output"
	if got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, gateRun{}, ToolCall{Name: "bash"}, okResult(small)); got.Output != small {
		t.Fatalf("small body must be untouched")
	}
	placeholder := "[pruned bash output (~500 tokens) to reclaim context]"
	if got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, gateRun{}, ToolCall{Name: "bash"}, okResult(placeholder)); got.Output != placeholder {
		t.Fatalf("already pruned body must be untouched")
	}
}

func TestGateSkipsImageResults(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0}}
	body := bigBody(100)
	result := okResult(body)
	result.Images = []kajicoderuntime.ImageBlock{{}}
	if got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, result); got.Output != body {
		t.Fatalf("image result must be untouched")
	}
}

func TestGateSkipsNonOKStatus(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0}}
	body := bigBody(100)
	result := okResult(body)
	result.Status = tools.StatusError
	if got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, result); got.Output != body {
		t.Fatalf("non-ok status must be untouched")
	}
	if stub.calls != 0 {
		t.Fatalf("non-ok status must not reach the classifier")
	}
}

func TestToolResultGateDefaults(t *testing.T) {
	gate := (&ToolResultGate{}).withDefaults()
	if gate.KeepThreshold != classifier.DefaultGateKeepThreshold {
		t.Fatalf("keep default = %v", gate.KeepThreshold)
	}
	if gate.DropThreshold != classifier.DefaultGateDropThreshold {
		t.Fatalf("drop default = %v", gate.DropThreshold)
	}
	if gate.MinPruneRatio != classifier.DefaultGateMinPruneRatio {
		t.Fatalf("ratio default = %v", gate.MinPruneRatio)
	}
	if gate.MinBytes != classifier.DefaultGateMinBytes {
		t.Fatalf("bytes default = %v", gate.MinBytes)
	}
	// An inverted band collapses to the keep threshold (empty band).
	inverted := (&ToolResultGate{KeepThreshold: 0.5, DropThreshold: 0.9}).withDefaults()
	if inverted.DropThreshold != 0.5 {
		t.Fatalf("inverted band must clamp, got %v", inverted.DropThreshold)
	}
}

// TestGateGatesPreTruncatedAndReusesSpill proves the coverage fix: an
// already-truncated body is gated like any other (its budget truncation does
// not exempt the largest results from relevance filtering), and when the budget
// already spilled the body the gate reuses that pointer rather than clobbering
// it with a fresh spill of the smaller budgeted view.
func TestGateGatesPreTruncatedAndReusesSpill(t *testing.T) {
	// Half the blocks are relevant, so the gate prunes (inside the ceiling) and
	// the pre-existing pointer is the one it must reuse.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	body := bigBody(100)
	// A real spill file, so the reuse path is the one a reader can follow.
	priorPath := tools.SpillOutput("bash", body)
	if priorPath == "" {
		t.Skip("spill directory unavailable")
	}
	result := okResult(body)
	result.Truncated = true
	result.Meta = map[string]string{"spill_path": priorPath}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, result)
	if got.Meta[gateMetaDecision] != gateDecisionPruned {
		t.Fatalf("a pre-truncated body must still be gated, got %q", got.Meta[gateMetaDecision])
	}
	if got.Meta["spill_path"] != priorPath {
		t.Fatalf("gate must reuse the existing spill pointer, got %q", got.Meta["spill_path"])
	}
	if !strings.Contains(got.Output, priorPath) {
		t.Fatalf("stub must name the reused spill path:\n%s", got.Output)
	}
	_ = os.Remove(priorPath)
}

// TestGateForeignSpillPointerIsNotReused guards the reuse against a pointer that
// is not a spill file: a path outside the spill root, or one the sweep removed,
// must be replaced with a fresh spill rather than advertised to the model as a
// recovery file the scoped read tools would refuse.
func TestGateForeignSpillPointerIsNotReused(t *testing.T) {
	outer := filepath.Join(t.TempDir(), "not-a-spill.txt")
	if err := os.WriteFile(outer, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, pointer := range map[string]string{
		"outside the spill root": outer,
		"inside but removed":     filepath.Join(t.TempDir(), "swept-away.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
			body := bigBody(100)
			result := okResult(body)
			result.Truncated = true
			result.Meta = map[string]string{"spill_path": pointer}
			got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, result)
			path := got.Meta["spill_path"]
			if path == pointer {
				t.Fatalf("a non-spill pointer must not be reused")
			}
			spilled, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("fresh spill unreadable: %v", err)
			}
			if strings.TrimSpace(string(spilled)) != strings.TrimSpace(body) {
				t.Fatalf("fresh spill must round-trip the body")
			}
			_ = os.Remove(path)
		})
	}
}

// TestGatePreTruncatedWithoutSpillCreatesOne covers the other half: a
// pre-truncated body that carries no usable spill gets one, so a hidden block is
// still recoverable.
func TestGatePreTruncatedWithoutSpillCreatesOne(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	body := bigBody(100)
	result := okResult(body)
	result.Truncated = true
	result.Meta = map[string]string{"truncation_reason": "head_limit"}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "grep"}, result)
	if got.Meta[gateMetaDecision] != gateDecisionPruned {
		t.Fatalf("expected pruned, got %q", got.Meta[gateMetaDecision])
	}
	path := got.Meta["spill_path"]
	if path == "" {
		t.Fatalf("a pre-truncated body with no spree must get one")
	}
	spilled, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if strings.TrimSpace(string(spilled)) != strings.TrimSpace(body) {
		t.Fatalf("spill must round-trip the body")
	}
	_ = os.Remove(path)
}

// TestGateByteBoundedBlocksGateShortButLargeBodies proves the byte cap in
// gateBlocks: a body well under 50 lines but over gateBlockBytes is split into
// several blocks and is gated. This is the web_search / terse-bash case the
// line-count minimum used to drop before the classifier ever saw it.
func TestGateByteBoundedBlocksGateShortButLargeBodies(t *testing.T) {
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = strings.Repeat("s", 200)
	}
	body := strings.Join(lines, "\n")
	if len(body) <= gateBlockBytes {
		t.Fatalf("test body must exceed gateBlockBytes, got %d", len(body))
	}

	blocks := gateBlocks(strings.Split(body, "\n"))
	if len(blocks) < gateMinBlocks {
		t.Fatalf("a short-but-large body must yield >= %d blocks, got %d", gateMinBlocks, len(blocks))
	}
	probs := map[string]float64{"gate_error": 0.01}
	for i, block := range blocks {
		if i == 0 {
			probs[block.id] = 0.02
			continue
		}
		probs[block.id] = 0.9
	}
	stub := &stubClassifier{probs: probs}
	result := okResult(body)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "web_search"}, result)
	if got.Meta[gateMetaDecision] != gateDecisionPruned {
		t.Fatalf("short-but-large body must gate, got %q", got.Meta[gateMetaDecision])
	}
	_ = os.Remove(got.Meta["spill_path"])
}

// TestGateBlocksHonorLineAndByteCaps asserts both block caps directly, so a
// future change to either bound is caught here rather than at a call site.
func TestGateBlocksHonorLineAndByteCaps(t *testing.T) {
	// Many short lines: the line cap closes each block.
	blocks := gateBlocks(bigBodyLines(gateBlockLines * 3))
	if len(blocks) != 3 {
		t.Fatalf("expected 3 line-capped blocks, got %d", len(blocks))
	}
	for _, block := range blocks {
		if block.end-block.start > gateBlockLines {
			t.Fatalf("block %s exceeds the line cap: %d lines", block.id, block.end-block.start)
		}
	}
	// Two wide lines: the byte cap closes the first block before the second.
	wideBlocks := gateBlocks([]string{strings.Repeat("w", gateBlockBytes), strings.Repeat("w", 10)})
	if len(wideBlocks) != 2 {
		t.Fatalf("expected the byte cap to split into 2 blocks, got %d", len(wideBlocks))
	}
	if wideBlocks[0].end != 1 {
		t.Fatalf("byte cap must close before the overflowing line, got end=%d", wideBlocks[0].end)
	}
}

// TestGateSingleLineBodyIsSkipped documents the one residual case: a body with
// no internal newline is a single block and cannot be partially hidden.
func TestGateSingleLineBodyIsSkipped(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0}}
	body := strings.Repeat("x", 5000)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Output != body {
		t.Fatalf("single-line body must be untouched")
	}
	if stub.calls != 0 {
		t.Fatalf("single-line body must not reach the classifier, got %d call(s)", stub.calls)
	}
}

func TestRenderGatedRunAtEdgesAndAllHidden(t *testing.T) {
	lines := make([]string, 8)
	for i := range lines {
		lines[i] = fmt.Sprintf("l%d", i)
	}
	blocks := []gateBlock{{id: "b000", start: 0, end: 2}, {id: "b001", start: 2, end: 4}, {id: "b002", start: 4, end: 6}, {id: "b003", start: 6, end: 8}}

	// Hidden at the first and last block only.
	out := renderGated(lines, blocks, []bool{true, false, false, true}, 4, 8, 0.05, "/p", "bash", "")
	if !strings.Contains(out, "l2") || !strings.Contains(out, "l3") {
		t.Fatalf("kept middle lost: %s", out)
	}
	if strings.Count(out, "[…") != 2 {
		t.Fatalf("expected two hidden runs, got: %s", out)
	}

	// All blocks hidden.
	all := renderGated(lines, blocks, []bool{true, true, true, true}, 8, 8, 0.05, "/p", "bash", "")
	if !strings.Contains(all, "[… 8 lines hidden …]") {
		t.Fatalf("all-hidden must render one run: %s", all)
	}
}

func TestGateMultiBatch(t *testing.T) {
	// 20 blocks of 25 lines = 4000 bytes/block > budget, so each block flushes
	// its own batch; every block must still be judged and hideable.
	stub := &stubClassifier{}
	probs := map[string]float64{"gate_error": 0.01}
	for i := 0; i < 20; i++ {
		probs[fmt.Sprintf("b%03d", i)] = 0.0
	}
	stub.probs = probs
	body := gateLongLines(500)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Meta[gateMetaDecision] != gateDecisionPruned {
		t.Fatalf("multi-batch result should gate, got %q", got.Meta[gateMetaDecision])
	}
	if stub.calls < 2 {
		t.Fatalf("expected multiple batches, got %d call(s)", stub.calls)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestGatePreservesOtherMeta(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.9, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	body := bigBody(100)
	result := okResult(body)
	result.Meta = map[string]string{"output_budget_category": "search", "custom": "keep-me"}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), ToolCall{Name: "bash"}, result)
	if got.Meta["custom"] != "keep-me" || got.Meta["output_budget_category"] != "search" {
		t.Fatalf("existing meta lost: %v", got.Meta)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

// gateLongLines returns n long lines so each 25-line block exceeds the batch
// size cap and forces multiple classifier batches.
func gateLongLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = strings.Repeat("z", 200)
	}
	return strings.Join(lines, "\n")
}

func TestGateAboveMaxHiddenRatioKeepsWholeResult(t *testing.T) {
	// Every block is confidently irrelevant. Without a ceiling the gate would
	// black out the entire result and leave the model a stub and a spill path.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.02, "b002": 0.02, "b003": 0.02, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Meta[gateMetaDecision] != gateDecisionAboveMax {
		t.Fatalf("expected above_max_hidden_ratio, got %q", got.Meta[gateMetaDecision])
	}
	if got.Output != body {
		t.Fatalf("a result past the hidden ceiling must be kept verbatim")
	}
	if got.Meta["spill_path"] != "" {
		t.Fatalf("a kept result must not spill")
	}
}

func TestGateAtCeilingKeepsWholeResult(t *testing.T) {
	// Half hidden sits AT the ceiling, and the ceiling is the largest share the
	// gate may not exceed, so this result is kept whole rather than pruned.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.02, "b001": 0.02, "b002": 0.9, "b003": 0.9, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	gate.MaxHiddenRatio = 0.5
	body := bigBody(100)
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), ToolCall{Name: "bash"}, okResult(body))
	if got.Meta[gateMetaDecision] != gateDecisionAboveMax {
		t.Fatalf("expected above_max_hidden_ratio at the ceiling, got %q", got.Meta[gateMetaDecision])
	}
	if got.Output != body {
		t.Fatalf("the ceiling must keep the result verbatim")
	}
}

func TestGateNeverGatesReadOfASpillFile(t *testing.T) {
	// The stub tells the model to read_file the spill path. That read is the
	// recovery step, so it must never be gated into another stub.
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0, "b002": 0.0, "b003": 0.0, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	path := tools.SpillOutput("bash", bigBody(100))
	if path == "" {
		t.Fatalf("spill must produce a path")
	}
	defer os.Remove(path)
	body := bigBody(100)
	call := ToolCall{Name: "read_file", Arguments: `{"path":"` + path + `"}`}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), call, okResult(body))
	if got.Meta[gateMetaDecision] != "" {
		t.Fatalf("a spill read must not be gated, got %q", got.Meta[gateMetaDecision])
	}
	if got.Output != body {
		t.Fatalf("a spill read must return the body verbatim")
	}
}

func TestGateStillGatesReadOfANonSpillFile(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0, "b002": 0.0, "b003": 0.0, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	body := bigBody(100)
	call := ToolCall{Name: "read_file", Arguments: `{"path":"/tmp/not-a-spill-file.txt"}`}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), call, okResult(body))
	if got.Meta[gateMetaDecision] != gateDecisionAboveMax {
		t.Fatalf("a normal read is still judged, got %q", got.Meta[gateMetaDecision])
	}
}

func TestGateTreatsUnparseableReadArgumentsAsGateable(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0, "b002": 0.0, "b003": 0.0, "gate_error": 0.01}}
	gate := gateFor(stub, false)
	body := bigBody(100)
	call := ToolCall{Name: "read_file", Arguments: `{not json`}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, messagesWithGoal(), call, okResult(body))
	if got.Meta[gateMetaDecision] == "" {
		t.Fatalf("a malformed call must still be judged")
	}
}

// A recovery read may name the spill file through any of read_file's path
// aliases. Gating one would answer the stub's pointer with another stub, so
// every alias must be recognized as a spill read.
func TestReadsSpillFileRecognizesPathAliases(t *testing.T) {
	path := tools.SpillOutput("bash", bigBody(80))
	if path == "" {
		t.Skip("spill directory unavailable")
	}
	for _, key := range []string{"path", "file", "file_path", "filepath", "filename"} {
		arguments := `{"` + key + `":"` + path + `"}`
		if !readsSpillFile("read_file", arguments) {
			t.Errorf("read_file %s of a spill file must be recognized as a spill read", key)
		}
	}
	if readsSpillFile("read_file", `{"path":"/etc/hosts"}`) {
		t.Error("a read outside the spill directory must not be treated as a spill read")
	}
	if readsSpillFile("bash", `{"path":"`+path+`"}`) {
		t.Error("only the read tools may take the spill-read exemption")
	}
}

// A MinPruneRatio above MaxHiddenRatio is unsatisfiable and would silently turn
// the gate into a no-op; the ceiling must win so results stay visible.
func TestGateClampsMaxHiddenRatioAboveMinPruneRatio(t *testing.T) {
	gate := (&ToolResultGate{Classifier: &stubClassifier{}, MinPruneRatio: 0.9}).withDefaults()
	if gate.MaxHiddenRatio != 0.9 {
		t.Fatalf("MaxHiddenRatio = %v, want it raised to MinPruneRatio 0.9", gate.MaxHiddenRatio)
	}
}
