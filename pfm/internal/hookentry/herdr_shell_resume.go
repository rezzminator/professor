package hookentry

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// herdrShellResumePending is the shim's auto-open question: does Herdr resume
// this pane's agent itself?
const herdrShellResumePending = "resume-pending"

// resume-pending's two answers, one line on stdout.
const (
	resumePending    = "pending"
	resumeNotPending = "no"
)

// herdrShellResume is `pfm internal herdr-shell resume-pending`: it prints
// "pending" when the shell's Herdr pane is one Herdr restored and resumes its
// agent in (herdr.ResumePending), so the shim's auto-open picker yields to it,
// and "no" when it is not, outside Herdr, and when Herdr could not be asked,
// which also logs one WARN naming the pane and the error, so the picker opens
// as before. Every answer exits 0: the hook log records a non-zero exit as an
// error, and "no" is none.
func herdrShellResume(ctx context.Context, args []string, stdout, stderr io.Writer, env paths.Env) int {
	if len(args) != 0 {
		fmt.Fprintf(stderr, "%s %s: unexpected arguments %q\n", herdrShellCommand, herdrShellResumePending, args)
		return 2
	}
	caller := resolve.CallerEnvironmentFrom(env)
	socket, pane := caller.HerdrSocket, caller.HerdrPane
	if env.Get(herdr.EnvMarker) != "1" || socket == "" || pane == "" {
		fmt.Fprintln(stdout, resumeNotPending)
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, herdr.ReportDeadline)
	defer cancel()
	pending, err := herdr.ResumePending(ctx, env, socket, pane)
	if err != nil {
		obs.Logger(ctx).Warn("herdr restore: could not read whether Herdr resumes this pane's agent; the picker opens",
			"pane", pane, obs.FieldErr, err.Error())
	}
	if err == nil && pending {
		fmt.Fprintln(stdout, resumePending)
	} else {
		fmt.Fprintln(stdout, resumeNotPending)
	}
	return 0
}
