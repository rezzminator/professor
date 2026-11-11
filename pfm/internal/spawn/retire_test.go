package spawn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The one retire path of a revived chat's dead seat closes its old pane only
// when Herdr proves the pane still holds that seat's terminal; a pane already
// gone needs nothing, and one now holding another terminal stays, named. The
// seat's reload record, row and recipe go whatever the pane's fate.
func TestRetireHerdrSeatClosesOnlyItsOwnPane(t *testing.T) {
	for _, tc := range []struct {
		name, terminal string
		closed         bool
		warning        string
	}{
		{name: "its own pane", terminal: "term-old", closed: true},
		{name: "a pane already gone"},
		{name: "a pane holding another terminal", terminal: "term-other", warning: "not proved its own"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			ctx := context.Background()
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			fake := &placeHerdr{panes: map[string]string{}}
			if tc.terminal != "" {
				fake.panes["w1:p3"] = tc.terminal
			}
			socket := servePlaceHerdr(t, fake)
			seat := fleetdb.Seat{
				SeatID: "old-seat", SessionID: "6fafd8a7-5059-4288-bf58-23e60c5fdb88", Engine: "cc",
				Backend: BackendHerdr, State: "lost", HerdrSocket: socket, HerdrPane: "w1:p3", HerdrWorkspace: "w1",
				HerdrTerminalID: "term-old",
			}
			state := fleetdb.OpenSharedState(ctx, values)
			if err := errors.Join(
				state.PutSeat(ctx, seat),
				state.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/bin/true", CWD: values.Home}),
				state.Close(),
			); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(values.SIDDir, 0o700); err != nil {
				t.Fatal(err)
			}
			reload := filepath.Join(values.SIDDir, paths.SIDReloadHerdrPrefix+seat.SeatID+".pid")
			if err := os.WriteFile(reload, []byte("1"), 0o600); err != nil {
				t.Fatal(err)
			}
			warning, err := RetireHerdrSeat(ctx, values, seat)
			if err != nil || !strings.Contains(warning, tc.warning) || (tc.warning == "") != (warning == "") {
				t.Fatalf("warning=%q err=%v, want warning %q", warning, err, tc.warning)
			}
			if closed := slices.Contains(fake.methods(), "pane.close"); closed != tc.closed {
				t.Fatalf("pane closed=%t, want %t (methods %q)", closed, tc.closed, fake.methods())
			}
			state = fleetdb.OpenSharedState(ctx, values)
			defer func() { _ = state.Close() }()
			if _, found, err := state.Seat(ctx, seat.SeatID); err != nil || found {
				t.Fatalf("retired seat found=%t err=%v", found, err)
			}
			if _, err := state.ReadSeatRun(seat.SeatID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retired recipe err=%v", err)
			}
			if _, err := os.Stat(reload); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("retired reload file err=%v", err)
			}
		})
	}
}
