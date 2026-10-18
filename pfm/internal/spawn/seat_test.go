package spawn

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRecordTmuxSeatRefusesEscapingSocket(t *testing.T) {
	if err := RecordTmuxSeat(
		context.Background(),
		paths.Values{TmuxDir: t.TempDir()},
		fleetdb.Seat{SessionID: "sid", TmuxSocket: "../foreign"},
	); err == nil {
		t.Fatal("foreign socket was accepted")
	}
}
