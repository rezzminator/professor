package hostcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
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
	return gather.TmuxProbe{TmuxTmpDir: filepath.Dir(tmuxDir)}
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

// TmuxReaper ends abandoned empty pfm tmux servers. Signal, Sleep and Now are
// injected so a test drives a server that ignores SIGTERM without a real
// process; Signal with signal 0 is the liveness probe, as kill(2) defines it.
type TmuxReaper struct {
	Client TmuxServerClient
	Signal func(pid int, signal syscall.Signal) error
	Sleep  func(ctx context.Context, duration time.Duration) error
	Now    func() time.Time
	Wait   time.Duration
	Poll   time.Duration
}

// NewTmuxReaper is the real reaper over pfm's tmux socket directory.
func NewTmuxReaper(tmuxDir string) TmuxReaper {
	return TmuxReaper{
		Client: tmuxClient(tmuxDir, nil),
		Signal: syscall.Kill,
		Sleep:  clock.Real.Sleep,
		Now:    clock.Real.Now,
		Wait:   reapWait,
		Poll:   reapPoll,
	}
}

// RunTmuxReap is `pfm chat ls --reap`: it reaps under the runtime's socket
// directory (resolved defaults when runtime is nil) and exits 0 when every
// abandoned server ended or was safely left alone, 1 otherwise.
func RunTmuxReap(runtime *config.Runtime, stdout, stderr io.Writer) int {
	var tmuxDir string
	if runtime != nil {
		tmuxDir = runtime.Paths.TmuxDir
	} else {
		resolved, err := config.ResolvePaths()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat ls --reap: resolve the tmux socket directory: %v\n", err)
			return 1
		}
		tmuxDir = resolved.TmuxDir
	}
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
	probe, err := gather.ProbeTmuxReadOnly(ctx, tmuxDir, reaper.Client, reaper.Now())
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
		line, ok := reaper.reap(ctx, socket)
		fmt.Fprintln(stdout, line)
		if !ok {
			failed++
		}
	}
	return failed, nil
}

func (reaper TmuxReaper) reap(ctx context.Context, socket string) (string, bool) {
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
	pid, err := reaper.Client.ServerPID(ctx, socket)
	if errors.Is(err, gather.ErrServerGone) {
		return socket + ": already gone", true
	}
	if err != nil {
		return fmt.Sprintf("%s: not ended, could not read its pid: %v", socket, err), false
	}
	if err := reaper.Signal(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Sprintf("%s (pid %d): not ended, SIGTERM failed: %v", socket, pid, err), false
	}
	if reaper.waitGone(ctx, socket, pid) {
		return fmt.Sprintf("%s (pid %d): ended by SIGTERM", socket, pid), true
	}
	if err := reaper.Signal(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Sprintf(
			"%s (pid %d): ignored SIGTERM for %s; SIGKILL failed: %v",
			socket, pid, reaper.Wait, err,
		), false
	}
	if reaper.waitGone(ctx, socket, pid) {
		return fmt.Sprintf("%s (pid %d): ignored SIGTERM for %s, ended by SIGKILL", socket, pid, reaper.Wait), true
	}
	return fmt.Sprintf("%s (pid %d): still running %s after SIGKILL", socket, pid, reaper.Wait), false
}

// waitGone polls until the server is gone or reaper.Wait passes. Gone is the
// pid answering ESRCH, or the socket answering no server — a server that died
// unreaped lingers as a zombie pid but stops answering its socket.
func (reaper TmuxReaper) waitGone(ctx context.Context, socket string, pid int) bool {
	deadline := reaper.Now().Add(reaper.Wait)
	for {
		if err := reaper.Signal(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := reaper.Client.ListPanes(probeCtx, socket)
		cancel()
		if errors.Is(err, gather.ErrServerGone) {
			return true
		}
		if !reaper.Now().Before(deadline) {
			return false
		}
		if err := reaper.Sleep(ctx, reaper.Poll); err != nil {
			// The caller gave up (its context ended): stop waiting, and the
			// reap reports the server as not ended.
			return false
		}
	}
}
