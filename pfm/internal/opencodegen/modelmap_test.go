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
	got := renderOpenCodeToolsBlock("Read, Bash, Agent")
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
		got := renderOpenCodeToolsBlock(allowList)
		if strings.Contains(got, "  apply_patch: false\n") {
			t.Fatalf("%s did not allow OpenCode's model editing tool:\n%s", allowList, got)
		}
	}
}
