package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/spawn"
)

// runInternalChatServer is `pfm internal chat-server <socket> <cwd> <run>`,
// the shim's door to the one chat-server creator (spawn.TmuxSpawner.NewSession):
// `cx` gets every door's options and a window named for the socket's engine.
// An unreadable config fails CLOSED on the title only — the chat still opens,
// the host keeps its title, and stderr says why.
func runInternalChatServer(args []string, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 3 || !filepath.IsAbs(args[1]) || args[2] == "" {
		fmt.Fprintln(stderr, "usage: pfm internal chat-server <socket> <absolute-cwd> <run>")
		return 2
	}
	engine, known := pfmengine.FromSocket(args[0])
	if _, err := runtime.Paths.SocketUnder(args[0]); err != nil || !known {
		fmt.Fprintf(
			stderr,
			"pfm internal chat-server: %q is not one bare socket name carrying an engine prefix\n",
			args[0],
		)
		return 2
	}
	titles := runtime.Config.Tmux.Titles
	if runtime.ConfigError != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal chat-server: config unreadable, leaving the terminal title to the host: %v\n",
			runtime.ConfigError,
		)
		titles = pfmconfig.TmuxTitles{Enabled: false}
	}
	creator := spawn.TmuxSpawner{TmuxDir: runtime.Paths.TmuxDir, Titles: &titles}
	if err := creator.NewSession(context.Background(), spawn.SessionSpec{
		Socket: args[0], Session: args[0], Window: pfmengine.MustLookup(engine).Short,
		CWD: args[1], Run: args[2],
	}); err != nil {
		fmt.Fprintf(stderr, "pfm internal chat-server: %v\n", err)
		return 1
	}
	return 0
}
