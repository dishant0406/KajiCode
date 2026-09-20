package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dishant0406/KajiCode/internal/tools"
)

// GenerateToolName is the model-facing name of the agent-authoring tool.
const GenerateToolName = "GenerateAgent"

var generatedNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// GenerateTool creates or replaces a project-local agent definition so the model
// can author reusable sub-agents.
type GenerateTool struct {
	projectDir string
}

// NewGenerateTool builds the GenerateAgent tool over the project agent dir.
func NewGenerateTool(projectDir string) *GenerateTool {
	return &GenerateTool{projectDir: projectDir}
}

func (tool *GenerateTool) Name() string { return GenerateToolName }

func (tool *GenerateTool) Description() string {
	return "Create or replace a project-local agent definition (a reusable sub-agent) from a name, description, and system prompt."
}

func (tool *GenerateTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"name": {
				Type:        "string",
				Description: "Lowercase agent id (letters, numbers, dashes), such as api-reviewer.",
			},
			"description": {
				Type:        "string",
				Description: "Short description of when to use this agent.",
			},
			"system_prompt": {
				Type:        "string",
				Description: "The agent's system prompt.",
			},
			"tools": {
				Type:        "array",
				Description: "Tool categories or tool names the agent may use (e.g. read, edit, execute, or a specific tool).",
				Items:       &tools.PropertySchema{Type: "string"},
			},
			"excludeTools": {
				Type:        "array",
				Description: "Tools to deny even if a category grants them.",
				Items:       &tools.PropertySchema{Type: "string"},
			},
			"overwrite": {
				Type:        "boolean",
				Description: "Replace an existing agent with the same name.",
				Default:     false,
			},
		},
		Required:             []string{"name", "description", "system_prompt"},
		AdditionalProperties: false,
	}
}

func (tool *GenerateTool) Safety() tools.Safety {
	return tools.Safety{
		SideEffect:      tools.SideEffectWrite,
		Permission:      tools.PermissionPrompt,
		Reason:          "Writes a project agent definition inside the workspace.",
		AdvertiseInAuto: true,
	}
}

func (tool *GenerateTool) Run(_ context.Context, args map[string]any) tools.Result {
	if strings.TrimSpace(tool.projectDir) == "" {
		return errorResult("GenerateAgent is only available inside a workspace")
	}
	name, _ := stringArg(args, "name")
	description, _ := stringArg(args, "description")
	systemPrompt, _ := stringArg(args, "system_prompt")
	toolsList, err := stringArrayArg(args, "tools")
	if err != nil {
		return errorResult(err.Error())
	}
	excludeList, err := stringArrayArg(args, "excludeTools")
	if err != nil {
		return errorResult(err.Error())
	}
	overwrite, _ := boolArg(args, "overwrite")

	if !generatedNamePattern.MatchString(name) {
		return errorResult(fmt.Sprintf("invalid agent name %q: use lowercase letters, numbers, and dashes", name))
	}
	if description == "" {
		return errorResult("GenerateAgent requires a description")
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return errorResult("GenerateAgent requires a system_prompt")
	}
	agent := Agent{
		Name:         name,
		Description:  description,
		Tools:        toolsList,
		ExcludeTools: excludeList,
		SystemPrompt: systemPrompt,
		Mode:         ModeSubagent,
	}
	if err := Validate(&agent); err != nil {
		return errorResult(err.Error())
	}

	path := filepath.Join(tool.projectDir, name+".md")
	if _, err := os.Lstat(path); err == nil {
		if !overwrite {
			return errorResult("agent " + name + " already exists; pass overwrite: true to replace it")
		}
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errorResult("refusing to overwrite symlink agent definition: " + path)
		}
	}
	if err := os.MkdirAll(tool.projectDir, 0o755); err != nil {
		return errorResult("create agent directory: " + err.Error())
	}
	if err := os.WriteFile(path, []byte(RenderMarkdown(agent)), 0o644); err != nil {
		return errorResult("write agent definition: " + err.Error())
	}
	return tools.Result{Status: tools.StatusOK, Output: "Wrote agent definition " + path, Meta: map[string]string{"path": path}}
}

// RenderMarkdown serializes an agent to markdown frontmatter + body. Keeping
// author (GenerateAgent, the agent CLI) and reader (ParseMarkdown) in one file
// guarantees a written definition always round-trips.
func RenderMarkdown(agent Agent) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + agent.Name + "\n")
	b.WriteString("description: " + agent.Description + "\n")
	b.WriteString("mode: " + string(NormalizeMode(agent.Mode)) + "\n")
	if len(agent.Tools) > 0 {
		b.WriteString("tools:\n")
		for _, tool := range agent.Tools {
			b.WriteString("  - " + tool + "\n")
		}
	}
	if len(agent.ExcludeTools) > 0 {
		b.WriteString("excludeTools:\n")
		for _, tool := range agent.ExcludeTools {
			b.WriteString("  - " + tool + "\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(agent.SystemPrompt))
	b.WriteString("\n")
	return b.String()
}

func stringArrayArg(args map[string]any, key string) ([]string, error) {
	if args == nil {
		return nil, nil
	}
	value, ok := args[key]
	if !ok || value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if strings, ok := value.([]string); ok {
			return strings, nil
		}
		return nil, errArgType(key, "array of strings")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, errArgType(key, "array of strings")
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}
