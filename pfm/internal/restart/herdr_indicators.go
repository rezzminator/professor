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
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const internalCommand = "internal"

type staleIndicator struct {
	process
	image gather.FileID
}

// StaleHerdrIndicators reports this user's painters executing an image other than the installed pfm.
// A painter whose identity could not be read is never counted stale: its pid is
// left out and the error names it, returned beside the pids of every painter that was read.
func StaleHerdrIndicators(dependencies Deps) ([]int, error) {
	stale, unreadable, err := (&discoverer{deps: dependencies}).staleHerdrIndicators()
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(stale))
	for _, painter := range stale {
		pids = append(pids, painter.PID)
	}
	return pids, errors.Join(unreadable...)
}

// staleHerdrIndicators scans every painter of this user. A painter that cannot be judged is one
// entry of unreadable and costs only itself; err is a failure that stops the whole scan.
func (finder *discoverer) staleHerdrIndicators() (stale []staleIndicator, unreadable []error, err error) {
	table, err := finder.readTable()
	if err != nil {
		return nil, nil, fmt.Errorf("herdr indicator process check: %w", err)
	}
	if err := table.unreadableErr(); err != nil {
		return nil, nil, fmt.Errorf("herdr indicator process check: %w", err)
	}
	var installed gather.FileID
	haveInstalled := false
	for _, proc := range table.Processes {
		args, ok := finder.pfmArgs(proc.Argv)
		if !ok || !slices.Equal(args, []string{internalCommand, herdr.IndicatorVerb}) {
			continue
		}
		image, counted, err := finder.indicatorImage(proc)
		if err != nil {
			unreadable = append(unreadable, err)
			continue
		}
		if !counted {
			continue
		}
		if !haveInstalled {
			if installed, err = gather.FileIDOf(finder.deps.Executable); err != nil {
				return nil, nil, fmt.Errorf("herdr indicator installed binary %s: %w", finder.deps.Executable, err)
			}
			haveInstalled = true
		}
		if image != installed {
			stale = append(stale, staleIndicator{process: proc, image: image})
		}
	}
	return stale, unreadable, nil
}

// indicatorImage is the executable identity of one painter this install owns. counted is false for
// a painter that is another user's, another install's, or gone: never this install's to judge.
func (finder *discoverer) indicatorImage(proc process) (image gather.FileID, counted bool, err error) {
	state, err := finder.daemonIdentityState(proc)
	if err != nil {
		return gather.FileID{}, false, fmt.Errorf("herdr indicator pid %d: %w", proc.PID, err)
	}
	if state != daemonOwned {
		return gather.FileID{}, false, nil
	}
	owned, err := finder.ownedByInstall(proc)
	if err != nil {
		return gather.FileID{}, false, fmt.Errorf("herdr indicator: %w", err)
	}
	if !owned {
		return gather.FileID{}, false, nil // another install's painter: never this install's to replace
	}
	images, ok := finder.deps.Procs.(gather.ProcImage)
	if !ok {
		return gather.FileID{}, false, fmt.Errorf(
			"herdr indicator pid %d: executable identity reader unavailable", proc.PID)
	}
	image, err = images.Image(proc.PID)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		state, proofErr := finder.daemonIdentityState(proc)
		if proofErr == nil && state == daemonGone {
			return gather.FileID{}, false, nil
		}
		err = errors.Join(err, proofErr)
	}
	if err != nil {
		return gather.FileID{}, false, fmt.Errorf("herdr indicator pid %d executable identity: %w", proc.PID, err)
	}
	return image, true, nil
}

// ConvergeHerdrIndicators replaces stale painters, never their Herdr server or an absent painter;
// the bool reports whether it stopped and restarted any.
func ConvergeHerdrIndicators(ctx context.Context, dependencies Deps) (string, bool, error) {
	stale, unreadable, err := (&discoverer{deps: dependencies}).staleHerdrIndicators()
	if err != nil {
		return "", false, err
	}
	// An unreadable painter is left running and named, never read as current.
	note := ""
	if len(unreadable) > 0 {
		note = fmt.Sprintf("; could not read %d painter(s), left running: %s",
			len(unreadable), stepLine(errors.Join(unreadable...).Error()))
	}
	if len(stale) == 0 {
		if note != "" {
			return "Herdr indicator process enumeration found no stale painter among those it could read" + note,
				false, nil
		}
		return "Herdr indicator process enumeration confirms no stale painter", false, nil
	}
	stopper := dependencies.stopper(&dependencies.Install)
	// Prove every replacement can be addressed before stopping any painter.
	environments := map[string][]string{}
	for _, painter := range stale {
		if painter.Start == 0 {
			return "", false, fmt.Errorf("herdr indicator pid %d: start identity unavailable", painter.PID)
		}
		env, err := dependencies.Procs.Environ(painter.PID)
		if err != nil {
			return "", false, fmt.Errorf("herdr indicator pid %d environment: %w", painter.PID, err)
		}
		same, err := stopper.same(painter.process)
		if err != nil {
			return "", false, fmt.Errorf("herdr indicator pid %d environment identity: %w", painter.PID, err)
		}
		if !same {
			return "", false, fmt.Errorf(
				"herdr indicator pid %d: identity changed while reading environment",
				painter.PID,
			)
		}
		socket := resolve.ProcessEnvironment(env).HerdrSocket
		if !filepath.IsAbs(socket) {
			return "", false, fmt.Errorf(
				"herdr indicator pid %d: missing or nonabsolute HERDR_SOCKET_PATH %q",
				painter.PID,
				socket,
			)
		}
		values := make([]string, 0, len(env)+1)
		for key, value := range env {
			if key != paths.EnvInstallMark {
				values = append(values, key+"="+value)
			}
		}
		// The replacement is this install's launch whatever the old painter
		// carried (one from before marks existed carries none).
		values = append(values, paths.EnvInstallMark+"="+dependencies.Install.Mark)
		sort.Strings(values)
		environments[socket] = values
	}
	for _, painter := range stale {
		stopper.verify = func(proc process) (bool, error) {
			image, err := dependencies.Procs.(gather.ProcImage).Image(proc.PID)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
				return false, nil // exited (or a zombie) since the scan: already stopped
			}
			if err != nil {
				return false, fmt.Errorf("herdr indicator pid %d executable before signal: %w", proc.PID, err)
			}
			if image != painter.image {
				return false, fmt.Errorf("herdr indicator pid %d executable changed before signal", proc.PID)
			}
			return true, nil
		}
		if _, err := stopper.stop(ctx, painter.process); err != nil {
			return "", false, fmt.Errorf("stop stale Herdr indicator: %w", err)
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
			return "", false, fmt.Errorf("start Herdr indicator for socket %s: %w", socket, err)
		}
	}
	return fmt.Sprintf(
		"replaced %d stale Herdr indicator painter(s) on %d socket(s)%s",
		len(stale),
		len(sockets),
		note,
	), true, nil
}
