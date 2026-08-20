package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// observingTool reports the directory given in its arg to the run's
// ProjectGuidelineObserver (fanning out from a "resolved" path) and returns a
// fixed status string. It mirrors what a real tool does when it resolves a file
// or scans a subtree, letting the integration test exercise the full
// observe-path → dynamic-catalog → skill-root propagation chain.
type observingTool struct {
	observed []string
}

func (tool *observingTool) Name() string { return "resolve_subtree" }
func (tool *observingTool) Description() string {
	return "resolves a directory for guideline observation"
}
func (tool *observingTool) Parameters() tools.Schema {
	return tools.Schema{
		Type: "object",
		Properties: map[string]tools.PropertySchema{
			"dir": {Type: "string"},
		},
		Required:             []string{"dir"},
		AdditionalProperties: false,
	}
}
func (tool *observingTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow, Reason: "read-only test tool"}
}
func (tool *observingTool) Run(ctx context.Context, args map[string]any) tools.Result {
	return tool.RunWithOptions(ctx, args, tools.RunOptions{})
}
func (tool *observingTool) RunWithOptions(_ context.Context, args map[string]any, options tools.RunOptions) tools.Result {
	dir, _ := args["dir"].(string)
	if dir != "" && options.ProjectGuidelines != nil {
		options.ProjectGuidelines.ObservePath(dir)
		tool.observed = append(tool.observed, dir)
	}
	return tools.Result{Status: tools.StatusOK, Output: "subtree loaded"}
}

// TestEndToEndProjectSkillDiscoveryAndDynamicCatalog drives the real agent loop:
// the model resolves a deep subtree via a tool, the loop re-renders the dynamic
// <available_skills> catalog into the next request, and the core skill tool
// resolves that project skill by name through the propagated ProjectSkillRoots.
func TestEndToEndProjectSkillDiscoveryAndDynamicCatalog(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)

	// A project skill living in a subtree the boot system prompt does NOT know
	// about (startup cwd is root; the skill is only reachable after resolving
	// deep/svc/api).
	apiDir := filepath.Join(root, "svc", "api")
	projectRoot := filepath.Join(apiDir, ".skills")
	mkdirAll := func(dir string) {
		testMkdirAll(t, dir)
	}
	writeFileTest := func(path string, content string) {
		testWriteFile(t, path, content)
	}
	mkdirAll(projectRoot)
	writeFileTest(filepath.Join(projectRoot, "repo-fix", "SKILL.md"),
		"---\nname: repo-fix\ndescription: repo-local fix convention\n---\n\n# Repo Fix\n\nAlways run gofmt after changing examples.")

	registry := tools.NewRegistry()
	resolver := &observingTool{}
	registry.Register(resolver)
	registry.Register(tools.NewSkillTool(filepath.Join(t.TempDir()))) // empty global dir

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			{
				{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "resolve_subtree"},
				{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"dir":"` + apiDir + `"}`},
				{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: kajicoderuntime.StreamEventDone},
			},
			{
				{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call-2", ToolName: "skill"},
				{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call-2", ArgumentsFragment: `{"name":"repo-fix"}`},
				{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call-2"},
				{Type: kajicoderuntime.StreamEventDone},
			},
			{
				{Type: kajicoderuntime.StreamEventText, Content: "fixed with the repo skill"},
				{Type: kajicoderuntime.StreamEventDone},
			},
		},
	}

	result, err := Run(context.Background(), "apply repo conventions", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeReadOnly,
		Cwd:            root,
		ProviderName:   "test-provider",
		Model:          "test-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.FinalAnswer, "repo skill") {
		t.Fatalf("final answer = %q", result.FinalAnswer)
	}
	if len(resolver.observed) != 1 || resolver.observed[0] != apiDir {
		t.Fatalf("tool observed dirs = %v, want [%s]", resolver.observed, apiDir)
	}

	// The skill tool must have resolved the repo skill through ProjectSkillRoots:
	// inspect the message emitted into the third (completion) request.
	if len(provider.requests) < 3 {
		t.Fatalf("provider requests = %d, want >= 3", len(provider.requests))
	}
	final := renderTranscript(provider.requests[2].Messages)
	if !strings.Contains(final, "repo-fix") || !strings.Contains(final, "gofmt") {
		t.Errorf("skill tool output did not surface the project skill:\n%s", final)
	}

	// The dynamic catalog must have been injected as a user message riding the
	// second request (the one after the subtree was observed).
	second := renderTranscript(provider.requests[1].Messages)
	if !strings.Contains(second, "Updated skill catalog") {
		t.Errorf("dynamic skill catalog block missing in the post-observation request:\n%s", second)
	}
	if !strings.Contains(second, "repo-fix") {
		t.Errorf("dynamic catalog should list the freshly discovered project skill, got:\n%s", second)
	}
}

func testMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdirAll %s: %v", dir, err)
	}
}

func testWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	testMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestEndToEndSkillAutoLoadOnPathMatch drives the real agent loop: the model
// resolves a subtree whose project skill declares a `when_to_use` glob matching
// that subtree; the tracker queues and the loop injects a coach message into the
// next request, nudging the model to load the skill via the skill tool.
func TestEndToEndSkillAutoLoadOnPathMatch(t *testing.T) {
	root := t.TempDir()
	makeGitRoot(t, root)

	targetDir := filepath.Join(root, "svc", "api")
	projectRoot := filepath.Join(targetDir, ".skills")
	testMkdirAll(t, filepath.Join(projectRoot, "svc-guide"))
	testWriteFile(t, filepath.Join(projectRoot, "svc-guide", "SKILL.md"),
		"---\nname: svc-guide\ndescription: writing svc handlers\nscope: when writing svc/** handlers\nwhen_to_use: svc/**\n---\n\n# Svc Guide\n\nHandlers live under svc.")
	// A second skill with NO why-to-use scope must not auto-load.
	testMkdirAll(t, filepath.Join(projectRoot, "manual-only"))
	testWriteFile(t, filepath.Join(projectRoot, "manual-only", "SKILL.md"),
		"---\nname: manual-only\ndescription: loaded by hand\n---\n\n# Manual\n\nNo auto-load.")

	registry := tools.NewRegistry()
	resolver := &observingTool{}
	registry.Register(resolver)
	registry.Register(tools.NewSkillTool(filepath.Join(t.TempDir())))

	provider := &mockProvider{
		turns: [][]kajicoderuntime.StreamEvent{
			{
				{Type: kajicoderuntime.StreamEventToolCallStart, ToolCallID: "call-1", ToolName: "resolve_subtree"},
				{Type: kajicoderuntime.StreamEventToolCallDelta, ToolCallID: "call-1", ArgumentsFragment: `{"dir":"` + targetDir + `"}`},
				{Type: kajicoderuntime.StreamEventToolCallEnd, ToolCallID: "call-1"},
				{Type: kajicoderuntime.StreamEventDone},
			},
			{
				{Type: kajicoderuntime.StreamEventText, Content: "noted the svc guide"},
				{Type: kajicoderuntime.StreamEventDone},
			},
		},
	}

	_, err := Run(context.Background(), "work in svc", provider, Options{
		Registry:       registry,
		PermissionMode: PermissionModeReadOnly,
		Cwd:            root,
		ProviderName:   "test-provider",
		Model:          "test-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.requests) < 2 {
		t.Fatalf("provider requests = %d, want >= 2", len(provider.requests))
	}
	second := renderTranscript(provider.requests[1].Messages)
	// The coach message (not the catalog, which lists all skills) must name the
	// scoped skill and not the unscoped one.
	coachIdx := strings.Index(second, "A skill applies to the location you are working in:")
	if coachIdx < 0 {
		t.Fatalf("auto-load coach message missing:\n%s", second)
	}
	coach := second[coachIdx:]
	if !strings.Contains(coach, "svc-guide") {
		t.Errorf("auto-load coach message missing scoped skill:\n%s", coach)
	}
	if strings.Contains(coach, "manual-only") {
		t.Errorf("unscoped skill must not auto-load:\n%s", coach)
	}
}
