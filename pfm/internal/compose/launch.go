package compose

import (
	"context"
	"errors"

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
	ctx := current.input.Context
	if ctx == nil {
		ctx = context.Background()
	}
	launch, err := current.input.Launches.LaunchFor(ctx, id)
	if errors.Is(err, fleetdb.ErrNoLaunch) {
		return fleetdb.Launch{}, false, false
	}
	if err != nil {
		return fleetdb.Launch{}, false, true
	}
	return launch, true, false
}

func (current *composer) applyLaunch(row *Row, id string) {
	launch, found, unread := current.launchFor(id)
	row.LaunchUnread = unread
	if found {
		row.Account = launch.Account
		row.C1H = launch.Cache1H
	}
}
