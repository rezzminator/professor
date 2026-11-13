package chat

import (
	"io"

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
