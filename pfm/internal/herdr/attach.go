package herdr

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

type attachRunnerKey struct{}

// WithAttachRunner hands Attach a runner in place of the real one, for a test to script the
// `herdr agent attach` child; the executable is still resolved through deps.Resolve.
func WithAttachRunner(ctx context.Context, runner deps.Runner) context.Context {
	return context.WithValue(ctx, attachRunnerKey{}, runner)
}

func attachRunner(ctx context.Context) deps.Runner {
	if runner, ok := ctx.Value(attachRunnerKey{}).(deps.Runner); ok {
		return runner
	}
	return deps.RealRunner{}
}

// Attach runs `herdr agent attach` on pane of socket in this terminal until
// the viewer detaches: the one attach of a Herdr chat outside Herdr. The
// inherited pane identity is stripped so the attach answers to socket alone.
func Attach(ctx context.Context, socket, pane string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary, err := deps.Resolve("herdr")
	if err != nil {
		return fmt.Errorf("resolve herdr: %w", err)
	}
	process, err := attachRunner(ctx).Start(
		ctx,
		[]string{binary, "agent", "attach", pane},
		deps.StartOptions{
			Env:    append(StripPane(os.Environ()), EnvSocket+"="+socket),
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		},
	)
	if err != nil {
		return err
	}
	return process.Wait()
}
