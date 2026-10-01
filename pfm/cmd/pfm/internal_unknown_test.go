package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunInternalUnknownSubcommandIsANonBlockingHookError pins the exit code a
// hook sees when a different pfm version registered it: 1, never 2. Exit 2 is
// Claude Code's BLOCKING hook code — after a rolled-back update left a newer
// version's UserPromptSubmit hook behind, the older binary's usage exit erased
// every prompt ("UserPromptSubmit operation blocked by hook").
func TestRunInternalUnknownSubcommandIsANonBlockingHookError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runInternal([]string{"hook-from-a-newer-pfm"}, &stdout, &stderr, commandRuntime{}); code != 1 {
		t.Fatalf("unknown internal subcommand exit = %d, want 1 (non-blocking); stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `unknown subcommand "hook-from-a-newer-pfm"`) ||
		!strings.Contains(stderr.String(), "pfm install --yes") {
		t.Fatalf("stderr does not name the unknown subcommand and the fix: %q", stderr.String())
	}
	stderr.Reset()
	if code := runInternal(
		nil,
		&stdout,
		&stderr,
		commandRuntime{},
	); code != 2 ||
		!strings.Contains(stderr.String(), "usage: pfm internal") {
		t.Fatalf("bare `pfm internal` = %d, stderr=%q; want the usage and 2", code, stderr.String())
	}
}

// TestRunInternalRetiredHookIsASilentNoOp pins what a Claude session started
// before an install sees when it runs a hook that install retired: Claude Code
// keeps the hooks it read at start, so the stripped `internal compact-nudge`
// still fires on every prompt until the session restarts. It must exit 0 and
// print nothing — a UserPromptSubmit hook's stdout becomes prompt context, and
// its stderr is a warning on every prompt that no install can clear.
func TestRunInternalRetiredHookIsASilentNoOp(t *testing.T) {
	for _, name := range []string{"compact-nudge", "clear-hide"} {
		var stdout, stderr bytes.Buffer
		if code := runInternal([]string{name}, &stdout, &stderr, commandRuntime{}); code != 0 ||
			stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("retired hook %q: exit %d stdout=%q stderr=%q; want 0 and no output",
				name, code, stdout.String(), stderr.String())
		}
	}
}
