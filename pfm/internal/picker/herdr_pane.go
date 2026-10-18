package picker

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// stdoutWatch counts what an open writes to stdout: under the eval protocol a
// written line is a command for THIS terminal to run, so its absence is the
// sign the chat opened somewhere else.
type stdoutWatch struct {
	out     io.Writer
	written int
}

func (watch *stdoutWatch) Write(p []byte) (int, error) {
	n, err := watch.out.Write(p)
	watch.written += n
	return n, err
}

// closePickerPaneAfter runs open and, when the picker runs inside a Herdr pane
// and the chat opened in a Herdr tab of its own (a new tab, or a live chat's
// existing one) with nothing left for this terminal to run, closes the
// picker's pane: the chosen chat is where the user lands, never a bare shell
// left behind. A failed close is reported; the chat is open either way.
func closePickerPaneAfter(ctx context.Context, stdout, stderr io.Writer, open func(io.Writer) int) int {
	env := paths.OSEnv{}
	socket, pane := env.Get(herdr.EnvSocket), env.Get(herdr.EnvPane)
	if env.Get(herdr.EnvMarker) != "1" || socket == "" || pane == "" {
		return open(stdout)
	}
	// A terminal stdout is never wrapped: Dispatch detects it by its file
	// descriptor to exec the action in place, and an exec never returns here.
	code, written := 0, 0
	if action.OutputIsTerminal(stdout) {
		code = open(stdout)
	} else {
		watch := &stdoutWatch{out: stdout}
		code, written = open(watch), watch.written
	}
	if code != 0 || written != 0 {
		return code
	}
	if err := call.ProveHerdrPane(ctx, socket, pane); err != nil {
		fmt.Fprintf(stderr, "pfm ls: the chat opened, but this picker’s Herdr pane %s stayed open: %v\n", pane, err)
		return code
	}
	if _, err := herdr.SeatRequest(ctx, socket, "pane.close", map[string]string{"pane_id": pane}); err != nil {
		fmt.Fprintf(stderr, "pfm ls: the chat opened, but this picker's Herdr pane %s stayed open: %v\n", pane, err)
	}
	return code
}
