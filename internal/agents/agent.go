package agents

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Location records where an agent definition came from.
type Location string

const (
	LocationBuiltin Location = "builtin"
	LocationUser    Location = "user"
	LocationProject Location = "project"
)

// Mode classifies where an agent may appear: "primary" agents are selectable
// top-level roles, "subagent" agents are spawnable via the Task tool only, and
// "all" (the default) is both. Primary and hidden agents are never advertised in
// the Task tool description.
type Mode string

const (
	ModeAll      Mode = "all"
	ModePrimary  Mode = "primary"
	ModeSubagent Mode = "subagent"
)

// MaxStepsLimit bounds a declared steps value so a definition cannot demand an
// unbounded loop. A zero-value Steps means "no per-agent cap" (the runtime
// default applies).
const MaxStepsLimit = 200

// Agent is one sub-agent definition: identity, routing, sampling, and the
// permission ruleset that decides which tools it may use.
type Agent struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Extends      string   `json:"extends,omitempty"`
	Model        string   `json:"model,omitempty"`
	Thinking     string   `json:"thinking,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	ExcludeTools []string `json:"excludeTools,omitempty"`
	Mode         Mode     `json:"mode,omitempty"`
	Hidden       bool     `json:"hidden,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Temperature  float64  `json:"temperature,omitempty"`
	TopP         float64  `json:"topP,omitempty"`
	Steps        int      `json:"steps,omitempty"`
	Disable      bool     `json:"disable,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Location     Location `json:"location,omitempty"`
	FilePath     string   `json:"filePath,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	// Rules is the compiled permission ruleset for this agent. It is derived from
	// Tools/ExcludeTools during load and is the single source of truth the
	// registry filter consults.
	Rules Ruleset `json:"-"`
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ParseMode validates a mode string from frontmatter or JSON.
func ParseMode(text string) (Mode, error) {
	switch Mode(strings.TrimSpace(text)) {
	case ModeAll:
		return ModeAll, nil
	case ModePrimary:
		return ModePrimary, nil
	case ModeSubagent:
		return ModeSubagent, nil
	default:
		return "", fmt.Errorf("unknown mode %q: use %q, %q, or %q", text, ModeAll, ModePrimary, ModeSubagent)
	}
}

// NormalizeMode resolves an empty or unknown stored mode to the default.
func NormalizeMode(mode Mode) Mode {
	switch mode {
	case ModePrimary, ModeSubagent:
		return mode
	default:
		return ModeAll
	}
}

// ParseMarkdown parses an agent definition from markdown with YAML-style
// frontmatter plus a system-prompt body.
func ParseMarkdown(content string) (Agent, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return Agent{}, err
	}
	agent, err := agentFromFrontmatter(frontmatter)
	if err != nil {
		return Agent{}, err
	}
	agent.SystemPrompt = strings.TrimSpace(body)
	for key := range frontmatter {
		if !knownKeys[key] {
			agent.Warnings = append(agent.Warnings, "unknown frontmatter key: "+key)
		}
	}
	// A disable-only override carries no body or description: it exists solely to
	// suppress a lower-precedence agent of the same name, so skip the prompt
	// requirement and only validate its name.
	if agent.Disable {
		if agent.Name == "" || !namePattern.MatchString(agent.Name) {
			return Agent{}, fmt.Errorf("invalid agent name %q: use lowercase letters, numbers, and dashes", agent.Name)
		}
		agent.Mode = NormalizeMode(agent.Mode)
		agent.Rules = Rules(agent.Tools, agent.ExcludeTools)
		return agent, nil
	}
	if agent.SystemPrompt == "" {
		if agent.Description != "" {
			agent.SystemPrompt = agent.Description
			agent.Warnings = append(agent.Warnings, "using description as system prompt")
		} else if agent.Extends == "" {
			return Agent{}, fmt.Errorf("system prompt cannot be empty")
		}
	}
	if err := Validate(&agent); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// Validate normalizes and checks an agent definition and compiles its ruleset.
func Validate(agent *Agent) error {
	agent.Name = strings.TrimSpace(agent.Name)
	agent.Description = strings.TrimSpace(agent.Description)
	agent.Extends = strings.TrimSpace(agent.Extends)
	agent.Model = strings.TrimSpace(agent.Model)
	agent.Thinking = strings.TrimSpace(agent.Thinking)
	agent.Mode = NormalizeMode(agent.Mode)

	if agent.Name == "" {
		return fmt.Errorf("agent name is required")
	}
	if !namePattern.MatchString(agent.Name) {
		return fmt.Errorf("invalid agent name %q: use lowercase letters, numbers, and dashes", agent.Name)
	}
	if agent.Description == "" && agent.Extends == "" {
		return fmt.Errorf("agent %q requires a description", agent.Name)
	}
	if agent.Extends != "" && !namePattern.MatchString(agent.Extends) {
		return fmt.Errorf("invalid base agent name %q: use lowercase letters, numbers, and dashes", agent.Extends)
	}
	if agent.Steps < 0 {
		return fmt.Errorf("agent %q steps must be a positive integer", agent.Name)
	}
	if agent.Steps > MaxStepsLimit {
		return fmt.Errorf("agent %q steps %d exceeds the maximum of %d", agent.Name, agent.Steps, MaxStepsLimit)
	}
	if agent.Temperature < 0 || agent.Temperature > 2 {
		return fmt.Errorf("agent %q temperature must be between 0 and 2", agent.Name)
	}
	if agent.TopP < 0 || agent.TopP > 1 {
		return fmt.Errorf("agent %q topP must be between 0 and 1", agent.Name)
	}
	for _, alias := range agent.Aliases {
		if alias != "" && !namePattern.MatchString(alias) {
			return fmt.Errorf("agent %q has invalid alias %q: use lowercase letters, numbers, and dashes", agent.Name, alias)
		}
	}
	agent.Rules = Rules(agent.Tools, agent.ExcludeTools)
	return nil
}

// MergeExtends overlays a child agent onto its base. Explicit child values win;
// prompt bodies are concatenated base-then-child so a child can extend a base
// with extra instructions.
func MergeExtends(base, child Agent) Agent {
	merged := child
	if merged.Description == "" {
		merged.Description = base.Description
	}
	if merged.Model == "" {
		merged.Model = base.Model
	}
	if merged.Thinking == "" {
		merged.Thinking = base.Thinking
	}
	if merged.Temperature == 0 {
		merged.Temperature = base.Temperature
	}
	if merged.TopP == 0 {
		merged.TopP = base.TopP
	}
	if merged.Steps == 0 {
		merged.Steps = base.Steps
	}
	if len(merged.Tools) == 0 {
		merged.Tools = append([]string(nil), base.Tools...)
	}
	if len(merged.ExcludeTools) == 0 {
		merged.ExcludeTools = append([]string(nil), base.ExcludeTools...)
	}
	if len(merged.Aliases) == 0 {
		merged.Aliases = append([]string(nil), base.Aliases...)
	}
	switch {
	case strings.TrimSpace(base.SystemPrompt) == "":
		merged.SystemPrompt = strings.TrimSpace(merged.SystemPrompt)
	case strings.TrimSpace(merged.SystemPrompt) == "":
		merged.SystemPrompt = strings.TrimSpace(base.SystemPrompt)
	default:
		merged.SystemPrompt = strings.TrimSpace(base.SystemPrompt) + "\n\n" + strings.TrimSpace(merged.SystemPrompt)
	}
	merged.Warnings = append(append([]string(nil), base.Warnings...), child.Warnings...)
	return merged
}

var knownKeys = map[string]bool{
	"name": true, "description": true, "extends": true, "model": true,
	"thinking": true, "tools": true, "excludeTools": true, "mode": true,
	"hidden": true, "aliases": true, "temperature": true, "topP": true,
	"steps": true, "maxSteps": true, "disable": true,
}

var listKeys = map[string]bool{
	"tools": true, "excludeTools": true, "aliases": true,
}

func isListKey(key string) bool { return listKeys[key] }

const frontmatterFence = "---"

func splitFrontmatter(content string) (map[string]any, string, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterFence {
		return map[string]any{}, strings.TrimSpace(normalized), nil
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == frontmatterFence {
			parsed, err := parseFrontmatter(strings.Join(lines[1:index], "\n"))
			if err != nil {
				return nil, "", err
			}
			return parsed, strings.Join(lines[index+1:], "\n"), nil
		}
	}
	return nil, "", fmt.Errorf("frontmatter is missing closing ---")
}

func parseFrontmatter(block string) (map[string]any, error) {
	raw := map[string]any{}
	if strings.TrimSpace(block) == "" {
		return raw, nil
	}
	lines := strings.Split(block, "\n")
	seen := map[string]int{}
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid frontmatter line %d", index+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("invalid empty frontmatter key on line %d", index+1)
		}
		if previous, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate frontmatter key %q on line %d; first seen on line %d", key, index+1, previous)
		}
		seen[key] = index + 1
		if isListKey(key) {
			if value == "" {
				values, next, err := parseBlockList(lines, index+1)
				if err != nil {
					return nil, err
				}
				raw[key] = values
				index = next - 1
				continue
			}
			values, err := parseInlineList(value)
			if err != nil {
				return nil, fmt.Errorf("%s must be an array", key)
			}
			raw[key] = values
			continue
		}
		raw[key] = unquote(value)
	}
	return raw, nil
}

func agentFromFrontmatter(raw map[string]any) (Agent, error) {
	agent := Agent{}
	for key, value := range raw {
		switch key {
		case "name":
			agent.Name = stringValue(value)
		case "description":
			agent.Description = stringValue(value)
		case "extends":
			agent.Extends = stringValue(value)
		case "model":
			agent.Model = stringValue(value)
		case "thinking":
			agent.Thinking = stringValue(value)
		case "tools":
			values, ok := value.([]string)
			if !ok {
				return Agent{}, fmt.Errorf("tools must be an array")
			}
			agent.Tools = values
		case "excludeTools":
			values, ok := value.([]string)
			if !ok {
				return Agent{}, fmt.Errorf("excludeTools must be an array")
			}
			agent.ExcludeTools = values
		case "aliases":
			values, ok := value.([]string)
			if !ok {
				return Agent{}, fmt.Errorf("aliases must be an array")
			}
			agent.Aliases = values
		case "mode":
			text := stringValue(value)
			if text == "" {
				return Agent{}, fmt.Errorf("mode must be one of %q, %q, %q", ModeAll, ModeSubagent, ModePrimary)
			}
			mode, err := ParseMode(text)
			if err != nil {
				return Agent{}, err
			}
			agent.Mode = mode
		case "hidden", "disable":
			flag, ok := value.(bool)
			if !ok {
				if text := stringValue(value); text == "true" || text == "false" {
					flag = text == "true"
				} else {
					return Agent{}, fmt.Errorf("%s must be a boolean", key)
				}
			}
			if key == "hidden" {
				agent.Hidden = flag
			} else {
				agent.Disable = flag
			}
		case "temperature", "topP":
			number, err := numericValue(value, key)
			if err != nil {
				return Agent{}, err
			}
			if number < 0 || number > 2 {
				return Agent{}, fmt.Errorf("%s must be between 0 and 2", key)
			}
			if key == "temperature" {
				agent.Temperature = number
			} else {
				agent.TopP = number
			}
		case "steps":
			steps, err := positiveIntValue(value, "steps")
			if err != nil {
				return Agent{}, err
			}
			agent.Steps = steps
		case "maxSteps":
			if _, exists := raw["steps"]; exists {
				return Agent{}, fmt.Errorf("use steps (maxSteps is deprecated) — set only one")
			}
			steps, err := positiveIntValue(value, "maxSteps")
			if err != nil {
				return Agent{}, err
			}
			agent.Steps = steps
		}
	}
	return agent, nil
}

func positiveIntValue(value any, key string) (int, error) {
	switch typed := value.(type) {
	case int:
		if typed <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return typed, nil
	case float64:
		if typed <= 0 || typed != float64(int(typed)) {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return int(typed), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
}

func numericValue(value any, key string) (float64, error) {
	switch typed := value.(type) {
	case int:
		return float64(typed), nil
	case float64:
		return typed, nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number", key)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func parseBlockList(lines []string, start int) ([]string, int, error) {
	values := []string{}
	index := start
	for ; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			break
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if value == "" {
			return nil, index, fmt.Errorf("empty list item on frontmatter line %d", index+1)
		}
		values = append(values, unquote(value))
	}
	return values, index, nil
}

func parseInlineList(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("not an inline array")
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if inner == "" {
		return []string{}, nil
	}
	values := []string{}
	for _, part := range strings.Split(inner, ",") {
		item := unquote(strings.TrimSpace(part))
		if item == "" {
			continue
		}
		values = append(values, item)
	}
	return values, nil
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		if value[0] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				return strings.TrimSpace(unquoted)
			}
		}
		return strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
