package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// HerdrPlaces scans the fleet the way Herdr placement files it: the rows and
// workbenches the picker's rail folds, read-only, and from no working
// directory, so a chat files under the same place wherever the command that
// places it runs. A nil runtime loads the machine's config, as every door must
// scan the same fleet. A seat or workbench the scan could not read is one of
// the fleet's Faults, never a chat that is not there.
func HerdrPlaces(ctx context.Context, runtime *pfmconfig.Runtime) (result *place.Fleet, returnErr error) {
	loaded, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		return nil, fmt.Errorf("herdr places: load runtime: %w", err)
	}
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		return nil, fmt.Errorf("herdr places: open fleet database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("herdr places: close fleet database: %w", err))
		}
	}()
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.DefaultView, ReadOnly: true, Workbenches: true, NoCurrentDir: true, Runtime: &loaded,
	}, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("herdr places: scan fleet: %w", err)
	}
	faults := slices.Clone(scan.Errors)
	for _, walk := range scan.Env.WorkbenchErrors {
		faults = append(faults, walk.Error())
	}
	return &place.Fleet{
		Rows:        scan.Output.Rows,
		ProjectDirs: scan.Output.ProjectDirs,
		Home:        scan.Env.Paths.Home,
		Benches:     scan.Env.Workbenches,
		Faults:      faults,
	}, nil
}

// herdrPlacesFor is HerdrPlaces on runtime, the seam a birth scans through.
func herdrPlacesFor(runtime *pfmconfig.Runtime) func(context.Context) (*place.Fleet, error) {
	return func(ctx context.Context) (*place.Fleet, error) { return HerdrPlaces(ctx, runtime) }
}

// HerdrPlacesEntry is `pfm internal herdr-places`, the startup hook of pfm's
// Herdr plugin: once a Herdr server starts, every live chat on it (the server
// HERDR_SOCKET_PATH names) moves into its place's workspace. With no Herdr
// socket it does nothing.
func HerdrPlacesEntry(stderr io.Writer, env paths.Env, runtime pfmconfig.Runtime) int {
	socket := env.Get(herdr.EnvSocket)
	if socket == "" {
		return 0
	}
	ctx := context.Background()
	places, err := HerdrPlaces(ctx, &runtime)
	if err == nil {
		err = spawn.ReconcileHerdr(ctx, runtime.Paths, socket, *places, "")
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal herdr-places: %v\n", err)
		return 1
	}
	return 0
}
