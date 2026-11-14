package costsink

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// listenerFD is the descriptor the sink finds its listening socket on: the first of exec's
// ExtraFiles.
const listenerFD = 3

// ArmOptions describes the process a sink is armed for.
type ArmOptions struct {
	// Self is the pfm binary that runs `internal cost-sink`.
	Self string
	// PID is the chat process the sink serves and outlives by one drain: the launcher that execs
	// Claude keeps its pid, so it is the pid of the chat itself.
	PID int
	// Home resolves the sink's log file.
	Home string
	// Environ is the sink's environment; nil takes the caller's.
	Environ []string
	// Runner starts the sink; nil is obs.Runner(deps.RealRunner{}).
	Runner deps.Runner
}

// Armed is a running sink: the loopback port Claude's exporters are pointed at.
type Armed struct{ Port int }

// Arm binds 127.0.0.1:0 in this process and hands the listening socket to a detached
// `pfm internal cost-sink` child (own session, no controlling terminal), so the port is known —
// and cannot be taken by another process — before Claude starts. The child identifies the chat by
// opts.PID plus that process's start time read here through gather. It returns once the child has
// started; the launcher closes its copies of the socket, and the runner reaps the child once it exits.
func Arm(opts ArmOptions) (Armed, error) {
	if opts.PID <= 0 || opts.Self == "" {
		return Armed{}, fmt.Errorf(
			"cost sink: arm needs a pid and the pfm binary, got pid %d self %q",
			opts.PID,
			opts.Self,
		)
	}
	stat, err := gather.NewProcFS("").Stat(opts.PID)
	if err != nil {
		return Armed{}, fmt.Errorf("cost sink: read the start time of pid %d: %w", opts.PID, err)
	}
	logFile, err := openSinkLog(opts.Home)
	if err != nil {
		return Armed{}, err
	}
	defer func() { _ = logFile.Close() }() // the child holds its own copy; ours only closes
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Armed{}, fmt.Errorf("cost sink: listen on 127.0.0.1: %w", err)
	}
	defer func() { _ = listener.Close() }() // the child holds its own copy; ours only closes
	file, err := listener.(*net.TCPListener).File()
	if err != nil {
		return Armed{}, fmt.Errorf("cost sink: duplicate the listening socket: %w", err)
	}
	defer func() { _ = file.Close() }() // the child holds its own copy; ours only closes
	runner := opts.Runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	argv := []string{
		opts.Self, "internal", "cost-sink",
		"--pid", strconv.Itoa(opts.PID), "--start", strconv.FormatUint(stat.StartTime, 10),
	}
	child, err := runner.Start(context.Background(), argv, deps.StartOptions{
		Env: opts.Environ, Stderr: logFile, ExtraFiles: []*os.File{file}, Detach: true,
	})
	if err != nil {
		return Armed{}, fmt.Errorf("cost sink: start %s internal cost-sink: %w", opts.Self, err)
	}
	if err := child.Release(); err != nil {
		return Armed{}, fmt.Errorf("cost sink: release the sink process: %w", err)
	}
	return Armed{Port: listener.Addr().(*net.TCPAddr).Port}, nil
}

// openSinkLog opens (creating it and its directory) the file the sink and the launcher log to.
func openSinkLog(home string) (*os.File, error) {
	path := paths.LogRoot(paths.OSEnv{}, home).CostSink()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("cost sink: create %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cost sink: open %s: %w", path, err)
	}
	return file, nil
}
