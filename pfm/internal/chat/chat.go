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
//   - verbs.go is the Verbs bundle the surfaces call.
//   - status.go and status_pane.go inspect a seat, reading its pane when its transcript cannot say.
//   - seat_inspector.go is the watch's inspector, holding one herdr-shield reader for each poll.
//   - list.go selects and presents the rows of `chat ls`.
//   - list_shield.go shares one herdr-shield reader between a listing's scan, Herdr presence probe and row inspections.
//   - last.go reads a chat's last answer; read.go its transcript entries; find.go searches transcripts.
//   - names.go resolves a chat's name; workbench_name.go reserves workbench names.
//   - open.go, open_herdr.go, restored.go, native_reboot.go and kill_server.go open, exit, kill and reboot chats.
//   - restored_recipe.go builds the resume recipe of a chat no seat holds.
//   - killoutcome.go words a kill's outcome; keys.go types the keys-send failures.
//   - birth.go carries the caller's identity; state.go records a verb's outcome.
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
