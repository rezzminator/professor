package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// `pfm chat space` takes at most one non-blank name, and from a process no
// lookup can place in a chat it fails with rc 1 naming why, printing no
// success line.
func TestChatSpaceRefusesWhatItCannotMeanAndFailsNamingTheCaller(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"TMUX": "", "TMUX_PANE": "", "HERDR_SOCKET_PATH": filepath.Join(testjail.ShortRoot(t), "absent.sock"),
		"HERDR_PANE_ID": "", "CLAUDE_CODE_SESSION_ID": "", "CODEX_THREAD_ID": "",
	} {
		t.Setenv(name, value)
	}
	const usage = "usage: pfm chat space [name]\n"
	for _, test := range []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"two names", []string{"space", "glass", "epic"}, 2, usage},
		{"a blank name", []string{"space", "  "}, 2, usage},
		{
			"a name from no chat",
			[]string{"space", "glass"},
			1,
			"pfm chat space: identify the calling chat: " + resolve.ErrNotInChat.Error() + "\n",
		},
		{
			"no name from no chat",
			[]string{"space"},
			1,
			"pfm chat space: identify the calling chat: " + resolve.ErrNotInChat.Error() + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runChatWithRuntime(
				test.args, nil, &stdout, &stderr, commandRuntime{Paths: values}, context.Background(),
			)
			if code != test.code || stdout.String() != "" || stderr.String() != test.stderr {
				t.Fatalf("pfm chat %v = rc %d, stdout %q, stderr %q; want rc %d, stderr %q",
					test.args, code, stdout.String(), stderr.String(), test.code, test.stderr)
			}
		})
	}
}
