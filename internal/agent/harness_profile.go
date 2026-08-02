package agent

import "strings"

const (
	editStrategyPatch      = "patch-first"
	editStrategyStructured = "structured-edit"
	editStrategyCareful    = "careful-verified-edit"
)

// harnessProfile is the model/provider-specific operating contract KajiCode
// applies around the common agent loop. It intentionally stays small: the
// provider adapters own wire details, while this profile selects prompt framing,
// context pressure, and tool-use posture.
type harnessProfile struct {
	Family                 string
	DisplayName            string
	PromptAddendum         string
	EditStrategy           string
	PlanningStrategy       string
	ToolUseStrategy        string
	ContextStrategy        string
	VerificationStrategy   string
	FinalResponseStrategy  string
	CompactionTriggerRatio float64
	PreserveRecentMessages int
}

func resolveHarnessProfile(options Options) harnessProfile {
	family := modelFamilyFromProvider(options.ProviderName, options.Model)
	profile := baseHarnessProfile(family)
	if profile.Family == "" {
		return harnessProfile{}
	}
	return profile
}

func baseHarnessProfile(family string) harnessProfile {
	switch family {
	case familyOpenAI:
		return harnessProfile{
			Family:                 familyOpenAI,
			DisplayName:            "OpenAI/Codex",
			PromptAddendum:         openAIPromptAddendum,
			EditStrategy:           editStrategyPatch,
			PlanningStrategy:       "Keep a short live checklist for multi-step work, then act without extra handoffs once the next step is clear.",
			ToolUseStrategy:        "Use exact tool schemas, prefer apply_patch/edit_file for edits, and keep calls small enough to stream cleanly.",
			ContextStrategy:        "Cache the current working set mentally, prune stale tool output before summarizing, and load deferred tools with tool_search only when needed.",
			VerificationStrategy:   "Run the narrow validator first, then broaden only when the touched surface or release risk requires it.",
			FinalResponseStrategy:  "Lead with what changed and what passed; keep prose compact and cite files when useful.",
			CompactionTriggerRatio: 0.68,
			PreserveRecentMessages: 8,
		}
	case familyGemini:
		return harnessProfile{
			Family:                 familyGemini,
			DisplayName:            "Gemini",
			PromptAddendum:         geminiPromptAddendum,
			EditStrategy:           editStrategyStructured,
			PlanningStrategy:       "State the current hypothesis, inspect before mutation, and close each loop with explicit evidence.",
			ToolUseStrategy:        "Prefer explicit read/search before mutation, use tool_search for deferred schemas, and keep each tool request independently answerable.",
			ContextStrategy:        "Restate only the freshest facts, avoid carrying obsolete branches of analysis, and summarize large reads before moving on.",
			VerificationStrategy:   "Prefer deterministic commands with bounded output; if a command fails, use the error text as the next hypothesis.",
			FinalResponseStrategy:  "Separate confirmed facts from assumptions and keep the final answer direct.",
			CompactionTriggerRatio: 0.66,
			PreserveRecentMessages: 8,
		}
	case familyAnthropic:
		return harnessProfile{
			Family:                 familyAnthropic,
			DisplayName:            "Claude",
			PromptAddendum:         anthropicPromptAddendum,
			EditStrategy:           editStrategyStructured,
			PlanningStrategy:       "Use concise, ordered plans for substantial work and update them when a file or validator changes state.",
			ToolUseStrategy:        "Keep preambles short, ground claims in tool results, and avoid re-reading large files already summarized in context.",
			ContextStrategy:        "Prefer summaries over repeated large reads; re-open exact bytes only when editing, citing, or resolving a contradiction.",
			VerificationStrategy:   "Treat validation failures as actionable evidence and fix the root cause before summarizing.",
			FinalResponseStrategy:  "Give a terse outcome, tests, and any remaining risk without extra narration.",
			CompactionTriggerRatio: 0.7,
			PreserveRecentMessages: 8,
		}
	case familyQwen, familyKimi, familyDeepSeek, familyMiniMax, familyGLM:
		return harnessProfile{
			Family:                 family,
			DisplayName:            displayNameForFamily(family),
			PromptAddendum:         openWeightPromptAddendum,
			EditStrategy:           editStrategyCareful,
			PlanningStrategy:       "Make the next action explicit before tool use, then verify assumptions from tool output instead of memory.",
			ToolUseStrategy:        "Use exact tool names and JSON fields, repair uncertainty with tool_search instead of inventing calls, and verify after edits.",
			ContextStrategy:        "Keep recent user intent and touched files in context; summarize noisy output early and compact before the model drifts.",
			VerificationStrategy:   "Prefer small edits, then run focused tests or builds to catch schema and syntax mistakes quickly.",
			FinalResponseStrategy:  "Report only observed outcomes and avoid claiming unrun validation.",
			CompactionTriggerRatio: 0.64,
			PreserveRecentMessages: 10,
		}
	case familyGeneric:
		return harnessProfile{
			Family:                 familyGeneric,
			DisplayName:            "generic OpenAI-compatible",
			PromptAddendum:         genericPromptAddendum,
			EditStrategy:           editStrategyCareful,
			PlanningStrategy:       "Use conservative step-by-step tool use because provider quirks are unknown.",
			ToolUseStrategy:        "Use exact advertised tool names and prefer dedicated file tools over shell for file reads and edits.",
			ContextStrategy:        "Preserve the latest task state, trim stale logs, and avoid assuming provider-specific behavior.",
			VerificationStrategy:   "Validate every edit path with the narrowest repeatable check available.",
			FinalResponseStrategy:  "Be explicit about what was inspected, changed, and verified.",
			CompactionTriggerRatio: compactionTriggerRatio,
			PreserveRecentMessages: defaultCompactionPreserveLast,
		}
	default:
		return harnessProfile{}
	}
}

func modelPromptAddendum(model string) string {
	return resolveHarnessProfile(Options{Model: model}).PromptAddendum
}

func harnessProfileContext(options Options) string {
	profile := resolveHarnessProfile(options)
	if profile.Family == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("<harness_profile>\n")
	b.WriteString("Model family: " + profile.DisplayName + "\n")
	b.WriteString("Edit strategy: " + profile.EditStrategy + "\n")
	appendProfileLine(&b, "Planning strategy", profile.PlanningStrategy)
	b.WriteString("Tool strategy: " + profile.ToolUseStrategy + "\n")
	appendProfileLine(&b, "Context strategy", profile.ContextStrategy)
	appendProfileLine(&b, "Validation strategy", profile.VerificationStrategy)
	appendProfileLine(&b, "Final response strategy", profile.FinalResponseStrategy)
	b.WriteString("</harness_profile>")
	if profile.PromptAddendum != "" {
		b.WriteString("\n\n")
		b.WriteString(profile.PromptAddendum)
	}
	return b.String()
}

func appendProfileLine(b *strings.Builder, label string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(label + ": " + value + "\n")
}
