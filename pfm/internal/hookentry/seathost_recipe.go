package hookentry

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// refreshSeatRun is a seat's recorded recipe as this engine start runs it.
// Every start of a Herdr native seat (the first, a restore, a retry, a reload)
// passes here: the supervisor can outlive a preference change and restart
// without the restore door, so the pinned config is read again, Remote Control
// reapplied, the launch MCP file rewritten and a pruned Claude build replaced.
func refreshSeatRun(
	run fleetdb.SeatRun,
	seat fleetdb.Seat,
	runtime config.Runtime,
	stderr io.Writer,
) (fleetdb.SeatRun, error) {
	engine := pfmengine.ID(seat.Engine)
	machine := runtime.Config
	var policyErr error
	if engine == pfmengine.Claude && machine.Path != "" {
		machine, policyErr = config.Load(
			machine.Path,
			runtime.Paths.Home,
			runtime.Paths.Roots[pfmengine.Claude],
			runtime.Paths.FirstRoot(pfmengine.Codex),
		)
	}
	rendering := machine
	if policyErr != nil {
		rendering = runtime.Config
		run = reload.DisableRecipeRemoteControl(
			run,
			seat.Name,
			fmt.Sprintf("refresh seat launch preferences: %v", policyErr),
			stderr,
		)
	} else {
		run = reload.RefreshRecipeRemoteControl(run, engine, machine, seat.Name, stderr)
	}
	if engine != pfmengine.Claude {
		return run, nil
	}
	// A file that cannot be rewritten never blocks the start: Claude names a
	// missing one itself, and the seat's start failure carries its words.
	if err := claudelaunch.RefreshMCPFiles(run.Args, runtime.Paths.Home, rendering); err != nil {
		fmt.Fprintf(stderr, "pfm WARN: chat %q: launch MCP file not refreshed: %v\n", seat.Name, err)
		obs.Logger(context.Background()).Warn(
			"seat-host: launch MCP file not refreshed", obs.FieldChat, seat.Name, obs.FieldErr, err.Error(),
		)
	}
	var err error
	run.Binary, err = currentClaudeBinary(run.Binary, runtime.Paths.Home, machine.Claude.Binary)
	return run, err
}
