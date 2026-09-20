package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dishant0406/KajiCode/internal/config"
)

// Paths are the on-disk locations the loader scans.
type Paths struct {
	UserDir    string
	ProjectDir string
}

// DefaultPaths resolves the user and project agent directories for a workspace.
func DefaultPaths(workspaceRoot string) (Paths, error) {
	userConfigDir, err := config.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	paths := Paths{UserDir: filepath.Join(userConfigDir, "kajicode", "agents")}
	if strings.TrimSpace(workspaceRoot) != "" {
		paths.ProjectDir = filepath.Join(filepath.Clean(workspaceRoot), ".kajicode", "agents")
	}
	return paths, nil
}

// LoadResult is the merged, extends-resolved agent set plus any load warnings.
type LoadResult struct {
	Paths    Paths
	Agents   []Agent
	Warnings []string
}

// Load reads builtin, project, and user agents, merges by name (project beats
// user beats builtin), applies disable overrides and aliases, and resolves
// extends chains. It never fails on a single bad file: the file is skipped with
// a warning so one broken agent cannot disable delegation entirely.
func Load(paths Paths) (LoadResult, error) {
	if strings.TrimSpace(paths.UserDir) == "" && strings.TrimSpace(paths.ProjectDir) == "" {
		return LoadResult{Paths: paths, Agents: Builtins()}, nil
	}

	// Precedence is by append order (later wins in mergeByName): builtin <
	// user < project, so a project definition overrides a user one.
	agents := Builtins()
	warnings := []string{}

	userAgents, userWarnings, err := loadDirectory(paths.UserDir, LocationUser)
	if err != nil {
		return LoadResult{}, err
	}
	warnings = append(warnings, userWarnings...)
	agents = append(agents, userAgents...)

	projectAgents, projectWarnings, err := loadDirectory(paths.ProjectDir, LocationProject)
	if err != nil {
		return LoadResult{}, err
	}
	warnings = append(warnings, projectWarnings...)
	agents = append(agents, projectAgents...)

	merged := mergeByName(agents)
	resolved, err := resolveExtends(merged)
	if err != nil {
		return LoadResult{}, err
	}
	return LoadResult{Paths: paths, Agents: resolved, Warnings: warnings}, nil
}

// Find returns the agent with the given name or alias.
func Find(result LoadResult, name string) (Agent, bool) {
	name = strings.TrimSpace(name)
	for _, agent := range result.Agents {
		if agent.Name == name {
			return agent, true
		}
		for _, alias := range agent.Aliases {
			if alias == name {
				return agent, true
			}
		}
	}
	return Agent{}, false
}

// Names lists the registered agent names, sorted, for corrective error messages.
func Names(result LoadResult) []string {
	names := make([]string, 0, len(result.Agents))
	for _, agent := range result.Agents {
		if name := strings.TrimSpace(agent.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func loadDirectory(dir string, location Location) ([]Agent, []string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read agent directory %s: %w", dir, err)
	}
	agents := []Agent{}
	warnings := []string{}
	for _, entry := range entries {
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf("skipped symlink agent definition: %s", filepath.Join(dir, entry.Name())))
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped unreadable agent definition %s: %s", path, err))
			continue
		}
		agent, err := ParseMarkdown(string(data))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped invalid agent definition %s: %s", path, err))
			continue
		}
		agent.Location = location
		agent.FilePath = path
		agents = append(agents, agent)
	}
	return agents, warnings, nil
}

// mergeByName applies precedence: later definitions win. A higher-precedence
// definition with disable: true suppresses a lower-precedence agent of the same
// name entirely.
func mergeByName(agents []Agent) []Agent {
	byName := map[string]*Agent{}
	order := []string{}
	for _, agent := range agents {
		existing, ok := byName[agent.Name]
		if !ok {
			order = append(order, agent.Name)
			copy := agent
			byName[agent.Name] = &copy
			continue
		}
		if agent.Disable {
			// Keep the name in order but mark it suppressed.
			*existing = Agent{Name: agent.Name, Disable: true}
			continue
		}
		copy := agent
		byName[agent.Name] = &copy
	}
	sort.Strings(order)
	merged := make([]Agent, 0, len(order))
	for _, name := range order {
		agent := byName[name]
		if agent.Disable {
			continue
		}
		merged = append(merged, *agent)
	}
	return merged
}

func resolveExtends(agents []Agent) ([]Agent, error) {
	byName := map[string]Agent{}
	for _, agent := range agents {
		byName[agent.Name] = agent
	}
	cache := map[string]Agent{}
	resolved := make([]Agent, 0, len(agents))
	for _, agent := range agents {
		item, err := resolveOne(agent.Name, byName, cache, map[string]bool{})
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func resolveOne(name string, byName map[string]Agent, cache map[string]Agent, stack map[string]bool) (Agent, error) {
	if agent, ok := cache[name]; ok {
		return agent, nil
	}
	agent, ok := byName[name]
	if !ok {
		return Agent{}, fmt.Errorf("agent %q not found", name)
	}
	if stack[name] {
		return Agent{}, fmt.Errorf("cycle detected in agent extends chain at %q", name)
	}
	stack[name] = true
	defer delete(stack, name)

	baseName := strings.TrimSpace(agent.Extends)
	if baseName == "" {
		cache[name] = agent
		return agent, nil
	}
	base, ok := byName[baseName]
	if !ok {
		return Agent{}, fmt.Errorf("base agent %q for %q not found", baseName, agent.Name)
	}
	base, err := resolveOne(base.Name, byName, cache, stack)
	if err != nil {
		return Agent{}, err
	}
	merged := MergeExtends(base, agent)
	if err := Validate(&merged); err != nil {
		return Agent{}, err
	}
	cache[name] = merged
	return merged, nil
}
