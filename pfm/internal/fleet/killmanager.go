package fleet

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/kill"
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
	manager, err := kill.New(database, KillDependencies(runtime))
	if err != nil {
		_ = database.Close()
		fmt.Fprintf(stderr, "pfm: %v\n", err)
		return nil, nil, 1
	}
	return database, manager, 0
}
