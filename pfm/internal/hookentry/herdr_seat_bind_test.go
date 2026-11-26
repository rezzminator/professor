package hookentry

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A refused binding carries no space: the new session never took the seat.
// The carry rows that bind are TestHerdrNativeSeatReporter's.
func TestBindNativeSeatRefusedCarriesNoSpace(t *testing.T) {
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	if err := db.SetSpace(context.Background(), "old-session", "glass", 1); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	reporter := &herdrReporter{deps: herdrDeps{seatPaths: &values}, stderr: &stderr}
	before := fleetdb.Seat{SeatID: "native-seat", SessionID: "old-session"}
	seat := fleetdb.Seat{SeatID: "other-seat", SessionID: "new-session"}

	err := reporter.bindNativeSeat(context.Background(), before, seat, "startup")

	const want = "bind native seat: update seat: immutable seat id changed"
	if err == nil || err.Error() != want {
		t.Fatalf("bindNativeSeat error = %v, want %q", err, want)
	}
	spaces, err := db.Spaces(context.Background())
	wantSpaces := map[string]fleetdb.ChatSpace{"old-session": {Space: "glass", UpdatedAt: 1}}
	if err != nil || !reflect.DeepEqual(spaces, wantSpaces) {
		t.Fatalf("spaces after a refused bind = %v, %v; want %v", spaces, err, wantSpaces)
	}
}
