package mockengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

func (loop *paneLoop) startAttentionEvents(steps []Step) (context.CancelFunc, <-chan error, error) {
	if len(steps) == 0 {
		return func() {}, nil, nil
	}
	emitter, ok := loop.engine.(interface {
		attentionEvents(context.Context, []Step) error
	})
	if !ok {
		return nil, nil, fmt.Errorf("%s has no concurrent attention protocol", loop.proc.engine)
	}
	ctx, cancel := context.WithCancel(loop.proc.ctx)
	finished := make(chan error, 1)
	go func() { finished <- emitter.attentionEvents(ctx, steps) }()
	return cancel, finished, nil
}

func (session *claudeSession) attentionEvents(ctx context.Context, steps []Step) error {
	// A sibling event uses the same identity but cannot mutate the chooser's pending tool.
	proc := session.proc
	emitter := *session
	emitter.proc = &process{
		ctx: ctx, engine: proc.engine, args: proc.args, stdin: proc.stdin, stdout: proc.stdout,
		stderr: proc.stderr, env: proc.env, script: proc.script, cwd: proc.cwd, pid: proc.pid, recorder: proc.recorder,
	}
	for index := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		step := steps[index]
		var err error
		switch step.Type {
		case StepHook:
			err = emitter.hook(step)
		case StepNotification:
			err = emitter.notification(step)
		case StepHold:
			bounded, cancel := context.WithTimeout(ctx, hookTimeout)
			err = waitHold(bounded, step.UntilGone)
			cancel()
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("hold %s: %w", step.UntilGone, err)
			}
		default:
			err = fmt.Errorf("unsupported concurrent attention step %s", step.Type)
		}
		if err != nil {
			return fmt.Errorf("during step %d: %w", index, err)
		}
	}
	return nil
}

func waitHold(ctx context.Context, path string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("hold %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
