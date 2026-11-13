package gather

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestTmuxReadClassifiesAFailureAsEveryProbeDoes: a read of a socket with no
// server behind it is ErrServerGone, one past its bound ErrServerUnresponsive,
// and every other failure keeps tmux's reason and is neither.
func TestTmuxReadClassifiesAFailureAsEveryProbeDoes(t *testing.T) {
	live := context.Background()
	expired, cancel := context.WithDeadline(live, time.Unix(0, 0))
	defer cancel()
	read := []string{"show-options", "-qv", "-t", "cc-1", "@pfm_herdr"}
	const lead = "tmux show-options -qv -t cc-1 @pfm_herdr: "
	for _, tc := range []struct {
		name     string
		probeCtx context.Context
		stderr   string
		sentinel error
		want     string
	}{
		{
			"socket file gone", live, "error connecting to /t/cc-1 (No such file or directory)\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"no server running", live, "no server running on /t/cc-1\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"refused", live, "error connecting to /t/cc-1 (Connection refused)\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"permission denied", live, "error connecting to /t/cc-1 (Permission denied)\n", nil,
			lead + "<nil>: error connecting to /t/cc-1 (Permission denied)",
		},
		{
			"session missing", live, "can't find session: cc-1\n", nil,
			lead + "<nil>: can't find session: cc-1",
		},
		{
			"past its bound", expired, "", ErrServerUnresponsive,
			lead + "tmux server did not answer within 2s: /t/cc-1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tmuxReadFailure(live, tc.probeCtx, &exec.ExitError{Stderr: []byte(tc.stderr)}, "/t/cc-1", read)
			gone, unresponsive := errors.Is(err, ErrServerGone), errors.Is(err, ErrServerUnresponsive)
			if err == nil || err.Error() != tc.want ||
				gone != (tc.sentinel == ErrServerGone) || unresponsive != (tc.sentinel == ErrServerUnresponsive) {
				t.Fatalf("tmuxReadFailure() = %v (gone %v, unresponsive %v), want %q wrapping %v",
					err, gone, unresponsive, tc.want, tc.sentinel)
			}
		})
	}
}
