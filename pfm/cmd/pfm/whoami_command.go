package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/resolve"
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

// codexSeatIdentifier adapts the fleet's thread-to-live-seat lookup to the
// injector's final sender-identity rung. It is deliberately separate from
// resolve.Whoami: tmux environment and process ancestry stay the first two
// rungs, because CODEX_THREAD_ID can be inherited by a process with a seat of
// its own.
type codexSeatIdentifier struct{ Runtime *commandRuntime }

func (identifier codexSeatIdentifier) Identify(ctx context.Context) (resolve.Identity, error) {
	identity, found := pfmchat.SeatIdentity(ctx, identifier.Runtime)
	if !found {
		return resolve.Identity{}, resolve.ErrNoTmux
	}
	return identity, nil
}

func newInjectEngine(runtimes ...commandRuntime) (*inject.Engine, error) {
	return newInjectEngineAllowingUnsigned(false, runtimes...)
}

// newInjectEngineAllowingUnsigned builds the same engine with the unsigned
// refusal lifted. Only `pfm chat inject --allow-unsigned` passes true: every
// other caller gets the refusal, because an unsigned message is one the
// recipient must not act on.
func newInjectEngineAllowingUnsigned(
	allowUnsigned bool,
	runtimes ...commandRuntime,
) (*inject.Engine, error) {
	identifier := codexSeatIdentifier{}
	dependencies := inject.Dependencies{}
	dependencies.Options.AllowUnsigned = allowUnsigned
	if len(runtimes) != 0 {
		identifier.Runtime = &runtimes[0]
		dependencies.Spawner = inject.CommandThenSpawner{
			ConfigPath: runtimes[0].Config.Path,
		}
		dependencies.ClaudeBinary = runtimes[0].Config.Claude.Binary
		dependencies.CodexBinary = runtimes[0].Config.Codex.Binary
		dependencies.OpenCodeBinary = runtimes[0].Config.OpenCode.Binary
		dependencies.Recorder = sharedCommsRecorder(runtimes[0].Paths)
		dependencies.WarningWriter = os.Stderr
		for _, account := range runtimes[0].Config.Accounts {
			if emoji := runtimes[0].Config.EmojiFor(account.ID); emoji != "" && emoji != "·" {
				dependencies.AccountEmojis = append(dependencies.AccountEmojis, emoji)
			}
		}
	}
	dependencies.Names = pfmchat.NameResolver{Runtime: identifier.Runtime}
	dependencies.CodexSeat = identifier
	return inject.New(dependencies)
}

func sharedCommsRecorder(values paths.Values) func(context.Context, fleetdb.CommsEvent) error {
	return func(ctx context.Context, event fleetdb.CommsEvent) error {
		state := fleetdb.OpenSharedState(ctx, values)
		recordErr := state.RecordComms(ctx, event)
		closeErr := state.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close shared state after comms event: %w", closeErr)
		}
		return errors.Join(recordErr, closeErr)
	}
}
