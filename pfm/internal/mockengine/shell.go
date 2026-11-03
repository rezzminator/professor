package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	shellStderrKey = "stderr"
	shellStdoutKey = "stdout"
)

type shellTask struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

func (proc *process) stopShellTasks() {
	for {
		proc.backgroundMu.Lock()
		tasks := proc.background
		proc.background = nil
		proc.backgroundMu.Unlock()
		if len(tasks) == 0 {
			return
		}
		for _, task := range tasks {
			task.cancel()
			<-task.done
		}
	}
}

// startShell is the common execution door for foreground tools and background agents.
func (proc *process) startShell(
	input json.RawMessage,
	completed func(toolExecution, error),
	toolEnvironment ...string,
) (deps.Process, <-chan struct{}, error) {
	var fields struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
		Workdir string `json:"workdir"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, nil, fmt.Errorf("decode shell input: %w", err)
	}
	if fields.Command == "" {
		fields.Command = fields.Cmd
	}
	if fields.Command == "" {
		return nil, nil, errors.New("shell tool needs command or cmd")
	}
	shell, err := deps.Resolve("bash")
	if err != nil {
		return nil, nil, err
	}
	environ, err := proc.actionEnvironment(append(proc.terminalEnvironment(os.Environ()), toolEnvironment...))
	if err != nil {
		return nil, nil, err
	}
	dir := proc.cwd
	if fields.Workdir != "" {
		dir = fields.Workdir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(proc.cwd, dir)
		}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(proc.ctx))
	var stdout, stderr bytes.Buffer
	child, err := (deps.RealRunner{}).Start(ctx, []string{shell, "-c", fields.Command}, deps.StartOptions{
		Dir: dir, Env: environ, Stdout: &stdout, Stderr: &stderr, WaitDelay: hookWaitDelay, ProcessGroup: true,
	})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	done := make(chan struct{})
	proc.backgroundMu.Lock()
	stop := func() {
		select {
		case <-done:
			return
		default:
		}
		if err := child.KillGroup(); err != nil {
			warn(proc.stderr, "stop shell task: %v", err)
		}
		cancel()
	}
	proc.background = append(proc.background, shellTask{stop, done})
	proc.backgroundMu.Unlock()
	go func() {
		select {
		case <-proc.ctx.Done():
			stop()
		case <-done:
		}
	}()
	go func() {
		defer close(done)
		defer cancel()
		waitErr := child.Wait()
		result := toolExecution{Text: stdout.String()}
		exitCode := 0
		if waitErr != nil {
			var failed *exec.ExitError
			if errors.As(waitErr, &failed) {
				exitCode = failed.ExitCode()
				result.Failure = fmt.Sprintf("Exit code %d\n%s", exitCode, stderr.String())
				waitErr = nil
			}
		}
		response, encodeErr := json.Marshal(
			map[string]any{
				shellStdoutKey: stdout.String(),
				shellStderrKey: stderr.String(),
				"interrupted":  ctx.Err() != nil,
			},
		)
		result.Response = response
		receipt, receiptErr := json.Marshal(
			map[string]any{
				keyCommand:     fields.Command,
				keyCWD:         dir,
				"pid":          child.Pid(),
				shellStdoutKey: stdout.String(),
				shellStderrKey: stderr.String(),
				"exit_code":    exitCode,
			},
		)
		if receiptErr == nil {
			receiptErr = proc.recorder.write("shell.jsonl", string(receipt)+"\n")
		}
		completed(result, errors.Join(waitErr, encodeErr, receiptErr))
	}()
	return child, done, nil
}

func (proc *process) shellTool(input json.RawMessage, toolEnvironment ...string) (toolExecution, error) {
	var background struct {
		Run bool `json:"run_in_background"`
	}
	if err := json.Unmarshal(input, &background); err != nil {
		return toolExecution{}, err
	}
	var result toolExecution
	var resultErr error
	child, done, err := proc.startShell(input, func(value toolExecution, err error) {
		if !background.Run {
			result, resultErr = value, err
		}
	}, toolEnvironment...)
	if err != nil {
		return toolExecution{}, err
	}
	if background.Run {
		raw, err := json.Marshal(
			map[string]any{
				"backgroundTaskId": fmt.Sprintf("bash_%d", child.Pid()),
				shellStdoutKey:     "",
				shellStderrKey:     "",
				"interrupted":      false,
			},
		)
		return toolExecution{Response: raw, Text: string(raw)}, err
	}
	<-done
	return result, resultErr
}
