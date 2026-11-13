package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
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
// keeps the hooks it read at start, so a stripped hook still fires on every
// prompt until the session restarts. It must exit 0 and print nothing — a
// UserPromptSubmit hook's stdout becomes prompt context, and its stderr is a
// warning on every prompt that no install can clear. A name the lists do not
// retire is unknown, and lists that cannot be read say so on that answer.
func TestRunInternalRetiredHookIsASilentNoOp(t *testing.T) {
	const lists = `{"hookCommands":[{"name":"sketch-nudge","subcommand":"internal sketch-nudge"}]}`
	for _, testCase := range []struct {
		name, lists, subcommand string
		code                    int
		stderr                  string
	}{
		{name: "retired", lists: lists, subcommand: "sketch-nudge"},
		{name: "not retired", lists: lists, subcommand: "doodle-nudge", code: 1, stderr: "pfm internal: unknown subcommand \"doodle-nudge\" — registered by a different pfm version than this binary (dev); run `pfm install --yes` with the binary you intend to keep\n"},
		{
			name: "unreadable lists", lists: `{"hookCommands":`, subcommand: "sketch-nudge", code: 1,
			stderr: "pfm internal: unknown subcommand \"sketch-nudge\" — registered by a different pfm version than this binary (dev); run `pfm install --yes` with the binary you intend to keep\npfm internal: retired hooks unread: retired lists {root}/002/templates/global/retired.json: decode: unexpected EOF\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			stageRetiredClone(t, home, testCase.lists)
			var stdout, stderr bytes.Buffer
			runtime := commandRuntime{Paths: paths.Values{Home: home}}
			code := runInternal([]string{testCase.subcommand}, &stdout, &stderr, runtime)
			if got := rooted(stderr.String(), filepath.Dir(home)); code != testCase.code || stdout.Len() != 0 ||
				got != testCase.stderr {
				t.Fatalf("runInternal(%q): exit %d (want %d) stdout=%q stderr = %q, want %q",
					testCase.subcommand, code, testCase.code, stdout.String(), got, testCase.stderr)
			}
		})
	}
}
