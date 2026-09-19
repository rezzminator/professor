package chat

import (
	"context"
	"fmt"

	"hostops/pfm/internal/obs"
)

// liveState is the chat state door's "live" state, named once because every
// registered verb below either starts or ends there.
const liveState = "live"

// verbTransitions is the prior -> next pair each CLI verb walks through the
// chat state door (obs.Transition, kind "chat") when it runs to completion —
// read straight off cmd/pfm/chat_command.go's own calls, which this table
// replaces. A verb absent from it is not one RecordVerb knows how to shape.
var verbTransitions = map[string]struct{ from, to string }{
	"kill":   {liveState, "killed"},
	"end":    {liveState, "ended"},
	"name":   {liveState, "renamed"},
	"unkill": {"killed", liveState},
	"new":    {"absent", "registered"},
}

// RecordVerb walks the chat state door for one CLI verb's outcome: code == 0
// records the verb's registered prior -> next transition as succeeded, any
// other code records the SAME transition as failed, its error naming only
// the verb and the exit code — never a resolved chat's id, socket or name.
// A verb outside verbTransitions is a no-op, so an unregistered caller can
// never write a mis-shaped record.
func RecordVerb(ctx context.Context, verb string, code int) {
	pair, ok := verbTransitions[verb]
	if !ok {
		return
	}
	var err error
	if code != 0 {
		err = fmt.Errorf("chat %s exited %d", verb, code)
	}
	obs.Transition(ctx, "chat", pair.from, pair.to, "pfm chat "+verb)(err)
}
