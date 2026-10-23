package restart

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

const internalCommand = "internal"

type staleIndicator struct {
	process
	image gather.FileID
}

// StaleHerdrIndicators reports this user's painters executing an image other than the installed pfm.
func StaleHerdrIndicators(dependencies Deps) ([]int, error) {
	stale, err := (&discoverer{deps: dependencies}).staleHerdrIndicators()
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(stale))
	for _, painter := range stale {
		pids = append(pids, painter.PID)
	}
	return pids, nil
}

func (finder *discoverer) staleHerdrIndicators() ([]staleIndicator, error) {
	table, err := finder.readTable()
	if err != nil {
		return nil, fmt.Errorf("herdr indicator process check: %w", err)
	}
	if err := table.unreadableErr(); err != nil {
		return nil, fmt.Errorf("herdr indicator process check: %w", err)
	}
	var stale []staleIndicator
	for _, proc := range table.Processes {
		args, ok := finder.pfmArgs(proc.Argv)
		if !ok || !slices.Equal(args, []string{internalCommand, herdr.IndicatorVerb}) {
			continue
		}
		state, err := finder.daemonIdentityState(proc)
		if err != nil {
			return nil, fmt.Errorf("herdr indicator pid %d: %w", proc.PID, err)
		}
		if state != daemonOwned {
			continue
		}
		images, ok := finder.deps.Procs.(gather.ProcImage)
		if !ok {
			return nil, fmt.Errorf("herdr indicator pid %d: executable identity reader unavailable", proc.PID)
		}
		image, err := images.Image(proc.PID)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			state, proofErr := finder.daemonIdentityState(proc)
			if proofErr == nil && state == daemonGone {
				continue
			}
			err = errors.Join(err, proofErr)
		}
		if err != nil {
			return nil, fmt.Errorf("herdr indicator pid %d executable identity: %w", proc.PID, err)
		}
		installed, err := gather.FileIDOf(finder.deps.Executable)
		if err != nil {
			return nil, fmt.Errorf("herdr indicator installed binary %s: %w", finder.deps.Executable, err)
		}
		if image != installed {
			stale = append(stale, staleIndicator{process: proc, image: image})
		}
	}
	return stale, nil
}

// ConvergeHerdrIndicators replaces stale painters, never their Herdr server or an absent painter.
func ConvergeHerdrIndicators(ctx context.Context, dependencies Deps) (string, error) {
	stale, err := (&discoverer{deps: dependencies}).staleHerdrIndicators()
	if err != nil {
		return "", err
	}
	if len(stale) == 0 {
		return "Herdr indicator process enumeration confirms no stale painter", nil
	}
	stopper := processStopper{
		procs: dependencies.Procs, signal: dependencies.Signal,
		clock: dependencies.Clock, uid: dependencies.EffectiveUID,
	}
	// Prove every replacement can be addressed before stopping any painter.
	environments := map[string][]string{}
	for _, painter := range stale {
		if painter.Start == 0 {
			return "", fmt.Errorf("herdr indicator pid %d: start identity unavailable", painter.PID)
		}
		env, err := dependencies.Procs.Environ(painter.PID)
		if err != nil {
			return "", fmt.Errorf("herdr indicator pid %d environment: %w", painter.PID, err)
		}
		same, err := stopper.same(painter.process)
		if err != nil {
			return "", fmt.Errorf("herdr indicator pid %d environment identity: %w", painter.PID, err)
		}
		if !same {
			return "", fmt.Errorf("herdr indicator pid %d: identity changed while reading environment", painter.PID)
		}
		socket := env["HERDR_SOCKET_PATH"]
		if !filepath.IsAbs(socket) {
			return "", fmt.Errorf(
				"herdr indicator pid %d: missing or nonabsolute HERDR_SOCKET_PATH %q",
				painter.PID,
				socket,
			)
		}
		values := make([]string, 0, len(env))
		for key, value := range env {
			values = append(values, key+"="+value)
		}
		sort.Strings(values)
		environments[socket] = values
	}
	for _, painter := range stale {
		stopper.verify = func(proc process) (bool, error) {
			image, err := dependencies.Procs.(gather.ProcImage).Image(proc.PID)
			if err != nil {
				return false, fmt.Errorf("herdr indicator pid %d executable before signal: %w", proc.PID, err)
			}
			if image != painter.image {
				return false, fmt.Errorf("herdr indicator pid %d executable changed before signal", proc.PID)
			}
			return true, nil
		}
		if _, err := stopper.stop(ctx, painter.process); err != nil {
			return "", fmt.Errorf("stop stale Herdr indicator: %w", err)
		}
	}
	sockets := make([]string, 0, len(environments))
	for socket := range environments {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	for _, socket := range sockets {
		if _, err := dependencies.Runner.Start(
			ctx,
			[]string{dependencies.Executable, internalCommand, herdr.IndicatorVerb},
			deps.StartOptions{Detach: true, Env: environments[socket]},
		); err != nil {
			return "", fmt.Errorf("start Herdr indicator for socket %s: %w", socket, err)
		}
	}
	return fmt.Sprintf("replaced %d stale Herdr indicator painter(s) on %d socket(s)", len(stale), len(sockets)), nil
}
