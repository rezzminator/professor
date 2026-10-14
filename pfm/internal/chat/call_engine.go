package chat

import (
	"context"
	"errors"
	"fmt"
	"os"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

type codexSeatIdentifier struct{ runtime *config.Runtime }

func (identifier codexSeatIdentifier) Identify(ctx context.Context) (resolve.Identity, error) {
	identity, found := SeatIdentity(ctx, identifier.runtime)
	if !found {
		return resolve.Identity{}, resolve.ErrNoTmux
	}
	return identity, nil
}

// NewCallEngine constructs the shared caller and optionally allows an
// unsigned sender when the explicit CLI flag authorizes it.
func NewCallEngine(allowUnsigned bool, runtime *config.Runtime) (*pfmcall.Engine, error) {
	identifier := codexSeatIdentifier{runtime: runtime}
	dependencies := pfmcall.Dependencies{}
	dependencies.Options.AllowUnsigned = allowUnsigned
	if runtime != nil {
		dependencies.Spawner = pfmcall.CommandThenSpawner{ConfigPath: runtime.Config.Path}
		dependencies.ClaudeBinary = runtime.Config.Claude.Binary
		dependencies.CodexBinary = runtime.Config.Codex.Binary
		dependencies.OpenCodeBinary = runtime.Config.OpenCode.Binary
		dependencies.Recorder = sharedCommsRecorder(runtime.Paths)
		dependencies.WarningWriter = os.Stderr
		for _, account := range runtime.Config.Accounts {
			if emoji := runtime.Config.EmojiFor(account.ID); emoji != "" && emoji != "·" {
				dependencies.AccountEmojis = append(dependencies.AccountEmojis, emoji)
			}
		}
	}
	dependencies.Names = NameResolver{Runtime: runtime}
	dependencies.CodexSeat = identifier
	return pfmcall.New(dependencies)
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
