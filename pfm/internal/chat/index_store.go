package chat

import (
	"context"
	"io"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// fleetIndex, when set, is the store the resolve, ls and status verbs read
// instead of the on-disk one (store.Open); its owner closes it.
var fleetIndex *store.Store

// openFleetIndex opens the store a verb reads (fleetIndex, else store.Open
// with warn) and returns the close to call when done: the owner's store is
// left open.
func openFleetIndex(warn io.Writer) (database *store.Store, closeIndex func() error, err error) {
	if fleetIndex != nil {
		return fleetIndex, func() error { return nil }, nil
	}
	database, err = store.Open(store.WithWarningWriter(warn))
	if err != nil {
		return nil, nil, err
	}
	return database, database.Close, nil
}

// scanFleetRows is ScanRows over an open store: the one ScanRows opens, or
// the one the Herdr indicator painter holds across its ticks.
func scanFleetRows(
	ctx context.Context, database *store.Store, warn io.Writer, runtime *pfmconfig.Runtime,
) (FleetScan, error) {
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.AllView, ReadOnly: true, Runtime: runtime,
	}, warn)
	if err != nil {
		return FleetScan{}, err
	}
	return FleetScan{Rows: scan.Output.Rows, Unreadable: scan.Live.UnreadableSockets}, nil
}
