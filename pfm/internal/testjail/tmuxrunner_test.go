package testjail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestHungTmuxRunnerAnswersOnlyKillServer: list-panes waits out its caller's
// deadline, kill-server exits with the asked code and an unknown subcommand
// fails loudly.
func TestHungTmuxRunnerAnswersOnlyKillServer(t *testing.T) {
	fake := HungTmuxRunner(3)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := fake.Run(
		ctx,
		[]string{"tmux", "-S", "/nowhere", "list-panes", "-a"},
		deps.RunOptions{},
	); !errors.Is(
		err,
		context.DeadlineExceeded,
	) {
		t.Fatalf("list-panes = %v, want context.DeadlineExceeded", err)
	}
	if result, err := fake.Run(
		context.Background(),
		[]string{"tmux", "-S", "/nowhere", "kill-server"},
		deps.RunOptions{},
	); err != nil ||
		result.ExitCode != 3 {
		t.Fatalf("kill-server = %+v, %v; want exit 3", result, err)
	}
	result, err := fake.Run(
		context.Background(),
		[]string{"tmux", "-S", "/nowhere", "display-message"},
		deps.RunOptions{},
	)
	if err != nil || result.ExitCode != 1 ||
		string(result.Stderr) != "hung tmux fake: unexpected -S /nowhere display-message\n" {
		t.Fatalf("display-message = %+v, %v; want exit 1 naming it", result, err)
	}
}

// TestTmuxRunnerAnswersEveryCallAlike: any tmux argv gets the one result.
func TestTmuxRunnerAnswersEveryCallAlike(t *testing.T) {
	fake := TmuxRunner(deps.RunResult{Stdout: []byte("out"), ExitCode: 2})
	result, err := fake.Run(context.Background(), []string{"tmux", "-S", "/nowhere", "list-clients"}, deps.RunOptions{})
	if err != nil || string(result.Stdout) != "out" || result.ExitCode != 2 {
		t.Fatalf("list-clients = %+v, %v; want out, exit 2", result, err)
	}
}

// TestAbsentTmuxRunnerAnswersNoServer: every call exits 1 with tmux's own
// words for the socket it names, or for none.
func TestAbsentTmuxRunnerAnswersNoServer(t *testing.T) {
	fake := AbsentTmuxRunner()
	for _, tc := range []struct {
		argv       []string
		wantStderr string
	}{
		{[]string{"tmux", "-S", "/tmp/s/cc-1", "list-panes", "-a"}, "no server running on /tmp/s/cc-1\n"},
		{[]string{"tmux", "list-sessions"}, "no server running on \n"},
		{[]string{"tmux", "-S"}, "no server running on \n"},
	} {
		result, err := fake.Run(context.Background(), tc.argv, deps.RunOptions{})
		if err != nil || result.ExitCode != 1 || string(result.Stderr) != tc.wantStderr {
			t.Fatalf("%q = %+v, %v; want exit 1, stderr %q", tc.argv, result, err, tc.wantStderr)
		}
	}
}
