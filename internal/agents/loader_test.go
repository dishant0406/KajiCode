package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMarkdownRoundTrips(t *testing.T) {
	content := `---
name: api-review
description: Reviews API changes
mode: subagent
tools:
  - read
  - mcp_github_*
excludeTools:
  - web_fetch
aliases:
  - reviewer
steps: 5
---

Review API changes.`
	agent, err := ParseMarkdown(content)
	if err != nil {
		t.Fatalf("ParseMarkdown: %v", err)
	}
	if agent.Name != "api-review" || agent.Mode != ModeSubagent || agent.Steps != 5 {
		t.Fatalf("unexpected agent: %#v", agent)
	}
	if Evaluate("mcp_github_create_issue", agent.Rules) != ActionAllow {
		t.Fatalf("mcp glob tool not allowed")
	}
	if Evaluate("web_fetch", agent.Rules) != ActionDeny {
		t.Fatalf("excluded tool not denied")
	}

	rendered := RenderMarkdown(agent)
	reparsed, err := ParseMarkdown(rendered)
	if err != nil {
		t.Fatalf("reparse rendered: %v", err)
	}
	if reparsed.Name != agent.Name || reparsed.Description != agent.Description {
		t.Fatalf("round-trip changed identity: %#v", reparsed)
	}
	if Evaluate("mcp_github_create_issue", reparsed.Rules) != ActionAllow {
		t.Fatalf("round-trip lost tools")
	}
}

func TestParseMarkdownRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"missing description": "---\nname: x\n---\nbody",
		"bad name":            "---\nname: Bad Name\ndescription: d\n---\nbody",
		"negative steps":      "---\nname: x\ndescription: d\nsteps: -1\n---\nbody",
		"duplicate key":       "---\nname: x\nname: y\ndescription: d\n---\nbody",
	}
	for name, content := range cases {
		if _, err := ParseMarkdown(content); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestLoadPrecedenceAndDisable(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project")
	userDir := filepath.Join(root, "user")
	writeAgent(t, userDir, "custom.md", "---\nname: custom\ndescription: user version\n---\nuser body")
	writeAgent(t, projectDir, "custom.md", "---\nname: custom\ndescription: project version\n---\nproject body")
	writeAgent(t, projectDir, "explorer.md", "---\nname: explorer\ndisable: true\n---\n")

	result, err := Load(Paths{UserDir: userDir, ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	custom, ok := Find(result, "custom")
	if !ok || custom.Description != "project version" {
		t.Fatalf("project should override user: %#v", custom)
	}
	if _, ok := Find(result, "explorer"); ok {
		t.Fatalf("explorer should be suppressed by disable: true")
	}
	if _, ok := Find(result, "worker"); !ok {
		t.Fatalf("builtin worker should survive")
	}
}

func TestLoadSkipsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "good.md", "---\nname: good\ndescription: ok\n---\nbody")
	writeAgent(t, dir, "broken.md", "---\nname: broken\n---\n") // no description/prompt
	result, err := Load(Paths{UserDir: dir})
	if err != nil {
		t.Fatalf("Load should not fail on one bad file: %v", err)
	}
	if _, ok := Find(result, "good"); !ok {
		t.Fatalf("good agent should load")
	}
	if _, ok := Find(result, "broken"); ok {
		t.Fatalf("broken agent should be skipped")
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected a warning for the skipped file")
	}
}

func TestExtendsCycleIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeAgent(t, dir, "a.md", "---\nname: a\ndescription: a\nextends: b\n---\nbody")
	writeAgent(t, dir, "b.md", "---\nname: b\ndescription: b\nextends: a\n---\nbody")
	if _, err := Load(Paths{UserDir: dir}); err == nil {
		t.Fatalf("expected a cycle error")
	}
}

func writeAgent(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
