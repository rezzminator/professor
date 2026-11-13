package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestHookRunnerEmptyCommandBuildsNoHook proves an unset --on-idle/--on-exit
// never scripts a spawn at all — the nil func headless.Watcher treats as
// "nothing to run".
func TestHookRunnerEmptyCommandBuildsNoHook(t *testing.T) {
	t.Parallel()
	if hook := hookRunner("  ", &deps.FakeRunner{}, &bytes.Buffer{}); hook != nil {
		t.Fatal("hookRunner(\"  \") returned a non-nil hook, want nil")
	}
}

// TestHookRunnerRunsThroughTheRunnerSeam pins the door this file exists to
// close (the unit-test law's three seams, item 2): the hook launches through deps.Runner.Start instead of a
// bare exec.Command, and its argv/env carry the chat's own facts.
func TestHookRunnerRunsThroughTheRunnerSeam(t *testing.T) {
	t.Parallel()
	fake := &deps.FakeRunner{}
	var stderr bytes.Buffer
	hook := hookRunner("echo hi", fake, &stderr)
	if hook == nil {
		t.Fatal("hookRunner(\"echo hi\") returned nil, want a hook")
	}
	status := headless.Status{
		Name: "chat-1", State: "error", Socket: "/tmp/cc-1", SessionID: "sid-1",
		Error: "server_overloaded", IdleSeconds: 7,
	}
	if err := hook(status); err != nil {
		t.Fatalf("hook() error = %v, want nil (errors are logged, never propagated)", err)
	}
	starts := fake.Starts()
	if len(starts) != 1 {
		t.Fatalf("Starts() returned %d entries, want 1", len(starts))
	}
	argv := starts[0].Argv
	if len(argv) != 3 || argv[1] != "-c" || argv[2] != "echo hi" {
		t.Fatalf("argv = %q, want a 3-element sh -c argv ending in the command", argv)
	}
	env := strings.Join(starts[0].Opts.Env, "\x00")
	for _, want := range []string{
		"CC_CHAT_NAME=chat-1",
		"CC_CHAT_STATE=error",
		"CC_CHAT_ERROR=server_overloaded",
		"CC_CHAT_IDLE_SECONDS=7",
		"CC_CHAT_SOCKET=/tmp/cc-1",
		"CC_CHAT_SESSION_ID=sid-1",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("hook env lacks %q: %q", want, env)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty on a hook the FakeRunner reports as succeeding", stderr.String())
	}
}

// TestHookRunnerReportsAFailedHookOnStderr proves a hook that cannot resolve
// sh, fails to start or exits non-nil is reported, never swallowed silently.
// Not parallel: the missing-sh row empties PATH with t.Setenv.
func TestHookRunnerReportsAFailedHookOnStderr(t *testing.T) {
	for _, tc := range []struct {
		name       string
		shMissing  bool
		wantStderr string
		wantStarts int
	}{
		{name: "start fails", wantStderr: "pfm chat watch: hook failed: boom\n", wantStarts: 1},
		{
			name:       "sh missing from PATH",
			shMissing:  true,
			wantStderr: "pfm chat watch: hook failed: resolve sh: exec: \"sh\": executable file not found in $PATH\n",
			wantStarts: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deps.FakeRunner{}
			fake.ScriptStart([]string{deps.Executable("sh")}, 0, errors.New("boom"), nil)
			if tc.shMissing {
				t.Setenv("PATH", t.TempDir())
			}
			var stderr bytes.Buffer
			hook := hookRunner("false", fake, &stderr)
			if err := hook(headless.Status{Name: "chat-1"}); err != nil {
				t.Fatalf("hook() error = %v, want nil (errors are logged, never propagated)", err)
			}
			if got := stderr.String(); got != tc.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tc.wantStderr)
			}
			if got := len(fake.Starts()); got != tc.wantStarts {
				t.Fatalf("Starts() = %d, want %d", got, tc.wantStarts)
			}
		})
	}
}

// TestRunHeadlessWatchRefusesBadTargetsAsUsage pins the refusals
// runHeadlessWatch owns before any poll, independent of any host door: no
// target at all, and a glob path.Match cannot parse.
func TestRunHeadlessWatchRefusesBadTargetsAsUsage(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{"no target", nil, "usage: pfm chat watch"},
		{"no target with a flag", []string{"--transitions"}, "usage: pfm chat watch"},
		{"malformed glob", []string{"fine", "["}, `"["`},
		{"quiet-after without transitions", []string{"x", "--quiet-after", "600"}, "usage: pfm chat watch"},
		{"negative quiet-after", []string{"x", "--transitions", "--quiet-after", "-1"}, "usage: pfm chat watch"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := runHeadlessWatch(testCase.args, &stdout, &stderr, &deps.FakeRunner{})
			if code != 2 {
				t.Fatalf("runHeadlessWatch(%q) rc = %d, want 2", testCase.args, code)
			}
			if !strings.Contains(stderr.String(), testCase.want) || stdout.Len() != 0 {
				t.Fatalf(
					"stdout = %q stderr = %q, want stderr naming %q",
					stdout.String(),
					stderr.String(),
					testCase.want,
				)
			}
		})
	}
}

// TestWatchTransitionsAnswersAnUnknownSeatWithSeen: under --transitions there
// is no pre-check, so first sight of a name nothing answers to is the SEEN
// snapshot; the target ends and the exit code says it was not alive.
func TestWatchNoWorkingRequiresTransitionsAndAppearsInHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runHeadlessWatch([]string{"ghost", "--no-working"}, &stdout, &stderr, &deps.FakeRunner{}); code != 2 ||
		!strings.Contains(stderr.String(), "usage: pfm chat watch") {
		t.Fatalf("without transitions: rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runHeadlessWatch([]string{"--help"}, &stdout, &stderr, &deps.FakeRunner{}); code != 0 ||
		!strings.Contains(stderr.String(), "--no-working") {
		t.Fatalf("help: rc=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestWatchHelpStatesTheTenSecondDefault: the flag's own help carries the
// default and the floor, so a caller reading --help learns both.
func TestWatchHelpStatesTheTenSecondDefault(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	runHeadlessWatch([]string{"--help"}, &stdout, &stderr, &deps.FakeRunner{})
	help := stdout.String() + stderr.String()
	if !strings.Contains(help, "seconds between samples (minimum 10; a smaller value runs as 10) (default 10)") {
		t.Fatalf("help = %q, want the --poll line naming the minimum and the default 10", help)
	}
}
