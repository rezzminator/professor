package reload

import (
	"errors"
	"fmt"
	"io/fs"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func engineLive(proc Process, panePID int, engine pfmengine.ID, claudeBinary, codexBinary string) (bool, error) {
	if proc == nil {
		return false, errors.New("process reader is unavailable")
	}
	if panePID <= 0 {
		return false, errors.New("pane process id is unavailable")
	}
	pids, err := proc.PIDs()
	if err != nil {
		return false, err
	}
	matcher, err := gather.MatcherFor(engine)
	if err != nil {
		return false, err
	}
	binary := claudeBinary
	if engine == pfmengine.Codex {
		binary = codexBinary
	}
	// A process whose command line cannot be read (on macOS kern.procargs2
	// answers EINVAL for launchd, other users' processes and zombies) is
	// skipped, never fatal: the pane's own engine is our own process and
	// always readable. The skips are counted so a miss says why.
	skipped, firstSkippedPID := 0, 0
	var firstSkippedErr error
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if skipped == 0 {
				firstSkippedPID, firstSkippedErr = pid, err
			}
			skipped++
			continue
		}
		if !matcher.IsCommand(argv, binary) {
			continue
		}
		current := pid
		for depth := 0; depth <= 4; depth++ {
			if current == panePID {
				return true, nil
			}
			stat, statErr := proc.Stat(current)
			if statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					break
				}
				return false, fmt.Errorf("read process %d ancestry: %w", current, statErr)
			}
			if stat.ParentPID <= 1 || stat.ParentPID == current {
				break
			}
			current = stat.ParentPID
		}
	}
	if skipped > 0 {
		return false, fmt.Errorf(
			"no %s process traced to pane pid %d; %d process(es) unreadable and skipped, first pid %d: %w",
			engineLabel(engine),
			panePID,
			skipped,
			firstSkippedPID,
			firstSkippedErr,
		)
	}
	return false, nil
}
