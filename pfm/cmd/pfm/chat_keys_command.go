package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"hostops/pfm/internal/chatkeys"
	"hostops/pfm/internal/inject"
)

const chatKeysUsage = "usage: pfm chat keys [--delay ms] [--literal] [--capture] <target> <key>..."

// chatKeysDefaultDelay spaces consecutive keystrokes. A TUI still painting a
// frame drops a key that lands in the same millisecond as the one before it —
// the failure that leaves a delivered prompt sitting unsent in the composer —
// so the default gap is wide enough for a redraw.
const chatKeysDefaultDelay = 120 * time.Millisecond

// chatKeysSettle lets the pane finish redrawing before --capture reads it.
const chatKeysSettle = 400 * time.Millisecond

func validKey(key string) bool { return chatkeys.Valid(key) }

func runChatKeys(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet("chat keys", chatKeysUsage, stderr)
	delay := flags.Duration(
		"delay", chatKeysDefaultDelay, "pause between keys (e.g. 250ms)",
	)
	literal := flags.Bool(
		"literal", false, "type the arguments as text instead of pressing them",
	)
	capture := flags.Bool(
		"capture", false, "print the pane after the keys land",
	)
	rest, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(rest) < 2 {
		flags.Usage()
		return 2
	}
	target, keys := rest[0], rest[1:]

	if !*literal {
		for _, key := range keys {
			if chatkeys.Valid(key) {
				continue
			}
			fmt.Fprintf(
				stderr,
				"pfm chat keys: %q is not a tmux key — tmux would type it as "+
					"text; pass --literal to mean that, or use one of: %s\n",
				key,
				chatkeys.Names(),
			)
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
	socketPath, err := chatSocketPath(chat.Socket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 1
	}
	pane := chatPaneTarget(chat.Pane, chat.Session, chat.Socket)

	tmux := inject.CommandTmux{}
	for index, key := range keys {
		if index > 0 && *delay > 0 {
			time.Sleep(*delay)
		}
		var err error
		if *literal {
			err = tmux.SendLiteral(ctx, socketPath, pane, key)
		} else {
			err = tmux.SendKey(ctx, socketPath, pane, key)
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
	time.Sleep(chatKeysSettle)
	captureText, err := tmux.Capture(ctx, socketPath, pane, false, 0)
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
	if pane != "" {
		return pane
	}
	if session != "" {
		return session
	}
	return socket
}
