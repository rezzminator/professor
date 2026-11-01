package herdr

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Attach runs `herdr agent attach` on pane of socket in this terminal until
// the viewer detaches: the one attach of a Herdr chat outside Herdr. The
// inherited pane identity is stripped so the attach answers to socket alone.
func Attach(ctx context.Context, socket, pane string, stdin io.Reader, stdout, stderr io.Writer) error {
	binary, err := deps.Resolve("herdr")
	if err != nil {
		return fmt.Errorf("resolve herdr: %w", err)
	}
	process, err := (deps.RealRunner{}).Start(
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
