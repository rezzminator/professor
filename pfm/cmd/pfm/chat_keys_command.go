package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

const chatKeysUsage = "usage: pfm chat keys [--delay ms] [--literal] [--capture] <target> <key>..."

func runChatKeys(args []string, stdout, stderr io.Writer, clk clock.Clock, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("chat keys", chatKeysUsage, stderr)
	delay := flags.Duration(
		"delay", pfmchat.KeysDefaultDelay, "pause between keys (e.g. 250ms)",
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
	target := rest[0]
	keys := pfmchat.KeysRequest{
		Keys: rest[1:], Literal: *literal, Delay: *delay, Capture: *capture, Clock: defaultClock(clk),
	}
	if err := keys.Validate(); err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 2
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
	door, seat, err := chatSeat(chat)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 1
	}
	sent, err := pfmchat.SendKeys(ctx, door, seat, keys)
	switch {
	case errors.Is(err, pfmchat.ErrKeyCapability):
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 2
	case errors.Is(err, pfmchat.ErrKeySend):
		fmt.Fprintf(stderr, "pfm chat keys: %s: %v\n", keys.Keys[sent.Count], err)
		return codeDeadChat
	case err != nil && !errors.Is(err, pfmchat.ErrKeyCapture):
		fmt.Fprintf(stderr, "pfm chat keys: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\t%d key(s)\t%s\n", chat.Name, sent.Count, strings.Join(keys.Keys, " "))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat keys: capture: %v\n", err)
		return codeDeadChat
	}
	fmt.Fprint(stdout, sent.Screen)
	return 0
}

// chatSeat is the call engine's door on a live chat's seat — the one address
// chat keys and chat capture both reach.
func chatSeat(chat headless.Chat) (*pfmcall.Engine, pfmcall.Target, error) {
	door, err := pfmchat.NewCallEngineWith(pfmchat.CallDependencies{})
	if err != nil {
		return nil, pfmcall.Target{}, err
	}
	seat, err := chatTarget(chat)
	return door, seat, err
}

// chatTarget is a live chat's seat address on either host.
func chatTarget(chat headless.Chat) (pfmcall.Target, error) {
	socketPath := chat.Socket
	if chat.Backend != pfmcall.HerdrBackend {
		var err error
		if socketPath, err = chatSocketPath(chat.Socket); err != nil {
			return pfmcall.Target{}, err
		}
	}
	return pfmcall.Target{Backend: chat.Backend, SocketPath: socketPath, Pane: pfmchat.PaneTarget(chat)}, nil
}

// chatPaneTarget picks the most specific tmux address a resolved chat carries.
func chatPaneTarget(pane, session, socket string) string {
	return cmp.Or(pane, session, socket)
}
