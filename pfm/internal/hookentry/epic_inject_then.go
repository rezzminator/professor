package hookentry

import (
	"context"
	"fmt"
	"io"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmreload "github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// reloadThenNotTyped marks a reload's follow-up the reborn chat receives
// through its first prompt instead of its input box.
const reloadThenNotTyped = "PFM RELOAD FOLLOW-UP NOT TYPED: this chat was started by pfm chat reload --then, " +
	"whose follow-up message below never reached this chat's input box. Carry it out as the user's instruction:\n"

// reloadThenClaimMargin is the time a claim needs left before the prompt
// hook's deadline: the claim removes the stored message, and a hook answered
// over at its deadline hands nothing out, so a later prompt claims it instead.
const reloadThenClaimMargin = time.Second

// reloadThenContext is the --then message a native reload of this chat's seat
// stored and could not type, claimed for this session; empty when none waits.
// A prompt command-intercept or exit-intercept blocks never claims it: a
// blocked prompt's context reaches no model.
func reloadThenContext(
	ctx context.Context,
	identity resolve.Identity,
	payload epicInjectPayload,
	stderr io.Writer,
) string {
	if identity.Backend != pfmcall.HerdrBackend || identity.SeatID == "" || interceptedPrompt(payload.Prompt) {
		return ""
	}
	if deadline, bounded := ctx.Deadline(); bounded && deadline.Sub(clock.Real.Now()) < reloadThenClaimMargin {
		fmt.Fprintf(
			stderr,
			"pfm internal epic-inject: too little time left to claim a reload --then message of seat %s; "+
				"a later prompt claims it\n",
			identity.SeatID,
		)
		return ""
	}
	session := payload.SessionID
	if session == "" {
		session = identity.ID
	}
	values, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal epic-inject: resolve the SID directory for a reload --then message: %v\n",
			err,
		)
		return ""
	}
	message, ok, err := pfmreload.ClaimThen(values.SIDDir, identity.SeatID, session)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal epic-inject: claim the reload --then message of seat %s: %v\n",
			identity.SeatID, err)
	}
	if !ok {
		return ""
	}
	return message
}
