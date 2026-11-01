package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// InlineRunBudget is the largest pane command handed to tmux inline. tmux
// packs a command's whole argv into ONE client message, and a message is
// capped at 16 KiB (MAX_IMSGSIZE in tmux's compat/imsg.h); past that the
// client answers "command too long" and no pane is born. The budget sits at
// half the cap so the rest of the argv (-s, -n, -c, the socket path) always
// fits beside it.
const InlineRunBudget = 8 << 10

// launchScriptPrefix names a one-shot launch script. The leading dot keeps it
// out of every socket-directory reader, which matches engine socket prefixes.
const launchScriptPrefix = ".pfm-launch-"

// Launch is one pane command ready for a tmux client message: the run itself
// when it fits InlineRunBudget, else `/bin/sh '{script}'` over a one-shot
// script that removes itself before it becomes the run.
type Launch struct {
	Command string
	script  string
}

// PrepareLaunch shapes run for new-session or respawn-pane. An over-budget
// run is written to a 0700 script created O_EXCL in dir, a private directory
// PrepareLaunch secures first (paths.EnsureTmuxDir). A failed write is an
// error — never a silent inline fallback, which tmux would refuse anyway.
func PrepareLaunch(dir, run string) (Launch, error) {
	if len(run) <= InlineRunBudget {
		return Launch{Command: run}, nil
	}
	if err := paths.EnsureTmuxDir(dir); err != nil {
		return Launch{}, fmt.Errorf("prepare launch script: %w", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Launch{}, fmt.Errorf("name launch script in %s: %w", dir, err)
	}
	script := filepath.Join(dir, launchScriptPrefix+hex.EncodeToString(random[:]))
	file, err := os.OpenFile(script, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return Launch{}, fmt.Errorf("create launch script: %w", err)
	}
	_, writeErr := file.WriteString("#!/bin/sh\nrm -f -- \"$0\"\n" + launchBody(run) + "\n")
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		if removeErr := os.Remove(script); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove partial launch script: %w", removeErr))
		}
		return Launch{}, fmt.Errorf("write launch script %s: %w", script, err)
	}
	return Launch{
		Command: "/bin/sh " + cmdparse.Quote(script),
		script:  script,
	}, nil
}

// Discard removes a script no pane ran, after tmux refused the launch. An
// inline launch, or a script the pane already consumed, is nothing to remove.
func (launch Launch) Discard() error {
	if launch.script == "" {
		return nil
	}
	if err := os.Remove(launch.script); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove unlaunched script %s: %w", launch.script, err)
	}
	return nil
}

// launchBody makes the pane process the run's own program: a simple command
// is exec'd, so reap, name-sync and every /proc exe check see the engine,
// never a lingering shell. `exec` cannot take a NAME=value word, so a run
// that opens with assignments execs env to apply them. Anything else — a
// list, a pipeline, a group, a redirection, a substitution, a parse error —
// runs verbatim, exactly as the shell would have run it inline.
func launchBody(run string) string {
	call, ok := simpleCommand(run)
	if !ok {
		return run
	}
	if len(call.Assigns) > 0 {
		return "exec env " + run
	}
	return "exec " + run
}

// simpleCommand returns run's one simple command, parsed as the /bin/sh the
// script runs under reads it. It errs toward "not simple", which only costs
// the exec.
func simpleCommand(run string) (*syntax.CallExpr, bool) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(run), "")
	if err != nil || len(file.Stmts) != 1 {
		return nil, false
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	// A command below a leading comment or blank line would leave the exec
	// prefixed on that first line, an exec of nothing.
	if !ok || stmt.Pos().Line() != 1 || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return nil, false
	}
	syntax.Walk(stmt, func(node syntax.Node) bool {
		switch node.(type) {
		case *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst:
			ok = false
		}
		return ok
	})
	return call, ok
}
