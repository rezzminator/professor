package hookentry

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

func TestExploreDenyFailsOpenAndSteersExploreToTracer(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		code          int
		want          string
	}{
		{name: "explore is denied", payload: `{"tool_input":{"subagent_type":"Explore","model":"sonnet"}}`, want: "permissionDecision\":\"deny\""},
		{name: "tracer child allowance", payload: `{"tool_input":{"subagent_type":"Explore","model":"haiku"}}`},
		{name: "other agent allowed", payload: `{"tool_input":{"subagent_type":"tracer","model":"sonnet"}}`},
		{name: "malformed fails open", payload: "not-json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := ExploreDeny(strings.NewReader(test.payload), &stdout, &stderr)
			if code != test.code || !strings.Contains(stdout.String(), test.want) {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q, want code=%d and %q",
					code,
					stdout.String(),
					stderr.String(),
					test.code,
					test.want,
				)
			}
		})
	}
}

// TestExploreDenyLogsAMalformedPayloadInsteadOfSwallowingIt pins L3-F18:
// fail-open is right for a PreToolUse hook, but the missing stderr line
// left a malformed payload indistinguishable from "nothing to deny" — every
// sibling (ExitClose, EpicInject, ReloadIntercept,
// ExitIntercept) logs its decode error in the same
// "pfm internal <hook>: decode hook payload (fail-open): %v" voice;
// ExploreDeny now does too.
func TestExploreDenyLogsAMalformedPayloadInsteadOfSwallowingIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExploreDeny(strings.NewReader("not-json"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ExploreDeny() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("stderr = %q, want it to name the decode failure instead of swallowing it", stderr.String())
	}
}

// TestExploreDenyReadFailureIsLoggedToo pins the same fail-open-but-loud
// contract for a stdin read error, not just a decode error.
func TestExploreDenyReadFailureIsLoggedToo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExploreDeny(failingReader{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ExploreDeny() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "read hook payload") {
		t.Fatalf("stderr = %q, want it to name the read failure instead of swallowing it", stderr.String())
	}
}

// TestExploreDenyAndTracerIntakeNeverAnswerTheSameSpawn pins the two hooks that
// share the Agent|Task PreToolUse matcher: a tracer spawn gets intake's allow
// and updatedInput with explore-deny silent, and an Explore spawn gets
// explore-deny's deny with intake silent, so Claude Code never merges two
// decisions for one spawn.
func TestExploreDenyAndTracerIntakeNeverAnswerTheSameSpawn(t *testing.T) {
	home := t.TempDir()
	link := filepath.Join(home, ".claude", "skills", "tracetree")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	if err := os.Symlink(
		filepath.Join(filepath.Dir(source), "..", "..", "..", "templates", "global", "skills", "tracetree"),
		link,
	); err != nil {
		t.Fatal(err)
	}
	var intake string
	for _, hook := range claudelaunch.HookTemplates(home) {
		if hook.Name == "tracer-intake" {
			intake = hook.Command
		}
	}
	if intake == "" {
		t.Fatal("no tracer-intake row in claudelaunch.HookTemplates")
	}
	root, err := os.MkdirTemp("", "intakeprobe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
		_ = os.RemoveAll("/tmp/" + filepath.Base(root))
	})
	spawn := func(subagent, model string) string {
		payload, err := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "tool_name": "Agent", "cwd": "/nonexistent-cwd",
			"tool_input": map[string]any{
				"subagent_type": subagent, "model": model, "description": "trace the pane",
				"prompt": "Repo root: " + root + "\n1. What closes the pane?\n",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(payload)
	}
	for _, test := range []struct {
		name, payload             string
		denyAnswers, intakeAnswer bool
	}{
		{"tracer spawn", spawn("tracer", "sonnet"), false, true},
		{"Explore spawn", spawn("Explore", "sonnet"), true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var denied, denyErr bytes.Buffer
			if code := ExploreDeny(strings.NewReader(test.payload), &denied, &denyErr); code != 0 {
				t.Fatalf("ExploreDeny() = %d, stderr %q", code, denyErr.String())
			}
			cmd := exec.Command("/bin/sh", "-c", intake)
			cmd.Stdin = strings.NewReader(test.payload)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			var allowed, intakeErr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &allowed, &intakeErr
			if err := cmd.Run(); err != nil {
				t.Fatalf("intake: %v, stderr %q", err, intakeErr.String())
			}
			if (denied.Len() > 0) != test.denyAnswers || (allowed.Len() > 0) != test.intakeAnswer {
				t.Fatalf("explore-deny %q, intake %q; want explore-deny answering=%v, intake answering=%v",
					denied.String(), allowed.String(), test.denyAnswers, test.intakeAnswer)
			}
			if test.denyAnswers && !strings.Contains(denied.String(), `"permissionDecision":"deny"`) {
				t.Fatalf("explore-deny stdout %q is no deny", denied.String())
			}
		})
	}
}
