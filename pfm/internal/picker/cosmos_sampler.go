package picker

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// commsVersioner is a comms reader that can say, without reading the ledger,
// whether it changed.
type commsVersioner interface {
	CommsVersion(context.Context) (fleetdb.CommsStamp, error)
}

// cosmosSampler is the cosmos tab's ledger read, every cosmosRefreshInterval.
type cosmosSampler struct {
	reader commsReader

	mutex  sync.Mutex
	have   bool
	stamp  fleetdb.CommsStamp
	events []fleetdb.CommsEvent
}

// Sample returns the newest events since sinceNS. With a reader that can stamp
// its changes, a ledger whose stamp has not moved since the last read is
// answered from that read, narrowed to the window as it has slid since (events
// are newest first, so the window only ever drops the tail); a moved stamp
// reads again, so a new event shows within one sample. The stamp is taken
// BEFORE the read: a commit that lands between the two moves the stamp again
// and is read on the next sample.
func (sampler *cosmosSampler) Sample(ctx context.Context, sinceNS int64) ([]fleetdb.CommsEvent, error) {
	versioner, gated := sampler.reader.(commsVersioner)
	var stamp fleetdb.CommsStamp
	if gated {
		var err error
		if stamp, err = versioner.CommsVersion(ctx); err != nil {
			return nil, fmt.Errorf("sample comms ledger: %w", err)
		}
		sampler.mutex.Lock()
		if sampler.have && sampler.stamp == stamp {
			events := windowSince(sampler.events, sinceNS)
			sampler.mutex.Unlock()
			return events, nil
		}
		sampler.mutex.Unlock()
	}
	events, err := sampler.reader.CommsSince(ctx, sinceNS, compose.CosmosEventCap)
	if err != nil {
		return nil, err
	}
	if gated {
		sampler.mutex.Lock()
		sampler.have, sampler.stamp, sampler.events = true, stamp, events
		sampler.mutex.Unlock()
	}
	return events, nil
}

// windowSince is a copy of the newest-first events that are not older than
// sinceNS.
func windowSince(events []fleetdb.CommsEvent, sinceNS int64) []fleetdb.CommsEvent {
	kept := len(events)
	for kept > 0 && events[kept-1].AtNS < sinceNS {
		kept--
	}
	return slices.Clone(events[:kept])
}
