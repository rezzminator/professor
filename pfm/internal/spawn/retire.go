package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RetireHerdrSeat is the one retire path of a dead seat a revive replaced: its
// old pane (RetireHerdrPane), then its reload schedule files, keyed by its id,
// which the new seat does not reuse, then its row and recipe. A pane that
// stays is the returned warning; the chat already lives elsewhere.
func RetireHerdrSeat(ctx context.Context, values paths.Values, seat fleetdb.Seat) (warning string, returnErr error) {
	warning = RetireHerdrPane(ctx, seat)
	if err := herdr.RemoveReloadFiles(values.SIDDir, seat.SeatID); err != nil {
		return warning, fmt.Errorf("retire replaced native seat %s reload files: %w", seat.SeatID, err)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	if err := state.DeleteSeat(ctx, seat.SeatID); err != nil {
		return warning, fmt.Errorf("retire replaced native seat %s: %w", seat.SeatID, err)
	}
	if err := state.DeleteSeatRun(seat.SeatID); err != nil {
		return warning, fmt.Errorf("retire replaced native recipe %s: %w", seat.SeatID, err)
	}
	return warning, nil
}

// RetireHerdrPane closes the old pane of a dead seat a revive replaced, only
// when Herdr proves it still holds that seat's terminal: a pane already gone
// needs nothing, and a pane Herdr cannot read, or one now holding another
// terminal, stays and is named in the returned warning.
func RetireHerdrPane(ctx context.Context, seat fleetdb.Seat) string {
	if seat.HerdrSocket == "" || seat.HerdrPane == "" {
		return ""
	}
	stays := func(why string, err error) string {
		if errors.Is(err, errPaneGone) {
			return ""
		}
		return fmt.Sprintf("old Herdr pane %s of chat %s stays: %s: %v", seat.HerdrPane, seat.SeatID, why, err)
	}
	body, err := paneRequest(ctx, seat, "pane.get")
	if err != nil {
		return stays("read it", err)
	}
	var reply struct {
		Pane struct {
			Terminal string `json:"terminal_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return stays("decode it", err)
	}
	if seat.HerdrTerminalID == "" || reply.Pane.Terminal != seat.HerdrTerminalID {
		return stays(
			"not proved its own",
			fmt.Errorf("terminal %q, seat terminal %q", reply.Pane.Terminal, seat.HerdrTerminalID),
		)
	}
	if _, err := paneRequest(ctx, seat, "pane.close"); err != nil {
		return stays("close it", err)
	}
	return ""
}

var errPaneGone = errors.New("pane gone")

// paneRequest sends method for seat's pane; Herdr's pane_not_found is errPaneGone.
func paneRequest(ctx context.Context, seat fleetdb.Seat, method string) (json.RawMessage, error) {
	body, err := herdr.SeatRequest(ctx, seat.HerdrSocket, method, map[string]string{"pane_id": seat.HerdrPane})
	var refusal *herdr.Refusal
	if errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
		return nil, errPaneGone
	}
	return body, err
}
