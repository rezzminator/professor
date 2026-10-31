package hookentry

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// ChatServerBornNative is the door's answer for a chat born as a native Herdr
// seat in its own tab: the shim's cx (pfm.zsh) then attaches nothing.
const ChatServerBornNative = 3

// NewChat runs `pfm chat new` argv under ctx: the one birth door a cx placed
// in Herdr hands its chat to.
type NewChat func(ctx context.Context, args []string, stdout, stderr io.Writer) int

// ChatServer is the shim's door to the one chat-server creator. Its one
// placement (shim.cx) decides: tmux creates the chat's server for cx to
// attach; Herdr births the chat natively through newChat, the placement
// carried so it is never asked twice. The arguments after run are cx's own.
func ChatServer(args []string, stdout, stderr io.Writer, runtime config.Runtime, newChat NewChat) int {
	if len(args) < 3 || !filepath.IsAbs(args[1]) || args[2] == "" {
		fmt.Fprintln(stderr, "usage: pfm internal chat-server <socket> <absolute-cwd> <run> [codex-argument…]")
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
	ctx := context.Background()
	choice, err := chat.BirthBackend(ctx, "shim.cx", runtime.Config.Chat.Backend)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal chat-server: %v\n", err)
		return 1
	}
	if choice.Backend == spawn.BackendHerdr {
		return chatServerNative(chat.WithPlacement(ctx, choice), args, stdout, stderr, newChat)
	}
	titles := runtime.Config.Tmux.Titles
	if runtime.ConfigError != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal chat-server: config unreadable, leaving the terminal title to the host: %v\n",
			runtime.ConfigError,
		)
		titles = config.TmuxTitles{Enabled: false}
	}
	creator := spawn.TmuxSpawner{TmuxDir: runtime.Paths.TmuxDir, Titles: &titles}
	if err := creator.NewSession(ctx, spawn.SessionSpec{
		Socket: args[0], Session: args[0], Window: pfmengine.MustLookup(engine).Short,
		CWD: args[1], Run: args[2],
	}); err != nil {
		fmt.Fprintf(stderr, "pfm internal chat-server: %v\n", err)
		return 1
	}
	return 0
}

// chatServerNative births cx's chat as a native Codex seat in a new, focused
// Herdr tab through chat new's own door, named by its socket token as a tmux cx
// chat's session is. A native seat carries one prompt and no other Codex
// argument, so anything else is refused before the birth.
func chatServerNative(ctx context.Context, args []string, stdout, stderr io.Writer, newChat NewChat) int {
	typed := args[3:]
	if len(typed) > 1 || len(typed) == 1 && (strings.HasPrefix(typed[0], "-") ||
		codexNonTUISubcommands[typed[0]] || codexSessionSubcommands[typed[0]]) {
		fmt.Fprintf(stderr, "pfm internal chat-server: a cx born in Herdr carries one prompt only, not %q; "+
			"type `codex …` in this pane, or start it with: pfm chat new {name} --engine codex\n",
			strings.Join(typed, " "))
		return 1
	}
	argv := []string{
		"new", "--engine=" + string(pfmengine.Codex), "--name", args[0], "--cwd", args[1], "--attach", "--",
	}
	if code := newChat(ctx, append(argv, typed...), stdout, stderr); code != 0 {
		return code
	}
	return ChatServerBornNative
}
