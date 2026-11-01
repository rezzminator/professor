package action

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Solo closes every competing tmux host and stray Claude process for a
// transcript while preserving the requested live socket and its ttys.
func (executor *Executor) Solo(
	ctx context.Context,
	id, keepSocket string,
	liveAgent bool,
	claudeBinaries ...string,
) (returnErr error) {
	trail := obs.NewTrail(ctx, "action", "multi")
	var kills soloKills
	// The cause names what this pass closed, never a fixed label: a pass that
	// found nothing says so, and an aborted pass reaches no state at all.
	defer func() {
		if returnErr == nil {
			trail.Reach("solo", kills.cause())
		}
		trail.End(returnErr)
	}()
	if id == "" {
		return nil
	}
	if err := os.MkdirAll(executor.sidDir, 0o700); err != nil {
		return fmt.Errorf("create solo lock directory: %w", err)
	}
	sum := sha256.Sum256([]byte(id))
	lockPath := filepath.Join(
		executor.sidDir,
		fmt.Sprintf(".solo-%x.lock", sum[:12]),
	)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open solo lock: %w", err)
	}
	defer func() {
		if err := lockFile.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close solo lock %s: %w", lockPath, err))
		}
	}()
	if err := flockContext(ctx, lockFile); err != nil {
		return err
	}
	defer func() {
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("unlock solo lock %s: %w", lockPath, err))
		}
	}()

	entries, err := os.ReadDir(executor.sidDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read solo crumbs: %w", err)
	}
	paneCrumbs := make(map[string]int)
	for _, entry := range entries {
		socket, paneID, ok := gather.ParseCrumbName(entry.Name())
		if ok && paneID != "" {
			paneCrumbs[socket]++
		}
	}
	for _, entry := range entries {
		socket, paneID, ok := gather.ParseCrumbName(entry.Name())
		engineID, known := pfmengine.FromSocket(socket)
		if !ok || !known || engineID != pfmengine.Claude ||
			(keepSocket != "" && socket == keepSocket) {
			continue
		}
		crumbPath := filepath.Join(executor.sidDir, entry.Name())
		content, err := os.ReadFile(crumbPath)
		if err != nil {
			continue
		}
		if !strings.HasSuffix(
			strings.TrimRight(string(content), "\r\n"),
			"/"+id+".jsonl",
		) {
			continue
		}
		panes, err := executor.tmux.ListPanes(ctx, socket)
		if err != nil {
			// A failed probe is not an empty server. The socket may still be
			// live but temporarily unreadable; leave its crumb so a later
			// pass does not mistake a working chat for an unowned one.
			fmt.Fprintf(
				executor.stderr,
				"cc: solo — pane probe on %s failed; leaving its crumb: %v\n",
				socket,
				err,
			)
			continue
		}
		if len(panes) == 0 {
			if err := removeFile(crumbPath); err != nil {
				return err
			}
			continue
		}
		if paneID != "" {
			if paneExists(panes, paneID) {
				fmt.Fprintf(
					executor.stderr,
					"cc: solo — closing duplicate of this chat on %s %s\n",
					socket,
					paneID,
				)
				kills.recordPane(ctx, "solo.kill_pane", executor.tmux.KillPane(ctx, socket, paneID), socket, paneID)
			}
		} else if paneCrumbs[socket] == 0 && len(panes) == 1 {
			fmt.Fprintf(
				executor.stderr,
				"cc: solo — closing duplicate of this chat on %s\n",
				socket,
			)
			kills.recordPane(ctx, "solo.kill_server", executor.tmux.KillServer(ctx, socket), socket, "")
		}
		if err := removeFile(crumbPath); err != nil {
			return err
		}
	}
	if liveAgent || executor.processes == nil {
		return nil
	}
	keepTTYs := make(map[string]struct{})
	keepTTYProbeFailed := false
	if keepSocket != "" {
		if panes, err := executor.tmux.ListPanes(ctx, keepSocket); err == nil {
			for _, pane := range panes {
				keepTTYs[strings.TrimPrefix(pane.TTY, "/dev/")] = struct{}{}
			}
		} else {
			keepTTYProbeFailed = true
			fmt.Fprintf(
				executor.stderr,
				"cc: solo — keep socket probe failed; skipping stray Claude cleanup: %v\n",
				err,
			)
		}
	}
	if keepTTYProbeFailed {
		return nil
	}
	processes, err := executor.processes.Processes(ctx)
	if err != nil {
		return fmt.Errorf("scan solo processes: %w", err)
	}
	for _, process := range processes {
		if !isStrayClaude(process, id, claudeBinaries...) {
			continue
		}
		if _, keep := keepTTYs[strings.TrimPrefix(process.TTY, "/dev/")]; keep {
			continue
		}
		if process.Unidentified != nil {
			obs.Logger(ctx).Warn(
				"solo.kill_process_failed", "pid", process.PID, "session_id", id,
				"err", process.Unidentified.Error(),
			)
			continue
		}
		// No site Check: the door withholds the signal unless the live argv
		// is the one matched here, so the stray match holds at the signal.
		outcome, err := executor.processes.Terminate(gather.Target{Identity: process.Identity()})
		switch {
		case err != nil:
			obs.Logger(ctx).Warn("solo.kill_process_failed", "pid", process.PID, "session_id", id, "err", err.Error())
		case outcome != gather.OutcomeSent:
			obs.Logger(ctx).Info(
				"solo.kill_process_skipped", "pid", process.PID, "session_id", id, "state", outcome.String(),
			)
		default:
			fmt.Fprintf(
				executor.stderr,
				"cc: solo — killed stray claude %d holding %s\n",
				process.PID,
				id,
			)
			kills.processes++
			obs.Logger(ctx).Info("solo.kill_process", "pid", process.PID, "session_id", id)
		}
	}
	return nil
}

// soloKills counts what one Solo pass actually closed.
type soloKills struct {
	panes     int
	processes int
}

// recordPane logs one duplicate-pane (or duplicate-server) close through the
// observability logger, not only the stderr notice, and counts it when it landed.
func (kills *soloKills) recordPane(ctx context.Context, message string, err error, socket, paneID string) {
	if err != nil {
		obs.Logger(ctx).Warn(message+"_failed", "socket", socket, "pane", paneID, "err", err.Error())
		return
	}
	kills.panes++
	obs.Logger(ctx).Info(message, "socket", socket, "pane", paneID)
}

// cause is the transition cause the trail records for the pass.
func (kills soloKills) cause() string {
	if kills.panes == 0 && kills.processes == 0 {
		return "no duplicates"
	}
	return fmt.Sprintf("closed %d duplicate panes, %d stray processes", kills.panes, kills.processes)
}

func flockContext(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(
			int(file.Fd()),
			syscall.LOCK_EX|syscall.LOCK_NB,
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock solo operation: %w", err)
		}
		timer := clock.Real.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C():
		}
	}
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove solo crumb %q: %w", path, err)
	}
	return nil
}

func paneExists(panes []ActionPane, paneID string) bool {
	for _, pane := range panes {
		if pane.PaneID == paneID {
			return true
		}
	}
	return false
}

// isStrayClaude reports whether process is a Claude chat holding session id:
// a Claude binary, not the background pty host, whose argv names id as a
// session the way Claude takes one (gather.ClaudeSessionIDs). An id that only
// appears inside another argument is no claim on the session.
func isStrayClaude(process Process, id string, claudeBinaries ...string) bool {
	if len(process.Argv) == 0 {
		return false
	}
	for _, argument := range process.Argv {
		if argument == "--bg-pty-host" {
			return false
		}
	}
	executable := process.Argv[0]
	executableName := filepath.Base(executable)
	configured := false
	for _, binary := range claudeBinaries {
		if binary != "" && executableName == filepath.Base(binary) {
			configured = true
			break
		}
	}
	descriptor := pfmengine.MustLookup(pfmengine.Claude)
	if executableName != descriptor.Binary && !configured &&
		!pathMatchesHints(executable, descriptor.BinaryPathHints) {
		return false
	}
	return slices.Contains(gather.ClaudeSessionIDs(process.Argv), id)
}

func pathMatchesHints(path string, hints []string) bool {
	for _, hint := range hints {
		if strings.Contains(path, hint) {
			return true
		}
	}
	return false
}
