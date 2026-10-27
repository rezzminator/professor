package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

const chatKeysUsage = "usage: pfm chat keys [--delay ms] [--literal] [--capture] <target> <key>..."

// chatKeysDefaultDelay spaces consecutive keystrokes. A TUI still painting a
// frame drops a key that lands in the same millisecond as the one before it —
// the failure that leaves a delivered prompt sitting unsent in the composer —
// so the default gap is wide enough for a redraw.
const chatKeysDefaultDelay = 120 * time.Millisecond

// chatKeysSettle lets the pane finish redrawing before --capture reads it.
const chatKeysSettle = 400 * time.Millisecond

func runChatKeys(args []string, stdout, stderr io.Writer, clk clock.Clock, runtimes ...commandRuntime) int {
	clk = defaultClock(clk)
	flags := cli.NewFlagSet("chat keys", chatKeysUsage, stderr)
	delay := flags.Duration(
		"delay", chatKeysDefaultDelay, "pause between keys (e.g. 250ms)",
	)
	literal := flags.Bool(
		"literal", false, "type the arguments as text instead of pressing them",
	)
	capture := flags.Bool(
		"capture", false, "print the pane after the keys land",
	)
	rest, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(rest) < 2 {
		flags.Usage()
		return 2
	}
	target, keys := rest[0], rest[1:]

	if !*literal {
		if err := pfmchat.ValidateKeyNames(keys); err != nil {
			fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
			return 2
		}
	}

	ctx := context.Background()
	chat, code := headlessTarget(ctx, target, stdout, stderr, false, runtimes...)
	if code != 0 {
		return code
	}
	if !chat.Live {
		fmt.Fprintf(stderr, "pfm chat keys: %q is not running\n", chat.Name)
		return codeDeadChat
	}
	transport, err := pfmcall.New(pfmcall.Dependencies{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 1
	}
	socketPath := chat.Socket
	if chat.Backend != pfmcall.HerdrBackend {
		socketPath, err = chatSocketPath(chat.Socket)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
			return 1
		}
	}
	seat := pfmcall.Target{Backend: chat.Backend, SocketPath: socketPath, Pane: pfmchat.PaneTarget(chat)}
	if !*literal {
		if err := pfmcall.ValidateKeyCapabilities(seat, keys); err != nil {
			fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
			return 2
		}
	}
	for index, key := range keys {
		if index > 0 && *delay > 0 {
			if err := clk.Sleep(ctx, *delay); err != nil {
				fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
				return 1
			}
		}
		var err error
		if *literal {
			err = transport.SendLiteral(ctx, seat, key)
		} else {
			err = transport.SendKey(ctx, seat, key)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat keys: %s: %v\n", key, err)
			return codeDeadChat
		}
	}
	fmt.Fprintf(stdout, "%s\t%d key(s)\t%s\n", chat.Name, len(keys), strings.Join(keys, " "))

	if !*capture {
		return 0
	}
	if err := clk.Sleep(ctx, chatKeysSettle); err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 1
	}
	captureText, err := transport.CaptureTarget(ctx, seat, 0)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: capture: %v\n", err)
		return codeDeadChat
	}
	fmt.Fprint(stdout, captureText)
	if !strings.HasSuffix(captureText, "\n") {
		fmt.Fprintln(stdout)
	}
	return 0
}

// chatPaneTarget picks the most specific tmux address a resolved chat carries.
func chatPaneTarget(pane, session, socket string) string {
	return cmp.Or(pane, session, socket)
}
