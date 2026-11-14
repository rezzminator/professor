package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// herdrRunFixture is a scratch fleet holding one native Herdr seat whose
// supervisor is proved dead (its pid is not in the process table), so a pane
// proof and a tab validation read the shared state, and the painter run built
// over it. Its Herdr socket is a path in the test's own temp directory that
// nothing listens on: the scan's and the bridge proof's dials end there.
type herdrRunFixture struct {
	socket  string
	runtime pfmconfig.Runtime
	run     *herdrIndicatorRun
	target  headless.Chat
	pane    herdr.TabPane
	warn    *bytes.Buffer
}

func newHerdrRunFixture(t *testing.T) herdrRunFixture {
	t.Helper()
	testjail.Fleet(t)
	runtime, err := pfmconfig.RuntimeOrDefault(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	socket := filepath.Join(t.TempDir(), "herdr.sock")
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	if err := db.PutSeat(ctx, fleetdb.Seat{
		SeatID: "seat1", SessionID: "s1", Engine: "cc", Backend: "herdr",
		HerdrSocket: socket, HerdrPane: "w1:p1", HerdrWorkspace: "w1", HerdrTerminalID: "term1",
		State: "live", SeatHostPID: 4_000_000, SeatHostStart: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	warn := &bytes.Buffer{}
	run, err := newHerdrIndicatorRun(&runtime, warn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := run.held.Close(); err != nil {
			t.Errorf("close the run's store: %v", err)
		}
	})
	focused := false
	return herdrRunFixture{
		socket:  socket,
		runtime: runtime,
		run:     run,
		target: headless.Chat{
			ID:      "s1",
			SeatID:  "seat1",
			Backend: "herdr",
			Socket:  socket,
			Pane:    "w1:p1",
			Live:    true,
		},
		pane: herdr.TabPane{
			Pane: "w1:p1", Terminal: "term1", Tab: "w1:t1", Workspace: "w1", Focused: &focused,
			Session: []byte(`{"source":"pfm","agent":"claude","kind":"id","value":"s1"}`),
		},
		warn: warn,
	}
}

// tick is one blinker half-period of the run with a blocked or done agent:
// the pane proof and the native tab's validation; every scanEvery-th tick
// also reads the fleet and the tab views again, as an idle refresh does.
func (fixture *herdrRunFixture) tick(
	ctx context.Context,
	t *testing.T,
	tick, scanEvery int,
	views *[]herdr.TabDecoration,
) {
	t.Helper()
	if tick%scanEvery == 0 {
		if _, err := fixture.run.provider.scan(ctx); err != nil {
			t.Fatalf("scan: %v (warnings %q)", err, fixture.warn)
		}
		got, err := fixture.run.reader.views(ctx, fixture.target, fixture.socket, []herdr.TabPane{fixture.pane})
		if err != nil || len(got) != 1 || got[0].State != headless.StateDead {
			t.Fatalf("views = %+v, %v; want the dead native tab (warnings %q)", got, err, fixture.warn)
		}
		*views = got
	}
	if err := fixture.run.provePane(ctx, fixture.socket, "w1:p1"); err == nil || errors.Is(err, herdr.ErrNotFleetPane) {
		t.Fatalf("pane proof = %v; want the owner's retired supervisor unproved, never a non-fleet pane", err)
	}
	if valid, err := (*views)[0].Validate(ctx); err != nil || !valid {
		t.Fatalf("validate = %t, %v; want the tab still valid (warnings %q)", valid, err, fixture.warn)
	}
}

// opens counts the SQLite door's open records by kind and database.
func (fixture *herdrRunFixture) opens(recorder *obs.Recorder) map[string]int {
	names := map[string]string{fixture.runtime.Paths.CacheDB: "cache", fixture.runtime.Paths.StateDB: "state"}
	opens := map[string]int{}
	for _, record := range recorder.Records() {
		if record.Message != "db.open" {
			continue
		}
		name, ok := names[fmt.Sprint(record.Fields["path"])]
		if !ok {
			name = fmt.Sprint(record.Fields["path"])
		}
		opens[fmt.Sprintf("%v %s", record.Fields["kind"], name)]++
	}
	return opens
}

// indexReads counts the select statements on the index's transcripts and
// rollouts tables, with the rows each returned.
func indexReads(recorder *obs.Recorder) map[string]int {
	reads := map[string]int{}
	for _, record := range recorder.Records() {
		table := fmt.Sprint(record.Fields["table"])
		if record.Message != "db.statement" || record.Fields["op"] != "select" ||
			(table != "transcripts" && table != "rollouts") {
			continue
		}
		reads[fmt.Sprintf("%s rows=%v", table, record.Fields["rows"])]++
	}
	return reads
}

// TestHerdrIndicatorRunHoldsItsStoreAcrossTicks: a painter run opens the
// fleet store once for all its ticks' pane proofs, tab validations and
// scans, and opens it again only when the shared state file is replaced,
// whose rows the next read then sees. The read-only opens are the fleet
// scan's own durable seat reads, four a scan.
func TestHerdrIndicatorRunHoldsItsStoreAcrossTicks(t *testing.T) {
	fixture := newHerdrRunFixture(t)
	ctx, recorder := obs.Test(t)
	var views []herdr.TabDecoration
	for tick := range 100 {
		fixture.tick(ctx, t, tick, 20, &views)
	}
	want := map[string]int{"store cache": 1, "store state": 1, "readonly state": 20}
	if got := fixture.opens(recorder); !reflect.DeepEqual(got, want) {
		t.Fatalf("opens over 100 ticks = %v, want %v", got, want)
	}
	// Five scans read the index tables whole once; the later ones ask only
	// whether anything was written since.
	wantReads := map[string]int{"transcripts rows=0": 1, "rollouts rows=0": 1, "rollouts rows=<nil>": 1}
	if got := indexReads(recorder); !reflect.DeepEqual(got, wantReads) {
		t.Fatalf("index table reads over 100 ticks = %v, want %v", got, wantReads)
	}
	// The SQLite door serves this test from memory, so nothing holds the file
	// the way the run's real connection does; hold it here, or the new state
	// file could be handed the freed inode.
	holder, err := os.Open(fixture.runtime.Paths.StateDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := holder.Close(); err != nil {
			t.Errorf("close the replaced state's holder: %v", err)
		}
	})
	if err := os.Remove(fixture.runtime.Paths.StateDB); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.run.reader.seat(ctx, "s1", "seat1"); err != nil || found {
		t.Fatalf(
			"seat after the shared state was replaced: found=%t err=%v; want the new, empty state read",
			found,
			err,
		)
	}
	want = map[string]int{"store cache": 2, "store state": 2, "readonly state": 20}
	if got := fixture.opens(recorder); !reflect.DeepEqual(got, want) {
		t.Fatalf("opens after the shared state was replaced = %v, want %v", got, want)
	}
}
