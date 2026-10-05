package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// The tool-result gate is the live relevance stage: it judges a large,
// non-error tool result AS IT IS PRODUCED and hides the blocks the classifier
// is confident are irrelevant, behind a stub that names a readable spill file.
//
// It is the tool-time counterpart of the compaction judge. Compaction cleans up
// stale history long after the model has already paid for it in every turn;
// the gate reduces the result before it ever enters the transcript.
//
// It is fail-open everywhere and off by default: a nil gate returns every
// result byte-identical, and any classifier/spill failure returns the original
// result unchanged. It never drops a tool result, never changes its role or
// ToolCallID, and never touches a result that carries an error or an image.
const (
	// gateBlockLines is the line cap for one judged block. A whole result is
	// never sent as one question: classifier recall collapses with input length
	// and is worst in the middle, so the result is split into small blocks and
	// each is judged separately.
	gateBlockLines = 25
	// gateBlockBytes is the byte cap for one judged block, and the reason a
	// short-but-large result (a 17-line web_search, a terse but wide bash dump)
	// is still gated: line count is not context cost. A block closes before the
	// line that would push it past this cap, so any body of two or more lines
	// with more than this many bytes yields at least two blocks.
	gateBlockBytes = 1500
	// gateMinBlocks is the smallest block count the gate will act on. A result
	// that is one block cannot be partially hidden.
	gateMinBlocks = 2
	// gateErrorQuestionID asks the second, semantic error belt. The regex belt is
	// deliberately narrow (structured failure signatures), so a result whose
	// failure is only described in prose is still caught here.
	gateErrorQuestionID = "gate_error"
	// gateCoverageQuestionID asks whether what SURVIVED the filter is enough to
	// proceed. The block question answers "is this block needed?" per block; this
	// one answers "is the kept text sufficient?" for the result as a whole, which
	// is the signal a re-query needs and a silent filter cannot provide.
	gateCoverageQuestionID = "gate_coverage"

	// Result metadata keys the gate sets. They are recorded in the result the loop
	// persists, displays, and appends, so a shadow run's decisions are measurable
	// from the session event log even though shadow never rewrites the body.
	gateMetaDecision     = "gate_decision"
	gateMetaHiddenLines  = "gate_hidden_lines"
	gateMetaKeptLines    = "gate_kept_lines"
	gateMetaRelevanceMin = "gate_relevance_min"
	gateMetaRelevanceMax = "gate_relevance_max"
	// gateMetaDropRelevanceMax is the highest score among the HIDDEN blocks. It is
	// bounded above by DropThreshold by construction, so it can never show whether
	// the classifier scores everything low — gateMetaRelevanceMin/Max over all
	// judged blocks exist for that. It is recorded only to explain a decision.
	gateMetaDropRelevanceMax = "gate_drop_relevance_max"
	gateMetaCoverage         = "gate_coverage"
	gateMetaRequery          = "gate_requery"
)

// gateStats is what the gate measured about one result's block scores. The
// decision alone cannot show whether the classifier is scoring every block low:
// the drop-set maximum is always below the drop threshold, so a broken and a
// healthy classifier look identical from it. relevanceMin/relevanceMax are taken
// over EVERY judged block and are not fixed by the decision rule, which is what
// makes an uncalibrated classifier visible.
type gateStats struct {
	relevanceMin float64
	relevanceMax float64
	dropMax      float64
	answered     int
}

// gateDecisions are the gate's outcomes, recorded in result metadata.
const (
	gateDecisionPruned   = "pruned"
	gateDecisionKept     = "kept"
	gateDecisionError    = "error_present"
	gateDecisionBelowMin = "below_min_prune_ratio"
	gateDecisionAboveMax = "above_max_hidden_ratio"
	gateDecisionShadow   = "shadow"
)

// gateErrorSignal is the deterministic error belt. It matches only structured,
// line-anchored failure signatures — never a bare word. A body scan for "error"
// or "failed" is structurally wrong for a code-reading tool: measured on real
// sessions, every match on an OK-status result was Go source containing "failed"
// in an identifier or comment (a false positive), which silently disabled the
// gate. Real tool failures already set status != OK; this belt only catches the
// catastrophic case where a tool returns OK but printed a failure, and the
// semantic half of the belt (the shared error question) covers prose failures.
var gateErrorSignal = regexp.MustCompile(`(?m)^[ \t]*(panic:|fatal error:|fatal:|Traceback \(most recent call last\)|--- FAIL:|FAIL[ \t]*$|Command failed with exit code [1-9])`)

// ToolResultGate carries the classifier and policy knobs for the live gate. A
// nil *ToolResultGate on Options means the feature is off and the tool path is
// byte-identical.
type ToolResultGate struct {
	Classifier    classifier.Classifier
	KeepThreshold float64
	DropThreshold float64
	MinPruneRatio float64
	// MaxHiddenRatio is the ceiling on the hidden share. At or above it the
	// result is kept whole rather than stubbed, so no classifier can black out an
	// entire result. <= 0 uses classifier.DefaultGateMaxHiddenRatio.
	MaxHiddenRatio float64
	MinBytes       int
	// CoverageThreshold is the kept-text coverage question's trigger: a coverage
	// probability BELOW it means the text that survived the filter is judged
	// insufficient to proceed, and a re-query is considered. It is only read when
	// Requery is on. <= 0 uses classifier.DefaultGateCoverageThreshold.
	CoverageThreshold float64
	// Requery, when true, allows the bounded re-query loop on a result the filter
	// left insufficient: a tool-less, out-of-band model call writes a NEW tool
	// call, it is executed under the normal policy, and the improved result is
	// APPENDED after the stub (never in place of it, so the spill recovery pointer
	// survives). The coverage question triggers the loop and can stop it early,
	// but it does not gate adoption: a real re-query result is always kept, since
	// appending can only add information. Bounded by MaxRequery. Off by default;
	// when off the gate only records the coverage signal.
	Requery bool
	// MaxRequery bounds the total re-query rounds for one result. This is the
	// loop's hard stop; <= 0 uses defaultGateMaxRequery.
	MaxRequery int
	// Shadow, when true, judges and records the decision but never rewrites the
	// result — how the gate is measured on real traffic before it is trusted.
	Shadow bool
}

// requeryMarker is the one-line label placed between the stub and the improved
// text a re-query adopted, so the model can tell where the filter's stub ends
// and the better result begins.
func requeryMarker(tool string) string {
	return "[gate] re-issued " + tool + " for a narrower result:"
}

// defaultGateMaxRequery bounds the re-query loop even when the config does not.
// It mirrors classifier.DefaultGateMaxRequery so the agent's own default matches
// the config layer's; the two are kept in step rather than re-literalised.
const defaultGateMaxRequery = classifier.DefaultGateMaxRequery

// gateRun carries the runtime the gate needs, supplied by the loop at the call
// site. provider/registry/permissionMode are read ONLY when the re-query loop is
// enabled, so a plain filter run never touches them.
type gateRun struct {
	provider       Provider
	registry       *tools.Registry
	permissionMode PermissionMode
	messages       []kajicoderuntime.Message
}

// gateBlock is one judged chunk of a tool result.
type gateBlock struct {
	id    string
	start int // first line index (inclusive)
	end   int // last line index (exclusive)
}

// maybeGateToolResult returns the tool result the model should see. It rewrites
// only result.Output, and only when the classifier is confidently irrelevant to
// a large, non-error result. Every other field survives unchanged.
//
// run carries the provider, registry, and permission mode the optional re-query
// loop needs; they are read only when that loop is enabled, so an ordinary
// filter run is a pure transform of the result.
func maybeGateToolResult(ctx context.Context, options Options, run gateRun, call ToolCall, result ToolResult) ToolResult {
	gate := options.ToolResultGate
	if gate == nil || gate.Classifier == nil {
		return result
	}
	gate = gate.withDefaults()

	body := result.Output
	if !gateableTool(call.Name) || result.Status != tools.StatusOK || len(result.Images) > 0 {
		return result
	}
	if readsSpillFile(call.Name, call.Arguments) {
		// The stub's recovery pointer is "read_file this spill file". Gating that
		// read would answer the pointer with another stub and another pointer, so
		// the one path the gate tells the model to take is never gated itself.
		return result
	}
	if len(body) < gate.MinBytes || isPrunedPlaceholder(body) {
		return result
	}
	lines := strings.Split(body, "\n")
	// Error belt, first half: a structured failure is never gated, because the
	// one line that explains the failure is the line a relevance filter is most
	// likely to drop. The result is returned verbatim, so every line is kept and
	// no block was judged (answered=0), which keeps the relevance keys absent.
	if gateErrorSignal.MatchString(body) {
		return result.withGateMeta(gateDecisionError, 0, len(lines), gateStats{})
	}

	blocks := gateBlocks(lines)
	if len(blocks) < gateMinBlocks {
		return result
	}

	goal := compactionGoal(run.messages)
	input := ""
	if len(call.Arguments) > 0 {
		input = call.Arguments
	}
	answers, errorPresent := gateJudge(ctx, gate, goal, call.Name, input, lines, blocks)

	hidden := make([]bool, len(blocks))
	hiddenLines := 0
	stats := gateStats{relevanceMin: 1}
	for i, block := range blocks {
		answer, ok := answers[block.id]
		if !ok || answer.Type != classifier.KindNoul {
			// No answer (batch error, missing id): KEEP. Keeping is cheap.
			continue
		}
		// Recorded over EVERY answered block, kept or hidden, so the pair reflects
		// the classifier's real output rather than the decision rule.
		stats.answered++
		stats.relevanceMin = min(stats.relevanceMin, answer.Probability)
		stats.relevanceMax = max(stats.relevanceMax, answer.Probability)
		if answer.Probability >= gate.DropThreshold {
			// Confident keep, or the uncertain band — both keep.
			continue
		}
		hidden[i] = true
		hiddenLines += block.end - block.start
		stats.dropMax = max(stats.dropMax, answer.Probability)
	}
	// The shared error belt: a block the classifier marked confidently
	// irrelevant is only hidden if the result as a whole reports no failure.
	if errorPresent {
		return result.withGateMeta(gateDecisionError, 0, len(lines), stats)
	}
	if hiddenLines == 0 {
		// Nothing was hidden, so the result is the whole truth and there is
		// nothing to be insufficient about.
		return result.withGateMeta(gateDecisionKept, 0, len(lines), stats)
	}
	if float64(hiddenLines)/float64(len(lines)) < gate.MinPruneRatio {
		return result.withGateMeta(gateDecisionBelowMin, hiddenLines, len(lines)-hiddenLines, stats)
	}
	if float64(hiddenLines)/float64(len(lines)) >= gate.MaxHiddenRatio {
		// A result the filter would mostly black out is not filtered, it is lost:
		// the model would see a stub and a spill path and nothing to reason about.
		return result.withGateMeta(gateDecisionAboveMax, hiddenLines, len(lines)-hiddenLines, stats)
	}
	if gate.Shadow {
		return result.withGateMeta(gateDecisionShadow, hiddenLines, len(lines)-hiddenLines, stats)
	}

	// Spill the body FIRST, so a hidden block is recoverable by reading the file
	// rather than by re-running a tool that may be expensive or non-idempotent (a
	// web_fetch, a read of a since-deleted file). A result the output budget
	// already spilled keeps that pointer — it holds the pre-budget (fuller) body,
	// strictly better than a fresh spill — but only when the reader can actually
	// open it: validation through the same spill-root check the scoped read tools
	// use rejects a foreign or swept path, so the stub never names a dead-end.
	path := ""
	if existing := result.Meta["spill_path"]; existing != "" {
		if _, ok := tools.ResolveSpillReadPath(existing); ok {
			path = existing
		}
	}
	if path == "" {
		path = tools.SpillOutput(call.Name, body)
	}
	if path == "" {
		return result.withGateMeta(gateDecisionKept, 0, len(lines), stats)
	}

	target := gateTarget(call)
	kept := renderGated(lines, blocks, hidden, hiddenLines, len(lines), stats.dropMax, path, call.Name, target)
	meta := gateMeta(result.Meta, gateDecisionPruned, hiddenLines, len(lines)-hiddenLines, stats, path)

	// Coverage: is what SURVIVED enough to proceed? This is the signal a silent
	// filter cannot produce and the trigger the re-query loop runs on. It is only
	// asked when a re-query could act on it, so a plain gate pays no extra cost.
	if gate.Requery && gateCoverageInsufficient(ctx, gate, goal, call, target, kept) {
		meta = metaWithCoverage(meta, false)
		improved, tool, rounds := gateRequery(ctx, options, run, call, target, kept)
		if improved != "" {
			// APPEND, never replace: the stub carries the spill path, which is the
			// only way the model can recover the body this gate hid. Replacing the
			// stub with the improved text would silently delete that recovery
			// pointer and turn "dropped" back into "lost".
			kept = kept + "\n" + requeryMarker(tool) + "\n" + improved
			// Re-ask about the ADOPTED text, not the pre-re-query stub: the
			// recorded coverage must describe what the model actually receives.
			meta = metaWithCoverage(meta, !gateCoverageInsufficient(ctx, gate, goal, call, target, improved))
		}
		meta[gateMetaRequery] = strconv.Itoa(rounds)
	}

	result.Output = kept
	result.Truncated = true
	result.Meta = meta
	return result
}

func metaWithCoverage(meta map[string]string, sufficient bool) map[string]string {
	meta[gateMetaCoverage] = strconv.FormatBool(sufficient)
	return meta
}

func (g *ToolResultGate) withDefaults() *ToolResultGate {
	out := *g
	if out.KeepThreshold <= 0 {
		out.KeepThreshold = classifier.DefaultGateKeepThreshold
	}
	if out.DropThreshold <= 0 {
		out.DropThreshold = classifier.DefaultGateDropThreshold
	}
	if out.DropThreshold > out.KeepThreshold {
		out.DropThreshold = out.KeepThreshold
	}
	if out.MinPruneRatio <= 0 {
		out.MinPruneRatio = classifier.DefaultGateMinPruneRatio
	}
	if out.MaxHiddenRatio <= 0 {
		out.MaxHiddenRatio = classifier.DefaultGateMaxHiddenRatio
	}
	if out.MaxHiddenRatio < out.MinPruneRatio {
		// Min above Max is unsatisfiable (every result is both too small a prune
		// and too large a hide), which would silently turn the gate into a no-op.
		// The ceiling wins: hiding too much is the failure that blinds the model.
		out.MaxHiddenRatio = out.MinPruneRatio
	}
	if out.MinBytes <= 0 {
		out.MinBytes = classifier.DefaultGateMinBytes
	}
	if out.CoverageThreshold <= 0 {
		out.CoverageThreshold = classifier.DefaultGateCoverageThreshold
	}
	if out.MaxRequery <= 0 {
		out.MaxRequery = defaultGateMaxRequery
	}
	return &out
}

// gateableTool reports whether a tool's output may be gated. The set that must
// never be gated is a denylist rather than an allowlist so a new read-ish tool
// is covered by default; every mutation, every structural state tool whose
// arguments a preserved-state reader parses, and every agent/meta tool is
// excluded, because their bodies are either tiny or consumed structurally.
func gateableTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "apply_patch", "multi_edit",
		"todo_read", "todo_write", "update_plan",
		"ask_user", "request_permissions",
		"Task", "TaskOutput", "TaskStop", "GenerateAgent",
		"skill", "recall", "recipe_run", "tool_search", "batch":
		return false
	}
	return strings.TrimSpace(name) != ""
}

// spillReadTools are the read tools a gate stub can point the model at when it
// names a spill file, so a read of one must never be gated.
var spillReadTools = map[string]bool{"read_file": true, "read_minified_file": true}

// readToolPathKeys are the argument keys a read tool accepts for its path,
// including the aliases read_file's schema declares. Missing one would let a
// recovery read slip through and be gated, re-creating the stub loop.
var readToolPathKeys = []string{"path", "file_path", "filepath", "filename", "file"}

// readsSpillFile reports whether a call is a read of a file inside the spill
// directory — the recovery step a gated result's stub instructs the model to
// take. It fails closed: an unparseable argument list is treated as not a spill
// read, so a malformed call is gated like any other.
func readsSpillFile(tool string, arguments string) bool {
	if !spillReadTools[tool] {
		return false
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return false
	}
	for _, key := range readToolPathKeys {
		path, ok := args[key].(string)
		if !ok || strings.TrimSpace(path) == "" {
			continue
		}
		if _, ok := tools.ResolveSpillReadPath(path); ok {
			return true
		}
	}
	return false
}

// gateBlocks groups lines into judged blocks, closing each block at
// gateBlockLines lines or before the line that would push it past
// gateBlockBytes bytes, whichever comes first. Closing BEFORE an overflowing
// line is what guarantees that any body of two or more lines and more than
// gateBlockBytes bytes splits into at least gateMinBlocks blocks; a single-line
// body is one block and is left alone. It returns nil when fewer than
// gateMinBlocks blocks result, since a lone block cannot be partially hidden.
func gateBlocks(lines []string) []gateBlock {
	if len(lines) < gateMinBlocks {
		return nil
	}
	blocks := make([]gateBlock, 0, len(lines)/gateBlockLines+1)
	start := 0
	bytes := 0
	for i, line := range lines {
		if i > start && (i-start >= gateBlockLines || bytes+len(line)+1 > gateBlockBytes) {
			blocks = append(blocks, gateBlock{id: fmt.Sprintf("b%03d", len(blocks)), start: start, end: i})
			start = i
			bytes = 0
		}
		bytes += len(line) + 1
	}
	blocks = append(blocks, gateBlock{id: fmt.Sprintf("b%03d", len(blocks)), start: start, end: len(lines)})
	if len(blocks) < gateMinBlocks {
		return nil
	}
	return blocks
}

// gateJudge asks the classifier about every block, in size-bounded batches, and
// reports whether the shared error question fired. A batch that errors
// contributes nothing (fail-open per batch): its blocks carry no answer and are
// therefore kept.
func gateJudge(ctx context.Context, gate *ToolResultGate, goal, tool, input string, lines []string, blocks []gateBlock) (map[string]classifier.Answer, bool) {
	answered := map[string]classifier.Answer{}
	errorPresent := false
	batch := make([]gateBlock, 0, len(blocks))
	bytes := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		result, err := gate.Classifier.Classify(ctx, classifier.Request{
			State:     gateState(goal, tool, input, lines, batch),
			Questions: gateQuestions(batch),
		})
		if err == nil {
			for id, answer := range result {
				answered[id] = answer
			}
			if answer, ok := result[gateErrorQuestionID]; ok && answer.Type == classifier.KindNoul && answer.Probability >= gate.KeepThreshold {
				errorPresent = true
			}
		}
		batch = batch[:0]
		bytes = 0
	}
	for _, block := range blocks {
		size := blockBytes(lines, block)
		if len(batch) > 0 && bytes+size > judgeStateByteBudget {
			flush()
		}
		batch = append(batch, block)
		bytes += size
	}
	flush()
	return answered, errorPresent
}

// blockBytes approximates a block's classifier-state cost after truncation,
// mirroring what gateState actually sends (which caps each block at
// judgeResultBytes), so batches pack as tightly as the request allows.
func blockBytes(lines []string, block gateBlock) int {
	total := 0
	for i := block.start; i < block.end; i++ {
		total += len(lines[i]) + 1
	}
	return min(total, judgeResultBytes) + 32
}

// gateState and gateQuestions build the classifier ask. Every judge-bound string
// passes through scrubForClassifier, and the tool body is always a VALUE in the
// state — never spliced into an instruction — because tool output is untrusted
// input and the classifier does not treat state as hostile by default.
func gateState(goal, tool, input string, lines []string, batch []gateBlock) map[string]any {
	blocks := make([]map[string]string, 0, len(batch))
	for _, block := range batch {
		body := strings.Join(lines[block.start:block.end], "\n")
		blocks = append(blocks, map[string]string{
			"id":   block.id,
			"text": scrubForClassifier(truncateHead(body, judgeResultBytes)),
		})
	}
	return map[string]any{
		"goal":   scrubForClassifier(truncateHead(goal, judgeGoalBytes)),
		"tool":   tool,
		"input":  scrubForClassifier(truncateHead(input, judgeInputBytes)),
		"blocks": blocks,
	}
}

func gateQuestions(batch []gateBlock) map[string]classifier.Question {
	questions := make(map[string]classifier.Question, len(batch)+1)
	for _, block := range batch {
		questions[block.id] = classifier.Noul(
			"For item "+block.id+": would the assistant have to read this block of tool output to accomplish the goal correctly? Judge only this block.",
			"The assistant must read this text to do the task right.",
			"The task can be completed correctly without ever reading this block. Do not mark a block needed only because it comes from the same file or command as something that is needed.",
		)
	}
	questions[gateErrorQuestionID] = classifier.Noul(
		"Does any block of this tool output report a failure, error, exception, or non-zero exit that the assistant must see to proceed correctly?",
		"This output contains a failure the assistant must see.",
		"This output reports only successful output.",
	)
	return questions
}

// gateCoverageInsufficient asks the single whole-result question: is the text
// that SURVIVED the filter enough to accomplish the goal? A probability below
// the coverage threshold means "not enough", which is the trigger the re-query
// loop acts on. Absent or wrong-kind answers are treated as sufficient, so a
// classifier hiccup never triggers a re-query.
func gateCoverageInsufficient(ctx context.Context, gate *ToolResultGate, goal string, call ToolCall, target, kept string) bool {
	result, err := gate.Classifier.Classify(ctx, classifier.Request{
		State: map[string]any{
			"goal": scrubForClassifier(truncateHead(goal, judgeGoalBytes)),
			"tool": call.Name,
			"input": scrubForClassifier(truncateHead(
				strings.TrimSpace(call.Arguments), judgeInputBytes)),
			"kept_output": scrubForClassifier(truncateHead(kept, judgeResultBytes)),
		},
		Questions: map[string]classifier.Question{
			gateCoverageQuestionID: classifier.Noul(
				"The text in kept_output is what the assistant will receive from a tool call. Does it contain everything the assistant needs to make progress on the goal, or must the assistant obtain more?",
				"kept_output is sufficient to proceed correctly.",
				"kept_output is missing information the assistant needs, so it must ask again or retrieve more.",
			),
		},
	})
	if err != nil {
		return false
	}
	answer, ok := result[gateCoverageQuestionID]
	if !ok || answer.Type != classifier.KindNoul {
		return false
	}
	return answer.Probability < gate.CoverageThreshold
}

// renderGated returns the surviving blocks verbatim, in order, with each run of
// hidden blocks replaced by a marker and a stub that names the tool, its target,
// and the recovery file. Naming the tool and target is what lets the model
// re-issue a better call itself: a stub that only says "N lines hidden" tells it
// nothing about HOW to ask again.
func renderGated(lines []string, blocks []gateBlock, hidden []bool, hiddenLines, totalLines int, dropMaxRel float64, path, tool, target string) string {
	out := make([]string, 0, len(lines))
	for index := 0; index < len(blocks); {
		if !hidden[index] {
			out = append(out, lines[blocks[index].start:blocks[index].end]...)
			index++
			continue
		}
		runLines := 0
		for index < len(blocks) && hidden[index] {
			runLines += blocks[index].end - blocks[index].start
			index++
		}
		out = append(out, fmt.Sprintf("[… %d lines hidden …]", runLines))
	}
	subject := tool
	if target != "" {
		subject = tool + " " + target
	}
	stub := fmt.Sprintf("[gate] %d of %d lines of %s hidden (max hidden relevance %.2f); full output saved to %s; read_file it or re-issue %s with a narrower target if you need it",
		hiddenLines, totalLines, subject, dropMaxRel, path, tool)
	return strings.Join(out, "\n") + "\n" + stub
}

// gateTarget summarizes a call's arguments into a single line naming WHAT it was
// aimed at (a path, a pattern, a command, a URL). It is only for the model-facing
// stub, so it reads well-known keys and falls back to a trimmed prefix — it is
// never spliced into a classifier instruction.
func gateTarget(call ToolCall) string {
	if strings.TrimSpace(call.Arguments) == "" {
		return ""
	}
	args := map[string]any{}
	if err := decodeToolArguments(call.Arguments, &args); err != nil {
		return ""
	}
	for _, key := range []string{"path", "file", "file_path", "pattern", "query", "url", "command", "glob", "cwd"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return fmt.Sprintf("%s=%q", key, truncateHead(strings.TrimSpace(value), 120))
		}
	}
	return ""
}

func (r ToolResult) withGateMeta(decision string, hiddenLines, keptLines int, stats gateStats) ToolResult {
	r.Meta = gateMeta(r.Meta, decision, hiddenLines, keptLines, stats, "")
	return r
}

func gateMeta(meta map[string]string, decision string, hiddenLines, keptLines int, stats gateStats, spillPath string) map[string]string {
	if meta == nil {
		meta = map[string]string{}
	}
	meta[gateMetaDecision] = decision
	meta[gateMetaHiddenLines] = strconv.Itoa(hiddenLines)
	meta[gateMetaKeptLines] = strconv.Itoa(keptLines)
	// Recorded over every answered block, so these two numbers answer "is the
	// classifier scoring everything low?" — which the decision alone cannot.
	if stats.answered > 0 {
		meta[gateMetaRelevanceMin] = strconv.FormatFloat(stats.relevanceMin, 'f', 3, 64)
		meta[gateMetaRelevanceMax] = strconv.FormatFloat(stats.relevanceMax, 'f', 3, 64)
	}
	if hiddenLines > 0 {
		meta[gateMetaDropRelevanceMax] = strconv.FormatFloat(stats.dropMax, 'f', 3, 64)
	}
	if spillPath != "" {
		meta["spill_path"] = spillPath
	}
	return meta
}
