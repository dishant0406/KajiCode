package agents

import (
	"path"
	"strings"
)

// Action is what a permission rule decides for a tool.
type Action string

const (
	ActionAllow Action = "allow"
	ActionAsk   Action = "ask"
	ActionDeny  Action = "deny"
)

// Rule grants an Action to every tool whose name matches Tool. Tool is a glob
// (see path.Match); "*" matches everything. Rules are evaluated last-match-wins,
// exactly like opencode's permission rulesets, so a later rule overrides an
// earlier one.
type Rule struct {
	Tool   string
	Action Action
}

// Ruleset is an ordered list of rules.
type Ruleset []Rule

// DefaultAction is the action when no rule matches a tool.
const DefaultAction = ActionAsk

// Evaluate returns the action for a tool name: the last matching rule wins, and
// an unmatched tool falls back to DefaultAction. Multiple rulesets are flattened
// in order, so a caller can append a narrower ruleset after a broader one.
func Evaluate(tool string, sets ...Ruleset) Action {
	action := DefaultAction
	for _, set := range sets {
		for _, rule := range set {
			if matchTool(rule.Tool, tool) {
				action = rule.Action
			}
		}
	}
	return action
}

func matchTool(pattern, tool string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == tool || pattern == "*" {
		return true
	}
	if ok, err := path.Match(pattern, tool); err == nil {
		return ok
	}
	return false
}

// CategoryTools maps a frontmatter tool category to the concrete core tool names
// it enables. Categories are a convenience over writing every tool name; a
// category expands to allow rules. Unknown names are treated as literal tool
// names (which is how MCP tools like "mcp_github_*" and plugin tools are
// granted).
var CategoryTools = map[string][]string{
	"read":    {"read_file", "read_minified_file", "list_directory", "ls", "glob", "grep", "lsp_navigate", "skill", "recall", "recipe_run", "code_search", "web_fetch", "web_search", "tool_search", "batch"},
	"edit":    {"write_file", "edit_file", "apply_patch", "multi_edit"},
	"execute": {"exec_command", "write_stdin", "bash"},
	"plan":    {"todo_read", "todo_write"},
	"task":    {"Task", "TaskOutput", "TaskStop", "GenerateAgent"},
}

// ToolsFromSelection turns frontmatter tool entries (categories or literal tool
// names, including globs and MCP names) into an allow ruleset. An empty
// selection falls back to read-only, so an agent that omits tools cannot mutate
// or run commands by accident.
func ToolsFromSelection(selection []string) Ruleset {
	if len(selection) == 0 {
		selection = []string{"read"}
	}
	rules := Ruleset{}
	for _, item := range selection {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if category, ok := CategoryTools[item]; ok {
			for _, tool := range category {
				rules = append(rules, Rule{Tool: tool, Action: ActionAllow})
			}
			continue
		}
		rules = append(rules, Rule{Tool: item, Action: ActionAllow})
	}
	return rules
}

// ExcludeRules turns frontmatter excludeTools entries into a deny ruleset. It
// expands categories the same way ToolsFromSelection does, so excluding "edit"
// denies every write tool.
func ExcludeRules(exclude []string) Ruleset {
	rules := Ruleset{}
	for _, item := range exclude {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if category, ok := CategoryTools[item]; ok {
			for _, tool := range category {
				rules = append(rules, Rule{Tool: tool, Action: ActionDeny})
			}
			continue
		}
		rules = append(rules, Rule{Tool: item, Action: ActionDeny})
	}
	return rules
}

// Rules builds an agent's effective ruleset from its tools and excludeTools.
// Excludes are appended last so they always win over the allow rules.
func Rules(tools []string, exclude []string) Ruleset {
	return append(ToolsFromSelection(tools), ExcludeRules(exclude)...)
}
