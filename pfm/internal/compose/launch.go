package compose

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

func (current *composer) launchFor(id string) (fleetdb.Launch, bool, bool) {
	if id == "" {
		return fleetdb.Launch{}, false, false
	}
	if current.input.LaunchError != nil {
		return fleetdb.Launch{}, false, true
	}
	if current.input.Launches == nil {
		return fleetdb.Launch{}, false, false
	}
	cache := &current.launches
	if !cache.read {
		ctx := current.input.Context
		if ctx == nil {
			ctx = context.Background()
		}
		cache.rows, cache.err = current.input.Launches.All(ctx)
		cache.read = true
	}
	if cache.err != nil {
		return fleetdb.Launch{}, false, true
	}
	launch, found := cache.rows[id]
	return launch, found, false
}

// launchCache is the launch table as one compose read it: one read for the
// whole fleet, never a query per row.
type launchCache struct {
	rows map[string]fleetdb.Launch
	err  error
	read bool
}

func (current *composer) applyLaunch(row *Row, id string) {
	launch, found, unread := current.launchFor(id)
	row.LaunchUnread = unread
	if found {
		row.Account = launch.Account
		row.C1H = launch.Cache1H
	}
}
