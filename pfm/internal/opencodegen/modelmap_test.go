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
	got, _, err := renderOpenCodeToolsBlock("worker.md", "Read, Bash, Agent", nil)
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
		got, _, err := renderOpenCodeToolsBlock("worker.md", allowList, nil)
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

func TestRenderOpenCodeToolsBlockWithoutMCPEntriesDeniesEveryKnownServer(t *testing.T) {
	got, warnings, err := renderOpenCodeToolsBlock("worker.md", "Read, Bash, Agent", []string{"local", "professor"})
	want := openCodeBuiltinDenialsForReadBashAgent + "  local_*: false\n  professor_*: false\n"
	if err != nil || got != want || len(warnings) != 0 {
		t.Fatalf("tools block = %q, %v, warnings %q; want %q", got, err, warnings, want)
	}
}

func TestRenderOpenCodeToolsBlockAllowsListedMCPTools(t *testing.T) {
	known := []string{"alpha", "my_server", "professor"}
	builtinDenials, _, err := renderOpenCodeToolsBlock("worker.md", "Read", nil)
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
			mcpLines: "  alpha_*: false\n  my_server_*: false\n" +
				"  professor_*: false\n" +
				"  professor_harvester_read: true\n" +
				"  professor_harvester_search_web: true\n",
		},
		{
			name:      "two servers sorted, ids sorted and de-duplicated",
			allowList: "Read, mcp__professor__harvester_search_web, mcp__alpha__zeta, mcp__professor__harvester_read, mcp__alpha__beta, mcp__alpha__zeta",
			mcpLines: "  alpha_*: false\n" +
				"  alpha_beta: true\n" +
				"  alpha_zeta: true\n" +
				"  my_server_*: false\n" +
				"  professor_*: false\n" +
				"  professor_harvester_read: true\n" +
				"  professor_harvester_search_web: true\n",
		},
		{
			name:      "sanitized server keeps its underscores",
			allowList: "Read, mcp__my.server__do-it",
			mcpLines:  "  alpha_*: false\n  my_server_*: false\n  my_server_do-it: true\n  professor_*: false\n",
		},
		{
			name:      "server wildcard grants the whole server",
			allowList: "Read, mcp__professor__*, mcp__professor__harvester_read",
			mcpLines:  "  alpha_*: false\n  my_server_*: false\n  professor_*: true\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := renderOpenCodeToolsBlock("worker.md", tc.allowList, known)
			if err != nil || got != builtinDenials+tc.mcpLines || len(warnings) != 0 {
				t.Fatalf("tools block = %q, %v, warnings %q; want %q", got, err, warnings, builtinDenials+tc.mcpLines)
			}
		})
	}
}

func TestRenderOpenCodeToolsBlockWarnsOnToolsItCannotMap(t *testing.T) {
	builtinDenials, _, err := renderOpenCodeToolsBlock("worker.md", "Read", nil)
	if err != nil {
		t.Fatalf("render Read: %v", err)
	}
	for _, tc := range []struct {
		name      string
		allowList string
		warning   string
	}{
		{
			name:      "retired harvester server",
			allowList: "Read, mcp__harvester__read",
			warning:   "retired MCP tool mcp__harvester__read in worker.md — the professor server serves it as mcp__professor__harvester_read",
		},
		{
			name:      "retired chat server",
			allowList: "Read, mcp__chat__chat_inject",
			warning:   "retired MCP tool mcp__chat__chat_inject in worker.md — the professor server serves it as mcp__professor__chat_inject",
		},
		{
			name:      "unknown server",
			allowList: "Read, mcp__nosuch__x",
			warning:   "unknown MCP server in tool mcp__nosuch__x in worker.md — neither professor nor a project .mcp.json server serves it; it stays denied",
		},
		{
			name:      "unmapped Claude tool",
			allowList: "Read, NotebookEdit",
			warning:   "unmapped Claude tool NotebookEdit in worker.md — no OpenCode equivalent; it stays denied",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := renderOpenCodeToolsBlock("worker.md", tc.allowList, []string{"professor"})
			want := builtinDenials + "  professor_*: false\n"
			if err != nil || got != want {
				t.Fatalf("tools block = %q, %v; want %q", got, err, want)
			}
			if len(warnings) != 1 || warnings[0] != tc.warning {
				t.Fatalf("warnings = %q, want [%q]", warnings, tc.warning)
			}
		})
	}
}

func TestRenderOpenCodeToolsBlockKeepsARetiredNameThatIsAProjectServer(t *testing.T) {
	got, warnings, err := renderOpenCodeToolsBlock(
		"worker.md",
		"Read, mcp__harvester__read",
		[]string{"harvester", "professor"},
	)
	if err != nil || len(warnings) != 0 || !strings.Contains(got, "  harvester_*: false\n  harvester_read: true\n") {
		t.Fatalf("tools block = %q, %v, warnings %q; want the project harvester server granted", got, err, warnings)
	}
}

func TestCompileFailsOnMalformedMCPToolNamingAgentAndEntry(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, ".claude", "agents", "broken.md")
	writeTestFile(t, agent, "---\ndescription: Broken role.\ntools: Read, mcp__broken\n---\nWork.\n")

	result, err := Compile(Options{Root: root, Home: filepath.Join(root, "home"), Mode: ModeCheck})
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
