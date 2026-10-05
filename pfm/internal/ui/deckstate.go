package ui

import (
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// deckState is what the deck layer keeps beside the compose snapshot: the
// reader's home for path display and the first-seen clock of each chat that
// arrived while the picker was open.
type deckState struct {
	home     string
	arrivals map[string]int64
	// rev counts the messages that can change what the fleet passes below read
	// (a key, a paste, a refresh); agg memoises those passes against it. agg is
	// a pointer so every copy of the model shares one cache, and each entry
	// carries the rev it was built at, so a stale copy rebuilds instead of
	// serving old numbers.
	rev uint64
	agg *deckAgg
	// bodyEpoch counts every message but the sweep tick; the remembered list
	// body (bodycache.go) is served only within one epoch.
	bodyEpoch uint64
	// freshNS is the scan clock (Snapshot.NowNS) of the last snapshot the model
	// applied, and
	// waking is set when a key arrives after RefreshStaleAfter without one: the
	// refresh stream is waking at that same key, and the header says so at once
	// rather than leaving a long-idle fleet looking current until the scan lands.
	freshNS int64
	waking  bool
	// factsError is the snapshot's FactsError: why the rows' model, effort and
	// working state may be missing.
	factsError string
	// sweeping is true while the masthead's fast clock is running.
	sweeping bool
}

// deckAgg memoises the three passes over the whole fleet that a frame would
// otherwise repeat — the project census, the masthead's vitals and the tempo
// axis's bins. A frame is drawn for every tick of the sky and the clock while
// the data has not moved, so on a large fleet they are the frame's biggest cost.
type deckAgg struct {
	talliesRev uint64
	tallies    map[string]projectTally
	vitalsRev  uint64
	vitals     fleetVitals
	binsRev    uint64
	binsNowNS  int64
	binsCells  int
	bins       []tempoBin
	// bandFrom/bandTo name the palette colours bands was blended from.
	bandFrom, bandTo string
	bands            [mastheadBands]string
	// epochs numbers the body epochs the models sharing this cache have taken;
	// bodyText is the Chats body last rendered, at bodyKey (bodycache.go).
	epochs    uint64
	bodyKey   bodyKey
	bodyText  string
	bodyValid bool
}

// RefreshStaleAfter is how old the fleet on screen must be before a key counts
// as waking a picker that slept. The refresh stream imports it, so the header's
// "refreshing" and the scan it announces start from the same bound.
const RefreshStaleAfter = 8 * time.Second

// touch notes a key at nowNS: if the fleet on screen is stale, the scan the key
// is about to wake is already under way.
func (state *deckState) touch(nowNS int64) {
	if state.freshNS != 0 && nowNS-state.freshNS > int64(RefreshStaleAfter) {
		state.waking = true
	}
}

// refreshed notes that a snapshot landed.
func (state *deckState) refreshed(nowNS int64, factsError string) {
	state.freshNS = nowNS
	state.waking = false
	state.factsError = factsError
}

const (
	// arrivalGlowNS is how long a freshly arrived chat's row flares before it
	// settles into its normal colour.
	arrivalGlowNS = int64(2200 * time.Millisecond)
)

// noteArrivals stamps every chat that is in after but was not in before. The
// first snapshot is never compared (there is no before), so opening the picker
// does not flare the whole fleet.
func (state *deckState) noteArrivals(before, after []compose.Row, nowNS int64) bool {
	known := make(map[string]bool, len(before))
	for index := range before {
		known[compose.RowKey(before[index])] = true
	}
	arrived := false
	for index := range after {
		row := &after[index]
		key := compose.RowKey(*row)
		if known[key] || !hasRecency(*row) {
			continue
		}
		if state.arrivals == nil {
			state.arrivals = make(map[string]int64)
		}
		state.arrivals[key] = nowNS
		arrived = true
	}
	return arrived
}

// glow is how bright a row's arrival flare still is, 1 at the moment it lands
// and 0 once arrivalGlowNS has passed.
func (state *deckState) glow(row compose.Row, nowNS int64) float64 {
	landed, ok := state.arrivals[compose.RowKey(row)]
	if !ok {
		return 0
	}
	elapsed := nowNS - landed
	if elapsed < 0 || elapsed >= arrivalGlowNS {
		return 0
	}
	return 1 - float64(elapsed)/float64(arrivalGlowNS)
}
