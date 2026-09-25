package opencodegen

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultOpenCodeModelMap(t *testing.T) {
	want := map[string]string{
		"opus":   "openai/gpt-5.6-sol",
		"sonnet": "openai/gpt-5.6-sol-fast",
		"haiku":  "openai/gpt-5.5-fast",
		"fable":  "openai/gpt-5.6-sol",
	}
	got := defaultOpenCodeModelMap()
	for alias, model := range want {
		if got[alias] != model {
			t.Fatalf("default model for %q = %q, want %q", alias, got[alias], model)
		}
	}
}

func TestLoadOpenCodeModelMapMergesOverride(t *testing.T) {
	root := t.TempDir()
	writeTestFile(
		t,
		filepath.Join(root, openCodeBuildConfigPath),
		`{"modelMap":{"sonnet":"openai/other"}}`,
	)

	got, err := loadOpenCodeModelMap(root)
	if err != nil {
		t.Fatal(err)
	}
	if got["sonnet"] != "openai/other" {
		t.Fatalf("sonnet override = %q, want openai/other", got["sonnet"])
	}
	if got["opus"] != "openai/gpt-5.6-sol" {
		t.Fatalf("unlisted opus default = %q, want openai/gpt-5.6-sol", got["opus"])
	}
}

func TestLoadOpenCodeModelMapNamesMalformedOverride(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, openCodeBuildConfigPath)
	writeTestFile(t, path, `{"modelMap":`)

	_, err := loadOpenCodeModelMap(root)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("load error = %v, want parse error naming %s", err, path)
	}
}

func TestRenderOpenCodeToolsBlockDisablesToolsOutsideClaudeAllowList(t *testing.T) {
	got, err := renderOpenCodeToolsBlock("Read, Bash, Agent")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, denied := range []string{
		"apply_patch", "edit", "execute", "glob", "grep", "invalid", "lsp", "plan_exit", "question",
		"skill", "todowrite", "webfetch", "websearch", "write",
	} {
		if !strings.Contains(got, "  "+denied+": false\n") {
			t.Errorf("tools block did not disable %q:\n%s", denied, got)
		}
	}
	for _, allowed := range []string{"bash", "read", "task"} {
		if strings.Contains(got, "  "+allowed+": false\n") {
			t.Errorf("tools block disabled allowed tool %q:\n%s", allowed, got)
		}
	}
}

func TestRenderOpenCodeToolsBlockLetsWriteAndEditReachApplyPatch(t *testing.T) {
	for _, allowList := range []string{"Write", "Edit"} {
		got, err := renderOpenCodeToolsBlock(allowList)
		if err != nil {
			t.Fatalf("render %s: %v", allowList, err)
		}
		if strings.Contains(got, "  apply_patch: false\n") {
			t.Fatalf("%s did not allow OpenCode's model editing tool:\n%s", allowList, got)
		}
	}
}

const openCodeBuiltinDenialsForReadBashAgent = "tools:\n" +
	"  apply_patch: false\n  edit: false\n  execute: false\n  glob: false\n  grep: false\n" +
	"  invalid: false\n  lsp: false\n  plan_exit: false\n  question: false\n  skill: false\n" +
	"  todowrite: false\n  webfetch: false\n  websearch: false\n  write: false\n"

func TestRenderOpenCodeToolsBlockWithoutMCPEntriesIsByteIdentical(t *testing.T) {
	got, err := renderOpenCodeToolsBlock("Read, Bash, Agent")
	if err != nil || got != openCodeBuiltinDenialsForReadBashAgent {
		t.Fatalf("tools block = %q, %v; want %q", got, err, openCodeBuiltinDenialsForReadBashAgent)
	}
}

func TestRenderOpenCodeToolsBlockAllowsListedMCPTools(t *testing.T) {
	builtinDenials, err := renderOpenCodeToolsBlock("Read")
	if err != nil {
		t.Fatalf("render Read: %v", err)
	}
	for _, tc := range []struct {
		name      string
		allowList string
		mcpLines  string
	}{
		{
			name:      "one server",
			allowList: "Read, mcp__professor__harvester_read, mcp__professor__harvester_search_web",
			mcpLines: "  professor_*: false\n" +
				"  professor_harvester_read: true\n" +
				"  professor_harvester_search_web: true\n",
		},
		{
			name:      "two servers sorted, ids sorted and de-duplicated",
			allowList: "Read, mcp__professor__harvester_search_web, mcp__alpha__zeta, mcp__professor__harvester_read, mcp__alpha__beta, mcp__alpha__zeta",
			mcpLines: "  alpha_*: false\n" +
				"  alpha_beta: true\n" +
				"  alpha_zeta: true\n" +
				"  professor_*: false\n" +
				"  professor_harvester_read: true\n" +
				"  professor_harvester_search_web: true\n",
		},
		{
			name:      "sanitized server keeps its underscores",
			allowList: "Read, mcp__my.server__do-it",
			mcpLines:  "  my_server_*: false\n  my_server_do-it: true\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderOpenCodeToolsBlock(tc.allowList)
			if err != nil || got != builtinDenials+tc.mcpLines {
				t.Fatalf("tools block = %q, %v; want %q", got, err, builtinDenials+tc.mcpLines)
			}
		})
	}
}

func TestCompileFailsOnMalformedMCPToolNamingAgentAndEntry(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, ".claude", "agents", "broken.md")
	writeTestFile(t, agent, "---\ndescription: Broken role.\ntools: Read, mcp__broken\n---\nWork.\n")

	result, err := Compile(Options{Root: root, Home: filepath.Join(root, "home"), Mode: ModeBuild})
	if err == nil && result.OK {
		t.Fatalf("compile succeeded with a malformed MCP tool: %#v", result)
	}
	report := strings.Join(result.Problems, "\n")
	if err != nil {
		report += "\n" + err.Error()
	}
	if !strings.Contains(report, agent) || !strings.Contains(report, `"mcp__broken"`) {
		t.Fatalf("compile report does not name %s and mcp__broken: %s", agent, report)
	}
}
