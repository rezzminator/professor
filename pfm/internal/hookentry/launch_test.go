package hookentry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchPassThroughPredicate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		tmux      string
		forced    bool
		want      bool
	}{
		{name: "interactive empty argv"},
		{name: "resume stays interactive", arguments: []string{"--resume", "session-id"}},
		{name: "print short", arguments: []string{"-p", "hello"}, want: true},
		{name: "print long anywhere", arguments: []string{"--model", "opus", "--print", "hello"}, want: true},
		{name: "output format separate", arguments: []string{"--output-format", "json"}, want: true},
		{name: "output format equals", arguments: []string{"--output-format=stream-json"}, want: true},
		{name: "help short", arguments: []string{"-h"}, want: true},
		{name: "help long", arguments: []string{"--help"}, want: true},
		{name: "version short", arguments: []string{"-v"}, want: true},
		{name: "version long", arguments: []string{"--version"}, want: true},
		{name: "agents subcommand", arguments: []string{"agents", "--json"}, want: true},
		{name: "subcommand after flags", arguments: []string{"--verbose", "doctor"}, want: true},
		{name: "option value is first nonflag", arguments: []string{"--model", "opus", "agents"}},
		{name: "ordinary prompt", arguments: []string{"hello"}},
		{name: "already in claude socket", tmux: "/tmp/tmux-1000/cc-1-2-3,123,0", want: true},
		{name: "already in codex socket", tmux: "/tmp/tmux-1000/cx-1-2-3,123,0", want: true},
		{name: "unmanaged tmux", tmux: "/tmp/tmux-1000/vsct,123,0"},
		{name: "forced", forced: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := launchPassThrough(test.arguments, test.tmux, test.forced); got != test.want {
				t.Fatalf(
					"launchPassThrough(%q, %q, %t) = %t, want %t",
					test.arguments,
					test.tmux,
					test.forced,
					got,
					test.want,
				)
			}
		})
	}
}

func TestReadLaunchStatusRejectsMissingAndInvalidFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	_, err := readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file missing") {
		t.Fatalf("readLaunchStatus missing error=%v", err)
	}
	if err := os.WriteFile(path, []byte("not-a-status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file invalid") {
		t.Fatalf("readLaunchStatus invalid error=%v", err)
	}
}
