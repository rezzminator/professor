package chat

import (
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// stateUnknown is the listed state of a seat whose inspection failed: the listing could not look.
const stateUnknown = "unknown"

// stateLost is the liveness word of a Herdr seat whose pane is gone.
const stateLost = "lost"

// RowState is a listed row's liveness word, read from the row alone, never from its pane: what caller matching
// reads, at no inspection's cost.
//
// The killed-but-live arm is the honesty half of the kill fix: a kill that closed nothing still wrote its
// tombstone, so a row came back asserting BOTH things at once — killed:true beside state "idle" and kind
// "live-claude" — and no caller could tell a real kill from a de-listing. A contradiction is reported as a
// contradiction, never smoothed into one of its two halves.
func RowState(row compose.Row) string {
	if row.BackendError != "" {
		return "error"
	}
	if row.Backend == spawn.BackendHerdr && row.LostNote != "" && !row.Kind.IsAddressable() {
		return stateLost
	}
	switch {
	case row.Killed && row.Kind.IsAddressable():
		return "killed-but-live"
	case row.Kind == compose.Booting:
		// A booting chat HAS a socket and already answers call by name. Excluding it made ls report a chat
		// that exists as simply absent for its first minute.
		return "booting"
	case row.Kind.IsAddressable():
		return headless.StateIdle
	default:
		return "resumable"
	}
}

// ListedState is the state word a chat listing shows its reader at both ls doors: every row the CLI's live
// listing shows says what its inspection says ("lost" and "dead" included), "unknown" when that failed; a booting
// row says "booting" unless its inspection says more than idle (a booting pane reads idle before its engine draws
// a prompt, F6); a resumable or killed row, never on that listing, says its liveness word (RowState) uninspected.
func ListedState(row compose.Row, inspect func() (headless.Status, error)) string {
	word := RowState(row)
	if word == "resumable" || word == "killed-but-live" {
		return word
	}
	status, err := inspect()
	if word == "booting" && (err != nil || status.State == headless.StateIdle) {
		return word
	}
	if err != nil {
		return stateUnknown
	}
	return status.State
}
