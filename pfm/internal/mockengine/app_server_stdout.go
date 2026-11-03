package mockengine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	envStdoutHold    = "MOCK_ENGINE_STDOUT_HOLD"
	envStdoutRecord  = "MOCK_ENGINE_STDOUT_RECORD"
	envStdoutTimeout = "MOCK_ENGINE_STDOUT_TIMEOUT_MS"
)

func stdoutHolder(ctx context.Context, gate string, stdout, stderr io.Writer, env func(string) string) int {
	timeout, err := strconv.Atoi(env(envStdoutTimeout))
	if err != nil || timeout <= 0 {
		warn(stderr, "stdout holder timeout must be positive")
		return ExitUsage
	}
	cwd, err := os.Getwd()
	if err != nil {
		warn(stderr, "stdout holder cwd: %v", err)
		return ExitUsage
	}
	proc := &process{
		ctx:      ctx,
		pid:      os.Getpid(),
		cwd:      cwd,
		stdout:   stdout,
		stderr:   stderr,
		recorder: recorder{dir: env(envStdoutRecord)},
	}
	if err := proc.birth("app-server-stdout-child.json"); err != nil {
		warn(stderr, "stdout holder birth: %v", err)
		return ExitUsage
	}
	if err := waitStartupPath(ctx, gate, time.Duration(timeout)*time.Millisecond); err != nil {
		warn(stderr, "stdout holder: %v", err)
		return ExitUsage
	}
	return 0
}

func (proc *process) retainAppServerStdout() error {
	if proc.script.AppServerStdoutHold == "" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("stdout holder executable: %w", err)
	}
	environ := append(os.Environ(),
		envStdoutHold+"="+proc.script.AppServerStdoutHold,
		envStdoutRecord+"="+proc.recorder.dir,
		envStdoutTimeout+"="+strconv.FormatInt(proc.script.startupBound().Milliseconds(), 10),
	)
	// The app-server's launcher owns its private process group; this descendant inherits it.
	child, err := (deps.RealRunner{}).Start(proc.ctx, []string{executable}, deps.StartOptions{
		Dir: proc.cwd, Env: environ, Stdout: proc.stdout, Stderr: proc.stderr,
	})
	if err != nil {
		return fmt.Errorf("start stdout holder: %w", err)
	}
	go func() { _ = child.Wait() }()
	return nil
}
