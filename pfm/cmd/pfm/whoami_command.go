package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// runWhoami prints THIS chat's own tmux session name — its identity, and the
// address another chat injects to. The stdout contract is chat.sh's whoami
// (chat.sh:482-484): one bare session name and nothing else, so an existing
// caller can switch to this binary without reading differently. --json adds
// the engine identity for callers that want more than the handle.
func runWhoami(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(whoamiCommand, "usage: pfm whoami [--json | --label]", stderr)
	asJSON := flags.Bool(jsonFormat, false, "print the full identity as JSON")
	asLabel := flags.Bool("label", false, "print the chat label, falling back to its session")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || (*asJSON && *asLabel) {
		flags.Usage()
		return 2
	}
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm whoami: %v\n", err)
		return 1
	}
	ctx := context.Background()
	identity, err := identifier.Identify(ctx)
	if err != nil {
		seat, found := pfmchat.SeatIdentity(ctx, firstRuntime(runtimes))
		if !found {
			fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		identity = seat
	}
	if *asJSON {
		encoded, err := json.Marshal(identity)
		if err != nil {
			fmt.Fprintf(stderr, "pfm whoami: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", encoded)
		return 0
	}
	if *asLabel && identity.SocketPath != "" {
		target := identity.Pane
		if target == "" {
			target = identity.Session
		}
		capture, captureErr := (inject.TmuxInjector{}).Capture(
			ctx, identity.SocketPath, target, true, inject.FullScrollback,
		)
		if captureErr == nil {
			emojis := []string(nil)
			if len(runtimes) != 0 {
				for _, account := range runtimes[0].Config.Accounts {
					if emoji := runtimes[0].Config.EmojiFor(account.ID); emoji != "" && emoji != "·" {
						emojis = append(emojis, emoji)
					}
				}
			}
			if label := naming.BookmarkLabelFor(capture, emojis); label != "" {
				fmt.Fprintln(stdout, label)
				return 0
			}
		}
	}
	fmt.Fprintf(stdout, "%s\n", identity.Session)
	return 0
}
