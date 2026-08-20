package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/skills"
)

// skillTool lets the model pull a reusable instruction "skill" into context on
// demand (PRD F15). It reads the skills directory itself via the internal/skills
// loader and returns the named skill's markdown body as its Output, so the model
// can opt into reusable guidance only when relevant. It is read-only.
type skillTool struct {
	baseTool
	dir string
}

// NewSkillTool builds the skill tool. An empty dir resolves to the standard
// skills data directory (skills.DefaultDir); pass an explicit dir in tests.
func NewSkillTool(dir string) *skillTool {
	if strings.TrimSpace(dir) == "" {
		dir = skills.DefaultDir(nil)
	}
	return &skillTool{
		dir: dir,
		baseTool: baseTool{
			name: "skill",
			description: "Load a specialized skill when the task at hand matches one of the available_skills entries. " +
				"Each skill is a reusable, on-demand instruction set (project conventions, confirmation policies, specialist procedures). " +
				"Before starting a request, scan <available_skills>; when it names a skill whose description matches the task, call this tool " +
				"with its exact name FIRST and follow the returned guidance — do not guess names, do not skip a matching skill, and do not " +
				"substitute your own approach for its instructions. An unknown name returns the list of available skills.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name": {
						Type:        "string",
						Description: "The name of the skill to load.",
					},
					"skill": {
						Type:        "string",
						Description: "Alias for name; supply either name or skill.",
					},
				},
				// Intentionally no strict Required: the tool needs exactly one of
				// name/skill, which Run enforces via aliasedStringArg. Declaring both
				// here keeps the alias usable under schema validators that reject
				// unknown keys (AdditionalProperties:false).
				AdditionalProperties: false,
			},
			safety:       readOnlySafety("Reads a local skill file; gathers reusable instructions only."),
			capabilities: ToolCapabilities{Effect: EffectReadOnly, ThreadSafe: true},
		},
	}
}

// Run loads the named skill and returns its Content. Unknown names return a
// clear error listing the available skill names so the model can self-correct.
func (tool *skillTool) Run(_ context.Context, args map[string]any) Result {
	return tool.run(args, nil, "")
}

// RunWithOptions implements tools.optionsAwareTool so the core skill tool also
// resolves project skill roots (skills.ProjectSkillRoots) the run has
// discovered, keeping the core surface consistent with the plugin overlay's
// project-scoped discovery.
func (tool *skillTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	return tool.run(args, options.ProjectSkillRoots, options.PermissionMode)
}

// PermissionForArgs implements tools.ArgsPermissioner so the agent loop consults a
// skill's frontmatter permission (deny/prompt/allow) for a specific load call.
// It resolves the named skill from the tool's configured dir and returns its
// permission, falling back to the read-only allow when unprovable. Returning
// deny here makes the registry hard-block loading that skill before its body is read.
func (tool *skillTool) PermissionForArgs(args map[string]any) Permission {
	name, err := aliasedStringArg(args, []string{"name", "skill"}, "", true, false)
	if err != nil || name == "" {
		return PermissionAllow
	}
	switch skillPermissionByDir(tool.dir, name) {
	case skills.PermissionDeny:
		return PermissionDeny
	case skills.PermissionPrompt:
		return PermissionPrompt
	default:
		return PermissionAllow
	}
}

// skillPermissionByDir resolves a named skill's frontmatter permission from a
// single skill directory. Unknown or unconstrained skills return allow.
func skillPermissionByDir(dir string, name string) string {
	loaded, _, err := skills.LoadFromRoots([]string{dir})
	if err != nil {
		return skills.PermissionAllow
	}
	for _, skill := range loaded {
		if skill.Name == name {
			return skills.NormalizePermission(skill.Permission)
		}
	}
	if strings.EqualFold(name, skills.BuiltinCustomizeKajicodeName) {
		return skills.BuiltinCustomizeKajicode().Permission
	}
	return skills.PermissionAllow
}

// run resolves a named skill across the tool's directory and the run's project
// skill roots. projectRoots are merged after the configured dir and treated as
// least-precedence. permissionMode is threaded from RunOptions so a deny-gated
// skill yields to bypass-all: profilePermission already lets bypass-all through
// at the loop before the tool call, and the in-tool guard mirrors it so the same
// permission system governs skill body loading rather than a separate one.
func (tool *skillTool) run(args map[string]any, projectRoots []string, permissionMode string) Result {
	name, err := aliasedStringArg(args, []string{"name", "skill"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for skill: " + err.Error())
	}

	bypassAll := sandbox.NormalizePermissionMode(sandbox.PermissionMode(permissionMode)) == sandbox.PermissionModeBypassAll

	roots := append([]string{tool.dir}, projectRoots...)
	loaded, _, err := skills.LoadFromRoots(roots)
	if err != nil {
		return errorResult("Error: failed to load skills: " + err.Error())
	}
	if len(loaded) == 0 {
		// Built-in synthesize skill is always available even when no on-disk skills
		// are installed, so the always-discoverable customize-kajicode resolves.
		if strings.EqualFold(name, skills.BuiltinCustomizeKajicodeName) {
			return okResult(skills.SkillOutput(skills.BuiltinCustomizeKajicode()))
		}
		return errorResult(fmt.Sprintf("Error: no skills are available (looked in %s).", tool.dir))
	}

	names := make([]string, 0, len(loaded))
	for _, skill := range loaded {
		if skill.Name == name {
			// Enforce a frontmatter-declared deny as a hard gate except under
			// bypass-all, so a deny skill's body is never returned without the
			// user opting into full permission bypass.
			if !bypassAll && skills.NormalizePermission(skill.Permission) == skills.PermissionDeny {
				return errorResult("Error: skill " + name + " is permission-denied and cannot be loaded.")
			}
			return okResult(skills.SkillOutput(skill))
		}
		names = append(names, skill.Name)
	}
	// Fall back to the built-in synthesize skill when no on-disk skill matches, so
	// loading the always-discoverable customize-kajicode resolves even when other
	// skills are installed but none shares its name.
	if strings.EqualFold(name, skills.BuiltinCustomizeKajicodeName) {
		return okResult(skills.SkillOutput(skills.BuiltinCustomizeKajicode()))
	}
	return errorResult(fmt.Sprintf("Error: unknown skill %q. Available skills: %s.", name, strings.Join(names, ", ")))
}
