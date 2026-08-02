package agent

import "unicode/utf8"

// BuildSystemPromptPreview returns the same system prompt a run would seed for
// the supplied options. It is intended for non-runtime reporting surfaces that
// need to estimate prompt footprint without starting a provider call.
func BuildSystemPromptPreview(options Options) string {
	return buildSystemPrompt(options)
}

type SystemPromptReport struct {
	Prompt          string                      `json:"prompt,omitempty"`
	TotalBytes      int                         `json:"totalBytes"`
	TotalRunes      int                         `json:"totalRunes"`
	EstimatedTokens int                         `json:"estimatedTokens"`
	Sections        []SystemPromptSectionReport `json:"sections"`
}

type SystemPromptSectionReport struct {
	Role            string `json:"role"`
	Bytes           int    `json:"bytes"`
	Runes           int    `json:"runes"`
	EstimatedTokens int    `json:"estimatedTokens"`
	Content         string `json:"content,omitempty"`
}

func BuildSystemPromptReport(options Options, includeContent bool) SystemPromptReport {
	parts := buildSystemPromptParts(options)
	report := SystemPromptReport{
		TotalBytes:      len(parts.prompt),
		TotalRunes:      utf8.RuneCountInString(parts.prompt),
		EstimatedTokens: ApproxTextTokens(parts.prompt),
		Sections:        make([]SystemPromptSectionReport, 0, len(parts.sections)),
	}
	if includeContent {
		report.Prompt = parts.prompt
	}
	for _, section := range parts.sections {
		item := SystemPromptSectionReport{
			Role:            string(section.role),
			Bytes:           len(section.content),
			Runes:           utf8.RuneCountInString(section.content),
			EstimatedTokens: ApproxTextTokens(section.content),
		}
		if includeContent {
			item.Content = section.content
		}
		report.Sections = append(report.Sections, item)
	}
	return report
}
