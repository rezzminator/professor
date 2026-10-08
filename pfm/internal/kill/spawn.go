package kill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// CommandSpawner starts the binary's killed finisher under a new session.
type CommandSpawner struct {
	Executable string
	Setsid     string
	Nohup      string
	ConfigPath string
	// LogDir is the SID dir, where the finisher's stdout and stderr append to
	// paths.SIDKillExitLogPrefix+socketName+".log" — reload's worker logs
	// live there too. Required: a detached finisher's errors and its reason
	// for typing no exit command are never thrown away.
	LogDir string
	// Runner is the deps.Runner seam Spawn launches the finisher through;
	// nil defaults to obs.Runner(deps.RealRunner{}).
	Runner deps.Runner
}

// Prepare proves the finisher's log opens before Kill writes the kill row: a
// full or unwritable SID dir then fails while nothing has changed yet.
func (spawner CommandSpawner) Prepare(args ExitArgs) error {
	log, err := spawner.openLog(args)
	if err != nil {
		return err
	}
	if err := log.Close(); err != nil {
		return fmt.Errorf("close kill finisher log %s: %w", log.Name(), err)
	}
	return nil
}

// openLog opens (creating) the finisher's append-only log in LogDir.
func (spawner CommandSpawner) openLog(args ExitArgs) (*os.File, error) {
	if spawner.LogDir == "" {
		return nil, errors.New("kill finisher log directory is unset")
	}
	if args.SocketName == "" || filepath.Base(args.SocketName) != args.SocketName {
		return nil, fmt.Errorf("kill finisher log: socket name %q is not a base name", args.SocketName)
	}
	if err := os.MkdirAll(spawner.LogDir, 0o700); err != nil {
		return nil, fmt.Errorf("create kill finisher log directory: %w", err)
	}
	logPath := filepath.Join(spawner.LogDir, paths.SIDKillExitLogPrefix+args.SocketName+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open kill finisher log: %w", err)
	}
	return log, nil
}

func (spawner CommandSpawner) Spawn(
	ctx context.Context,
	args ExitArgs,
) (returnErr error) {
	executable := spawner.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pfm executable: %w", err)
		}
	}
	launcher, prefixArgs, forked, err := deps.DetachLauncher(spawner.Setsid, spawner.Nohup)
	if err != nil {
		return fmt.Errorf("detach kill finisher: %w", err)
	}
	arguments := append([]string{}, prefixArgs...)
	arguments = append(arguments, executable)
	if spawner.ConfigPath != "" {
		arguments = append(arguments, "--config", spawner.ConfigPath)
	}
	arguments = append(arguments,
		"internal",
		"kill-exit",
		"--engine",
		string(args.Engine),
		"--id",
		args.ID,
		"--path",
		args.DataPath,
		"--socket",
		args.SocketPath,
		"--socket-name",
		args.SocketName,
		"--pane",
		args.PaneID,
	)
	log, err := spawner.openLog(args)
	if err != nil {
		return err
	}
	defer func() {
		if err := log.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close kill finisher log %s: %w", log.Name(), err))
		}
	}()
	runner := spawner.Runner
	if runner == nil {
		runner = obs.Runner(deps.RealRunner{})
	}
	// !forked (the POSIX floor with no `setsid -f`) starts asynchronously and
	// releases the process handle so the finisher outlives this caller —
	// under nohup the launched process IS the finisher, so waiting on it
	// would block until the finisher itself completes.
	process, err := runner.Start(ctx, append([]string{launcher}, arguments...), deps.StartOptions{
		Stdout: log,
		Stderr: log,
		Detach: !forked,
	})
	if err != nil {
		if !forked {
			return fmt.Errorf("start detached kill finisher with nohup: %w", err)
		}
		return fmt.Errorf("start detached kill finisher with setsid: %w", err)
	}
	if !forked {
		if err := process.Release(); err != nil {
			return fmt.Errorf("release detached kill finisher: %w", err)
		}
		return nil
	}
	if err := process.Wait(); err != nil {
		return fmt.Errorf("start detached kill finisher with setsid: %w", err)
	}
	return nil
}
