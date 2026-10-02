package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	status := headless.Status{Name: "chat-1", State: "idle", Socket: "/tmp/cc-1", SessionID: "sid-1"}
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
		"CC_CHAT_STATE=idle",
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

// TestHookRunnerReportsAFailedHookOnStderr proves a hook that fails to start
// or exits non-nil is reported, never swallowed silently.
func TestHookRunnerReportsAFailedHookOnStderr(t *testing.T) {
	t.Parallel()
	fake := &deps.FakeRunner{}
	fake.ScriptStart([]string{deps.Executable("sh")}, 0, errors.New("boom"), nil)
	var stderr bytes.Buffer
	hook := hookRunner("false", fake, &stderr)
	if err := hook(headless.Status{Name: "chat-1"}); err != nil {
		t.Fatalf("hook() error = %v, want nil (errors are logged, never propagated)", err)
	}
	if !strings.Contains(stderr.String(), "hook failed") || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q, want it to name the failed hook and its cause", stderr.String())
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
func TestWatchTransitionsAnswersAnUnknownSeatWithSeen(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "watch", "ghost", "--transitions"}, &stdout, &stderr)
	if code != codeDeadChat || stdout.String() != "SEEN ghost not-found\n" {
		t.Fatalf(
			"rc = %d stdout = %q stderr = %q, want rc %d and the SEEN row",
			code,
			stdout.String(),
			stderr.String(),
			codeDeadChat,
		)
	}
}

// TestWatchScanFailureIsAnErrorLineNeverAbsence: when the fleet scan cannot
// look, the watch says so on stdout, where a monitor reads, in both modes.
func TestWatchScanFailureIsAnErrorLineNeverAbsence(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
	for _, testCase := range []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{"legacy", []string{"chat", "watch", "ghost"}, 2, "pfm chat: "},
		{"transitions", []string{"chat", "watch", "ghost", "--transitions"}, 1, "pfm chat watch: ghost: "},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(testCase.args, &stdout, &stderr)
			if code != testCase.wantCode || !strings.HasPrefix(stdout.String(), "ERROR ghost ") ||
				strings.Count(stdout.String(), "\n") != 1 || !strings.Contains(stderr.String(), testCase.wantStderr) {
				t.Fatalf("rc = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}
