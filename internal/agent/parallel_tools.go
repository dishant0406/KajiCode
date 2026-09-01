package agent

import (
	"context"
	"sync"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// Parallel read-ahead for tool batches. When a turn requests several
// independent lookups (read_file + grep + glob is the common shape), executing
// them one after another serializes pure I/O waits. A consecutive run of
// capability-safe read-only calls is executed concurrently instead; results are
// then consumed in the original call order, so guard counters, message
// ordering, abort semantics, and the surface's call/result event pairing are
// byte-identical to sequential execution. Runs never span a mutating call: a
// read that follows a write must observe the write, so eligibility is decided
// per consecutive run, not per batch.
//
// Eligibility uses the PR5 tool-effect contract (tools.CapabilitiesOf):
//
//	Effect == ReadOnly
//	AND ThreadSafe == true
//	AND auto-allowed (no interactive permission prompt on the hot path)
//	AND no resource-key conflict with an earlier call in the same concurrent window
//
// Unknown, mutators, interactive tools, and non-thread-safe reads stay
// sequential. Empty resource keys do not conflict (ThreadSafe is the safety
// gate); shared non-empty keys force a batch boundary.

// maxParallelReadTools bounds concurrent read-only tool executions in a turn.
const maxParallelReadTools = 8

// maxParallelTaskTools bounds concurrent read-only Task (sub-agent) calls
// within a batch. Each Task is a whole child process (CPU/memory heavy), far
// costlier than an in-process read, so it gets a tighter cap than plain reads.
const maxParallelTaskTools = 4

// precomputedToolResult is one parallel read-ahead execution, keyed back to
// its batch index by the caller.
type precomputedToolResult struct {
	result   ToolResult
	abortErr error
}

// parallelSafeToolCall reports whether call may run concurrently with its
// neighbors under the PR5 capability contract. Loop-intercepted tools
// (ask_user, request_permissions) and tool_search (mutates the deferred-tool
// set) always stay sequential.
func parallelSafeToolCall(registry *tools.Registry, call ToolCall, options Options) bool {
	switch call.Name {
	case "ask_user", tools.RequestPermissionsToolName, tools.ToolSearchToolName:
		return false
	}
	tool, found := registry.Get(call.Name)
	if !found {
		return false
	}
	args, ok := decodeCallArgs(call)
	if !ok {
		return false
	}
	// Args-aware lookup: for most tools this equals the static capabilities; for
	// the Task tool it classifies by the TARGET specialist's resolved effect, so a
	// fresh delegation to a read-only specialist (explorer, code-review) becomes
	// batch-eligible while write-capable ones stay sequential. Fail-closed on
	// malformed args via CapabilitiesForArgsOf's unknown default.
	caps := tools.CapabilitiesForArgsOf(tool, args)
	// Fail-closed: only audited concurrent-safe pure reads.
	if caps.Effect != tools.EffectReadOnly || !caps.ThreadSafe {
		return false
	}
	return effectivePermission(tool, args) == tools.PermissionAllow
}

// decodeCallArgs decodes tool call JSON arguments. Returns false on malformed
// input so the call stays sequential (never panics into the parallel path).
func decodeCallArgs(call ToolCall) (map[string]any, bool) {
	args := map[string]any{}
	if call.Arguments == "" {
		return args, true
	}
	if err := decodeToolArguments(call.Arguments, &args); err != nil {
		return nil, false
	}
	return args, true
}

// resourceKeysForCall returns the conflict keys for a call, or nil when the
// tool has no ResourceKeys function / no keys for these args. It resolves
// capabilities through the args-aware path so argument-dependent tools (Task)
// contribute their per-target conflict keys.
func resourceKeysForCall(registry *tools.Registry, call ToolCall) []string {
	tool, found := registry.Get(call.Name)
	if !found {
		return nil
	}
	args, ok := decodeCallArgs(call)
	if !ok {
		return nil
	}
	caps := tools.CapabilitiesForArgsOf(tool, args)
	if caps.ResourceKeys == nil {
		return nil
	}
	return caps.ResourceKeys(args)
}

// resourceKeysConflict reports whether two key sets share any non-empty key.
// Empty key sets never conflict: ThreadSafe is the eligibility gate; keys only
// refine conflict detection among tools that declare specific resources.
func resourceKeysConflict(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, key := range a {
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
	}
	for _, key := range b {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			return true
		}
	}
	return false
}

// extendParallelRun returns the exclusive end index of a consecutive parallel-
// safe run starting at start. The run stops before the first call that is not
// parallel-safe or that conflicts on resource keys with any earlier call in
// the same window (so two read_file calls on the same path stay sequential).
func extendParallelRun(registry *tools.Registry, calls []ToolCall, start int, options Options) int {
	if start >= len(calls) || !parallelSafeToolCall(registry, calls[start], options) {
		return start
	}
	end := start + 1
	keysWindow := [][]string{resourceKeysForCall(registry, calls[start])}
	for end < len(calls) {
		if !parallelSafeToolCall(registry, calls[end], options) {
			break
		}
		nextKeys := resourceKeysForCall(registry, calls[end])
		conflict := false
		for _, prev := range keysWindow {
			if resourceKeysConflict(prev, nextKeys) {
				conflict = true
				break
			}
		}
		if conflict {
			break
		}
		keysWindow = append(keysWindow, nextKeys)
		end++
	}
	return end
}

// executeParallelReadBatch runs calls[start:end] concurrently (bounded by
// maxParallelReadTools) and returns results indexed relative to start. All
// execution-side callbacks that can fire inside executeToolCall are serialized
// behind one mutex: a permission prompt (a sandbox preflight can demand one
// even for an auto-allowed read) must never appear twice at once on an
// interactive front-end, and OnPermission event handlers append to shared
// session-recording state without their own locking — two batched reads under
// a granted extra root would otherwise race (the pre-batch serial loop never
// had two callbacks in flight at once).
func executeParallelReadBatch(ctx context.Context, registry *tools.Registry, calls []ToolCall, start, end int, permissionMode PermissionMode, options Options) []precomputedToolResult {
	batchOptions := options
	var callbackMutex sync.Mutex
	if options.OnPermissionRequest != nil {
		inner := options.OnPermissionRequest
		batchOptions.OnPermissionRequest = func(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
			callbackMutex.Lock()
			defer callbackMutex.Unlock()
			return inner(ctx, request)
		}
	}
	if options.OnPermission != nil {
		inner := options.OnPermission
		batchOptions.OnPermission = func(event PermissionEvent) {
			callbackMutex.Lock()
			defer callbackMutex.Unlock()
			inner(event)
		}
	}

	results := make([]precomputedToolResult, end-start)
	semaphore := make(chan struct{}, maxParallelReadTools)
	// Task calls get their own tighter gate: they are already inside the
	// read-only-safe window, but each one spawns a child process, so cap them
	// below the plain-read concurrency. A Task slot also holds a read slot, so a
	// mixed batch can never exceed maxParallelReadTools processes total.
	taskSemaphore := make(chan struct{}, maxParallelTaskTools)
	var waitGroup sync.WaitGroup
	for index := start; index < end; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			if calls[index].Name == "Task" {
				taskSemaphore <- struct{}{}
				defer func() { <-taskSemaphore }()
			}
			result, abortErr := executeToolCall(ctx, registry, calls[index], permissionMode, batchOptions)
			results[index-start] = precomputedToolResult{result: result, abortErr: abortErr}
		}(index)
	}
	waitGroup.Wait()
	return results
}
