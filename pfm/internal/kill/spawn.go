package kill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"hostops/pfm/internal/deps"
)

// CommandSpawner starts the binary's killed finisher under a new session.
type CommandSpawner struct {
	Executable string
	Setsid     string
	Nohup      string
	ConfigPath string
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
	arguments := append(prefixArgs, executable)
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
	var command *exec.Cmd
	if forked {
		command = exec.CommandContext(ctx, launcher, arguments...)
	} else {
		// The POSIX floor has no `setsid -f`; start it asynchronously and
		// release the process handle so the finisher outlives this caller —
		// under nohup the launched process IS the finisher, so waiting on it
		// would block until the finisher itself completes.
		command = exec.Command(launcher, arguments...)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device for kill finisher: %w", err)
	}
	defer func() {
		if err := null.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close null device for kill finisher: %w", err))
		}
	}()
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	if !forked {
		if err := command.Start(); err != nil {
			return fmt.Errorf("start detached kill finisher with nohup: %w", err)
		}
		if err := command.Process.Release(); err != nil {
			return fmt.Errorf("release detached kill finisher: %w", err)
		}
		return nil
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("start detached kill finisher with setsid: %w", err)
	}
	return nil
}
