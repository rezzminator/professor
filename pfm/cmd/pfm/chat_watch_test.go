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

// TestRunHeadlessWatchRejectsMultipleTargets pins the flag-parsing refusal
// runHeadlessWatch owns, independent of any host door.
func TestRunHeadlessWatchRejectsMultipleTargets(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runHeadlessWatch([]string{"a", "b"}, &stdout, &stderr, &deps.FakeRunner{})
	if code != 2 {
		t.Fatalf("runHeadlessWatch(a, b) rc = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat watch") {
		t.Fatalf("stderr = %q, want the usage line", stderr.String())
	}
}
