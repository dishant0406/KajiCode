package agents

import "testing"

func TestEvaluateDefaultsToAsk(t *testing.T) {
	if got := Evaluate("anything", Ruleset{{Tool: "read_file", Action: ActionAllow}}); got != ActionAsk {
		t.Fatalf("unmatched = %q, want ask", got)
	}
}

func TestToolsFromSelectionExpandsCategories(t *testing.T) {
	rules := ToolsFromSelection([]string{"read"})
	if got := Evaluate("grep", rules); got != ActionAllow {
		t.Fatalf("grep = %q, want allow", got)
	}
	if got := Evaluate("write_file", rules); got != ActionDeny && got != ActionAsk {
		t.Fatalf("write_file = %q, want not-allow", got)
	}
}

func TestToolsFromSelectionEmptyIsReadOnly(t *testing.T) {
	rules := ToolsFromSelection(nil)
	if got := Evaluate("read_file", rules); got != ActionAllow {
		t.Fatalf("read_file = %q, want allow", got)
	}
	if got := Evaluate("exec_command", rules); got == ActionAllow {
		t.Fatalf("exec_command should not be allowed by default")
	}
}

func TestExcludeRulesWin(t *testing.T) {
	rules := Rules([]string{"read", "edit"}, []string{"edit"})
	if got := Evaluate("write_file", rules); got != ActionDeny {
		t.Fatalf("write_file = %q, want deny", got)
	}
	if got := Evaluate("grep", rules); got != ActionAllow {
		t.Fatalf("grep = %q, want allow", got)
	}
}

func TestMCPStyleGlobTools(t *testing.T) {
	rules := Rules([]string{"mcp_github_*", "read"}, nil)
	if got := Evaluate("mcp_github_create_issue", rules); got != ActionAllow {
		t.Fatalf("mcp tool = %q, want allow", got)
	}
}
