package fleetdb

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestStoreOperationsRecordUnderTheDBComponent: every public fleetdb
// operation reaches SQLite through the store's logged statement helpers —
// one comp=db record per statement with the verb, table and rows — and a
// bound value (the chat id) never reaches the file.
// It stays serial: obs.Test swaps the process logger, so it never overlaps
// the package's parallel tests.
func TestStoreOperationsRecordUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	state := openTestStore(t)
	const id = "cccccccc-cccc-4ccc-8ccc-PLANTEDBOUND"
	if err := state.Kill(ctx, id, 1700000000); err != nil {
		t.Fatal(err)
	}
	if _, err := state.KilledAt(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Meta(ctx, "schema_version"); err != nil {
		t.Fatal(err)
	}
	var kill, killed, meta obs.Record
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		if kind, _ := record.Field("kind"); kind != "fleet" {
			t.Fatalf("kind = %v, want fleet", kind)
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		switch {
		case op == "insert" && table == "hidden":
			kill = record
		case op == "select" && table == "hidden":
			killed = record
		case op == "select" && table == "meta":
			meta = record
		}
	}
	if kill.Message == "" || killed.Message == "" || meta.Message == "" {
		t.Fatalf(
			"missing statement records (kill=%v killed=%v meta=%v): %s",
			kill.Message != "",
			killed.Message != "",
			meta.Message != "",
			recorder.Raw(),
		)
	}
	if rows, _ := kill.Field("rows"); rows != float64(1) {
		t.Fatalf("kill rows = %v, want 1", rows)
	}
	if _, found := kill.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", kill.Fields)
	}
	if strings.Contains(recorder.Raw(), "PLANTEDBOUND") || strings.Contains(recorder.Raw(), "INSERT") {
		t.Fatalf("a bound value or SQL text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}

// TestListReadsSkipOnlyTheBadRow: a stored row a list read cannot decode costs
// that row alone — every other row is returned and one fleet.row.skipped
// warning names the table and the decode error. Serial: obs.Test swaps the
// process logger.
func TestListReadsSkipOnlyTheBadRow(t *testing.T) {
	const goodClear = "22222222-2222-4222-8222-222222222222"
	for _, test := range []struct {
		name    string
		seed    func(context.Context, *Store) error
		read    func(context.Context, *Store) (any, error)
		want    any
		table   string
		skipped string
	}{
		{
			name: "seats",
			seed: func(ctx context.Context, state *Store) error {
				_, err := state.db.ExecContext(ctx, `INSERT INTO seat(`+seatColumns+`) VALUES
('good','S1','claude','tmux','cc-1','%1','','','','alpha','live',0,'',1,'',0,0,0,0),
('nameless','S2','claude','tmux','cc-2','%2','','','',NULL,'live',0,'',2,'',0,0,0,0)`)
				return err
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				seats, err := state.Seats(ctx)
				ids := []string{}
				for _, seat := range seats {
					ids = append(ids, seat.SeatID+"/"+seat.Name)
				}
				return ids, err
			},
			want:    []string{"good/alpha"},
			table:   "seat",
			skipped: `read seat row: sql: Scan error on column index 9, name "name": converting NULL to string is unsupported`,
		},
		{
			name: "schedules",
			seed: func(ctx context.Context, state *Store) error {
				if _, err := state.CreateSchedule(ctx, newTestSchedule("s-good", time.Unix(1_800_000_000, 0), time.Hour)); err != nil {
					return err
				}
				_, err := state.db.ExecContext(ctx, `INSERT INTO schedules(session_id,engine,prompt,interval_ns,next_fire_ns,created_ns)
VALUES('s-bad','claude','wake',3600000000000,1,'yesterday')`)
				return err
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				schedules, err := state.Schedules(ctx)
				ids := []string{}
				for _, schedule := range schedules {
					ids = append(ids, schedule.SessionID)
				}
				// The lookup by id refuses the row the list skipped.
				_, found, lookupErr := state.Schedule(ctx, 2)
				ids = append(ids, fmt.Sprintf("lookup found=%v: %v", found, lookupErr))
				return ids, err
			},
			want: []string{"s-good", `lookup found=false: read schedule 2: scan: sql: Scan error on column index 7, ` +
				`name "created_ns": converting driver.Value type string ("yesterday") to a int64: invalid syntax`},
			table: "schedules",
			skipped: `list schedules: scan: sql: Scan error on column index 7, name "created_ns": ` +
				`converting driver.Value type string ("yesterday") to a int64: invalid syntax`,
		},
		{
			name: "comms",
			seed: func(ctx context.Context, state *Store) error {
				if err := state.RecordComms(ctx, CommsEvent{AtNS: 10, Kind: KindCall, Target: "beta", Message: "kept"}); err != nil {
					return err
				}
				_, err := state.db.ExecContext(ctx, `INSERT INTO comms(at_ns,kind,message) VALUES('later','call','unreadable')`)
				return err
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				events, err := state.CommsSince(ctx, 0, 10)
				messages := []string{}
				for _, event := range events {
					messages = append(messages, event.Message)
				}
				return messages, err
			},
			want:  []string{"kept"},
			table: "comms",
			skipped: `sql: Scan error on column index 1, name "at_ns": ` +
				`converting driver.Value type string ("later") to a int64: invalid syntax`,
		},
		{
			// A kill whose id still reads stays killed with its time unknown;
			// only a row with no id at all is skipped.
			name: "kills",
			seed: func(ctx context.Context, state *Store) error {
				if err := state.Kill(ctx, "good", 1700); err != nil {
					return err
				}
				_, err := state.db.ExecContext(ctx, `INSERT INTO hidden(uuid,hidden_at,at_payload) VALUES('timeless','noon',5)`)
				return err
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				return state.KilledRecords(ctx)
			},
			want:  map[string]KilledRecord{"good": {KilledAt: 1700}, "timeless": {}},
			table: "hidden",
			skipped: `scan shared kill: sql: Scan error on column index 1, name "hidden_at": ` +
				`converting driver.Value type string ("noon") to a int64: invalid syntax`,
		},
		{
			name: "clear names",
			seed: func(ctx context.Context, state *Store) error {
				fresh, err := naming.ClearSuccessorName("Desk", goodClear)
				if err != nil {
					return err
				}
				if err := state.PutClearName(ctx, goodClear, naming.ClearName{Inherited: "Desk", Name: fresh}, 1); err != nil {
					return err
				}
				return state.SetMeta(ctx, clearNamePrefix+"broken", "broken-json", 2)
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				names, err := state.ClearNames(ctx)
				ids := []string{}
				for id := range names {
					ids = append(ids, id)
				}
				return ids, err
			},
			want:    []string{goodClear},
			table:   "meta",
			skipped: "decode clear name broken: invalid character 'b' looking for beginning of value",
		},
		{
			name: "branch seats",
			seed: func(ctx context.Context, state *Store) error {
				if err := state.RecordBranchSeat(ctx, "cc-1-2-3", "parent", 7); err != nil {
					return err
				}
				return state.SetMeta(ctx, branchSeatPrefix, "orphan", 8)
			},
			read: func(ctx context.Context, state *Store) (any, error) {
				return state.BranchSeats(ctx)
			},
			want:    map[string]BranchSeat{"cc-1-2-3": {Socket: "cc-1-2-3", Parent: "parent", CreatedAt: 7}},
			table:   "meta",
			skipped: `branch seat key "branch-seat:" names no socket`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			state := openTestStore(t)
			if err := test.seed(ctx, state); err != nil {
				t.Fatal(err)
			}
			got, err := test.read(ctx, state)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("read = %#v, %v; want %#v", got, err, test.want)
			}
			var skips []string
			for _, record := range recorder.Records() {
				if record.Message != "fleet.row.skipped" {
					continue
				}
				table, _ := record.Field("table")
				text, _ := record.Field(obs.FieldErr)
				skips = append(skips, record.Level+" "+table.(string)+": "+text.(string))
			}
			if want := []string{"WARN " + test.table + ": " + test.skipped}; !reflect.DeepEqual(skips, want) {
				t.Fatalf("skip records = %q, want %q", skips, want)
			}
		})
	}
}
