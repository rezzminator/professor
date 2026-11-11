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
	arguments := []string{executable}
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
	// The finisher outlives this caller in a session of its own; the runner
	// reaps it in the background, so Release only gives up the handle.
	process, err := runner.Start(ctx, arguments, deps.StartOptions{Stdout: log, Stderr: log, Detach: true})
	if err != nil {
		return fmt.Errorf("start detached kill finisher: %w", err)
	}
	if err := process.Release(); err != nil {
		return fmt.Errorf("release detached kill finisher: %w", err)
	}
	return nil
}
