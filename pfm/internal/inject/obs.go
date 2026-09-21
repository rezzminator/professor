package inject

import (
	"context"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// outcome closes a coordinator's state trail (spec § Middleware, `state`)
// from its Result: an error ends it failed; a non-zero code is a refusal
// with the code as cause (never the message, which may quote the body);
// a typed delivery reaches `typed`. One record per coordinator call.
func outcome(trail *obs.Trail, result Result, err error) {
	switch {
	case err != nil:
		trail.End(err)
	case result.Code != 0:
		trail.Reach("refused", "code "+strconv.Itoa(result.Code))
	default:
		trail.Reach("typed", "delivered")
	}
}

// trail opens the state trail for one coordinator call.
func trail(ctx context.Context, coordinator string) *obs.Trail {
	return obs.NewTrail(ctx, coordinator, "requested")
}
