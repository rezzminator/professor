package kill

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// killTrail opens the kill coordinator's state trail: a chat is `live`
// until the kill lands (spec § Middleware, `state`).
func killTrail(ctx context.Context) *obs.Trail { return obs.NewTrail(ctx, "kill", "live") }

// requestShape names how the kill was addressed — never the id or a path.
func requestShape(request Request) string {
	cause := "id"
	if request.Self {
		cause = "--self"
	}
	if request.Exit {
		cause += " --exit"
	}
	return cause
}

// killed closes a kill trail: `killed` for cause, or failed with err.
func killed(trail *obs.Trail, cause string, err error) {
	if err != nil {
		trail.End(err)
		return
	}
	trail.Reach("killed", cause)
}

// cleared closes a clear-kill trail: `killed` when the id was a fleet chat,
// `skipped` when it was not one, failed with err.
func cleared(trail *obs.Trail, found bool, err error) {
	switch {
	case err != nil:
		trail.End(err)
	case found:
		trail.Reach("killed", "prompt baseline recorded")
	default:
		trail.Reach("skipped", "not an indexed fleet chat")
	}
}
