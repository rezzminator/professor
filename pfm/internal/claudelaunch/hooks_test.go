package claudelaunch

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestHookTemplates(t *testing.T) {
	home := t.TempDir()
	hooks := HookTemplates(home)
	if len(hooks) != 21 {
		t.Fatalf("registrations=%d, want 21", len(hooks))
	}
	for _, hook := range hooks {
		if hook.Name == hookTracerIntake {
			continue
		}
		if !strings.HasPrefix(hook.Command, home+"/.local/bin/pfm ") {
			t.Errorf("command=%q", hook.Command)
		}
	}
}

func TestTracerIntakeHookRidesBesideExploreDeny(t *testing.T) {
	home := t.TempDir()
	kit := filepath.Join(home, ".claude", "skills", "tracetree", "tracetree.py")
	hooks := HookTemplates(home)
	var names []string
	for _, hook := range hooks {
		if hook.Event == hookEventPreToolUse && hook.Matcher == HookExploreMatcher {
			names = append(names, hook.Name)
		}
		if hook.Name != hookTracerIntake {
			continue
		}
		if hook.Event != hookEventPreToolUse || hook.Matcher != HookExploreMatcher || hook.Async {
			t.Errorf("tracer-intake row = %+v, want a synchronous PreToolUse row on %q", hook, HookExploreMatcher)
		}
		if want := "python3 '" + kit + "' intake || "; !strings.HasPrefix(hook.Command, want) {
			t.Errorf("command=%q, want prefix %q", hook.Command, want)
		}
	}
	if want := []string{"explore-deny", hookTracerIntake}; !slices.Equal(names, want) {
		t.Errorf("Agent|Task PreToolUse hooks = %q, want %q", names, want)
	}
}

// intakeSpawn is a recorded PreToolUse payload of a tracer spawn: the caller
// names the repo root in its brief and asks one numbered question.
func intakeSpawn(t *testing.T, root string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Agent", "cwd": "/nonexistent-cwd",
		"tool_input": map[string]any{
			"subagent_type": "tracer", "description": "trace the pane",
			"prompt": "Repo root: " + root + " (read-only)\nGoal: close panes safely\n1. What closes the pane?\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

// intakeRoot is a repo root for a tracer spawn; the tracetree kit opens its run
// under /tmp/{root's name}/tracer, removed with the test.
func intakeRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "intakeprobe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
		_ = os.RemoveAll("/tmp/" + filepath.Base(root))
	})
	return root
}

// linkKit places the shipped tracetree kit where pfm install links it: the
// whole skill directory at {home}/.claude/skills/tracetree.
func linkKit(t *testing.T, home string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	kit := filepath.Join(filepath.Dir(source), "..", "..", "..", "templates", "global", "skills", "tracetree")
	link := filepath.Join(home, ".claude", "skills", "tracetree")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kit, link); err != nil {
		t.Fatal(err)
	}
}

// runHookCommand runs one hook command line the way Claude Code does: through
// sh -c with the payload on stdin.
func runHookCommand(t *testing.T, command, payload string, env []string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		exit, isExit := err.(*exec.ExitError)
		if !isExit {
			t.Fatalf("run %q: %v", command, err)
		}
		code = exit.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func intakeCommand(t *testing.T, home string) string {
	t.Helper()
	for _, hook := range HookTemplates(home) {
		if hook.Name == hookTracerIntake {
			return hook.Command
		}
	}
	t.Fatal("no tracer-intake row in HookTemplates")
	return ""
}

func TestRenderedTracerIntakeCommandOpensARunForATracerSpawn(t *testing.T) {
	for name, home := range map[string]string{
		"plain home":       t.TempDir(),
		"home with spaces": filepath.Join(t.TempDir(), "a home with spaces"),
		"home with quote":  filepath.Join(t.TempDir(), "o'neil"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			linkKit(t, home)
			machine := pfmconfig.Defaults(home, []string{filepath.Join(home, ".claude", "projects", "one")})
			installFakePFM(t, home)
			_, parsed := renderParsed(t, Request{Purpose: PurposeInteractive, Home: home, SessionID: "S"}, machine)
			var commands []string
			for _, hook := range parsed.Hooks {
				if hook.Event == hookEventPreToolUse && hook.Matcher == HookExploreMatcher {
					commands = append(commands, hook.Command)
				}
			}
			exploreDeny := filepath.Join(home, ".local", "bin", "pfm") + " internal explore-deny"
			if len(commands) != 2 || commands[0] != exploreDeny {
				t.Fatalf("rendered Agent|Task PreToolUse commands = %q, want explore-deny then tracer-intake", commands)
			}
			command := commands[1]
			stdout, stderr, code := runHookCommand(
				t,
				command,
				intakeSpawn(t, intakeRoot(t)),
				[]string{"PATH=" + os.Getenv("PATH")},
			)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
			}
			var out struct {
				Spec struct {
					Decision string `json:"permissionDecision"`
					Updated  struct {
						Prompt string `json:"prompt"`
					} `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatalf("stdout %q: %v", stdout, err)
			}
			lines := strings.Split(out.Spec.Updated.Prompt, "\n")
			if out.Spec.Decision != "allow" || len(lines) < 2 || !strings.HasPrefix(lines[1], "RUN: /tmp/") {
				t.Fatalf(
					"decision=%q prompt=%q, want allow and a RUN: line after the Repo root line",
					out.Spec.Decision,
					out.Spec.Updated.Prompt,
				)
			}
		})
	}
}

// A hook that exits 2 blocks the tool call; the tracer-intake hook must turn
// every failure of its own into a non-blocking exit that names itself.
func TestTracerIntakeHookNeverBlocksASpawn(t *testing.T) {
	emptyPath := t.TempDir()
	for _, test := range []struct {
		name string
		link bool
		path string
	}{
		{"kit file missing", false, os.Getenv("PATH")},
		{"python3 missing", true, emptyPath},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.link {
				linkKit(t, home)
			}
			stdout, stderr, code := runHookCommand(
				t, intakeCommand(t, home), intakeSpawn(t, intakeRoot(t)), []string{"PATH=" + test.path},
			)
			if code != 1 || stdout != "" {
				t.Errorf("code=%d stdout=%q, want exit 1 (non-blocking) and no stdout", code, stdout)
			}
			if !strings.Contains(stderr, "pfm tracer-intake hook failed") ||
				!strings.Contains(stderr, filepath.Join(home, ".claude", "skills", "tracetree", "tracetree.py")) {
				t.Errorf("stderr=%q, want the hook and the kit path named", stderr)
			}
		})
	}
}

// TestTracerIntakeHookIsTheRegistryRow pins the accessor the doctor check
// reads: the hook is HookTemplates' own tracer-intake row, and the kit it
// returns is the file that row's command runs.
func TestTracerIntakeHookIsTheRegistryRow(t *testing.T) {
	home := t.TempDir()
	hook, kit, ok := TracerIntakeHook(home)
	if !ok {
		t.Fatal("TracerIntakeHook found no tracer-intake row in HookTemplates")
	}
	if !slices.Contains(HookTemplates(home), hook) {
		t.Errorf("hook = %+v, want a HookTemplates row", hook)
	}
	if want := "python3 " + shellQuote(kit) + " intake || "; !strings.HasPrefix(hook.Command, want) {
		t.Errorf("command=%q, want prefix %q (the returned kit is not the file the command runs)", hook.Command, want)
	}
}

func TestStoreGuardRidesBesideGitGuardOnBash(t *testing.T) {
	home := t.TempDir()
	var got []Hook
	for _, hook := range HookTemplates(home) {
		if hook.Event == hookEventPreToolUse && hook.Matcher == "Bash" {
			got = append(got, hook)
		}
	}
	want := []string{home + "/.local/bin/pfm internal git-guard", home + "/.local/bin/pfm internal store-guard"}
	if len(got) != len(want) {
		t.Fatalf("PreToolUse Bash hooks=%+v, want git-guard and store-guard", got)
	}
	for i, hook := range got {
		if hook.Command != want[i] {
			t.Errorf("PreToolUse Bash hook %d command=%q, want %q", i, hook.Command, want[i])
		}
	}
}

// The Herdr reporter rides every Claude launch: one row per lifecycle event,
// the per-turn and per-tool state flips async so a turn never waits on Herdr,
// PermissionRequest reports blocked with the tool it names, and the
// Notification row selects only the types the reporter maps.
func TestHerdrReporterRowsCoverTheClaudeLifecycle(t *testing.T) {
	home := t.TempDir()
	binary := home + "/.local/bin/pfm internal herdr-state --engine claude "
	want := []Hook{
		{Event: "SessionStart", Command: binary + "start", Name: "herdr-start"},
		{Event: "UserPromptSubmit", Command: binary + "working", Name: "herdr-working-prompt", Async: true},
		{Event: "PostToolUse", Command: binary + "working", Name: "herdr-working-tool", Async: true},
		{Event: "PostToolUseFailure", Command: binary + "working", Name: "herdr-working-tool-failure", Async: true},
		{Event: "PermissionRequest", Command: binary + "blocked", Name: "herdr-blocked", Async: true},
		{
			Event: "Notification", Matcher: "permission_prompt|elicitation_dialog|idle_prompt",
			Command: binary + "notification", Name: "herdr-notification", Async: true,
		},
		{Event: "Stop", Command: binary + "idle", Name: "herdr-idle"},
		{Event: "SessionEnd", Command: binary + "end", Name: "herdr-end"},
	}
	var got []Hook
	for _, hook := range HookTemplates(home) {
		if strings.HasPrefix(hook.Name, "herdr-") {
			got = append(got, hook)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("herdr rows = %+v, want %+v", got, want)
	}
}
