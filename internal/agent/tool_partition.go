package agent

import (
	"sort"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// partitionTools builds the per-turn advertised tool list and optional
// tool_search discovery text. INACTIVE (DeferThreshold <= 0 or the eligible count
// is below it): every visible tool is exposed with its full schema EXCEPT
// tool_search (dropped so it is never advertised when it cannot help), and the
// discovery text is empty. ACTIVE: deferred-eligible tools stay hidden until
// tool_search loads them; non-deferred tools and tool_search remain visible.
func partitionTools(registry *tools.Registry, permissionMode PermissionMode, options Options, loaded map[string]bool) ([]kajicoderuntime.ToolDefinition, string) {
	return partitionToolsCached(registry, permissionMode, options, loaded, nil)
}

// partitionToolsCached is partitionTools with an optional per-tool definition
// cache. Partitioning is recomputed every call because a tool's deferred state
// can flip mid-run; only schema rendering is memoized by tool name.
func partitionToolsCached(registry *tools.Registry, permissionMode PermissionMode, options Options, loaded map[string]bool, defCache map[string]kajicoderuntime.ToolDefinition) ([]kajicoderuntime.ToolDefinition, string) {
	registeredTools := registry.All()

	visible := make([]tools.Tool, 0, len(registeredTools))
	eligible := 0
	for _, tool := range registeredTools {
		if !ToolVisible(tool, permissionMode, options.EnabledTools, options.DisabledTools) {
			continue
		}
		visible = append(visible, tool)
		if tools.IsDeferralEligible(tool) {
			eligible++
		}
	}

	loader, loaderFound := registry.Get(tools.ToolSearchToolName)
	loaderUsable := loaderFound &&
		!containsToolName(options.DisabledTools, tools.ToolSearchToolName) &&
		ToolAdvertised(loader, permissionMode)
	active := options.DeferThreshold > 0 && eligible >= options.DeferThreshold && loaderUsable
	if !active {
		return eagerToolDefinitions(visible, defCache), ""
	}

	eager := make([]kajicoderuntime.ToolDefinition, 0, len(visible))
	loadedTail := make([]kajicoderuntime.ToolDefinition, 0)
	hiddenTools := make([]tools.Tool, 0)
	for _, tool := range visible {
		name := tool.Name()
		if name == tools.ToolSearchToolName {
			continue
		}
		if tools.IsDeferred(tool) {
			if loaded[name] {
				loadedTail = append(loadedTail, cachedRuntimeToolDefinition(defCache, tool))
			} else {
				hiddenTools = append(hiddenTools, tool)
			}
			continue
		}
		eager = append(eager, cachedRuntimeToolDefinition(defCache, tool))
	}
	sortToolDefinitions(eager)
	sortToolDefinitions(loadedTail)

	discovery := ""
	if len(hiddenTools) > 0 {
		discovery = tools.BuildToolSearchDescription(hiddenTools)
	}
	description := loader.Description()
	if discovery != "" {
		description = discovery
	}
	definitions := make([]kajicoderuntime.ToolDefinition, 0, len(eager)+1+len(loadedTail))
	definitions = append(definitions, eager...)
	definitions = append(definitions, kajicoderuntime.ToolDefinition{
		Name:        loader.Name(),
		Description: description,
		Parameters:  schemaToRuntimeMap(loader.Parameters()),
	})
	definitions = append(definitions, loadedTail...)
	return definitions, discovery
}

func eagerToolDefinitions(visible []tools.Tool, defCache map[string]kajicoderuntime.ToolDefinition) []kajicoderuntime.ToolDefinition {
	definitions := make([]kajicoderuntime.ToolDefinition, 0, len(visible))
	for _, tool := range visible {
		if tool.Name() == tools.ToolSearchToolName {
			continue
		}
		definitions = append(definitions, cachedRuntimeToolDefinition(defCache, tool))
	}
	sortToolDefinitions(definitions)
	return definitions
}

func sortToolDefinitions(definitions []kajicoderuntime.ToolDefinition) {
	sort.Slice(definitions, func(left int, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})
}

// cachedRuntimeToolDefinition returns the tool's rendered definition, reusing a
// cached render when defCache holds one for this tool name. A nil cache computes
// fresh.
func cachedRuntimeToolDefinition(defCache map[string]kajicoderuntime.ToolDefinition, tool tools.Tool) kajicoderuntime.ToolDefinition {
	if defCache == nil {
		return runtimeToolDefinition(tool)
	}
	if def, ok := defCache[tool.Name()]; ok {
		return def
	}
	def := runtimeToolDefinition(tool)
	defCache[tool.Name()] = def
	return def
}

// runtimeToolDefinition renders a tool's advertised definition as sent to the
// provider.
func runtimeToolDefinition(tool tools.Tool) kajicoderuntime.ToolDefinition {
	return kajicoderuntime.ToolDefinition{
		Name:        tool.Name(),
		Description: tool.Description(),
		Parameters:  schemaToRuntimeMap(tool.Parameters()),
	}
}
