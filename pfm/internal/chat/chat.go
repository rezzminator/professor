// Package chat is the verb layer: one operation on one chat, reachable from
// the CLI, the MCP server, the picker and the daemon as a typed call — never
// as argv into package main. A verb takes a typed request and returns a typed
// result, or an error whose kind each surface maps onto its own contract: a
// *TargetError wrapping ErrUnknownChat when nothing answers to the target,
// ErrNoTranscript and ErrNoAnswer when the chat exists but has nothing to
// read, and any other error when a step could not run.
//
// Files, one line each:
//   - target.go resolves a target to a chat, and scans the fleet's rows.
//   - index_store.go opens the store those verbs, ls and status read, or the one a test hands in.
//   - verbs.go is the Verbs bundle the surfaces call.
//   - doors.go decides each outcome the CLI and the MCP door of a chat function both answer.
//   - liststate.go names the state word a listed chat shows at both ls doors.
//   - status.go and status_pane.go inspect a seat, or every row of a listing, reading its pane when its transcript cannot say.
//   - seat_inspector.go is the watch's inspector, carrying each transcript read from one poll to the next.
//   - list.go selects and presents the rows of `chat ls`.
//   - last.go reads a chat's last answer; read.go its transcript entries; find.go searches transcripts.
//   - names.go resolves a chat's name; workbench_name.go reserves workbench names.
//   - open.go, open_herdr.go, restored.go, native_reboot.go and kill_server.go open, exit, kill and reboot chats.
//   - restored_resume.go rebinds a dead native seat to the Herdr pane whose `claude --resume` resumes it.
//   - restored_recipe.go builds the resume recipe of a chat no seat holds.
//   - resume_choice.go opens a gone native seat's chat under the account and cache a picker walk chose.
//   - end_tmux.go ends a tmux chat's server, only while its pane is the server's sole pane, and clears its handles.
//   - killoutcome.go words a kill's outcome; keys.go types the keys-send failures.
//   - birth.go and birth_caller.go carry the caller's identity; state.go records a verb's outcome.
//   - branch_herdr.go births `pfm chat branch`'s fork as a native Herdr seat.
//   - call_engine.go and call_types.go build the call engine.
//   - loss_note.go reads a seat's durable loss note from its transcript.
//   - fleetshare.go shares one fleet scan between watches; seatprint.go fingerprints a seat for a watch.
//   - herdr_indicators.go, herdr_indicators_probe.go and herdr_indicator_config.go paint Herdr tab indicators.
//   - herdr_name.go and herdr_name_sync.go name Herdr tabs; herdr_places.go lists the Herdr places.
package chat

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/headless"
)

type resolvedSelfKey struct{}

// WithResolvedSelf binds an immutable, request-local caller seat. A shared MCP
// daemon uses it to resolve self without consulting process-global identity.
func WithResolvedSelf(ctx context.Context, self headless.Chat) context.Context {
	return context.WithValue(ctx, resolvedSelfKey{}, self)
}

// ScopedSelf returns the exact request-local caller seat, when one was bound.
func ScopedSelf(ctx context.Context) (headless.Chat, bool) {
	if ctx == nil {
		return headless.Chat{}, false
	}
	self, ok := ctx.Value(resolvedSelfKey{}).(headless.Chat)
	return self, ok
}
