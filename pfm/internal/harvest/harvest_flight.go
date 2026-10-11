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
// answered, but neither cached nor scored. A failure the source owns is
// written to the negative cache before its flight leaves the table, and a
// caller finding no flight reads that cache under the same lock, so no caller
// can fall between the two and walk a source that has just failed.
func (h *Harvester) fetchShared(ctx context.Context, key, source string, options FetchOptions) Result {
	if h.hooks.entering != nil {
		h.hooks.entering(key)
	}
	for {
		h.flightMu.Lock()
		if flight, ok := h.flights[key]; ok {
			h.flightMu.Unlock()
			if h.hooks.joined != nil {
				h.hooks.joined(key)
			}
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
		if cached, ok := h.neg.get(key); ok { // a walk that failed after this caller's own cache read
			h.flightMu.Unlock()
			if options.SizeOnly {
				cached.Content = ""
			}
			return cached
		}
		flight := &fetchFlight{done: make(chan struct{})}
		h.flights[key] = flight
		h.flightMu.Unlock()
		whole := options // the flight shares the whole page: a size-only leader blanks only its own answer
		whole.SizeOnly = false
		result, abandoned, refused := h.walk(ctx, source, whole)
		owned := !abandoned && !refused // the source's own outcome: cached when it failed, and scored
		h.flightMu.Lock()
		if owned && result.Error != "" && !flight.overtaken {
			h.neg.put(key, result)
		}
		flight.result, flight.abandoned = result, abandoned
		close(flight.done)
		delete(h.flights, key)
		h.flightMu.Unlock()
		if h.hooks.left != nil {
			h.hooks.left(key)
		}
		if owned {
			h.recordStat(source, result) // scoreboard: every terminal outcome lands in stats.jsonl
		}
		if options.SizeOnly && result.Error == "" {
			result.Content = ""
		}
		return result
	}
}

// clearFailure forgets key's cached failure once a refresh has read its
// source, and keeps a walk of key still in flight — begun before that read —
// from caching its failure over it.
func (h *Harvester) clearFailure(key string) {
	h.flightMu.Lock()
	defer h.flightMu.Unlock()
	h.neg.drop(key)
	if flight, ok := h.flights[key]; ok {
		flight.overtaken = true
	}
}

// flightHooks are fetchShared's test seams, nil outside tests: entering runs
// as a caller reaches the flight table, joined once it has found a flight to
// wait on, left once a leader has removed its flight from the table.
type flightHooks struct {
	entering func(key string)
	joined   func(key string)
	left     func(key string)
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
