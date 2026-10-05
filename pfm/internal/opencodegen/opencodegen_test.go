package opencodegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
		!strings.Contains(string(worker), "model: openai/gpt-6.1-sol-fast") ||
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

func TestOpenCodeAgentWithoutMCPToolsDeniesEveryKnownServer(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	agent := filepath.Join(root, ".claude", "agents", "reader.md")
	writeTestFile(
		t,
		agent,
		"---\ndescription: Reader role.\ntools:\n  - Read\n  - Grep\n  - NotebookEdit\n---\nRead.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".mcp.json"),
		`{"mcpServers":{"local":{"command":"pfm","args":["mcp","serve"]},"my.remote":{"url":"http://127.0.0.1:9/mcp"}}}`,
	)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	compiled, _ := os.ReadFile(filepath.Join(root, ".opencode", "agent", "reader.md"))
	if !strings.Contains(
		string(compiled),
		"  write: false\n  local_*: false\n  my_remote_*: false\n  professor_*: false\n---\n",
	) ||
		strings.Contains(string(compiled), "  read: false\n") ||
		strings.Contains(string(compiled), "  grep: false\n") {
		t.Fatalf("agent without MCP tools did not deny every known server: %s", compiled)
	}
	want := "unmapped Claude tool NotebookEdit in " + agent + " — no OpenCode equivalent; it stays denied"
	if !containsProblem(result.Warnings, want) {
		t.Fatalf("warnings = %q, want %q", result.Warnings, want)
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

func TestOpenCodeUnmappedModelAliasIsOmittedWithAWarning(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	unmapped := filepath.Join(root, ".claude", "agents", "worker.md")
	writeTestFile(t, unmapped, "---\ndescription: Worker role.\nmodel: inherit\n---\nWork.\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "lead.md"),
		"---\ndescription: Lead role.\nmodel: opus\n---\nLead.\n",
	)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	want := `unmapped OpenCode model alias "inherit" in ` + unmapped + " — model omitted"
	if !containsProblem(result.Warnings, want) {
		t.Fatalf("warnings = %q, want %q", result.Warnings, want)
	}
	worker, err := os.ReadFile(filepath.Join(root, ".opencode", "agent", "worker.md"))
	if err != nil || strings.Contains(strings.SplitN(string(worker), "---", 3)[1], "\nmodel:") {
		t.Fatalf("unmapped agent: worker=%q err=%v", worker, err)
	}
	lead, err := os.ReadFile(filepath.Join(root, ".opencode", "agent", "lead.md"))
	if err != nil || !strings.Contains(string(lead), "model: openai/gpt-6.1-sol\n") {
		t.Fatalf("mapped agent: lead=%q err=%v", lead, err)
	}
}

func TestOpenCodeDanglingCommandWarnsOnceInBuildAndIsAProblemInCheck(t *testing.T) {
	for _, action := range []string{"build", "check", "doctor"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			writeTestFile(
				t,
				filepath.Join(root, ".claude", "commands", "review.md"),
				"---\ndescription: Review.\n---\nReview.\n",
			)
			writeTestFile(
				t,
				filepath.Join(root, ".claude", "agents", "worker.md"),
				"---\ndescription: Worker.\n---\nWork.\n",
			)
			dangling := filepath.Join(root, ".claude", "commands", "gone.md")
			if err := os.Symlink(filepath.Join(root, "nowhere.md"), dangling); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := RunCommand(
				[]string{action, root, "--home", home},
				func() (string, error) { return root, nil },
				home,
				&stdout,
				&stderr,
			)
			mentions := 0
			for _, line := range strings.Split(stderr.String(), "\n") {
				if strings.Contains(line, dangling) {
					mentions++
				}
			}
			if action == "build" {
				if code != 0 || mentions != 1 || !strings.Contains(stderr.String(), "warning: DANGLING "+dangling) {
					t.Fatalf(
						"build: code=%d mentions=%d stdout=%q stderr=%q",
						code,
						mentions,
						stdout.String(),
						stderr.String(),
					)
				}
				for _, path := range []string{
					filepath.Join(root, ".opencode", "command", "review.md"),
					filepath.Join(root, ".opencode", "agent", "worker.md"),
					filepath.Join(root, ".opencode", "opencode.jsonc"),
				} {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("build skipped healthy output %s: %v", path, err)
					}
				}
				return
			}
			if code != 1 || mentions != 1 || !strings.Contains(stderr.String(), "pfm opencode: DANGLING "+dangling) {
				t.Fatalf("%s: code=%d mentions=%d stderr=%q", action, code, mentions, stderr.String())
			}
		})
	}
}

func TestOpenCodeUnmarkedConfigFailsAndIsLeftAloneInEveryMode(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, ".opencode", "opencode.jsonc")
	const handWritten = "{\n  \"model\": \"mine\"\n}\n"
	writeTestFile(t, config, handWritten)
	writeTestFile(t, filepath.Join(root, ".claude", "agents", "worker.md"), "---\ndescription: Worker.\n---\nWork.\n")

	for _, mode := range []Mode{ModeBuild, ModeCheck, ModeDoctor} {
		result, err := Compile(Options{Root: root, Home: home, Mode: mode})
		if err != nil || result.OK || !containsProblem(result.Problems, "CONFLICT "+config) {
			t.Fatalf("mode %d result=%#v err=%v", mode, result, err)
		}
		got, err := os.ReadFile(config)
		if err != nil || string(got) != handWritten {
			t.Fatalf("mode %d touched the unmarked config: %q err=%v", mode, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode", "agent", "worker.md")); err != nil {
		t.Fatalf("build skipped the agent beside an unmarked config: %v", err)
	}
}

func TestOpenCodeArchivedAgentIsNotCompiled(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeTestFile(t, filepath.Join(root, ".claude", "agents", "worker.md"), "---\ndescription: Worker.\n---\nWork.\n")
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "agents", "archive", "old.md"),
		"---\ndescription: Old.\n---\nOld.\n",
	)

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode", "agent", "worker.md")); err != nil {
		t.Fatalf("top-level agent missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".opencode", "agent", "old.md")); !os.IsNotExist(err) {
		t.Fatalf("archived agent was compiled: err=%v", err)
	}
}

func TestOpenCodeMirrorCopyConflictNamesTheRemedy(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeTestFile(t, filepath.Join(root, "LICENSE"), "the source license\n")
	copyPath := filepath.Join(root, ".opencode", "LICENSE")
	writeTestFile(t, copyPath, "a different license\n")

	result, err := Compile(Options{Root: root, Home: home, Mode: ModeCheck})
	want := "CONFLICT " + copyPath + " — differs from " + filepath.Join(root, "LICENSE") +
		" and pfm cannot prove it wrote it; delete it and rebuild"
	if err != nil || result.OK || !containsProblem(result.Problems, want) {
		t.Fatalf("problems=%q err=%v, want %q", result.Problems, err, want)
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

func TestOpenCodeAgentRelativePathFailureIsAProblemAndSkipsTheAgent(t *testing.T) {
	root := t.TempDir()
	var problems []string
	added := 0
	compileOpenCodeAgent(
		root,
		"worker",
		filepath.Join("relative", "worker.md"),
		nil,
		nil,
		nil,
		func(generatedFile) { added++ },
		func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) },
		func(string, ...any) {},
	)
	want := "relative path of " + filepath.Join("relative", "worker.md") + ": "
	if added != 0 || !containsProblem(problems, want) {
		t.Fatalf("added=%d problems=%q, want the entry skipped and %q", added, problems, want)
	}
}

// A broken source skips that one entry the way a dangling one does: build
// warns and writes every healthy output, check and doctor fail naming it.
func TestOpenCodeBrokenSourceIsSkippedInBuildAndIsAProblemInCheck(t *testing.T) {
	const agent, agentOut = ".claude/agents/broken.md", ".opencode/agent/broken.md"
	writes := func(content string) func(*testing.T, string) {
		return func(t *testing.T, path string) { writeTestFile(t, path, content) }
	}
	for _, door := range []struct {
		name   string
		source string
		output string
		plant  func(t *testing.T, path string)
	}{
		{"unreadable agent", agent, agentOut, plantUnreadableSource},
		{"malformed agent frontmatter", agent, agentOut, writes("---\ndescription: Broken role.\nWork.\n")},
		{"malformed agent tools", agent, agentOut, writes("---\ndescription: B.\ntools: mcp__broken\n---\nWork.\n")},
		{
			"malformed command frontmatter",
			".claude/commands/broken.md",
			".opencode/command/broken.md",
			writes("---\ndescription: Broken.\nReview.\n"),
		},
	} {
		for _, action := range []string{"build", "check"} {
			t.Run(door.name+"/"+action, func(t *testing.T) {
				root := t.TempDir()
				home := filepath.Join(root, "home")
				review := filepath.Join(root, ".claude", "commands", "review.md")
				writeTestFile(t, review, "---\ndescription: Review.\n---\nReview.\n")
				worker := filepath.Join(root, ".claude", "agents", "worker.md")
				writeTestFile(t, worker, "---\ndescription: Worker.\n---\nWork.\n")
				broken := filepath.Join(root, filepath.FromSlash(door.source))
				door.plant(t, broken)

				var stdout, stderr bytes.Buffer
				code := RunCommand(
					[]string{action, root, "--home", home},
					func() (string, error) { return root, nil },
					home,
					&stdout,
					&stderr,
				)
				named := ""
				for _, line := range strings.Split(stderr.String(), "\n") {
					if strings.Contains(line, broken) {
						named = line
					}
				}
				if action == "check" {
					if code != 1 || named == "" || strings.Contains(named, "warning:") {
						t.Fatalf("check: code=%d stderr=%q", code, stderr.String())
					}
					return
				}
				if code != 0 || !strings.Contains(named, "pfm opencode: warning: SKIP ") {
					t.Fatalf("build: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
				}
				for _, path := range []string{
					filepath.Join(root, ".opencode", "command", "review.md"),
					filepath.Join(root, ".opencode", "agent", "worker.md"),
					filepath.Join(root, ".opencode", "opencode.jsonc"),
				} {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("build skipped healthy output %s: %v", path, err)
					}
				}
				output := filepath.Join(root, filepath.FromSlash(door.output))
				if _, err := os.Stat(output); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("build wrote an output for the broken source %s: %v", broken, err)
				}
			})
		}
	}
}

// plantUnreadableSource plants a regular-file agent source whose read fails
// even for root (the fence runs as root, where a chmod does not bite): on
// Linux a symlink to /proc/self/mem, which stats regular and reads EIO.
func plantUnreadableSource(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" {
		if err := os.Symlink("/proc/self/mem", path); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, []byte("---\ndescription: Broken.\n---\nWork.\n"), 0o000); err != nil {
		t.Fatal(err)
	}
}

// buildOpenCodeForTest runs `pfm opencode {action}` over root and returns its
// exit code, stdout and stderr.
func buildOpenCodeForTest(t *testing.T, action, root, home string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunCommand(
		[]string{action, root, "--home", home},
		func() (string, error) { return root, nil },
		home,
		&stdout,
		&stderr,
	)
	return code, stdout.String(), stderr.String()
}

// seedOpenCodeLabber builds root with a regular .claude/agents/labber.md and
// returns the source path, its twin and the twin's seeded bytes.
func seedOpenCodeLabber(t *testing.T, root, home string) (string, string, []byte) {
	t.Helper()
	source := filepath.Join(root, ".claude", "agents", "labber.md")
	twin := filepath.Join(root, ".opencode", "agent", "labber.md")
	writeTestFile(t, source, "---\ndescription: Fixture lab role.\n---\nRun the lab.\n")
	if code, stdout, stderr := buildOpenCodeForTest(t, "build", root, home); code != 0 {
		t.Fatalf("seed build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	seeded, err := os.ReadFile(twin)
	if err != nil {
		t.Fatalf("seed build wrote no twin %s: %v", twin, err)
	}
	return source, twin, seeded
}

// An adopter's .claude/agents/labber.md symlinked into an uninitialised
// submodule names a source pfm cannot read right now, not one the adopter
// retired: build keeps its .opencode/agent twin byte for byte and says so.
func TestOpenCodeUnresolvableAgentSourceKeepsItsTwin(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source, twin, seeded := seedOpenCodeLabber(t, root, home)
	target := "../../vendor/lab/agents/labber.md"
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := buildOpenCodeForTest(t, "build", root, home)
	if code != 0 {
		t.Fatalf("build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	have, err := os.ReadFile(twin)
	if err != nil {
		t.Fatalf("twin %s of the unresolvable source was removed: %v (stdout %q)", twin, err, stdout)
	}
	if !bytes.Equal(have, seeded) {
		t.Fatalf("twin %s was rewritten:\nhave %q\nwant %q", twin, have, seeded)
	}
	want := "pfm opencode: warning: source unresolvable: " + source + " → " + target + "; twin kept"
	if !strings.Contains(stderr, want) {
		t.Fatalf("build stderr lacks %q: %q", want, stderr)
	}
	if strings.Contains(stdout, "pfm opencode: deleted ") || !strings.Contains(stdout, "deleted=0") {
		t.Fatalf("build deleted something with the twin kept: %q", stdout)
	}

	code, _, stderr = buildOpenCodeForTest(t, "check", root, home)
	if strings.Contains(stderr, "ORPHAN") {
		t.Fatalf("check reports the kept twin as an orphan: %q", stderr)
	}
	if code != 1 || !strings.Contains(stderr, "pfm opencode: DANGLING "+source) {
		t.Fatalf("check no longer gates on the dangling source: code=%d stderr=%q", code, stderr)
	}
}

// A source that is truly gone (no file, no link) still loses its twin.
func TestOpenCodeAbsentAgentSourceStillDeletesItsTwin(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source, twin, _ := seedOpenCodeLabber(t, root, home)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := buildOpenCodeForTest(t, "build", root, home)
	if code != 0 {
		t.Fatalf("build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Lstat(twin); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("twin %s of an absent source survived: %v", twin, err)
	}
	if !strings.Contains(stdout, "pfm opencode: deleted "+twin) {
		t.Fatalf("build did not name the deleted twin: %q", stdout)
	}
	if strings.Contains(stderr, "twin kept") {
		t.Fatalf("an absent source claimed a kept twin: %q", stderr)
	}
}

func TestOpenCodeWorkbenchParentProjects(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, "scribe", "CLAUDE.md"), "Scribe.\n")
	writeTestFile(t, filepath.Join(root, "scribe", ".professor", "workbench.json"), `{}`)
	writeTestFile(
		t,
		filepath.Join(root, "scribe", ".claude", "agents", "clerk.md"),
		"---\ndescription: Clerk.\n---\nClerk.\n",
	)
	writeTestFile(t, filepath.Join(root, "api", "CLAUDE.md"), "API.\n")
	writeTestFile(
		t,
		filepath.Join(root, "api", ".claude", "agents", "clerk.md"),
		"---\ndescription: Clerk.\n---\nClerk.\n",
	)
	result, err := Compile(Options{Root: root, Home: home, Mode: ModeBuild})
	if err != nil || !result.OK {
		t.Fatalf("build=%#v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode", "agent", "clerk-api.md")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode", "agent", "clerk-scribe.md")); !os.IsNotExist(err) {
		t.Errorf("parent compiled workbench: %v", err)
	}
}

func TestOpenCodeWorkbenchHomeFree(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(root, ".professor", "workbench.json"), `{}`)
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "memo.md"),
		"---\ndescription: Memo.\n---\nUse /tools:review.\n",
	)
	writeTestFile(
		t,
		filepath.Join(home, ".claude", "commands", "tools", "review.md"),
		"---\ndescription: Review.\n---\nReview.\n",
	)
	writeTestFile(
		t,
		filepath.Join(root, ".claude", "commands", "local.md"),
		"---\ndescription: Local.\n---\nUse /tools:review.\n",
	)
	sentinel := filepath.Join(home, ".config", "opencode", "command", "kept.md")
	body := newMarker + " from fixture\n---\ndescription: \n---\nKept.\n"
	writeTestFile(t, sentinel, body)
	for _, mode := range []Mode{ModeBuild, ModeCheck, ModeDoctor} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			result, err := Compile(Options{Root: root, Home: home, Mode: mode})
			if err != nil || !result.OK {
				t.Errorf("mode=%v result=%#v err=%v", mode, result, err)
			}
			if _, err := os.Stat(
				filepath.Join(home, ".config", "opencode", "command", "memo.md"),
			); !os.IsNotExist(
				err,
			) {
				t.Errorf("home memo: %v", err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != body {
				t.Errorf("home sentinel=%q err=%v", data, err)
			}
			data, err := os.ReadFile(filepath.Join(root, ".opencode", "command", "local.md"))
			if err != nil || !strings.Contains(string(data), "Use /tools-review.") {
				t.Errorf("home roster not used: %q err=%v", data, err)
			}
		})
	}
}
