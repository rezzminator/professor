package opencodegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCheckDoctorCompileOpenCodeTree(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "worker.md"),
		"---\ndescription: Worker role.\nmodel: sonnet\n---\nUse /tools:review.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "tools", "review.md"),
		"---\ndescription: Review.\n---\nReview the change.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "skills", "context-meter", "SKILL.md"),
		"---\ndescription: Meter.\n---\nCount context.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".mcp.json"),
		`{"mcpServers":{"local":{"command":"pfm","args":["mcp","serve"],"env":{"X":"1"}},"remote":{"url":"http://127.0.0.1:9/mcp"}}}`,
	)
	writeTestFile(t, filepath.Join(root, "LICENSE"), "license\n")
	writeTestFile(t, filepath.Join(root, "SECURITY.md"), "security\n")
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "global.md"),
		"---\ndescription: Global.\n---\nGlobal.\n",
	)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	for _, path := range []string{
		filepath.Join(root, ".opencode", "agent", "worker.md"),
		filepath.Join(root, ".opencode", "command", "tools-review.md"),
		filepath.Join(root, ".opencode", "skills", "context-meter"),
		filepath.Join(root, ".opencode", "opencode.jsonc"),
		filepath.Join(root, ".opencode", "LICENSE"),
		filepath.Join(root, ".opencode", "SECURITY.md"),
		filepath.Join(home, ".config", "opencode", "command", "global.md"),
	} {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Fatalf("build missing %s: %v", path, statErr)
		}
	}
	worker, _ := os.ReadFile(filepath.Join(root, ".opencode", "agent", "worker.md"))
	if !strings.Contains(string(worker), "mode: subagent") || strings.Contains(string(worker), "model:") ||
		!strings.Contains(string(worker), "/tools-review") ||
		!strings.Contains(string(worker), "Model tier: sonnet") {
		t.Fatalf("agent projection did not follow OpenCode shape: %s", worker)
	}
	command, _ := os.ReadFile(filepath.Join(root, ".opencode", "command", "tools-review.md"))
	commandText := string(command)
	if !strings.Contains(commandText, "description: \"Review.\"") || strings.Contains(commandText, "name:") ||
		strings.Contains(commandText, "allowed-tools:") ||
		strings.Contains(commandText, "argument-hint:") {
		t.Fatalf("command projection leaked Claude-only frontmatter: %s", commandText)
	}
	configRaw, _ := os.ReadFile(filepath.Join(root, ".opencode", "opencode.jsonc"))
	var config map[string]any
	if err := json.Unmarshal(parseOpenCodeJSONC(configRaw), &config); err != nil {
		t.Fatalf("generated config is not JSON: %v", err)
	}
	if _, ok := config["mcp"]; !ok {
		t.Fatalf("generated config omitted MCP mapping: %s", configRaw)
	}
	check, err := Compile(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || !check.OK {
		t.Fatalf("check result=%#v err=%v", check, err)
	}
	doctor, err := Compile(Options{Root: root, Home: home, Mode: ModeDoctor})
	if err != nil || !doctor.OK {
		t.Fatalf("doctor result=%#v err=%v", doctor, err)
	}
}

func TestCheckReportsUnmarkedConflictAndBuildPreservesIt(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".opencode", "command", "review.md")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "review.md"),
		"---\ndescription: Review.\n---\nReview.\n",
	)
	writeTestFile(t, path, "hand authored\n")
	result, err := Compile(Options{Root: root, Home: filepath.Join(root, "home"), Mode: ModeBuild})
	if err != nil || result.OK || !containsProblem(result.Problems, "CONFLICT") {
		t.Fatalf("unmarked conflict was not refused: result=%#v err=%v", result, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hand authored\n" {
		t.Fatalf("conflict was overwritten: %q", got)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}
