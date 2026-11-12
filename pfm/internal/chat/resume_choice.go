package chat

import (
	"context"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// resumeChoiceKey marks an open whose account and cache a resume walk chose (WithResumeChoice).
type resumeChoiceKey struct{}

// WithResumeChoice marks ctx's open, when chosen, as the resume a picker walk chose: a native seat that
// is gone then opens under the walk's account and cache (resumeNativeUnder) instead of replaying its
// recorded recipe. Unchosen, ctx is returned as it is.
func WithResumeChoice(ctx context.Context, chosen bool) context.Context {
	if !chosen {
		return ctx
	}
	return context.WithValue(ctx, resumeChoiceKey{}, true)
}

// resumeChosen reports whether ctx's open carries a resume walk's choice.
func resumeChosen(ctx context.Context) bool { return ctx.Value(resumeChoiceKey{}) == true }

// resumeNativeUnder opens the chat of seat, a native seat that is gone, as a tmux chat's resume row
// opens: planned afresh under account (0 keeps the chat's own), primary and cache1H, on the placement
// the open decides, the seat retired once the chat is open.
func resumeNativeUnder(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
	account, primary int,
	cache1H bool,
	stdout, stderr io.Writer,
) int {
	row, err := nativeResumeRow(ctx, seat, runtime, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	if account != 0 {
		row.Account = account
	}
	row.C1H = cache1H
	return openRowReplacing(ctx, row, primary, cache1H, "", stdout, stderr, runtime, &seat)
}
