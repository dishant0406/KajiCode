package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// batchFakeTool is a read-only, thread-safe tool that records calls so tests can
// assert parallel vs serial execution by counting in-flight concurrency.
type batchFakeTool struct {
	name string
	mut  *sync.Mutex
	live *int
	max  *int
}

func (t batchFakeTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true}
}

func (t batchFakeTool) Name() string        { return t.name }
func (t batchFakeTool) Description() string { return "fake batch tool " + t.name }
func (t batchFakeTool) Parameters() Schema  { return Schema{Type: "object"} }
func (t batchFakeTool) Safety() Safety {
	return Safety{SideEffect: SideEffectRead, Permission: PermissionAllow}
}
func (t batchFakeTool) Run(_ context.Context, _ map[string]any) Result {
	t.mut.Lock()
	*t.live++
	if *t.live > *t.max {
		*t.max = *t.live
	}
	live := *t.live
	t.mut.Unlock()
	// Hold the "in flight" marker for a short window so concurrent overlap is
	// observable; the lock is released during the wait.
	time.Sleep(30 * time.Millisecond)
	_ = live
	t.mut.Lock()
	*t.live--
	t.mut.Unlock()
	return Result{Status: StatusOK, Output: t.name + " done"}
}

// batchFakeMutator simulates a mutating tool that must never run concurrently.
type batchFakeMutator struct {
	batchFakeTool
}

func (t batchFakeMutator) Capabilities() ToolCapabilities {
	return ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false}
}

func buildBatchFixture() (*Registry, *int, *int, *sync.Mutex) {
	reg := NewRegistry()
	live := 0
	max := 0
	var mut sync.Mutex
	reg.Register(batchFakeTool{name: "read_a", mut: &mut, live: &live, max: &max})
	reg.Register(batchFakeTool{name: "read_b", mut: &mut, live: &live, max: &max})
	reg.Register(batchFakeTool{name: "read_c", mut: &mut, live: &live, max: &max})
	reg.Register(batchFakeMutator{batchFakeTool{name: "write_x", mut: &mut, live: &live, max: &max}})
	return reg, &live, &max, &mut
}

func TestBatchRunsCallsAndSummarizes(t *testing.T) {
	reg, _, _, _ := buildBatchFixture()
	tool := NewBatchTool(reg)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "read_a", "parameters": map[string]any{}},
			map[string]any{"tool": "read_b", "parameters": map[string]any{}},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("batch failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Ran 2 sub-tool call(s):") {
		t.Fatalf("missing count line: %q", res.Output)
	}
	if !strings.Contains(res.Output, "read_a => ok") || !strings.Contains(res.Output, "read_b => ok") {
		t.Fatalf("missing per-call ok status: %q", res.Output)
	}
}

func TestBatchParallelizesIndependentReads(t *testing.T) {
	reg, _, max, _ := buildBatchFixture()
	tool := NewBatchTool(reg)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "read_a", "parameters": map[string]any{}},
			map[string]any{"tool": "read_b", "parameters": map[string]any{}},
			map[string]any{"tool": "read_c", "parameters": map[string]any{}},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("batch failed: %s", res.Output)
	}
	if *max < 2 {
		t.Fatalf("expected parallel execution (concurrency >= 2), got max=%d", *max)
	}
}

func TestBatchSerializesMutators(t *testing.T) {
	reg, _, max, _ := buildBatchFixture()
	tool := NewBatchTool(reg)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "write_x", "parameters": map[string]any{}},
			map[string]any{"tool": "read_a", "parameters": map[string]any{}},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("batch failed: %s", res.Output)
	}
	// The mutator is serial-only; the trailing read can still run after it, so
	// max concurrency from the read+mutator mix must be 1.
	if *max > 1 {
		t.Fatalf("mutator should not run concurrently, got max=%d", *max)
	}
}

func TestBatchReportsPerCallFailure(t *testing.T) {
	reg, _, _, _ := buildBatchFixture()
	// An unknown tool fails that single call but the batch still reports.
	tool := NewBatchTool(reg)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "missing_tool", "parameters": map[string]any{}},
			map[string]any{"tool": "read_a", "parameters": map[string]any{}},
		},
	})
	if res.Status != StatusOK {
		t.Fatalf("batch should not fail entirely: %s", res.Output)
	}
	if !strings.Contains(res.Output, "One or more sub-tools failed.") {
		t.Fatalf("expected failure notice: %q", res.Output)
	}
	if !strings.Contains(res.Output, "missing_tool => error") {
		t.Fatalf("expected per-call error status: %q", res.Output)
	}
}

func TestBatchRejectsInvalidArgs(t *testing.T) {
	reg, _, _, _ := buildBatchFixture()
	tool := NewBatchTool(reg)
	bad := []map[string]any{
		{},
		{"calls": []any{}},
		{"calls": "not-an-array"},
		{"calls": []any{map[string]any{}}}, // missing tool name
		{"calls": []any{map[string]any{"tool": "read_a", "parameters": "nope"}}}, // bad params
	}
	for i, args := range bad {
		if res := tool.Run(context.Background(), args); res.Status != StatusError {
			t.Fatalf("case %d: expected error, got %s: %s", i, res.Status, res.Output)
		}
	}

	// Over the cap.
	over := make([]any, batchCap+1)
	for i := range over {
		over[i] = map[string]any{"tool": "read_a", "parameters": map[string]any{}}
	}
	if res := tool.Run(context.Background(), map[string]any{"calls": over}); res.Status != StatusError {
		t.Fatalf("expected cap error, got %s", res.Status)
	}
}

func TestBatchRejectsNestedBatch(t *testing.T) {
	reg, _, _, _ := buildBatchFixture()
	reg.Register(NewBatchTool(reg))
	tool := NewBatchTool(reg)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{
			map[string]any{"tool": "batch", "parameters": map[string]any{"calls": []any{}}},
		},
	})
	if res.Status != StatusError || !strings.Contains(res.Output, "nested batch") {
		t.Fatalf("expected nested-batch rejection, got %s: %s", res.Status, res.Output)
	}
}

func TestBatchUnwiredRegistry(t *testing.T) {
	tool := NewBatchTool(nil)
	res := tool.Run(context.Background(), map[string]any{
		"calls": []any{map[string]any{"tool": "read_a", "parameters": map[string]any{}}},
	})
	if res.Status != StatusError || !strings.Contains(res.Output, "registry") {
		t.Fatalf("expected registry error, got %s: %s", res.Status, res.Output)
	}
}

func TestBatchCapabilityAudit(t *testing.T) {
	// The batch tool itself is read-only + thread-safe, so it must not trip the
	// mutator-thread-safe audit even though it internally invokes mutators.
	if caps := CapabilitiesOf(NewBatchTool(NewRegistry())); caps.Effect != EffectReadOnly || !caps.ThreadSafe {
		t.Fatalf("batch capabilities wrong: %+v", caps)
	}
}
