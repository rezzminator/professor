package reload

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// RefreshRecipeRemoteControl reapplies current policy to a saved native recipe.
// Recipe identity, never the picker primary, selects account preferences. An
// unresolved identity disables only Remote Control, not the underlying chat.
func RefreshRecipeRemoteControl(
	run fleetdb.SeatRun,
	engine pfmengine.ID,
	machine pfmconfig.Config,
	name string,
	stderr io.Writer,
) fleetdb.SeatRun {
	if engine != pfmengine.Claude {
		return run
	}
	prefs := machine.Claude
	if len(machine.Accounts) != 0 {
		account, found := RecipeAccount(machine, engine, run.Env)
		if !found {
			return DisableRecipeRemoteControl(
				run,
				name,
				"recipe account is missing, ambiguous, or no longer configured",
				stderr,
			)
		}
		prefs = machine.EffectiveClaude(account)
	}
	run.Args = claudelaunch.RefreshRemoteControl(run.Args, prefs.RemoteControl, name)
	return run
}

// DisableRecipeRemoteControl makes a policy-read failure visible without
// preventing the chat from starting or retaining stale remote access.
func DisableRecipeRemoteControl(run fleetdb.SeatRun, name, reason string, stderr io.Writer) fleetdb.SeatRun {
	if name == "" {
		name = "<unnamed>"
	}
	fmt.Fprintf(stderr, "pfm WARN: chat %q: Remote Control disabled: %s\n", name, reason)
	obs.Logger(context.Background()).Warn("launch.remote-control.disabled", obs.FieldChat, name, obs.FieldErr, reason)
	run.Args = claudelaunch.RefreshRemoteControl(run.Args, false, name)
	return run
}
