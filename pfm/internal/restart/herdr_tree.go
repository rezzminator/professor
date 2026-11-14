package restart

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// herdrSessionEnv names the Herdr session a server runs: `herdr --session
// {name}` starts its server with it set, the default session's server runs
// without it, and a herdr command reads it to pick its session.
const herdrSessionEnv = "HERDR_SESSION"

// herdrTree is what one Herdr server runs: the server process and every
// process under it, read from the process table when the plan was built. Err
// says why the tree could not be read.
type herdrTree struct {
	processes []process
	err       error
}

// stopOutlivers proves every process the server ran gone once the server
// reads stopped: one that outlived it is stopped like any other process, and
// one that cannot be stopped or read fails the step, named.
func stopOutlivers(ctx context.Context, detail string, tree []process, stopper processStopper) (string, error) {
	var outlived []string
	var failures []error
	for _, proc := range tree {
		same, err := stopper.same(proc)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !same {
			continue
		}
		how, err := stopper.stop(ctx, proc)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		outlived = append(outlived, fmt.Sprintf("pid %d %s", proc.PID, how))
	}
	if len(failures) > 0 {
		return "", fmt.Errorf("%s, but %d of the %d process(es) it ran may still run: %w",
			detail, len(failures), len(tree), errors.Join(failures...))
	}
	if len(outlived) == 0 {
		return fmt.Sprintf("%s; all %d process(es) it ran are gone", detail, len(tree)), nil
	}
	return fmt.Sprintf("%s; %d of its %d process(es) outlived it, each stopped: %s",
		detail, len(outlived), len(tree), strings.Join(outlived, ", ")), nil
}

// herdrProcesses reads the server's tree from the process table: the `herdr
// server` process of binary running the session the restart's environment
// names (the default one when it names none) and everything under it, except
// the pids the restart spares (the front that launched it). Another session's
// server and what it runs are never in it.
func (finder *discoverer) herdrProcesses(binary string, table processTable, tableErr error) herdrTree {
	if tableErr != nil {
		return herdrTree{err: tableErr}
	}
	if err := table.unreadableErr(); err != nil {
		return herdrTree{err: err}
	}
	session := envValue(finder.deps.Env, herdrSessionEnv)
	inTree := map[int]bool{}
	for _, proc := range table.Processes {
		base := filepath.Base(proc.Argv[0])
		if len(proc.Argv) != 2 || proc.Argv[1] != herdrServerVerb || base != filepath.Base(binary) && base != "herdr" {
			continue
		}
		environment, err := finder.deps.Procs.Environ(proc.PID)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // exited since the table was read: nothing of it to stop
		case err != nil:
			return herdrTree{err: fmt.Errorf("read the session of herdr server pid %d: %w", proc.PID, err)}
		case environment[herdrSessionEnv] == session:
			inTree[proc.PID] = true
		}
	}
	if len(inTree) == 0 {
		return herdrTree{err: fmt.Errorf("no %s server process of %s in the process table (%d listed)",
			filepath.Base(binary), herdrSessionLabel(session), table.Listed)}
	}
	for grew := true; grew; {
		grew = false
		for _, proc := range table.Processes {
			if !inTree[proc.PID] && inTree[proc.Parent] && !slices.Contains(finder.deps.Spare, proc.PID) {
				inTree[proc.PID], grew = true, true
			}
		}
	}
	var tree herdrTree
	for _, proc := range table.Processes {
		if inTree[proc.PID] {
			tree.processes = append(tree.processes, proc)
		}
	}
	return tree
}

// envValue is name's value in environment, a list of NAME=value entries; ""
// when it is not set.
func envValue(environment []string, name string) string {
	for _, entry := range environment {
		if value, found := strings.CutPrefix(entry, name+"="); found {
			return value
		}
	}
	return ""
}

// herdrSessionLabel is how a report names a Herdr session: the default one has no name.
func herdrSessionLabel(session string) string {
	if session == "" {
		return "the default session"
	}
	return "session " + session
}
