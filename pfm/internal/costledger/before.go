package costledger

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// exitGrace is how long after a launch's exit a ledger row still belongs to that launch when the
// next launch's first transcript line comes sooner (a reload): a call's row trails its transcript
// line by up to a quarter second (measured over three live sessions), and a reload starts the next
// launch's first call seconds later.
const exitGrace = 2 * time.Second

// CostState is one `cost-state` line of a Claude transcript: Claude Code's own running total at
// the moment a launch ended, USD; At, that exit as the transcript places it (the zero time when it
// is not known); and Until, the first timestamped line after it, which the next launch wrote before
// its first call (the zero time when no stamped line follows: no launch came after it).
type CostState struct {
	USD   float64
	At    time.Time
	Until time.Time
}

// Anchors collects a transcript's CostStates while a reader walks its lines in file order. A
// reader that already decodes every line costs it nothing extra: Stamp is a string copy, the
// timestamp is parsed only for a line that is a cost-state.
type Anchors struct {
	states  []CostState
	stamp   string
	stamped bool // a timestamped line followed the last cost-state
	open    int  // states from this index on have no Until yet
}

// Stamp tells the collector the timestamp text of a line that carries one. The first readable
// stamp after a cost-state is that state's Until.
func (a *Anchors) Stamp(text string) {
	a.stamp = text
	a.stamped = true
	if a.open == len(a.states) {
		return
	}
	if until, ok := transcript.ParseTimestamp(text, time.UTC); ok {
		for index := a.open; index < len(a.states); index++ {
			a.states[index].Until = until
		}
		a.open = len(a.states)
	}
}

// State tells the collector a cost-state line with total usd was met, and the startTime and
// totalDuration (milliseconds, 0 when absent) it carries. At is the later of startTime +
// totalDuration and the last timestamped line before the cost-state: on a session's first launch
// the clock is the wall clock of the exit, but Claude Code keeps startTime across a resume while
// totalDuration counts no time between launches, so after a resume the clock lags the exit by
// those gaps and the stamp leads. Neither sees a main thread idle while background agents work:
// Until, the next launch's first line, bounds the launch instead.
func (a *Anchors) State(usd, startMillis, durationMillis float64) {
	state := CostState{USD: usd}
	if parsed, ok := transcript.ParseTimestamp(a.stamp, time.UTC); ok {
		state.At = parsed
	}
	if startMillis > 0 && durationMillis > 0 {
		if clock := time.UnixMilli(int64(startMillis + durationMillis)); clock.After(state.At) {
			state.At = clock
		}
	}
	a.states = append(a.states, state)
	a.stamped = false
}

// anchorsJSON is Anchors as a reader keeps it between processes.
type anchorsJSON struct {
	States  []CostState `json:"states"`
	Stamp   string      `json:"stamp"`
	Stamped bool        `json:"stamped"`
	Open    int         `json:"open"`
}

// MarshalJSON keeps the collector's whole state, so a reader that checkpoints
// its walk resumes it at the next line with nothing lost.
func (a Anchors) MarshalJSON() ([]byte, error) {
	return json.Marshal(anchorsJSON{States: a.states, Stamp: a.stamp, Stamped: a.stamped, Open: a.open})
}

// UnmarshalJSON restores what MarshalJSON kept, refusing an open index outside
// the states it names.
func (a *Anchors) UnmarshalJSON(raw []byte) error {
	var kept anchorsJSON
	if err := json.Unmarshal(raw, &kept); err != nil {
		return fmt.Errorf("decode cost-state anchors: %w", err)
	}
	if kept.Open < 0 || kept.Open > len(kept.States) {
		return fmt.Errorf("decode cost-state anchors: open index %d outside %d states", kept.Open, len(kept.States))
	}
	*a = Anchors{states: kept.States, stamp: kept.Stamp, stamped: kept.Stamped, open: kept.Open}
	return nil
}

// States is every cost-state met, in file order.
func (a *Anchors) States() []CostState { return a.states }

// Closed reports that no timestamped line follows the last cost-state: Claude Code's total at
// that state is the session's final figure, not a floor the chat has run past.
func (a *Anchors) Closed() bool { return len(a.states) != 0 && !a.stamped }

// BeforeLedger is, in micro-dollars, the spend Claude Code's totals hold that the ledger's rows do
// not: spend before the ledger began, in a launch without a sink, or in calls whose export never
// became a row. A cost-state is Claude Code's exact running total at one exit (it restores that
// total on resume), so the rows of the launches up to that exit, taken from it, leave what the
// ledger never saw; the largest such remainder over the cost-states is the answer, 0 when the
// ledger covers every exit. A row belongs to the launches up to an exit when its time is no later
// than the next launch's first line (Until), or than At plus the grace when that comes later; with
// no launch after the exit every row does. A row with no readable time counts as logged by every
// exit; a row whose cost never arrived counts nothing.
func BeforeLedger(rows []Row, states []CostState) int64 {
	type priced struct {
		at     time.Time
		micros int64
		timed  bool
	}
	var priceds []priced
	for index := range rows {
		if rows[index].CostUSDMicros == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, rows[index].TS)
		priceds = append(priceds, priced{at: at, micros: *rows[index].CostUSDMicros, timed: err == nil})
	}
	var before int64
	for _, state := range states {
		bounded := !state.Until.IsZero()
		cutoff := state.At.Add(exitGrace)
		if state.Until.After(cutoff) {
			cutoff = state.Until
		}
		var logged int64
		for _, row := range priceds {
			if !bounded || !row.timed || !row.at.After(cutoff) {
				logged += row.micros
			}
		}
		if remainder := int64(math.Round(state.USD*1e6)) - logged; remainder > before {
			before = remainder
		}
	}
	return before
}
