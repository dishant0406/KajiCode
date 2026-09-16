package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dishant0406/KajiCode/internal/sandbox"
	"github.com/dishant0406/KajiCode/internal/skills"
)

// skillTool lets the model pull a reusable instruction "skill" into context on
// demand (PRD F15). It is the single skill tool for every surface: it resolves a
// named skill across the primary skills dir, the shared ~/.agents/skills and
// ~/.claude/skills roots, the project roots governing the run's working
// directories (RunOptions.ProjectSkillRoots), and plugin-contributed roots, in the
// order defined by skills.MergeRoots (global → project → plugin, earlier wins).
// Keeping one tool — instead of a single-dir core tool plus a multi-root plugin
// overlay that shadowed each other by registration order — guarantees the skills
// the system prompt advertises are exactly the skills this tool can load. It is
// read-only.
type skillTool struct {
	baseTool
	// dir is the primary skills directory (skills.DefaultDir unless overridden).
	dir string
	// pluginRoots are the plugin-contributed skill roots for this run, resolved
	// last, after the global and project roots (see skills.MergeRoots).
	pluginRoots []string
}

// NewSkillTool builds the skill tool. An empty dir resolves to the standard
// skills data directory (skills.DefaultDir); pass an explicit dir in tests.
// pluginRoots are additional plugin skill roots, resolved after the global
// roots (see skills.MergeRoots).
func NewSkillTool(dir string, pluginRoots []string) *skillTool {
	if strings.TrimSpace(dir) == "" {
		dir = skills.DefaultDir(nil)
	}
	return &skillTool{
		dir:         dir,
		pluginRoots: append([]string{}, pluginRoots...),
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

// RunWithOptions implements tools.optionsAwareTool so the skill tool also
// resolves the project skill roots (skills.ProjectSkillRoots) the run has
// discovered, so repo skills are loadable by name as soon as the run touches
// their subtree — matching opencode's project-scoped discovery without a restart.
func (tool *skillTool) RunWithOptions(_ context.Context, args map[string]any, options RunOptions) Result {
	return tool.run(args, options.ProjectSkillRoots, options.PermissionMode)
}

// PermissionForArgs implements tools.ArgsPermissioner so the agent loop consults a
// skill's frontmatter permission (deny/prompt/allow) for a specific load call.
// It resolves the named skill across the tool's global + plugin roots (the loop
// has no project roots at this point) and returns its permission, falling back to
// the read-only allow when unprovable. Returning deny here makes the registry
// hard-block loading that skill before its body is read; a deny project skill is
// additionally enforced inside run(), which does receive the project roots.
func (tool *skillTool) PermissionForArgs(args map[string]any) Permission {
	name, err := aliasedStringArg(args, []string{"name", "skill"}, "", true, false)
	if err != nil || name == "" {
		return PermissionAllow
	}
	switch skillPermissionByName(tool.dir, tool.pluginRoots, name) {
	case skills.PermissionDeny:
		return PermissionDeny
	case skills.PermissionPrompt:
		return PermissionPrompt
	default:
		return PermissionAllow
	}
}

// skillPermissionByName resolves a named skill's frontmatter permission across the
// tool's global + plugin roots. Unknown or unconstrained skills return allow.
func skillPermissionByName(dir string, pluginRoots []string, name string) string {
	loaded, _, err := skills.LoadMerged(dir, pluginRoots, nil)
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

// run resolves a named skill across the tool's global + plugin roots and the
// run's project skill roots, in skills.MergeRoots order (global → project →
// plugin, earlier wins). permissionMode is threaded from RunOptions so a
// deny-gated skill yields to bypass-all: the loop's profilePermission already
// lets bypass-all through at the loop before the tool call, and the in-tool guard
// mirrors it so the same permission system governs skill body loading.
func (tool *skillTool) run(args map[string]any, projectRoots []string, permissionMode string) Result {
	name, err := aliasedStringArg(args, []string{"name", "skill"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for skill: " + err.Error())
	}

	bypassAll := sandbox.NormalizePermissionMode(sandbox.PermissionMode(permissionMode)) == sandbox.PermissionModeBypassAll

	loaded, _, err := skills.LoadMerged(tool.dir, tool.pluginRoots, projectRoots)
	if err != nil {
		return errorResult("Error: failed to load skills: " + err.Error())
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
	if len(names) == 0 {
		return errorResult(fmt.Sprintf("Error: no skills are available (looked in %s).", tool.dir))
	}
	return errorResult(fmt.Sprintf("Error: unknown skill %q. Available skills: %s.", name, strings.Join(names, ", ")))
}
