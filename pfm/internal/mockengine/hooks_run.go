package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runHookCommand runs one hook under its timeout, which passes on clk (clockBound) as the real engine's
// passes on its own. Its output goes to unlinked files, never pipes, so a child the hook leaves running with
// them open (an orphan, a background job) never holds the answer back: the answer is read once sh is gone.
func runHookCommand(
	ctx context.Context,
	clk clock.Clock,
	command string,
	payload []byte,
	cwd string,
	environ []string,
	timeout time.Duration,
) (hookAnswer, error) {
	shell, err := deps.Resolve("sh")
	if err != nil {
		return hookAnswer{}, fmt.Errorf("sh not found: %w", err)
	}
	stdout, stderr, err := hookOutputs()
	if err != nil {
		return hookAnswer{}, err
	}
	defer func() { _ = stdout.Close(); _ = stderr.Close() }()
	bounded, cancel := clockBound(ctx, clk, timeout)
	defer cancel()
	cmd := exec.CommandContext(bounded, shell, "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if boundPassed(bounded) {
		return hookAnswer{}, fmt.Errorf("timed out after %s", timeout)
	}
	output, errOutput, readErr := readHookOutputs(stdout, stderr)
	if readErr != nil {
		return hookAnswer{}, readErr
	}
	answer := hookAnswer{stderr: strings.TrimSpace(errOutput)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		answer.exitCode = exitErr.ExitCode()
	default:
		return hookAnswer{}, fmt.Errorf("run: %w", err)
	}
	if text := strings.TrimSpace(output); text != "" && strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &answer); err != nil {
			return hookAnswer{}, fmt.Errorf("decode stdout %q: %w", text, err)
		}
	}
	return answer, nil
}

// hookOutputs are the two unlinked files a hook writes its stdout and stderr to: created in a directory of
// their own and unlinked with it at once, never renamed anywhere (no atomic write), so nothing is left behind.
func hookOutputs() (stdout, stderr *os.File, err error) {
	dir, err := paths.MkdirTemp("mock-engine-hook", "hook-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create the hook output directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil && err == nil {
			_ = stdout.Close()
			_ = stderr.Close()
			stdout, stderr, err = nil, nil, fmt.Errorf("unlink the hook output directory %s: %w", dir, removeErr)
		}
	}()
	if stdout, err = os.Create(filepath.Join(dir, "stdout")); err != nil {
		return nil, nil, fmt.Errorf("create the hook stdout file: %w", err)
	}
	if stderr, err = os.Create(filepath.Join(dir, "stderr")); err != nil {
		_ = stdout.Close()
		return nil, nil, fmt.Errorf("create the hook stderr file: %w", err)
	}
	return stdout, stderr, nil
}

// readHookOutputs reads what the hook wrote so far into its two output files.
func readHookOutputs(stdout, stderr *os.File) (output, errOutput string, err error) {
	read := func(file *os.File) (string, error) {
		content, err := io.ReadAll(io.NewSectionReader(file, 0, 1<<30))
		if err != nil {
			return "", fmt.Errorf("read the hook output %s: %w", file.Name(), err)
		}
		return string(content), nil
	}
	if output, err = read(stdout); err != nil {
		return "", "", err
	}
	errOutput, err = read(stderr)
	return output, errOutput, err
}
