package opencodegen

import (
	"bytes"
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
		"---\ndescription: Worker role.\nmodel: sonnet # tier comment\ntools: Read, Bash, Agent\n---\nUse /tools:review.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "unconfigured.md"),
		"---\ndescription: Unconfigured role.\n---\nUse the defaults.\n",
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
	if !strings.Contains(string(worker), "mode: all") ||
		!strings.Contains(string(worker), "model: openai/gpt-5.6-sol-fast") ||
		!strings.Contains(string(worker), "tools:\n") ||
		!strings.Contains(string(worker), "  edit: false\n") ||
		strings.Contains(string(worker), "  read: false\n") ||
		strings.Contains(string(worker), "  bash: false\n") ||
		strings.Contains(string(worker), "  task: false\n") ||
		!strings.Contains(string(worker), "/tools-review") ||
		strings.Contains(string(worker), "Model tier:") {
		t.Fatalf("agent projection did not follow OpenCode shape: %s", worker)
	}
	unconfigured, _ := os.ReadFile(filepath.Join(root, ".opencode", "agent", "unconfigured.md"))
	unconfiguredFrontmatter := strings.SplitN(string(unconfigured), "---", 3)[1]
	if strings.Contains(unconfiguredFrontmatter, "\nmodel:") || strings.Contains(unconfiguredFrontmatter, "\ntools:") {
		t.Fatalf("agent without source model/tools acquired policy keys: %s", unconfigured)
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

func TestOpenCodeAgentModelOverrideWinsOverDefault(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "worker.md"),
		"---\ndescription: Worker role.\nmodel: sonnet\n---\nWork.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, openCodeBuildConfigPath),
		`{"modelMap":{"sonnet":"openai/other"}}`,
	)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	worker, err := os.ReadFile(filepath.Join(root, ".opencode", "agent", "worker.md"))
	if err != nil || !strings.Contains(string(worker), "model: openai/other\n") {
		t.Fatalf("override was not emitted: worker=%q err=%v", worker, err)
	}
}

func TestOpenCodeUnmappedModelIsAProblemInEveryMode(t *testing.T) {
	for _, action := range []string{"build", "check", "doctor"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			source := filepath.Join(root, ".claude", "agents", "worker.md")
			writeTestFile(
				t,
				source,
				"---\ndescription: Worker role.\nmodel: nonesuch\n---\nWork.\n",
			)

			var stdout, stderr bytes.Buffer
			code := RunCommand(
				[]string{action, root, "--home", home},
				func() (string, error) { return root, nil },
				home,
				&stdout,
				&stderr,
			)
			if code != 1 || !strings.Contains(stderr.String(), "nonesuch") ||
				!strings.Contains(stderr.String(), source) || strings.Contains(stdout.String(), "PASS") {
				t.Fatalf(
					"%s unmapped result: code=%d stdout=%q stderr=%q",
					action,
					code,
					stdout.String(),
					stderr.String(),
				)
			}
			if action == "build" {
				if _, err := os.Stat(filepath.Join(root, ".opencode", "agent", "worker.md")); !os.IsNotExist(err) {
					t.Fatalf("build wrote an agent despite unmapped model: %v", err)
				}
			}
		})
	}
}

func TestOpenCodeMalformedModelOverrideIsAProblem(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	path := filepath.Join(root, openCodeBuildConfigPath)
	writeTestFile(t, path, `{"modelMap":`)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeCheck})
	if err != nil || result.OK || !containsProblem(result.Problems, path) ||
		!containsProblem(result.Problems, "parse") {
		t.Fatalf("malformed override result=%#v err=%v", result, err)
	}
}

func TestOpenCodeDoctorRejectsGeneratedAgentModeDrift(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "worker.md"),
		"---\ndescription: Worker role.\nmodel: sonnet\n---\nWork.\n",
	)
	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	path := filepath.Join(root, ".opencode", "agent", "worker.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, strings.Replace(string(raw), "mode: all", "mode: subagent", 1))

	doctor, err := Compile(Options{Root: root, Home: home, Mode: ModeDoctor})
	joined := strings.Join(doctor.Problems, "\n")
	if err != nil || doctor.OK || !strings.Contains(joined, "INVALID "+path) ||
		!strings.Contains(joined, `mode "subagent"`) {
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
