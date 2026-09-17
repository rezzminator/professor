package hookentry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
)

type tmuxTitleRenudger interface {
	ShowGlobalOption(ctx context.Context, socket, name string) (string, error)
	NudgeTitlesIdentity(ctx context.Context, socket string) error
}

// TmuxTitleRenudge re-emits each live tmux server's own terminal title.
func TmuxTitleRenudge(args []string, stderr io.Writer, runtime config.Runtime) int {
	probe := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(runtime.Paths.TmuxDir)}
	return tmuxTitleRenudgeWith(args, stderr, runtime, probe)
}

func tmuxTitleRenudgeWith(
	args []string,
	stderr io.Writer,
	runtime config.Runtime,
	probe tmuxTitleRenudger,
) int {
	flags := cli.NewFlagSet(
		"internal tmux-title-renudge",
		"usage: pfm internal tmux-title-renudge",
		stderr,
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}

	entries, err := os.ReadDir(runtime.Paths.TmuxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal tmux-title-renudge: read tmux directory %s: %v\n",
			runtime.Paths.TmuxDir,
			err,
		)
		return 1
	}

	ctx := context.Background()
	nudged := 0
	hostOwned := 0
	failed := false
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			fmt.Fprintf(
				stderr,
				"pfm internal tmux-title-renudge: inspect socket %s: %v\n",
				filepath.Join(runtime.Paths.TmuxDir, entry.Name()),
				infoErr,
			)
			failed = true
			continue
		}
		if info.Mode()&os.ModeSocket == 0 {
			continue
		}

		socket := entry.Name()
		titles, readErr := probe.ShowGlobalOption(ctx, socket, "set-titles")
		if errors.Is(readErr, gather.ErrServerGone) {
			continue
		}
		if readErr != nil {
			fmt.Fprintf(stderr, "pfm internal tmux-title-renudge: read set-titles on socket %s: %v\n", socket, readErr)
			failed = true
			continue
		}
		if titles != "on" {
			hostOwned++
			continue
		}
		if nudgeErr := probe.NudgeTitlesIdentity(ctx, socket); errors.Is(nudgeErr, gather.ErrServerGone) {
			continue
		} else if nudgeErr != nil {
			fmt.Fprintf(stderr, "pfm internal tmux-title-renudge: nudge socket %s: %v\n", socket, nudgeErr)
			failed = true
			continue
		}
		nudged++
	}

	if failed {
		return 1
	}
	if nudged == 0 && hostOwned > 0 {
		fmt.Fprintf(
			stderr,
			"pfm internal tmux-title-renudge: nothing to nudge — all %d live servers have set-titles off (host-owned titles)\n",
			hostOwned,
		)
		return 3
	}
	return 0
}
