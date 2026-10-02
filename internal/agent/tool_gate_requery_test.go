package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dishant0406/KajiCode/internal/classifier"
	"github.com/dishant0406/KajiCode/internal/kajicoderuntime"
	"github.com/dishant0406/KajiCode/internal/tools"
)

// recordingProvider answers a tool-less request with scripted text and records
// every request, so a re-query test can assert both the generated call and that
// the generator ran out-of-band (no tools advertised).
type recordingProvider struct {
	text   string
	reqs   []kajicoderuntime.CompletionRequest
	failOn int
	calls  int
}

func (p *recordingProvider) StreamCompletion(_ context.Context, request kajicoderuntime.CompletionRequest) (<-chan kajicoderuntime.StreamEvent, error) {
	p.calls++
	p.reqs = append(p.reqs, request)
	if p.failOn > 0 && p.calls == p.failOn {
		return nil, errors.New("provider unavailable")
	}
	events := []kajicoderuntime.StreamEvent{
		{Type: kajicoderuntime.StreamEventText, Content: p.text},
		{Type: kajicoderuntime.StreamEventDone},
	}
	return streamEvents(events), nil
}

// coverageClassifier answers the block questions as irrelevant and the coverage
// question with a scripted sequence, so a re-query test controls how many rounds
// the loop takes (insufficient → sufficient).
type coverageClassifier struct {
	blockProb float64
	errorProb float64
	coverage  []float64
	calls     int
}

func (c *coverageClassifier) Name() string { return "coverage-stub" }

func (c *coverageClassifier) Classify(_ context.Context, req classifier.Request) (classifier.Result, error) {
	result := classifier.Result{}
	for id := range req.Questions {
		switch id {
		case gateErrorQuestionID:
			result[id] = classifier.Answer{Type: classifier.KindNoul, Probability: c.errorProb}
		case gateCoverageQuestionID:
			probability := 0.0
			if len(c.coverage) > 0 {
				idx := min(c.calls, len(c.coverage)-1)
				probability = c.coverage[idx]
			}
			c.calls++
			result[id] = classifier.Answer{Type: classifier.KindNoul, Probability: probability}
		default:
			result[id] = classifier.Answer{Type: classifier.KindNoul, Probability: c.blockProb}
		}
	}
	return result, nil
}

func TestStubNamesToolAndTarget(t *testing.T) {
	stub := &stubClassifier{probs: map[string]float64{"b000": 0.0, "b001": 0.0, "b002": 0.0, "b003": 0.0, "gate_error": 0.01}}
	call := ToolCall{Name: "grep", Arguments: `{"pattern":"TODO","path":"internal/"}`}
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gateFor(stub, false)}, messagesWithGoal(), call, okResult(bigBody(100)))
	if !strings.Contains(got.Output, "grep") || !strings.Contains(got.Output, `path="internal/"`) {
		t.Fatalf("stub must name the tool and its target so the model can re-query:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "re-issue grep") {
		t.Fatalf("stub must tell the model how to ask again:\n%s", got.Output)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestCoverageRecordsInsufficiencyWithoutRequery(t *testing.T) {
	// Requery is off: the gate must record the coverage signal but never rewrite
	// beyond the stub, and must not spend a generator call.
	provider := &recordingProvider{text: `{"name":"grep","arguments":{"pattern":"x"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1}}
	gate := gateFor(cl, false)
	gate.Requery = false
	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate, OnUsage: nil}, gateRun{provider: provider, registry: tools.NewRegistry()}, ToolCall{Name: "bash"}, okResult(bigBody(100)))
	if provider.calls != 0 {
		t.Fatalf("requery off must not call the provider, got %d", provider.calls)
	}
	if got.Meta[gateMetaCoverage] != "" || got.Meta[gateMetaRequery] != "" {
		t.Fatalf("requery off must not set coverage meta, got %v", got.Meta)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryAppendsImprovedResult(t *testing.T) {
	// The generator proposes a narrower grep; the re-query executes it against a
	// real registry and adopts the result once coverage says it is sufficient.
	provider := &recordingProvider{text: `{"name":"grep","arguments":{"pattern":"TODO"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1, 0.9}}
	gate := gateFor(cl, false)
	gate.Requery = true
	gate.MaxRequery = 2

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "grep", caps: tools.ToolCapabilities{Effect: tools.EffectReadOnly, ThreadSafe: true}, permission: tools.PermissionAllow, out: "MATCHED LINE FROM RE-QUERY"})

	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "grep", Arguments: `{"pattern":"TODO","path":"."}`}, okResult(bigBody(100)))

	if provider.calls != 1 {
		t.Fatalf("expected one generator call, got %d", provider.calls)
	}
	if len(provider.reqs[0].Tools) != 0 {
		t.Fatalf("the generator must be tool-less, got %d tools", len(provider.reqs[0].Tools))
	}
	if !strings.Contains(got.Output, "MATCHED LINE FROM RE-QUERY") {
		t.Fatalf("improved result must be adopted:\n%s", got.Output)
	}
	// The stub carries the spill recovery pointer; the improved text must be
	// APPENDED after it, never replace it, or a dropped body becomes unrecoverable.
	if !strings.Contains(got.Output, "[gate]") || !strings.Contains(got.Output, got.Meta["spill_path"]) {
		t.Fatalf("the recovery stub must survive the re-query:\n%s", got.Output)
	}
	if strings.Index(got.Output, "[gate]") > strings.Index(got.Output, "MATCHED LINE FROM RE-QUERY") {
		t.Fatalf("the improved text must come after the stub:\n%s", got.Output)
	}
	if got.Meta[gateMetaCoverage] != "true" {
		t.Fatalf("coverage meta must record sufficiency, got %q", got.Meta[gateMetaCoverage])
	}
	if got.Meta[gateMetaRequery] != "1" {
		t.Fatalf("expected one re-query round, got %q", got.Meta[gateMetaRequery])
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryAdoptsEvenWhenNeverJudgedSufficient(t *testing.T) {
	// Regression: requiring the classifier to bless the re-queried text as
	// "sufficient" before keeping it made the loop inert — the same distribution
	// that yields no clean low tail means that verdict almost never fires, so the
	// rounds ran and their output was thrown away. Because the improved text is
	// APPENDED after the stub, adopting it can only add information, so a real
	// re-query result must be kept even when coverage keeps saying "not enough".
	provider := &recordingProvider{text: `{"name":"read_file","arguments":{"path":"pkg/huge.go"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.05}} // always insufficient
	gate := gateFor(cl, false)
	gate.Requery = true
	gate.MaxRequery = 2

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "read_file", caps: tools.ToolCapabilities{Effect: tools.EffectReadOnly, ThreadSafe: true}, permission: tools.PermissionAllow, out: "const TARGET_VALUE = \"the-answer\""})

	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "bash", Arguments: `{"command":"cat pkg/huge.go"}`}, okResult(bigBody(100)))

	if !strings.Contains(got.Output, "the-answer") {
		t.Fatalf("a real re-query result must be adopted even when never judged sufficient:\n%s", got.Output)
	}
	if got.Meta[gateMetaRequery] == "0" || got.Meta[gateMetaRequery] == "" {
		t.Fatalf("rounds must be recorded, got %q", got.Meta[gateMetaRequery])
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryRefusesMutatingCall(t *testing.T) {
	// A generator that proposes a write must be refused: an unattended re-query
	// can never mutate the workspace.
	provider := &recordingProvider{text: `{"name":"bash","arguments":{"command":"rm -rf x"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1}}
	gate := gateFor(cl, false)
	gate.Requery = true

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "bash", caps: tools.ToolCapabilities{Effect: tools.EffectWorkspaceWrite}, permission: tools.PermissionPrompt, out: "should not run"})

	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "bash", Arguments: `{"command":"ls"}`}, okResult(bigBody(100)))

	if strings.Contains(got.Output, "should not run") {
		t.Fatalf("a mutating generated call must never execute:\n%s", got.Output)
	}
	if got.Meta[gateMetaCoverage] != "false" {
		t.Fatalf("insufficient coverage should be recorded, got %q", got.Meta[gateMetaCoverage])
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryRepeatedCallStopsImmediately(t *testing.T) {
	// A generator that repeats the same call can only return the same bytes; the
	// loop must stop rather than churn.
	provider := &recordingProvider{text: `{"name":"grep","arguments":{"pattern":"TODO"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1}}
	gate := gateFor(cl, false)
	gate.Requery = true

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "grep", caps: tools.ToolCapabilities{Effect: tools.EffectReadOnly, ThreadSafe: true}, permission: tools.PermissionAllow, out: "same"})

	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "grep", Arguments: `{"pattern":"TODO"}`}, okResult(bigBody(100)))

	if got.Meta[gateMetaRequery] != "0" {
		t.Fatalf("a repeated call must not count as a round, got %q", got.Meta[gateMetaRequery])
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryStripsInteractiveCallbacks(t *testing.T) {
	// A re-query runs unattended: it must not fire the permission prompt, the
	// user's hooks, or UI phases, or an invisible retry would look like an extra
	// tool call to the surface (and could block on a prompt nobody can answer).
	provider := &recordingProvider{text: `{"name":"grep","arguments":{"pattern":"TODO"}}`}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1, 0.9}}
	gate := gateFor(cl, false)
	gate.Requery = true

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "grep", caps: tools.ToolCapabilities{Effect: tools.EffectReadOnly, ThreadSafe: true}, permission: tools.PermissionAllow, out: "ok"})

	permissionRequests, toolResults := 0, 0
	options := Options{
		ToolResultGate: gate,
		OnPermissionRequest: func(context.Context, PermissionRequest) (PermissionDecision, error) {
			permissionRequests++
			return PermissionDecision{Action: PermissionDecisionAllow}, nil
		},
		OnToolResult: func(ToolResult) { toolResults++ },
	}
	got := maybeGateToolResult(context.Background(), options, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "grep", Arguments: `{"pattern":"TODO","path":"."}`}, okResult(bigBody(100)))

	if permissionRequests != 0 {
		t.Fatalf("a re-query must never surface a permission prompt, got %d", permissionRequests)
	}
	if toolResults != 0 {
		t.Fatalf("a re-query must not surface as a tool result to the surface, got %d", toolResults)
	}
	if !strings.Contains(got.Output, "ok") {
		t.Fatalf("the improved result should still be adopted:\n%s", got.Output)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestRequeryProviderFailureLeavesStub(t *testing.T) {
	provider := &recordingProvider{text: "x", failOn: 1}
	cl := &coverageClassifier{blockProb: 0.0, errorProb: 0.01, coverage: []float64{0.1}}
	gate := gateFor(cl, false)
	gate.Requery = true

	registry := tools.NewRegistry()
	registry.Register(&fakeGateTool{name: "grep", caps: tools.ToolCapabilities{Effect: tools.EffectReadOnly, ThreadSafe: true}, permission: tools.PermissionAllow, out: "never reached"})

	got := maybeGateToolResult(context.Background(), Options{ToolResultGate: gate}, gateRun{
		provider: provider, registry: registry, messages: messagesWithGoal().messages,
	}, ToolCall{Name: "grep", Arguments: `{"pattern":"TODO"}`}, okResult(bigBody(100)))

	if strings.Contains(got.Output, "never reached") {
		t.Fatalf("a failed generator must not adopt anything:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "[gate]") {
		t.Fatalf("the stub must survive a generator failure:\n%s", got.Output)
	}
	_ = os.Remove(got.Meta["spill_path"])
}

func TestParseGeneratedCall(t *testing.T) {
	cases := map[string]struct {
		text string
		want string
	}{
		"plain object":        {`{"name":"grep","arguments":{"pattern":"x"}}`, "grep"},
		"fenced-ish trailer":  {`{"name":"read_file","arguments":{"path":"a.go"}}`, "read_file"},
		"done sentinel":       {`{"done":true}`, ""},
		"no name":             {`{"arguments":{"x":1}}`, ""},
		"empty arguments":     {`{"name":"grep","arguments":{}}`, ""},
		"not json":            {"sorry, I cannot", ""},
		"nested json inside":  {`{"name":"grep","arguments":{"pattern":"a{2}"}}`, "grep"},
		"trailing whole objs": {`{"name":"grep","arguments":{"pattern":"x"}}{"z":1}`, "grep"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseGeneratedCall(tc.text); got.Name != tc.want {
				t.Fatalf("parseGeneratedCall(%q).Name = %q, want %q", tc.text, got.Name, tc.want)
			}
		})
	}
}

// fakeGateTool is a minimal tools.Tool for re-query tests: it returns a fixed
// body and declares a fixed capability/permission, so requeryAllowed can be
// exercised without a real filesystem tool.
type fakeGateTool struct {
	name       string
	caps       tools.ToolCapabilities
	permission tools.Permission
	out        string
}

func (f *fakeGateTool) Name() string             { return f.name }
func (f *fakeGateTool) Description() string      { return "fake" }
func (f *fakeGateTool) Parameters() tools.Schema { return tools.Schema{Type: "object"} }
func (f *fakeGateTool) Safety() tools.Safety     { return tools.Safety{Permission: f.permission} }
func (f *fakeGateTool) Run(context.Context, map[string]any) tools.Result {
	return tools.Result{Status: tools.StatusOK, Output: f.out}
}
func (f *fakeGateTool) Capabilities() tools.ToolCapabilities { return f.caps }
