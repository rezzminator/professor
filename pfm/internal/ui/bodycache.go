package ui

// A sweep tick moves only the masthead's light, yet every tick used to lay the
// whole list out again: at ~30 ticks a second that was most of a sweep's CPU,
// all of it producing the bytes the last frame already had. The Chats body is
// therefore remembered against the one thing that can change it between two
// sweep ticks — the clock — and against a message epoch that every other
// message advances, so a key, a refresh, a resize or a sky tick can never be
// served a stale body.

// bodyFrameNS is the clock grain of a remembered body. It is the gauge's frame
// length, which every other time-driven cell of the list (the 500 ms marker
// pulse, the age labels, the heat colours) already divides, so nothing in a
// body moves faster than a bucket.
const bodyFrameNS = workFrameNS

// bodyKey names the inputs of one remembered body.
type bodyKey struct {
	epoch         uint64
	bucket        int64
	width, height int
}

// noteMessage gives the model a fresh body epoch for every message except the
// sweep tick, the only one that leaves the list as it found it. The epoch comes
// from a counter every copy of the model shares, so two copies that handled
// different messages from one parent can never agree on an epoch.
func (state *deckState) noteMessage(message any) {
	if state.agg == nil {
		return
	}
	if _, isSweep := message.(sweepTickMsg); !isSweep {
		state.agg.epochs++
		state.bodyEpoch = state.agg.epochs
	}
}

// chatsBody is renderChatsBody behind the body memo. A model with no deck
// cache (tests that build one by hand) renders every time.
func (model Model) chatsBody(width, height int) string {
	agg := model.deck.agg
	if agg == nil {
		return model.renderChatsBody(width, height)
	}
	key := bodyKey{epoch: model.deck.bodyEpoch, bucket: model.nowNS / bodyFrameNS, width: width, height: height}
	if agg.bodyValid && agg.bodyKey == key {
		return agg.bodyText
	}
	agg.bodyText, agg.bodyKey, agg.bodyValid = model.renderChatsBody(width, height), key, true
	return agg.bodyText
}
