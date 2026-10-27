package fleet

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// OpenKillManager opens the fleet store and constructs its kill manager.
func OpenKillManager(stderr io.Writer, runtimes ...config.Runtime) (*store.Store, *kill.Manager, int) {
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm: config: %v\n", err)
		return nil, nil, 1
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm: %v\n", err)
		return nil, nil, 1
	}
	dependencies := KillDependencies(runtime)
	dependencies.ResolveTarget = func(ctx context.Context, identity resolve.Identity) (kill.Target, error) {
		rows, err := rowsForKill(ctx, database, stderr, &runtime)
		if err != nil {
			return kill.Target{}, fmt.Errorf("read caller target rows: %w", err)
		}
		return killTargetFromRows(rows, identity)
	}
	manager, err := kill.New(database, dependencies)
	if err != nil {
		_ = database.Close()
		fmt.Fprintf(stderr, "pfm: %v\n", err)
		return nil, nil, 1
	}
	return database, manager, 0
}

func killTargetFromRows(rows []compose.Row, identity resolve.Identity) (kill.Target, error) {
	var target kill.Target
	matches := 0
	socket := identity.SocketName
	if socket == "" {
		socket = filepath.Base(identity.SocketPath)
	}
	for index := range rows {
		row := &rows[index]
		if row.Backend == "herdr" || row.Killed || !row.Kind.IsAddressable() || row.PaneID != identity.Pane ||
			row.Socket != socket && row.Socket != identity.SocketPath {
			continue
		}
		if row.BackendError != "" {
			return kill.Target{}, fmt.Errorf(
				"read caller pane %s on %s: %s",
				identity.Pane,
				identity.SocketPath,
				row.BackendError,
			)
		}
		matches++
		target = kill.Target{ID: row.ID, Engine: compose.EngineForKind(row.Kind), DataPath: row.Path}
	}
	if matches != 1 {
		return kill.Target{}, fmt.Errorf(
			"caller pane %s on %s matched %d live fleet targets",
			identity.Pane,
			identity.SocketPath,
			matches,
		)
	}
	return target, nil
}
