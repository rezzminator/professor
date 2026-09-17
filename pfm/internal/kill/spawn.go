package kill

import (
	"context"
	"errors"
	"fmt"
	"os"

	"hostops/pfm/internal/deps"
)

// CommandSpawner starts the binary's killed finisher under a new session.
type CommandSpawner struct {
	Executable string
	Setsid     string
	Nohup      string
	ConfigPath string
	// Runner is the deps.Runner seam Spawn launches the finisher through;
	// nil defaults to deps.RealRunner{}.
	Runner deps.Runner
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
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device for kill finisher: %w", err)
	}
	defer func() {
		if err := null.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close null device for kill finisher: %w", err))
		}
	}()
	runner := spawner.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	// !forked (the POSIX floor with no `setsid -f`) starts asynchronously and
	// releases the process handle so the finisher outlives this caller —
	// under nohup the launched process IS the finisher, so waiting on it
	// would block until the finisher itself completes.
	process, err := runner.Start(ctx, append([]string{launcher}, arguments...), deps.StartOptions{
		Stdout: null,
		Stderr: null,
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
