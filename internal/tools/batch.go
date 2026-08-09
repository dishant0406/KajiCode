package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dishant0406/KajiCode/internal/sandbox"
)

// batchCap is the maximum number of sub-tools one batch call will run. It bounds
// the blast radius of a batch (each sub-tool can itself be expensive) and
// mirrors opencode's limit.
const batchCap = 10

// batchToolField identifies a single sub-tool call in a batch: the tool name and
// its parameters.
type batchCall struct {
	Tool       string         `json:"tool"`
	Parameters map[string]any `json:"parameters"`
}

// batchTool runs several independent tool calls in one message. Read-only,
// thread-safe sub-calls with non-conflicting resource keys run in parallel;
// everything else is serialized. This mirrors the agent's auto-parallel
// executor but exposes it explicitly so a provider that expects a batch shape
// can use it without needing the harness to infer parallelism.
type batchTool struct {
	baseTool
	registry *Registry
}

func NewBatchTool(registry *Registry) Tool {
	return batchTool{
		baseTool: baseTool{
			name: "batch",
			description: "Run multiple independent tool calls (up to 10) in one message. Read-only, " +
				"thread-safe calls whose resource keys don't collide run in parallel; mutating, interactive, " +
				"or conflicting calls run one after another. Use this to collapse several independent reads " +
				"or small edits into a single round-trip. Each sub-call reports its own tool name and result.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"calls": {
						Type:        "array",
						Description: "The sub-tool calls to run, each {tool, parameters}. Max 10.",
						Items: &PropertySchema{
							Type: "object",
							Properties: map[string]PropertySchema{
								"tool":       {Type: "string", Description: "Tool name to invoke."},
								"parameters": {Type: "object", Description: "Arguments for the tool."},
							},
							Required: []string{"tool"},
						},
					},
				},
				Required:             []string{"calls"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Runs sub-tools and aggregates their results; does not itself modify files."),
			capabilities: ToolCapabilities{
				Effect:       EffectReadOnly,
				ThreadSafe:   true,
				ResourceKeys: func(args map[string]any) []string { return nil },
			},
		},
		registry: registry,
	}
}

func (t batchTool) Run(ctx context.Context, args map[string]any) Result {
	return t.run(ctx, args, RunOptions{})
}

func (t batchTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	return t.run(ctx, args, options)
}

func (t batchTool) RunWithSandbox(ctx context.Context, args map[string]any, engine *sandbox.Engine) Result {
	return t.run(ctx, args, RunOptions{Sandbox: engine})
}

func (t batchTool) run(ctx context.Context, args map[string]any, options RunOptions) Result {
	if t.registry == nil {
		return errorResult("Error: batch is not wired to a tool registry.")
	}
	rawCalls, ok := args["calls"].([]any)
	if !ok {
		return errorResult("Error: Invalid arguments for batch: calls must be an array of objects.")
	}
	if len(rawCalls) == 0 {
		return errorResult("Error: Invalid arguments for batch: calls must not be empty.")
	}
	if len(rawCalls) > batchCap {
		return errorResult(fmt.Sprintf("Error: Invalid arguments for batch: at most %d calls are allowed, got %d.", batchCap, len(rawCalls)))
	}

	calls := make([]batchCall, 0, len(rawCalls))
	for i, raw := range rawCalls {
		obj, ok := raw.(map[string]any)
		if !ok {
			return errorResult(fmt.Sprintf("Error: Invalid arguments for batch: call %d must be an object.", i))
		}
		toolName, _ := obj["tool"].(string)
		if toolName == "" {
			return errorResult(fmt.Sprintf("Error: Invalid arguments for batch: call %d is missing a tool name.", i))
		}
		params := map[string]any{}
		if p, exists := obj["parameters"]; exists {
			if pm, ok := p.(map[string]any); ok {
				params = pm
			} else {
				return errorResult(fmt.Sprintf("Error: Invalid arguments for batch: call %d parameters must be an object.", i))
			}
		}
		calls = append(calls, batchCall{Tool: toolName, Parameters: params})
	}

	// Reject nested batch calls to bound recursion and depth.
	for _, call := range calls {
		if call.Tool == "batch" {
			return errorResult("Error: Invalid arguments for batch: nested batch calls are not allowed.")
		}
	}

	// Partition into parallel-safe runs: ReadOnly + ThreadSafe + permitted, with
	// pairwise non-conflicting resource keys. Everything else serializes.
	results := t.execute(ctx, calls, options)

	lines := make([]string, 0, len(results)+1)
	lines = append(lines, fmt.Sprintf("Ran %d sub-tool call(s):", len(results)))
	anyFailed := false
	for _, res := range results {
		label := res.label
		status := "ok"
		if res.status != StatusOK {
			status = "error"
			anyFailed = true
		}
		lines = append(lines, fmt.Sprintf("[%d] %s => %s", res.index+1, label, status))
	}
	// Only expose the first sub-error's detail; the model can call that tool
	// again individually if it wants the full output.
	firstErr := ""
	for _, res := range results {
		if res.status != StatusOK {
			firstErr = res.output
			break
		}
	}
	if anyFailed {
		lines = append(lines, "\nOne or more sub-tools failed.")
		if firstErr != "" {
			lines = append(lines, "First failure: "+firstErr)
		}
	}
	return Result{Status: StatusOK, Output: strings.Join(lines, "\n")}
}

type batchResult struct {
	index  int
	label  string
	status Status
	output string
}

// execute runs the calls respecting capability-based parallelism, reusing the
// same eligibility rule as the agent's auto-parallel executor: a call may run
// concurrently only when it is ReadOnly + ThreadSafe, already permission-allow
// (no denial prompt mid-flight), and its resource keys don't conflict with an
// in-flight sibling.
func (t batchTool) execute(ctx context.Context, calls []batchCall, options RunOptions) []batchResult {
	results := make([]batchResult, 0, len(calls))

	// Group consecutive eligibility runs to bound each parallel window.
	i := 0
	for i < len(calls) {
		if !t.callIsParallelEligible(calls[i], options) {
			results = append(results, t.runOne(ctx, calls[i], i, options))
			i++
			continue
		}
		// Collect the longest prefix eligible with pairwise non-conflicting keys.
		end := i + 1
		keys := [][]string{t.resourceKeysFor(calls[i])}
		for end < len(calls) {
			if !t.callIsParallelEligible(calls[end], options) {
				break
			}
			nextKeys := t.resourceKeysFor(calls[end])
			if conflictsWithAny(keys, nextKeys) {
				break
			}
			keys = append(keys, nextKeys)
			end++
		}
		results = append(results, t.runParallel(ctx, calls[i:end], i, options)...)
		i = end
	}
	return results
}

func (t batchTool) callIsParallelEligible(call batchCall, options RunOptions) bool {
	tool, ok := t.registry.Get(call.Tool)
	if !ok {
		return false
	}
	caps := CapabilitiesOf(tool)
	if caps.Effect != EffectReadOnly || !caps.ThreadSafe {
		return false
	}
	// Permission must be statically allow — a sub-tool that would need a
	// mid-flight permission prompt from a sibling would be incorrect. The batch
	// tool itself is read-only/allow; sub-calls that require approval keep the
	// batch results individually-reported but serial to the permission model.
	return tool.Safety().Permission == PermissionAllow
}

func (t batchTool) resourceKeysFor(call batchCall) []string {
	tool, ok := t.registry.Get(call.Tool)
	if !ok {
		return nil
	}
	caps := CapabilitiesOf(tool)
	if caps.ResourceKeys == nil {
		return nil
	}
	return caps.ResourceKeys(call.Parameters)
}

func conflictsWithAny(keys [][]string, next []string) bool {
	if len(next) == 0 {
		return false
	}
	for _, prev := range keys {
		if len(prev) != 0 && resourceKeysIntersect(prev, next) {
			return true
		}
	}
	return false
}

func resourceKeysIntersect(a, b []string) bool {
	seen := make(map[string]struct{}, len(a))
	for _, k := range a {
		if k != "" {
			seen[k] = struct{}{}
		}
	}
	for _, k := range b {
		if k != "" {
			if _, ok := seen[k]; ok {
				return true
			}
		}
	}
	return false
}

func (t batchTool) runParallel(ctx context.Context, calls []batchCall, baseIndex int, options RunOptions) []batchResult {
	results := make([]batchResult, len(calls))
	var wg sync.WaitGroup
	for idx := range calls {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = t.runOne(ctx, calls[idx], baseIndex+idx, options)
		}(idx)
	}
	wg.Wait()
	return results
}

func (t batchTool) runOne(ctx context.Context, call batchCall, index int, options RunOptions) batchResult {
	label := "<unknown>"
	if tool, ok := t.registry.Get(call.Tool); ok {
		label = tool.Name()
	} else {
		label = call.Tool
	}
	res := t.registry.RunWithOptions(ctx, call.Tool, call.Parameters, options)
	output := res.Output
	if output == "" {
		output = "(no output)"
	}
	return batchResult{index: index, label: label, status: res.Status, output: output}
}
