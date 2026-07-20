package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
)

// stateConversation is a long enough conversation that Compact elides a middle
// containing an update_plan call and a loaded skill (call + result).
func stateConversation() []kajicoderuntime.Message {
	return []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "build the thing"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "planning", ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "p1", Name: "update_plan", Arguments: `{"plan":[{"content":"write code","status":"in_progress"},{"content":"add tests","status":"pending"}]}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, Content: "plan updated", ToolCallID: "p1"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "loading skill", ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"deploy"}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, Content: "Deploy skill: run `make deploy` then tag the release.", ToolCallID: "s1"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "done step 1"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "continue"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
	}
}

func compactStateConversation(t *testing.T, messages []kajicoderuntime.Message) string {
	t.Helper()
	compacted, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("Compact returned error: %v", err)
	}
	// [system, summaryUserMsg, ...suffix] — the summary is the message after system.
	if len(compacted) < 2 || compacted[1].Role != kajicoderuntime.MessageRoleUser {
		t.Fatalf("unexpected compacted shape: %#v", compacted)
	}
	if !strings.Contains(compacted[1].Content, summaryLabel) {
		t.Fatalf("summary message missing label: %q", compacted[1].Content)
	}
	return compacted[1].Content
}

func TestCompactPreservesActivePlan(t *testing.T) {
	summary := compactStateConversation(t, stateConversation())
	if !strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("expected preserved-state block, got %q", summary)
	}
	for _, want := range []string{"- [in_progress] write code", "- [pending] add tests"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("plan item %q not preserved in %q", want, summary)
		}
	}
}

func TestCompactPreservesBoundedTaskContext(t *testing.T) {
	objective := strings.Repeat("世", maxTaskObjectiveBytes)
	task := newTaskState(objective, nil)
	task.observe(taskStateEvent{kind: taskStateEventPlan, arguments: `{"plan":[{"content":"write code","status":"in_progress"},{"content":"add tests","status":"pending"}]}`})
	messages := stateConversation()
	compacted, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SUMMARY", nil },
		taskState:    task.snapshotForCompaction(messages),
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	state := parsePreservedStateBlock(compacted[1].Content)
	if state.Task == nil || state.Task.Status != taskStatusActive || state.Task.InProgress != 1 || state.Task.Pending != 1 {
		t.Fatalf("unexpected compact task state: %#v", state.Task)
	}
	if len(state.Task.Objective) > maxTaskObjectiveBytes || !utf8.ValidString(state.Task.Objective) {
		t.Fatalf("objective was not safely bounded: %d bytes %q", len(state.Task.Objective), state.Task.Objective)
	}
}

func TestCompactPreservesObjectiveAfterPlanParityMismatch(t *testing.T) {
	prior := preservedState{Task: &preservedTaskState{Objective: "stale objective", Status: taskStatusActive, Pending: 1}}
	encoded, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: summaryLabel + "\nold\n\n" + preservedStateLabel + "\n" + string(encoded)},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "more"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "working"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "again"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "done"},
	}
	compacted, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SUMMARY", nil },
		taskState: &taskStateSnapshot{
			Objective:  "current objective",
			PlanParity: taskPlanParityMismatch,
		},
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	state := parsePreservedStateBlock(compacted[1].Content)
	if state.Task == nil || state.Task.Objective != "current objective" {
		t.Fatalf("immutable objective was lost on plan mismatch: %#v", state.Task)
	}
	if state.Task.Status != "" || state.Task.Pending != 0 {
		t.Fatalf("uncorroborated mutable task fields survived plan mismatch: %#v", state.Task)
	}
}

func TestTaskObjectiveSurvivesRepeatedCompactionWithoutPlanRefresh(t *testing.T) {
	task := newTaskState("keep this objective", nil)
	task.observe(taskStateEvent{kind: taskStateEventPlan, arguments: `{"plan":[{"content":"write code","status":"in_progress"},{"content":"add tests","status":"pending"}]}`})
	messages := stateConversation()

	first, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "FIRST", nil },
		taskState:    task.snapshotForCompaction(messages),
	})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	secondInput := append(append([]kajicoderuntime.Message{}, first...),
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "more"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "working"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "again"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "done"},
	)
	snapshot := task.snapshotForCompaction(secondInput)
	if snapshot.PlanParity != taskPlanParityMismatch {
		t.Fatalf("plan call should be absent after first compaction, parity=%q", snapshot.PlanParity)
	}
	second, err := Compact(secondInput, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SECOND", nil },
		taskState:    snapshot,
	})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	state := parsePreservedStateBlock(second[1].Content)
	if state.Task == nil || state.Task.Objective != "keep this objective" {
		t.Fatalf("objective lost after second compaction: %#v", state.Task)
	}
	if state.Task.Status != "" || state.Task.InProgress != 0 {
		t.Fatalf("uncorroborated mutable fields survived second compaction: %#v", state.Task)
	}
}

func TestCompactPreservesLoadedSkills(t *testing.T) {
	summary := compactStateConversation(t, stateConversation())
	if !strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("expected preserved-state block, got %q", summary)
	}
	if !strings.Contains(summary, `"name":"deploy"`) || !strings.Contains(summary, "make deploy") {
		t.Fatalf("skill name/body not preserved in %q", summary)
	}
}

func TestCompactPreservesLoadedToolSearchSchemas(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "load weather tool"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "loading", ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "t1", Name: "tool_search", Arguments: `{"query":"select:weather_lookup"}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "t1", Content: "Loaded 1 tool. Full schemas follow; call them on the next turn.\n\n## weather_lookup\nLook up weather.\ninput schema:\n{\n  \"type\": \"object\"\n}"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "ready"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "continue"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
	}
	summary := compactStateConversation(t, messages)
	if !strings.Contains(summary, `"name":"weather_lookup"`) || !strings.Contains(summary, "input schema") {
		t.Fatalf("loaded tool schema not preserved in %q", summary)
	}
}

func TestCompactPreservesProjectInstructions(t *testing.T) {
	projectInstructions := "# AGENTS.md instructions for D:\\repo\n\n<INSTRUCTIONS>\nUse `go test ./internal/agent` for agent changes.\nDo not touch TUI code.\n</INSTRUCTIONS>\n\n<environment_context>\nignored runtime context\n</environment_context>"
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: projectInstructions},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "ack"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "work on compaction"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "working"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "continue"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
	}
	summary := compactStateConversation(t, messages)
	state := parsePreservedStateBlock(summary)
	if len(state.ProjectInstructions) != 1 {
		t.Fatalf("expected one preserved project instruction block, got %#v", state.ProjectInstructions)
	}
	body := state.ProjectInstructions[0].Body
	if state.ProjectInstructions[0].Source != "AGENTS.md instructions for D:\\repo" ||
		!strings.Contains(body, "# AGENTS.md instructions for D:\\repo") ||
		!strings.Contains(body, "Do not touch TUI code.") ||
		strings.Contains(body, "ignored runtime context") {
		t.Fatalf("project instructions not preserved cleanly in %#v", state.ProjectInstructions[0])
	}
}

func TestProjectInstructionBlockAcceptsProjectGuidelineFilename(t *testing.T) {
	source, body := projectInstructionBlock("# KAJICODE.md instructions for /repo\n\n<INSTRUCTIONS>\nPrefer Go commands.\n</INSTRUCTIONS>")
	if source != "KAJICODE.md instructions for /repo" || !strings.Contains(body, "Prefer Go commands.") {
		t.Fatalf("expected KAJICODE.md instruction block to parse, got source=%q body=%q", source, body)
	}
}

func TestCompactWithoutStateHasNoPreserveSections(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "hello"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "hi there"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "tell me more"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "sure"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "and more"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "ok"},
	}
	summary := compactStateConversation(t, messages)
	if strings.Contains(summary, preservedStateLabel) {
		t.Fatalf("did not expect a preserved-state block without plan/skill: %q", summary)
	}
}

func TestCompactCarriesPreservedStateAcrossRepeatedCompaction(t *testing.T) {
	// First compaction: real update_plan + skill load in the elided middle.
	first, err := Compact(stateConversation(), CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "FIRST SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}

	// Grow the history so the first summary (which holds the preserved sections,
	// but no real tool calls) falls into the SECOND compaction's middle.
	second := append([]kajicoderuntime.Message{}, first...)
	second = append(second,
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "what next"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "keep going"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "done"},
	)

	// The second summarizer deliberately DROPS the preserved sections.
	out, err := Compact(second, CompactionOptions{
		PreserveLast: 2,
		Summarize: func([]kajicoderuntime.Message) (string, error) {
			return "SECOND SUMMARY with no preserved sections", nil
		},
	})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if len(out) < 2 || out[1].Role != kajicoderuntime.MessageRoleUser {
		t.Fatalf("unexpected compacted shape: %#v", out)
	}
	newSummary := out[1].Content
	// Plan and skill must survive even though the summarizer didn't copy them.
	if !strings.Contains(newSummary, preservedStateLabel) || !strings.Contains(newSummary, "write code") {
		t.Fatalf("active plan lost on the second compaction: %q", newSummary)
	}
	if !strings.Contains(newSummary, `"name":"deploy"`) || !strings.Contains(newSummary, "make deploy") {
		t.Fatalf("loaded skill lost on the second compaction: %q", newSummary)
	}
}

func TestCompactCarriesLoadedToolsAndProjectInstructionsAcrossRepeatedCompaction(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\nStay in internal/agent.\n</INSTRUCTIONS>"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "loading", ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "t1", Name: "tool_search", Arguments: `{"query":"select:weather_lookup"}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "t1", Content: "Loaded 1 tool. Full schemas follow; call them on the next turn.\n\n## weather_lookup\nLook up weather.\ninput schema:\n{\n  \"type\": \"object\"\n}"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "ready"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "continue"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
	}

	first, err := Compact(messages, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "FIRST SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	second := append(append([]kajicoderuntime.Message{}, first...),
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "more"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "ok"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "again"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "fine"},
	)

	out, err := Compact(second, CompactionOptions{
		PreserveLast: 2,
		Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SECOND SUMMARY", nil },
	})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	state := parsePreservedStateBlock(out[1].Content)
	if len(state.Tools) != 1 || state.Tools[0].Name != "weather_lookup" || !strings.Contains(state.Tools[0].Body, "input schema") {
		t.Fatalf("loaded tool state was not carried forward: %#v", state.Tools)
	}
	if len(state.ProjectInstructions) != 1 ||
		state.ProjectInstructions[0].Source != "AGENTS.md instructions for /repo" ||
		!strings.Contains(state.ProjectInstructions[0].Body, "Stay in internal/agent.") {
		t.Fatalf("project instructions were not carried forward: %#v", state.ProjectInstructions)
	}
}

// TestCompactPreservesSkillBodyWithMarkdownHeadings is CodeRabbit's regression:
// a verbatim skill body containing ## / ### headings (and a code fence) must
// round-trip across TWO compactions without truncation or bogus extra skills —
// which the old markdown-delimited format could not guarantee.
func TestCompactPreservesSkillBodyWithMarkdownHeadings(t *testing.T) {
	body := "## Usage\nrun it\n### Examples\n```\nzero do\n```\n## Notes\ndone"
	conv := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleSystem, Content: "system"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "load a skill"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "loading", ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"guide"}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, Content: body, ToolCallID: "s1"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "done step 1"},
		{Role: kajicoderuntime.MessageRoleUser, Content: "continue"},
		{Role: kajicoderuntime.MessageRoleAssistant, Content: "continuing"},
	}

	mustContainBody := func(label string, messages []kajicoderuntime.Message) []kajicoderuntime.Message {
		out, err := Compact(messages, CompactionOptions{
			PreserveLast: 2,
			Summarize:    func([]kajicoderuntime.Message) (string, error) { return "SUMMARY", nil },
		})
		if err != nil {
			t.Fatalf("%s Compact: %v", label, err)
		}
		if len(out) < 2 || out[1].Role != kajicoderuntime.MessageRoleUser {
			t.Fatalf("%s: unexpected compacted shape: %#v", label, out)
		}
		_, skills := parsePreservedState(out[1].Content)
		if len(skills) != 1 || skills[0].name != "guide" || skills[0].body != body {
			t.Fatalf("%s: skill body not round-tripped intact: %#v", label, skills)
		}
		return out
	}

	first := mustContainBody("first", conv)
	// Second compaction with NO fresh tool calls and a summarizer that drops it.
	second := append(append([]kajicoderuntime.Message{}, first...),
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "more"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "ok"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleUser, Content: "again"},
		kajicoderuntime.Message{Role: kajicoderuntime.MessageRoleAssistant, Content: "fine"},
	)
	mustContainBody("second", second)
}

func TestExtractLatestPlanReturnsMostRecent(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleAssistant, ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "a", Name: "update_plan", Arguments: `{"plan":[{"content":"old step","status":"completed"}]}`},
		}},
		{Role: kajicoderuntime.MessageRoleAssistant, ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "b", Name: "update_plan", Arguments: `{"plan":[{"content":"new step","status":"in_progress"}]}`},
		}},
	}
	got := extractLatestPlan(messages)
	if !strings.Contains(got, "new step") || strings.Contains(got, "old step") {
		t.Fatalf("extractLatestPlan should return the most recent plan, got %q", got)
	}
}

func TestFormatPlanArgumentsRejectsUnsupportedStepAlias(t *testing.T) {
	got := formatPlanArguments(`{"plan":[{"step":"write failing test","status":"in_progress"},{"content":"keep existing shape","status":"pending"}]}`)
	if got != "" {
		t.Fatalf("unsupported alias should reject the whole plan like update_plan, got %q", got)
	}
}

func TestFormatPlanArgumentsPreservesNotes(t *testing.T) {
	got := formatPlanArguments(`{"plan":[{"content":"finish preservation","status":"in_progress","notes":"keep TUI untouched"}]}`)
	if !strings.Contains(got, "- [in_progress] finish preservation") || !strings.Contains(got, "Notes: keep TUI untouched") {
		t.Fatalf("expected plan content and notes to be preserved, got %q", got)
	}
}

func TestCapBodyShortBodyUnchanged(t *testing.T) {
	body := "short skill body"
	if got := capBody(body); got != body {
		t.Fatalf("capBody changed a short body: %q", got)
	}
	if strings.Contains(capBody(body), "truncated") {
		t.Fatalf("note added without truncation")
	}
}

func TestCapBodyRespectsByteBudgetForMultibyte(t *testing.T) {
	// Each '世' is 3 bytes; build a body well over the byte budget.
	body := strings.Repeat("世", maxPreservedSkillBytes)
	got := capBody(body)
	if len(got) > maxPreservedSkillBytes {
		t.Fatalf("capBody returned %d bytes, want <= %d (byte budget)", len(got), maxPreservedSkillBytes)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation note on an over-budget body")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("capBody split a multibyte rune (invalid UTF-8)")
	}
}

func TestCapBodyNoteOnlyWhenTruncated(t *testing.T) {
	// A body whose RUNE count is under the cap but BYTE length is over it must
	// still be byte-capped (the old rune-based check mishandled this case).
	body := strings.Repeat("世", (maxPreservedSkillBytes/3)+100)
	if len(body) <= maxPreservedSkillBytes {
		t.Fatalf("test setup: body should exceed the byte budget, got %d", len(body))
	}
	got := capBody(body)
	if len(got) > maxPreservedSkillBytes {
		t.Fatalf("capBody returned %d bytes, want <= %d", len(got), maxPreservedSkillBytes)
	}
	if !strings.Contains(got, "truncated") || !utf8.ValidString(got) {
		t.Fatalf("expected a valid, truncated body, got %q", got)
	}
}

func TestLoadedSkillsSkipsCallsWithoutResult(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleAssistant, ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"name":"ghost"}`}, // no matching tool result
		}},
	}
	if got := loadedSkills(messages); len(got) != 0 {
		t.Fatalf("expected no skills without a result body, got %#v", got)
	}
}

func TestLoadedSkillsAcceptsSkillArgumentAlias(t *testing.T) {
	messages := []kajicoderuntime.Message{
		{Role: kajicoderuntime.MessageRoleAssistant, ToolCalls: []kajicoderuntime.ToolCall{
			{ID: "s1", Name: "skill", Arguments: `{"skill":"deploy"}`},
		}},
		{Role: kajicoderuntime.MessageRoleTool, ToolCallID: "s1", Content: "deploy instructions"},
	}
	got := loadedSkills(messages)
	if len(got) != 1 || got[0].name != "deploy" || got[0].body != "deploy instructions" {
		t.Fatalf("loadedSkills should honor skill alias, got %#v", got)
	}
}
