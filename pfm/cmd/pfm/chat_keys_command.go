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

	keyed, err := pfmchat.PressKeys(context.Background(), firstRuntime(runtimes), target, keys)
	if !keyed.Landed {
		return reportChatRefusal("keys", err, target, stdout, stderr)
	}
	fmt.Fprintf(stdout, "%s\t%d key(s)\t%s\n", keyed.Chat.Name, keyed.Sent.Count, strings.Join(keys.Keys, " "))
	if err != nil {
		return reportChatRefusal("keys", err, target, stdout, stderr)
	}
	fmt.Fprint(stdout, keyed.Sent.Screen)
	return 0
}

// reportChatRefusal renders a chat function's failure at the CLI door: a target scan that could not look as
// reportTargetError, an unknown chat as its missing row (renderNoSuchChat), any other refusal (pfmchat.Refusal)
// as the verb's stderr line exiting its code; an error that is no refusal means a step could not run (exit 1).
func reportChatRefusal(verb string, err error, name string, stdout, stderr io.Writer) int {
	var target *pfmchat.TargetError
	var refusal pfmchat.Refusal
	switch {
	case errors.As(err, &target):
		return reportTargetError(err, name, stdout, stderr, false)
	case errors.As(err, &refusal) && refusal.Code == pfmcall.CodeUnknown:
		return renderNoSuchChat(name, stdout, stderr, false)
	case errors.As(err, &refusal) && refusal.Code == pfmcall.CodeAmbiguous:
		fmt.Fprintf(stderr, "pfm chat: %s\n", refusal.Message)
		return refusal.Code
	case errors.As(err, &refusal):
		fmt.Fprintf(stderr, "pfm chat %s: %s\n", verb, refusal.Message)
		return refusal.Code
	}
	fmt.Fprintf(stderr, "pfm chat %s: %v\n", verb, err)
	return 1
}

// chatPaneTarget picks the most specific tmux address a resolved chat carries.
func chatPaneTarget(pane, session, socket string) string {
	return cmp.Or(pane, session, socket)
}
