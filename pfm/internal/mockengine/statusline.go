package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// statusLineSetting is a settings layer's statusLine: the command, and how
// often Claude re-runs it besides its own events (refreshInterval, seconds;
// 0 never). A layer that names no command names no statusline.
type statusLineSetting struct {
	command string
	refresh time.Duration
}

// claudeStatusAbort is how long Claude Code lets one statusline run live: it aborts the run at 5 s, and
// at the next refresh tick (refreshInterval) when that comes sooner, and an aborted run paints nothing.
const claudeStatusAbort = 5 * time.Second

// statusRunBound is the life Claude Code gives a statusline run under a refreshInterval (0 never ticks).
func statusRunBound(refresh time.Duration) time.Duration {
	if refresh > 0 && refresh < claudeStatusAbort {
		return refresh
	}
	return claudeStatusAbort
}

// invokeStatusLine feeds the statusLine command its input and returns the first
// line it printed — the line the pane shows under the composer — and whether it
// landed: a run still going at bound is aborted, as Claude Code aborts it.
func invokeStatusLine(
	ctx context.Context, command string, input any, cwd string, environ []string, bound time.Duration,
) (string, bool, error) {
	if command == "" {
		return "", true, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", false, fmt.Errorf("encode statusline input: %w", err)
	}
	shell, err := deps.Resolve("sh")
	if err != nil {
		return "", false, fmt.Errorf("statusline: sh not found: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	cmd := exec.CommandContext(bounded, shell, "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("statusline %q: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	line, _, _ := strings.Cut(stdout.String(), "\n")
	return line, true, nil
}
