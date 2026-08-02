package agent

import "github.com/dishant0406/KajiCode/internal/kajicoderuntime"

// turnContextPlan is the pre-request budget decision used by the compactor. It
// keeps token accounting and policy thresholds in one place so prompt/tool
// changes do not fork their own context heuristics.
type turnContextPlan struct {
	MessageTokens int
	ToolTokens    int
	TotalTokens   int
	Threshold     int
	PreserveLast  int
	ShouldCompact bool
}

func planTurnContext(
	messages []kajicoderuntime.Message,
	tools []kajicoderuntime.ToolDefinition,
	threshold int,
	preserveLast int,
	calibrationRatio float64,
) turnContextPlan {
	messageTokens := estimateTokens(messages)
	toolTokens := estimateToolDefTokens(tools)
	total := calibrateTokenEstimate(messageTokens+toolTokens, calibrationRatio)
	return turnContextPlan{
		MessageTokens: messageTokens,
		ToolTokens:    toolTokens,
		TotalTokens:   total,
		Threshold:     threshold,
		PreserveLast:  preserveLast,
		ShouldCompact: threshold > 0 && total > threshold,
	}
}

func calibrateTokenEstimate(raw int, ratio float64) int {
	if ratio <= 0 {
		return raw
	}
	return int(float64(raw) * ratio)
}

func effectiveCompactionPreserveLast(options Options) int {
	if options.CompactionPreserveLast > 0 {
		return options.CompactionPreserveLast
	}
	if preserve := resolveHarnessProfile(options).PreserveRecentMessages; preserve > 0 {
		return preserve
	}
	return defaultCompactionPreserveLast
}

func effectiveCompactionTriggerRatio(options Options) float64 {
	if ratio := resolveHarnessProfile(options).CompactionTriggerRatio; ratio > 0 {
		return ratio
	}
	return compactionTriggerRatio
}
