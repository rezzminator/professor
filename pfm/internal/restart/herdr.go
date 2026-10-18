package restart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	// herdrStatusBound bounds one `herdr status --json`.
	herdrStatusBound = 5 * time.Second
	// herdrServerVerb is `herdr server`: the server process, and the verb
	// that starts and stops it.
	herdrServerVerb = "server"
)

// herdrStatus is the part of `herdr status --json` the restart reads.
type herdrStatus struct {
	Running bool
	Socket  string
}

// herdrServer drives one Herdr binary's server.
type herdrServer struct {
	serviceTools
	binary string
	logDir string
	env    []string
}

// status asks the binary for its server's state. A non-answer is an error.
func (server herdrServer) status(ctx context.Context) (herdrStatus, error) {
	bounded, cancel := context.WithTimeout(ctx, herdrStatusBound)
	defer cancel()
	result, err := server.runner.Run(bounded, []string{server.binary, "status", "--json"}, deps.RunOptions{
		Env: server.env, WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	switch {
	case bounded.Err() != nil:
		return herdrStatus{}, fmt.Errorf("herdr status --json: %w", bounded.Err())
	case err != nil:
		return herdrStatus{}, fmt.Errorf("herdr status --json: %w", err)
	case result.ExitCode != 0:
		return herdrStatus{}, fmt.Errorf("herdr status --json: exit %d: %s",
			result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var parsed struct {
		Server *struct {
			Running *bool  `json:"running"`
			Socket  string `json:"socket"`
		} `json:"server"`
	}
	if err := json.Unmarshal(result.Stdout, &parsed); err != nil {
		return herdrStatus{}, fmt.Errorf("herdr status --json: malformed status: %w", err)
	}
	if parsed.Server == nil || parsed.Server.Running == nil {
		return herdrStatus{}, errors.New("herdr status --json: no server.running in the answer")
	}
	return herdrStatus{Running: *parsed.Server.Running, Socket: parsed.Server.Socket}, nil
}

// stopped reports whether the server reads stopped: status says not running,
// or status cannot reach a server whose socket file is gone.
func (server herdrServer) stopped(ctx context.Context, socket string) (bool, error) {
	status, err := server.status(ctx)
	if err == nil {
		return !status.Running, nil
	}
	if socket != "" {
		if _, statErr := os.Stat(socket); errors.Is(statErr, fs.ErrNotExist) {
			return true, nil
		}
	}
	return false, err
}

// herdrTree is what one Herdr server runs: the server process and every
// process under it, read from the process table when the plan was built. Err
// says why the tree could not be read.
type herdrTree struct {
	processes []process
	err       error
}

func (server herdrServer) stop(
	ctx context.Context, socket string, tree herdrTree, stopper processStopper,
) (string, error) {
	result, err := server.runner.Run(
		ctx,
		[]string{server.binary, herdrServerVerb, "stop"},
		deps.RunOptions{Env: server.env},
	)
	if err != nil {
		return "", fmt.Errorf("herdr server stop: %w", err)
	}
	done, err := server.waitFor(ctx, serviceStopWithin, func() (bool, error) { return server.stopped(ctx, socket) })
	if err != nil {
		return "", err
	}
	if !done {
		return "", fmt.Errorf("herdr server still running %s after herdr server stop (exit %d: %s)",
			serviceStopWithin, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	detail := "server stopped; herdr status reads not running"
	if tree.err != nil {
		return "", fmt.Errorf("%s, but the processes it ran are unverified: %w", detail, tree.err)
	}
	return stopOutlivers(ctx, detail, tree.processes, stopper)
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

// herdrProcesses reads the server's tree from the process table: every
// `herdr server` process of binary and everything under it, except the pids
// the restart spares (the front that launched it).
func (finder *discoverer) herdrProcesses(binary string, table processTable, tableErr error) herdrTree {
	if tableErr != nil {
		return herdrTree{err: tableErr}
	}
	if err := table.unreadableErr(); err != nil {
		return herdrTree{err: err}
	}
	inTree := map[int]bool{}
	for _, proc := range table.Processes {
		base := filepath.Base(proc.Argv[0])
		if len(proc.Argv) == 2 && proc.Argv[1] == herdrServerVerb &&
			(base == filepath.Base(binary) || base == "herdr") {
			inTree[proc.PID] = true
		}
	}
	if len(inTree) == 0 {
		return herdrTree{err: fmt.Errorf("no %s server process in the process table (%d listed)",
			filepath.Base(binary), table.Listed)}
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

func (server herdrServer) start(ctx context.Context) (string, error) {
	log, logPath, err := openStartLog(server.logDir, "herdr-server.log")
	if err != nil {
		return "", err
	}
	defer func() { _ = log.Close() }() // the detached server holds its own copy
	process, err := server.runner.Start(ctx, []string{server.binary, herdrServerVerb}, deps.StartOptions{
		Detach: true, Env: server.env, Stdout: log, Stderr: log,
	})
	if err != nil {
		return "", fmt.Errorf("start herdr server: %w", err)
	}
	var last error
	up, err := server.waitFor(ctx, serviceStartWithin, func() (bool, error) {
		status, err := server.status(ctx)
		last = err
		return err == nil && status.Running, nil
	})
	if err != nil {
		return "", err
	}
	if !up {
		return "", fmt.Errorf("herdr server pid %d not running %s after start (last status: %v; log %s)",
			process.Pid(), serviceStartWithin, last, logPath)
	}
	return fmt.Sprintf("server pid %d up; herdr status reads running", process.Pid()), nil
}

// findHerdr is the Herdr family: the server the herdr binary reports, when
// one is running.
func (finder *discoverer) findHerdr(ctx context.Context, table processTable, tableErr error) {
	name := finder.deps.HerdrBinary
	if name == "" {
		name = "herdr"
	}
	binary, err := finder.deps.Runner.LookPath(name)
	if errors.Is(err, exec.ErrNotFound) {
		finder.found(FamilyHerdr, "herdr is not installed ("+name+" is not on PATH)")
		return
	}
	if err != nil {
		finder.failed(FamilyHerdr, fmt.Errorf("resolve %s: %w", name, err))
		return
	}
	server := herdrServer{
		serviceTools: serviceTools{runner: finder.deps.Runner, clock: finder.deps.Clock},
		binary:       binary, logDir: finder.deps.LogDir, env: finder.deps.Env,
	}
	status, err := server.status(ctx)
	if err != nil {
		finder.failed(FamilyHerdr, err)
		return
	}
	if !status.Running {
		finder.found(FamilyHerdr, binary+" status --json: no server running — nothing to stop or start")
		return
	}
	finder.found(FamilyHerdr, binary+" status --json: server running on "+status.Socket)
	tree := finder.herdrProcesses(binary, table, tableErr)
	foundBy := fmt.Sprintf("%s status --json; process table: %d process(es), the server and every one under it",
		binary, len(tree.processes))
	if tree.err != nil {
		foundBy = binary + " status --json; the processes it runs are unknown: " + tree.err.Error()
	}
	stopper := processStopper{procs: finder.deps.Procs, signal: finder.deps.Signal, clock: finder.deps.Clock}
	finder.plan.Units = append(finder.plan.Units, Unit{
		Family:  FamilyHerdr,
		Name:    "herdr server " + status.Socket,
		FoundBy: foundBy,
		StopHow: binary + " server stop; verified: herdr status reads not running, and every process it ran " +
			"is gone (one that outlives it: SIGTERM, then SIGKILL)",
		StartHow: binary + " server, detached; verified: herdr status reads running",
		Stop: func(ctx context.Context) (string, error) {
			return server.stop(ctx, status.Socket, tree, stopper)
		},
		Start: server.start,
	})
}

// openStartLog opens (creating it and its directory) the append-only log a
// process the restart starts detached writes to.
func openStartLog(dir, name string) (*os.File, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name)
	log, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", path, err)
	}
	return log, path, nil
}
