package hostcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/tmux"
)

const (
	checkTmuxServers = "tmux-servers"
	// tmuxProbeTimeout bounds one read of the servers: a wedged server never
	// answers, and the detector reports that as a failed look, not a hang.
	tmuxProbeTimeout = 10 * time.Second
	// reapWait is the bounded wait after SIGTERM before SIGKILL, and again
	// after SIGKILL before the reap reports the server still running.
	reapWait = 5 * time.Second
	reapPoll = 100 * time.Millisecond
)

// TmuxServerClient is the tmux view the tmux-servers detector and the reap
// read through: the chat probe's pane listing plus the server's own pid.
type TmuxServerClient interface {
	gather.TmuxClient
	ServerPID(ctx context.Context, socket string) (int, error)
}

func tmuxClient(tmuxDir string, injected TmuxServerClient) TmuxServerClient {
	if injected != nil {
		return injected
	}
	return gather.TmuxProbe{TmuxDir: tmuxDir}
}

// tmuxServers warns for each abandoned empty pfm tmux server: a server on a
// pfm chat socket (gather.IsChatSocketName) under pfm's socket directory that
// answers with no session and no pane. Such a server holds its socket, counts
// as an open chat and blocks "all chats closed"; its fix is the reap. A server
// with a live pane, or on a socket pfm does not own, is never a row. A probe
// that failed is a row saying so, never silence.
func tmuxServers(env Env) ([]Row, error) {
	if env.TmuxDir == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxProbeTimeout)
	defer cancel()
	probe, err := gather.ProbeTmuxReadOnly(ctx, env.TmuxDir, tmuxClient(env.TmuxDir, env.Tmux), env.Now)
	if err != nil {
		return []Row{{
			Warn,
			checkTmuxServers,
			env.TmuxDir,
			fmt.Sprintf("not checked: probing the pfm tmux servers failed: %v", err),
			"make tmux runnable on PATH and each server under " + env.TmuxDir +
				" answer tmux -L {socket} list-sessions, then rerun pfm doctor",
		}}, nil
	}
	empty := map[string]bool{}
	var rows []Row
	for _, socket := range probe.EmptyServers {
		empty[gather.EmptyServerWarning(socket)] = true
		rows = append(rows, Row{
			Warn,
			checkTmuxServers,
			filepath.Join(env.TmuxDir, socket),
			"abandoned empty tmux server: it runs with no session and no pane yet holds a pfm chat socket, " +
				"so it counts as an open chat",
			fmt.Sprintf(
				"%s (SIGTERM, then SIGKILL after %s; a server with a live pane is never touched)",
				gather.ReapCommand,
				reapWait,
			),
		})
	}
	for _, warning := range probe.ProbeWarnings {
		if empty[warning] {
			continue
		}
		rows = append(rows, Row{
			Warn,
			checkTmuxServers,
			env.TmuxDir,
			"tmux probe failed: " + warning,
			"run the check the problem names; " + gather.ReapCommand +
				" ends only a server that answers empty, so this one stays until it answers",
		})
	}
	return rows, nil
}

// TmuxReaper ends abandoned empty pfm tmux servers through the signal door:
// Door re-identifies a server right before each signal and holds the clock the
// waits run on, so a test drives a server that ignores SIGTERM over a fake
// process table without a real process.
type TmuxReaper struct {
	Client TmuxServerClient
	Door   gather.Door
	Wait   time.Duration
	Poll   time.Duration
}

// NewTmuxReaper is the real reaper over pfm's tmux socket directory. A server's
// pid comes from the live tmux server, so its identity is read from the
// kernel's own process table, never a configured proc root: a jail's fixture
// table cannot describe a pid a real server reports.
func NewTmuxReaper(tmuxDir string) TmuxReaper {
	return TmuxReaper{
		Client: tmuxClient(tmuxDir, nil),
		Door:   gather.NewDoor(gather.NewProcFS("")),
		Wait:   reapWait,
		Poll:   reapPoll,
	}
}

// RunTmuxReap is `pfm chat ls --reap`: it reaps under the runtime's socket
// directory (resolved defaults when runtime is nil) and exits 0 when every
// abandoned server ended or turned out to hold a pane again, 1 when one is
// still running, refused or unreadable included.
func RunTmuxReap(runtime *config.Runtime, stdout, stderr io.Writer) int {
	var resolved paths.Values
	if runtime != nil {
		resolved = runtime.Paths
	} else {
		var err error
		if resolved, err = config.ResolvePaths(); err != nil {
			fmt.Fprintf(stderr, "pfm chat ls --reap: resolve the tmux socket directory: %v\n", err)
			return 1
		}
	}
	tmuxDir := resolved.TmuxDir
	failed, err := ReapTmuxServers(context.Background(), tmuxDir, NewTmuxReaper(tmuxDir), stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat ls --reap: %v\n", err)
		return 1
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "pfm chat ls --reap: %d server(s) not ended\n", failed)
		return 1
	}
	return 0
}

// ReapTmuxServers ends every abandoned empty pfm tmux server under tmuxDir —
// SIGTERM, then SIGKILL once reaper.Wait passes — and writes one line per
// server saying what happened. Sockets pfm does not own are never probed; a
// server that holds a pane, or whose read failed, is left alone. It returns
// how many servers it could not end.
func ReapTmuxServers(ctx context.Context, tmuxDir string, reaper TmuxReaper, stdout io.Writer) (int, error) {
	probe, err := gather.ProbeTmuxReadOnly(ctx, tmuxDir, reaper.Client, reaper.Door.Clock.Now())
	if err != nil {
		return 0, fmt.Errorf("probe the pfm tmux servers under %s: %w", tmuxDir, err)
	}
	empty := map[string]bool{}
	for _, socket := range probe.EmptyServers {
		empty[gather.EmptyServerWarning(socket)] = true
	}
	for _, warning := range probe.ProbeWarnings {
		if !empty[warning] {
			fmt.Fprintf(stdout, "left alone, its read failed: %s\n", warning)
		}
	}
	if len(probe.EmptyServers) == 0 {
		fmt.Fprintf(stdout, "no abandoned empty pfm tmux server under %s\n", tmuxDir)
		return 0, nil
	}
	failed := 0
	for _, socket := range probe.EmptyServers {
		line, ok := reaper.reap(ctx, tmuxDir, socket)
		fmt.Fprintln(stdout, line)
		if !ok {
			failed++
		}
	}
	return failed, nil
}

func (reaper TmuxReaper) reap(ctx context.Context, tmuxDir, socket string) (string, bool) {
	// Re-read right before signalling: a server that gained a pane since the
	// probe is a live chat again.
	panes, err := reaper.Client.ListPanes(ctx, socket)
	switch {
	case err == nil:
		return fmt.Sprintf("%s: left alone, it now holds %d pane(s)", socket, len(panes)), true
	case errors.Is(err, gather.ErrServerGone):
		return socket + ": already gone", true
	case !errors.Is(err, gather.ErrServerEmpty):
		return fmt.Sprintf("%s: left alone, re-reading it failed: %v", socket, err), false
	}
	server, line, ok := reaper.server(ctx, filepath.Join(tmuxDir, socket), socket)
	if line != "" {
		return line, ok
	}
	stopped, err := reaper.Door.Stop(
		ctx,
		server,
		gather.Escalation{Term: reaper.Wait, Kill: reaper.Wait, Poll: reaper.Poll},
	)
	switch {
	case errors.Is(err, gather.ErrStillRunning):
		return fmt.Sprintf("%s (pid %d): still running %s after SIGKILL", socket, server.PID, reaper.Wait), false
	case err != nil:
		return fmt.Sprintf("%s (pid %d): not ended: %v", socket, server.PID, err), false
	}
	switch stopped {
	case gather.StoppedGone:
		return socket + ": already gone", true
	case gather.StoppedChanged:
		return fmt.Sprintf(
			"%s (pid %d): left alone, the process planned as %q changed before SIGTERM",
			socket, server.PID, server.Argv,
		), false
	case gather.StoppedOnTerm:
		return fmt.Sprintf("%s (pid %d): ended by SIGTERM", socket, server.PID), true
	case gather.StoppedKilled:
		return fmt.Sprintf(
			"%s (pid %d): ignored SIGTERM for %s, ended by SIGKILL",
			socket,
			server.PID,
			reaper.Wait,
		), true
	}
	return fmt.Sprintf("%s (pid %d): not ended, the stop ended as %d", socket, server.PID, stopped), false
}

// server captures the identity of the tmux server on socketPath for the door.
// The server names its own pid, the process table identifies that pid, and
// the server names its pid once more: the same answer on both sides of the
// read proves the identity is the server's, not a process that took a freed
// pid in between. The identity must also be tmux naming this socket. A line
// returned is the reap's verdict, the server not to be signalled.
func (reaper TmuxReaper) server(ctx context.Context, socketPath, socket string) (gather.Target, string, bool) {
	pid, err := reaper.Client.ServerPID(ctx, socket)
	if errors.Is(err, gather.ErrServerGone) {
		return gather.Target{}, socket + ": already gone", true
	}
	if err != nil {
		return gather.Target{}, fmt.Sprintf("%s: not ended, could not read its pid: %v", socket, err), false
	}
	identity, err := gather.IdentifyProcess(reaper.Door.Procs, pid)
	if gather.IsGone(err) {
		return gather.Target{}, socket + ": already gone", true
	}
	if err != nil {
		return gather.Target{}, fmt.Sprintf(
			"%s (pid %d): not ended, could not identify it: %v",
			socket,
			pid,
			err,
		), false
	}
	again, err := reaper.Client.ServerPID(ctx, socket)
	if errors.Is(err, gather.ErrServerGone) {
		return gather.Target{}, socket + ": already gone", true
	}
	if err != nil {
		return gather.Target{}, fmt.Sprintf(
			"%s (pid %d): not ended, could not re-read its pid: %v",
			socket,
			pid,
			err,
		), false
	}
	if again != pid {
		return gather.Target{}, fmt.Sprintf("%s: left alone, it answered pid %d then %d", socket, pid, again), false
	}
	check := namesServer(socketPath)
	if holds, _ := check(identity); !holds {
		return gather.Target{}, fmt.Sprintf(
			"%s (pid %d): left alone, its command line %q is not tmux naming %s",
			socket, pid, identity.Argv, socketPath,
		), false
	}
	return gather.Target{Identity: identity, Check: check}, "", true
}

// namesServer is the reap's own match on a server's identity: it runs tmux,
// and the command line it kept from the client that started it names the
// server at socketPath — by its -S path, or, with no -S, by an -L label that
// is the socket's name (tmux places a label in the socket directory).
func namesServer(socketPath string) gather.Check {
	return func(live gather.Identity) (bool, error) {
		label, named, isTmux := tmux.NamedServer(live.Argv)
		switch {
		case !isTmux:
			return false, nil
		case named != "":
			return paths.PhysicalPath(named) == paths.PhysicalPath(socketPath), nil
		}
		return label == filepath.Base(socketPath), nil
	}
}
