package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dishant0406/KajiCode/internal/config"
	"github.com/dishant0406/KajiCode/internal/harness"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// requestLearnMeta is the Meta key the learn tool sets to request a manual
// learning pass at the next safe boundary.
const requestLearnMeta = "request_learn"

// Learned-memory prompt-bounds. Durable state is stored unbounded on disk (up to
// the configured cap), but only a bounded, truncated slice is ever injected into
// the prompt so a growing memory can never blow the model's context window.
const (
	// learnedPromptMaxPerKind caps how many entries of a given kind are surfaced
	// in the <learned_memory> prompt block.
	learnedPromptMaxPerKind = 6
	// learnedPromptMaxContentLen truncates each entry's content shown in the
	// <learned_memory> prompt block.
	learnedPromptMaxContentLen = 160
	// learnedMemoryTokenBudgetValue bounds the whole <learned_memory> block so a
	// store with many lessons can never monopolize the context budget.
	learnedMemoryTokenBudgetValue = 1200
)

// usedLesson records a lesson that was surfaced in the prompt, so a completed
// run can reinforce exactly what it re-used.
type usedLesson struct {
	store *harness.Store
	kind  harness.Kind
	id    string
}

// storeDir keys a lesson for de-duplication within a run.
func (u usedLesson) storeDir() string {
	if u.store == nil {
		return ""
	}
	return u.store.Dir
}

// LearningEngine is the self-learning controller for a run. It is the agent-side
// counterpart to the harness pipeline (review → plan → apply → record) and to
// the manual learn tool. Hooking a non-nil engine into agent.Options.Learning
// enables automatic learning; nil leaves the loop byte-identical, exactly like
// Profile, SelfCorrect, and Trace.
//
// Learning is event-driven. The loop reports what actually happened — a tool
// call that failed, a user correction — and the engine runs a pass when those
// signals say a lesson is worth capturing, subject to a debounce, plus once
// after a compaction and once when a run completes. It is safe for concurrent
// use (the mutex guards counters and the pending flags).
//
// Provider and stores are injected at construction (by the CLI), so the loop
// hook needs no provider plumbing. Proposals are routed by scope: session-scoped
// lessons land in the session store, everything else in the project store.
type LearningEngine struct {
	cfg          config.LearningConfig
	provider     kajicoderuntime.Provider
	globalStore  *harness.Store
	projectStore *harness.Store
	sessionStore *harness.Store

	mu sync.Mutex
	// pendingSignals holds human-readable reasons a pass is warranted. A pass
	// consumes them; the reasons become the plan's focus so the refinement targets
	// the failure the agent actually hit.
	pendingSignals []string
	// failedTools / fixedTools track the per-tool outcome history for the current
	// run: a tool that failed and later succeeded is the strongest "you learned
	// something" signal (the model found a working approach).
	failedTools map[string]bool
	fixedTools  map[string]bool
	// compactionSignal is set when a compaction happened this turn.
	compactionSignal bool
	// manualReady is set by the learn tool's run action.
	manualReady bool
	// used records the lessons surfaced in the prompt this run, keyed so a run
	// that surfaces the same lesson on several turns reinforces it once.
	used     map[string]usedLesson
	lastPass time.Time
	finished bool
}

// NewLearningEngine builds the engine. cfg is the effective (defaulted) learning
// config; provider must be non-nil or the engine is a no-op. globalStore,
// projectStore, and sessionStore are the harness stores for each scope. A nil
// store for a scope means a proposal destined for it is promoted to the next
// broader scope (session -> project -> global) rather than silently dropped.
func NewLearningEngine(cfg config.LearningConfig, provider kajicoderuntime.Provider, globalStore, projectStore, sessionStore *harness.Store) *LearningEngine {
	return &LearningEngine{
		cfg:          cfg.Effective(),
		provider:     provider,
		globalStore:  globalStore,
		projectStore: projectStore,
		sessionStore: sessionStore,
		failedTools:  map[string]bool{},
		fixedTools:   map[string]bool{},
		used:         map[string]usedLesson{},
	}
}

// Enabled reports whether the engine will act. An engine with a nil provider or
// a disabled config is inert.
func (e *LearningEngine) Enabled() bool {
	return e != nil && e.provider != nil && e.cfg.IsEnabled()
}

// NoteToolResult lets the loop hand each tool result to the engine. It records
// the failure/success history that drives the eager triggers: a tool that fails
// and later succeeds becomes a pending signal. A manual learn-tool request
// (Meta["request_learn"]=="true") arms a pass at the next boundary.
func (e *LearningEngine) NoteToolResult(result ToolResult) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if result.Status == tools.StatusError {
		e.failedTools[result.Name] = true
		e.fixedTools[result.Name] = false
	} else if e.failedTools[result.Name] && !e.fixedTools[result.Name] {
		// This tool failed earlier and now succeeded: the model worked out how to
		// use it. Capture the lesson.
		e.fixedTools[result.Name] = true
		e.addSignalLocked("tool " + result.Name + " failed then succeeded")
	}
	if result.Meta[requestLearnMeta] == "true" {
		e.manualReady = true
	}
}

// NoteUserTurn lets the loop report a user turn that looks like a correction or
// re-instruction — the classic "the agent got it wrong and the user told it how"
// signal that is otherwise lost. It is a cheap, local classification.
func (e *LearningEngine) NoteUserTurn(text string) {
	if e == nil || !looksLikeCorrection(text) {
		return
	}
	e.mu.Lock()
	e.addSignalLocked("user correction")
	e.mu.Unlock()
}

// NoteCompaction records that a context compaction happened, which on its own is
// enough to warrant a pass: a compaction means a lot of context is about to be
// lost, so it is a natural capture point.
func (e *LearningEngine) NoteCompaction() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.compactionSignal = true
	e.mu.Unlock()
}

// addSignalLocked records a pending signal (callers hold the mutex).
func (e *LearningEngine) addSignalLocked(reason string) {
	for _, existing := range e.pendingSignals {
		if existing == reason {
			return
		}
	}
	e.pendingSignals = append(e.pendingSignals, reason)
}

// RunTurn is the loop hook, called once per assistant turn after any compaction.
// It runs a pass when a signal is pending, a compaction happened (when enabled),
// or a manual request was armed, subject to the debounce. It returns whether any
// learning was applied, so the loop can splice the fresh learned-memory block
// into the next request (same-session pickup) without an extra store read on
// every turn.
func (e *LearningEngine) RunTurn(ctx context.Context, messages []kajicoderuntime.Message) bool {
	return e.run(ctx, messages, false)
}

// Finish is the end-of-run hook. It runs a final pass over the completed session
// turn is processed here (a failure→fix or correction raised on the last turn
// that the loop did not get to act on) and the lessons the run actually surfaced
// are reinforced. A clean run with no signal performs no provider work, so a
// routine session costs nothing extra; it is idempotent per run.
func (e *LearningEngine) Finish(ctx context.Context, messages []kajicoderuntime.Message) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return false
	}
	e.finished = true
	e.mu.Unlock()
	applied := e.run(ctx, messages, true)
	e.Reinforce()
	return applied
}

// run is the shared implementation of RunTurn and Finish. force bypasses the
// debounce (a run boundary is a natural capture point).
func (e *LearningEngine) run(ctx context.Context, messages []kajicoderuntime.Message, force bool) bool {
	if !e.Enabled() {
		return false
	}
	e.mu.Lock()
	signals := e.pendingSignals
	manual := e.manualReady
	compacted := e.compactionSignal
	due := manual || len(signals) > 0 || (compacted && e.cfg.IsCompactEnabled())
	if !due {
		e.mu.Unlock()
		return false
	}
	// Debounce: an automatic pass within the window is deferred, NOT dropped —
	// the pending signals stay queued and the next turn or the end of the run
	// processes them. A manual request bypasses the debounce.
	if !force && !manual && !e.lastPass.IsZero() && time.Since(e.lastPass) < e.cfg.Debounce() {
		e.mu.Unlock()
		return false
	}
	// The pass runs now: consume the queued signals and one-shot flags.
	e.pendingSignals = nil
	e.compactionSignal = false
	e.manualReady = false
	e.lastPass = time.Now()
	e.mu.Unlock()

	conversation := renderTranscript(messages)
	decision, err := harness.RunReview(ctx, harness.ReviewOptions{
		Provider:     e.provider,
		Conversation: conversation,
	})
	if err != nil || decision == nil || !decision.ShouldLearn {
		return false
	}
	// The plan's focus is the union of the model's own instructions and the local
	// signals that triggered the pass, so the refinement targets the failure the
	// agent actually hit rather than a generic transcript summary.
	instructions := strings.TrimSpace(decision.Instructions)
	if len(signals) > 0 {
		joined := "Observed during this run: " + strings.Join(signals, "; ") + "."
		if instructions == "" {
			instructions = joined
		} else {
			instructions = instructions + "\n" + joined
		}
	}

	plan, err := harness.PlanLearning(ctx, harness.PlanOptions{
		Provider:     e.provider,
		Conversation: conversation,
		State:        e.loadProjectState(),
		Refinements:  e.loadRefinements(),
		Instructions: instructions,
		ScopePolicy:  "Default to the project scope. Use the session scope only for lessons that apply to this one session, and the global scope only for durable cross-project lessons.",
	})
	if err != nil || len(plan.Proposals) == 0 {
		return false
	}
	return e.apply(plan)
}

// apply routes the plan's proposals by scope and writes each group to the store
// that owns it, so a session-scoped lesson never lands in the project store and
// vice versa. Each store is pruned and capped afterwards.
func (e *LearningEngine) apply(plan harness.LearningPlan) bool {
	byScope := map[harness.Scope][]harness.EditProposal{}
	for _, proposal := range plan.Proposals {
		scope := proposal.Scope
		if scope == "" {
			scope = harness.ScopeProject
		}
		byScope[scope] = append(byScope[scope], proposal)
	}

	applied := false
	for scope, proposals := range byScope {
		store := e.storeForScope(scope)
		if store == nil {
			continue
		}
		outcome := harness.ApplyLearning(store, harness.ApplyOptions{
			Plan:    harness.LearningPlan{Summary: plan.Summary, Rationale: plan.Rationale, Proposals: proposals, Baseline: plan.Baseline},
			Trigger: "auto",
			Now:     time.Now,
		})
		if countApplied(outcome.Outcomes) > 0 {
			applied = true
		}
		store.PruneStale(e.cfg.PruneAfterDays, e.cfg.MaxEntries, time.Now())
	}
	return applied
}

// storeForScope resolves the store a proposal's scope writes to. A missing
// narrower store falls back to a broader one (session -> project -> global) so a
// lesson is never silently dropped.
func (e *LearningEngine) storeForScope(scope harness.Scope) *harness.Store {
	switch scope {
	case harness.ScopeSession:
		if e.sessionStore != nil {
			return e.sessionStore
		}
		fallthrough
	case harness.ScopeProject:
		if e.projectStore != nil {
			return e.projectStore
		}
		return e.globalStore
	default:
		return e.globalStore
	}
}

// countApplied reports how many proposals in a set actually landed. A proposal
// that was rejected (conflict, validation, duplicate) is not "applied" even if
// the pipeline ran.
func countApplied(outcomes []harness.EditOutcome) int {
	n := 0
	for _, outcome := range outcomes {
		if outcome.Applied {
			n++
		}
	}
	return n
}

func (e *LearningEngine) loadProjectState() harness.State {
	if e.projectStore == nil {
		return harness.State{Scope: harness.ScopeProject}
	}
	state, _ := e.projectStore.Load()
	return state
}

func (e *LearningEngine) loadGlobalState() harness.State {
	if e.globalStore == nil {
		return harness.State{Scope: harness.ScopeGlobal}
	}
	state, _ := e.globalStore.Load()
	return state
}

func (e *LearningEngine) loadSessionState() harness.State {
	if e.sessionStore == nil {
		return harness.State{Scope: harness.ScopeSession}
	}
	state, _ := e.sessionStore.Load()
	return state
}

func (e *LearningEngine) loadRefinements() []harness.RefinementEvent {
	return e.loadProjectState().Refinements
}

// Context renders the bounded, merged learned memory as a system prompt section.
// It is called at run start by buildSystemPromptParts so the model sees durable
// lessons on the first turn, and later by the loop's same-session refresh to pick
// up newly applied lessons. The merged set is recall-ordered (freshest-first),
// capped per kind, and obeys a whole-block token budget, so a growing store can
// never blow the context window and is biased toward memory that has actually
// been re-used. Each surfaced lesson is remembered for reinforcement.
func (e *LearningEngine) Context() string {
	if e == nil {
		return ""
	}
	// Merge broadest-first: global, then project over it, then session over that.
	project := harness.MergeHarnessStates(e.loadGlobalState(), e.loadProjectState())
	session := e.loadSessionState()
	merged := harness.MergeHarnessStates(harness.State{Entries: project}, session)
	if len(merged) == 0 {
		return ""
	}
	var b strings.Builder
	shows := map[harness.Kind]int{}
	budget := learnedMemoryTokenBudgetValue
	surfaced := make([]usedLesson, 0, len(merged))
	for _, entry := range merged {
		switch entry.Kind {
		case harness.KindMemory, harness.KindPrompt, harness.KindSubagent:
			if shows[entry.Kind] >= learnedPromptMaxPerKind {
				continue
			}
			title := strings.TrimSpace(entry.Title)
			if title == "" {
				title = entry.ID
			}
			scope := string(entry.Scope)
			if scope == "" {
				scope = string(harness.ScopeProject)
			}
			line := fmt.Sprintf("- [%s] %s: %s\n", scope, title, trimLearningContent(entry.Content))
			weight := ApproxTextTokens(line)
			if weight > budget {
				break
			}
			b.WriteString(line)
			budget -= weight
			shows[entry.Kind]++
			surfaced = append(surfaced, usedLesson{store: e.storeForEntryScope(entry.Scope), kind: entry.Kind, id: entry.ID})
		}
	}
	e.mu.Lock()
	for _, lesson := range surfaced {
		e.used[string(lesson.kind)+"\x00"+lesson.id+"\x00"+lesson.storeDir()] = lesson
	}
	e.mu.Unlock()
	return strings.TrimSpace(b.String())
}

// storeForEntryScope resolves which store an entry lives in, so reinforcement
// stamps the store that actually holds it. It mirrors storeForScope exactly.
func (e *LearningEngine) storeForEntryScope(scope harness.Scope) *harness.Store {
	return e.storeForScope(scope)
}

// Reinforce stamps every lesson the run surfaced, so a lesson that keeps proving
// useful accumulates reinforcements and resists pruning. It is called once when a
// run completes.
func (e *LearningEngine) Reinforce() {
	if e == nil {
		return
	}
	e.mu.Lock()
	used := e.used
	e.used = map[string]usedLesson{}
	e.mu.Unlock()
	now := time.Now()
	for _, lesson := range used {
		if lesson.store == nil || lesson.id == "" {
			continue
		}
		lesson.store.TouchEntry(lesson.kind, lesson.id, now, false)
	}
}

// trimLearningContent normalizes a learned entry's content for the one-line
// prompt summary: newlines collapse to spaces and the value is truncated with a
// marker so a verbose lesson cannot monopolize the context budget.
func trimLearningContent(content string) string {
	normalized := strings.Join(strings.Fields(content), " ")
	if len(normalized) <= learnedPromptMaxContentLen {
		return normalized
	}
	const marker = "..."
	return normalized[:learnedPromptMaxContentLen-len(marker)] + marker
}

// learnedMemoryOpen and learnedMemoryClose delimit the same-session injectable
// block. They match the static block built in system_prompt.go's
// learningContext, so splicing an updated block replaces the prior one without
// duplicating it.
const (
	learnedMemoryOpen  = "<learned_memory>"
	learnedMemoryClose = "</learned_memory>"
)

// EnsurePromptHasMemory splices a fresh, bounded <learned_memory> block into the
// leading system message so lessons applied mid-session take effect on the next
// provider call (same-session pickup). It is idempotent: if the message already
// carries a <learned_memory> block, only that block is replaced; otherwise the
// block is appended after any existing content. It returns the messages
// unchanged when there is nothing to add.
func (e *LearningEngine) EnsurePromptHasMemory(messages []kajicoderuntime.Message) []kajicoderuntime.Message {
	if e == nil {
		return messages
	}
	memory := e.Context()
	if memory == "" {
		return messages
	}
	block := learningMemoryBlock(memory)
	changed := false
	out := make([]kajicoderuntime.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role != kajicoderuntime.MessageRoleSystem {
			continue
		}
		modified := spliceMemoryBlock(out[i].Content, block)
		if modified != out[i].Content {
			out[i].Content = modified
			changed = true
		}
		break // only the leading system message
	}
	if !changed {
		return messages
	}
	return out
}

func learningMemoryBlock(memory string) string {
	if memory == "" {
		return ""
	}
	return learnedMemoryOpen + "\nDurable lessons learned across prior sessions. Treat these as project/user conventions, not as immutable facts; if a current instruction contradicts one, follow the current instruction.\n" + memory + "\n" + learnedMemoryClose
}

// spliceMemoryBlock replaces an existing <learned_memory>...</learned_memory>
// block with replacement, or appends replacement at the end when no block is
// present. An empty replacement removes any existing block. It returns the
// modified content; identical to the input when no change was needed.
func spliceMemoryBlock(content, replacement string) string {
	start := strings.Index(content, learnedMemoryOpen)
	end := strings.Index(content, learnedMemoryClose)
	if start != -1 && end != -1 && end+len(learnedMemoryClose) >= start {
		before := content[:start]
		after := content[end+len(learnedMemoryClose):]
		if replacement == "" {
			return strings.TrimSpace(before + " " + after)
		}
		return strings.TrimSpace(before + replacement + after)
	}
	if replacement == "" {
		return content
	}
	if strings.TrimSpace(content) == "" {
		return replacement
	}
	return strings.TrimSpace(content) + "\n\n" + replacement
}

// looksLikeCorrection reports whether a user turn reads as a correction or
// re-instruction ("no, use X", "don't do that", "actually ...", "that's wrong").
// It is a conservative, local heuristic: a false negative just means the normal
// end-of-run capture handles it.
func looksLikeCorrection(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	markers := []string{
		"no,", "no ", "nope", "wrong", "incorrect", "that's not", "thats not",
		"don't ", "dont ", "do not ", "stop ", "instead", "actually", "but i ",
		"you should", "you need to", "should have", "i said", "i meant", "not what",
		"revert", "undo", "fix that", "not correct",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
