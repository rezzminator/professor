package harvest

import (
	"context"
	"sync/atomic"
)

// fetchShared joins the in-flight walk of key or leads one. A walk that failed
// once its leader's own context ended (an MCP client that gave up) is
// ABANDONED: its failure is the cancel's, not the source's, so it is neither
// handed to the callers joined on it — each one whose context is alive walks
// again — nor written to the negative cache or the source's scoreboard. A walk
// that succeeded is shared whatever became of its leader. A failure whose walk
// was refused for load (NoteRefusedForLoad) is the server's, not the source's:
// answered, but neither cached nor scored.
func (h *Harvester) fetchShared(ctx context.Context, key, source string, options FetchOptions) Result {
	for {
		h.flightMu.Lock()
		if flight, ok := h.flights[key]; ok {
			h.flightMu.Unlock()
			select {
			case <-flight.done:
				if flight.abandoned {
					if err := ctx.Err(); err != nil {
						return Result{Source: source, Error: err.Error()} // never lead a walk on a dead context
					}
					continue
				}
				result := flight.result
				if options.SizeOnly {
					result.Content = ""
				}
				return result
			case <-ctx.Done():
				return Result{Source: source, Error: ctx.Err().Error()}
			}
		}
		flight := &fetchFlight{done: make(chan struct{})}
		h.flights[key] = flight
		h.flightMu.Unlock()
		result, abandoned, refused := h.walk(ctx, source, options)
		h.flightMu.Lock()
		flight.result, flight.abandoned = result, abandoned
		close(flight.done)
		delete(h.flights, key)
		h.flightMu.Unlock()
		if !abandoned && !refused {
			if result.Error != "" {
				h.neg.put(key, result)
			}
			h.recordStat(source, result) // scoreboard: every terminal outcome lands in stats.jsonl
		}
		if options.SizeOnly && result.Error == "" {
			result.Content = ""
		}
		return result
	}
}

// walk runs one fetchUnshared under a fresh load-refusal probe. abandoned: it
// failed once ctx had ended, the cancel's failure. refused: it failed and a
// rung was refused for load on the way (NoteRefusedForLoad), the server's.
// Either one is not the source's outcome: never cached, never scored.
func (h *Harvester) walk(ctx context.Context, source string, options FetchOptions) (Result, bool, bool) {
	probe := new(atomic.Bool)
	result := h.fetchUnshared(context.WithValue(ctx, loadRefusalKey{}, probe), source, options)
	failed := result.Error != ""
	return result, failed && ctx.Err() != nil, failed && probe.Load()
}

// loadRefusalKey carries a walk's load-refusal probe (Harvester.walk).
type loadRefusalKey struct{}

// NoteRefusedForLoad records on the walk ctx belongs to that the server
// refused part of it for load — its converter pool or its browser render slots
// were full — rather than failing on the document. The adapter that knows the
// refusal's identity calls it (harvestmcp: harvestpy.ErrConverterBusy, a
// render slot that never freed); outside a walk it does nothing.
func NoteRefusedForLoad(ctx context.Context) {
	if probe, ok := ctx.Value(loadRefusalKey{}).(*atomic.Bool); ok {
		probe.Store(true)
	}
}
