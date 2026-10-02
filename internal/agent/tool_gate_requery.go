package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// The gate's re-query loop is the "ask again, without polluting the parent" half
// of tool-time filtering. When the gate hides most of a result AND the coverage
// question says what survived is not enough, a tool-less, out-of-band model call
// writes a NEW, narrower tool call for the same underlying need. That call is
// executed under the normal permission policy and only the resulting text is
// adopted, appended after the stub.
//
// Two properties make it safe:
//
//   - Isolation: the generator runs with its own tiny prompt and never sees the
//     parent transcript; the generated call is executed directly (not through the
//     loop), so no attempt enters the parent's messages. The parent still gets
//     exactly one tool_result, so strict provider replay is untouched.
//   - Bounded and fail-closed: it runs only on read-only, auto-allowed tools (a
//     mutating or permission-prompting tool can never be re-issued unattended),
//     it stops on the first sufficient answer, and any error leaves the stub as
//     it was. It can never loop: MaxRequery caps the rounds and a repeated
//     identical call stops the loop.
//
// It is off by default (ToolResultGate.Requery). With it off, the gate only
// records the coverage signal; the parent model, which can generate freely,
// remains the actor that re-queries.

// gateRequeryResultBytes caps the adopted improved text. A good re-query returns
// the relevant subset, which is smaller than what was hidden; the cap only
// guards against a generator that re-issues a call returning as much as before.
const gateRequeryResultBytes = 24_000

// requeryToolOptions strips the tool-call and permission callbacks from the
// options a re-query's tool call runs under. A re-query is unattended: it must
// never surface a permission prompt (it only re-issues tools the policy already
// auto-allows, so a prompt would mean a bug) and must never fire the user's
// beforeTool/afterTool hooks or announce itself as a tool call, which would make
// an invisible retry look like an extra tool row to the surface. The sandbox
// engine is kept: it is a safety boundary, not a UI surface, and a denied
// re-query must fail closed rather than run unsandboxed.
func requeryToolOptions(options Options) Options {
	out := options
	out.OnPermissionRequest = nil
	out.OnPermission = nil
	out.Hooks = nil
	out.OnToolCall = nil
	out.OnToolResult = nil
	out.OnToolProgress = nil
	// OnPhase and OnToolCallStart/Delta are intentionally NOT stripped here: the
	// loop emits one PhaseRetrying liveness update when it adopts a result, and
	// that is a status signal, not a tool call.
	return out
}

// gateRequery runs the bounded re-query loop for one gated result. It returns the
// best improved text it produced ("" only when no round produced any) and the
// number of rounds attempted.
//
// The coverage question TRIGGERS the loop and lets it stop early, but it does not
// gate adoption: a re-query that came back with a real result is always worth
// keeping, because the caller APPENDS it after the stub rather than replacing it,
// so it can only add information about the goal and never remove the stub or its
// recovery pointer. Requiring the classifier to bless the text as "sufficient"
// before keeping it made this loop inert in practice — the same no-clean-tail
// distribution that limits the gate's drop side means that verdict almost never
// fires, so measured rounds ran and their output was discarded.
func gateRequery(ctx context.Context, options Options, run gateRun, call ToolCall, target, stub string) (string, string, int) {
	gate := options.ToolResultGate.withDefaults()
	if !gate.Requery || run.provider == nil || run.registry == nil {
		return "", "", 0
	}

	goal := compactionGoal(run.messages)
	best := ""
	bestTool := ""
	previous := stub
	rounds := 0
	for attempted := 0; attempted < gate.MaxRequery; attempted++ {
		plan, err := generateRequeryCall(ctx, run.provider, options, goal, call, target, previous)
		if err != nil || plan.Name == "" {
			break
		}
		if sameCall(call, plan) || !requeryAllowed(run.registry, plan, options) {
			// The generator proposed the same call (no progress) or a call that is
			// not a safe read. Stop rather than churn.
			break
		}
		rounds++
		attempt, abortErr := executeToolCall(ctx, run.registry, plan, run.permissionMode, requeryToolOptions(options))
		if abortErr != nil || attempt.Status != tools.StatusOK {
			break
		}
		text := truncateHead(attempt.Output, gateRequeryResultBytes)
		if strings.TrimSpace(text) == "" {
			break
		}
		best, bestTool = text, plan.Name
		if !gateCoverageInsufficient(ctx, gate, goal, call, target, text) {
			// The improved result is judged sufficient: no need for another round.
			emitPhase(options, PhaseRetrying, "re-queried "+plan.Name)
			break
		}
		// Still judged insufficient — feed it back as the new baseline and try
		// once more, if the budget allows. The best text is still returned.
		previous = text
	}
	return best, bestTool, rounds
}

// requeryAllowed reports whether a generated call may be re-issued unattended.
// It is the same effect contract the parallel batcher uses: only a read-only tool
// the policy already auto-allows (no interactive prompt on the hot path) is
// eligible, and the gate's own structural denylist (todo/plan/agent/meta tools
// whose bodies are consumed, not read) still applies.
func requeryAllowed(registry *tools.Registry, plan ToolCall, options Options) bool {
	if registry == nil || !gateableTool(plan.Name) {
		return false
	}
	tool, found := registry.Get(plan.Name)
	if !found {
		return false
	}
	args := map[string]any{}
	if strings.TrimSpace(plan.Arguments) != "" {
		if err := decodeToolArguments(plan.Arguments, &args); err != nil {
			return false
		}
	}
	if tools.CapabilitiesForArgsOf(tool, args).Effect != tools.EffectReadOnly {
		return false
	}
	return effectivePermission(tool, args) == tools.PermissionAllow
}

// sameCall reports whether the generated call is byte-identical to the original,
// so re-issuing it could only return the same bytes.
func sameCall(original, plan ToolCall) bool {
	return plan.Name == original.Name &&
		strings.TrimSpace(plan.Arguments) == strings.TrimSpace(original.Arguments)
}

// generatedCall is the generator's reply: a tool call to re-issue, or nothing.
type generatedCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// generateRequeryCall asks the model, in a separate tool-less call, for a better
// tool call. It returns a zero generatedCall when the model declines or replies
// with nothing parseable.
func generateRequeryCall(ctx context.Context, provider Provider, options Options, goal string, call ToolCall, target, insufficient string) (ToolCall, error) {
	request := kajicoderuntime.CompletionRequest{
		Messages: []kajicoderuntime.Message{
			{Role: kajicoderuntime.MessageRoleSystem, Content: requerySystemPrompt},
			{Role: kajicoderuntime.MessageRoleUser, Content: requeryUserPrompt(goal, call, target, insufficient)},
		},
	}
	stream, err := provider.StreamCompletion(ctx, request)
	if err != nil {
		return ToolCall{}, err
	}
	// The generator's tokens are accounted, but its text is never shown: the
	// re-query stays invisible, exactly like the compaction summarizer.
	collected := kajicoderuntime.CollectStreamWithOptions(ctx, stream, kajicoderuntime.CollectOptions{OnUsage: options.OnUsage})
	if collected.Error != "" {
		return ToolCall{}, nil
	}
	return parseGeneratedCall(collected.Text), nil
}

// parseGeneratedCall extracts the first JSON object from the model's reply and
// reads a tool name plus arguments from it. A reply with no name (or the
// sentinel {"done":true}) yields a zero call, which stops the loop.
func parseGeneratedCall(text string) ToolCall {
	raw, ok := recoverableToolArguments(strings.TrimSpace(text))
	if !ok {
		return ToolCall{}
	}
	var parsed generatedCall
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ToolCall{}
	}
	name := strings.TrimSpace(parsed.Name)
	if name == "" || len(parsed.Arguments) == 0 {
		return ToolCall{}
	}
	encoded, err := json.Marshal(parsed.Arguments)
	if err != nil {
		return ToolCall{}
	}
	return ToolCall{Name: name, Arguments: string(encoded)}
}

const requerySystemPrompt = `You improve ONE tool call so its output is more useful to another assistant.

You are given the assistant's goal, the tool call it already made, and the text it received back, which was judged insufficient. Reply with ONLY a JSON object and nothing else:
{"name": "<tool name>", "arguments": { ... }}

Rules:
- Choose a READ-ONLY tool (read_file, grep, glob, list_directory, web_search, web_fetch, and similar). Never choose a tool that writes, deletes, runs a command, or prompts for permission.
- Make the call NARROWER and different from the one already made: tighten a search pattern, target a single path, or fetch one specific URL. Repeating the same call is useless.
- If no better read-only call exists, reply {"done": true}.`

func requeryUserPrompt(goal string, call ToolCall, target, insufficient string) string {
	var b strings.Builder
	b.WriteString("Goal:\n")
	b.WriteString(truncateHead(strings.TrimSpace(goal), judgeGoalBytes))
	b.WriteString("\n\nTool call already made:\n")
	b.WriteString(call.Name)
	if target != "" {
		b.WriteString(" ")
		b.WriteString(target)
	}
	b.WriteString("\n")
	b.WriteString(truncateHead(strings.TrimSpace(call.Arguments), judgeInputBytes))
	b.WriteString("\n\nOutput received (judged insufficient):\n")
	b.WriteString(truncateHead(insufficient, judgeResultBytes))
	b.WriteString("\n\nReply with one JSON tool call, or {\"done\": true}.")
	return b.String()
}
