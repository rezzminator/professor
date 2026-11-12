package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
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

// invokeStatusLine feeds the statusLine command its input and returns the first
// line it printed — the line the pane shows under the composer.
func invokeStatusLine(ctx context.Context, command string, input any, cwd string, environ []string) (string, error) {
	if command == "" {
		return "", nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode statusline input: %w", err)
	}
	shell, err := deps.Resolve("sh")
	if err != nil {
		return "", fmt.Errorf("statusline: sh not found: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, shell, "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("statusline %q: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	line, _, _ := strings.Cut(stdout.String(), "\n")
	return line, nil
}
