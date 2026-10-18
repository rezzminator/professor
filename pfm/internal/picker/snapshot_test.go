package picker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/rowfacts"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

type fakeCommsReader struct {
	events  []fleetdb.CommsEvent
	err     error
	sinceNS int64
	limit   int
}

func TestBuildSnapshotInitialCacheMatchesPrimaryAccount(t *testing.T) {
	machine := config.Config{
		Claude: config.ClaudePrefs{Cache1H: true},
		Accounts: []config.Account{
			{ID: 1},
			{ID: 2, Claude: &config.ClaudePrefs{Cache1H: false}},
		},
	}
	for _, testCase := range []struct {
		account int
		want    bool
	}{{1, true}, {2, false}} {
		snapshot := buildSnapshot(
			context.Background(),
			fleet.Env{Config: machine, Primary: testCase.account},
			scanRequest{},
			compose.Output{},
		)
		if snapshot.Cache1H != testCase.want || snapshot.Cache1HByAccount[testCase.account] != testCase.want {
			t.Fatalf("account %d snapshot cache = %t map = %t, want %t", testCase.account,
				snapshot.Cache1H, snapshot.Cache1HByAccount[testCase.account], testCase.want)
		}
	}
}

func (reader *fakeCommsReader) CommsSince(_ context.Context, sinceNS int64, limit int) ([]fleetdb.CommsEvent, error) {
	reader.sinceNS = sinceNS
	reader.limit = limit
	return reader.events, reader.err
}

func TestComposeFleetPacksCosmosLedgerState(t *testing.T) {
	const nowNS = int64(48 * time.Hour)
	t.Run("healthy", func(t *testing.T) {
		reader := &fakeCommsReader{events: []fleetdb.CommsEvent{{
			AtNS: nowNS - 1, Kind: fleetdb.KindCall,
			SenderLabel: "Alpha", Target: "Beta", Message: "hello",
		}}}
		snapshot := buildSnapshot(
			context.Background(),
			fleet.Env{NowNS: nowNS},
			scanRequest{Comms: reader},
			compose.Output{},
		)
		if reader.sinceNS != nowNS-int64(compose.CosmosWindow) || reader.limit != compose.CosmosEventCap {
			t.Fatalf("CommsSince() args = %d, %d", reader.sinceNS, reader.limit)
		}
		if snapshot.Cosmos.Err != "" || len(snapshot.Cosmos.Edges) != 1 {
			t.Fatalf("Cosmos = %#v", snapshot.Cosmos)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		reader := &fakeCommsReader{err: errors.New("database unavailable")}
		snapshot := buildSnapshot(
			context.Background(),
			fleet.Env{NowNS: nowNS},
			scanRequest{Comms: reader},
			compose.Output{},
		)
		if !strings.Contains(snapshot.Cosmos.Err, "database unavailable") ||
			len(snapshot.Cosmos.Nodes) != 0 {
			t.Fatalf("failed Cosmos = %#v", snapshot.Cosmos)
		}
	})

	t.Run("cap warning", func(t *testing.T) {
		events := make([]fleetdb.CommsEvent, compose.CosmosEventCap)
		for index := range events {
			events[index] = fleetdb.CommsEvent{
				AtNS: int64(index + 1), Kind: fleetdb.KindCall,
				SenderLabel: "Alpha", Target: "Beta", Message: "hello",
			}
		}
		snapshot := buildSnapshot(
			context.Background(),
			fleet.Env{NowNS: nowNS},
			scanRequest{Comms: &fakeCommsReader{events: events}},
			compose.Output{},
		)
		warnings := snapshot.Cosmos.Warnings
		if len(warnings) != 1 || warnings[0] != compose.CosmosTruncationWarning {
			t.Fatalf("cap warnings = %v", warnings)
		}
	})
}

type fakeScheduleReader struct {
	unseen map[string]bool
	err    error
}

func (reader fakeScheduleReader) UnseenScheduleSessionIDs(context.Context) (map[string]bool, error) {
	return reader.unseen, reader.err
}

func TestBuildSnapshotMarksScheduleFiredRows(t *testing.T) {
	output := compose.Output{Rows: []compose.Row{{ID: "idA"}, {ID: "idB"}, {}}}
	snapshot := buildSnapshot(
		context.Background(),
		fleet.Env{},
		scanRequest{Schedules: fakeScheduleReader{unseen: map[string]bool{"idA": true, "": true}}},
		output,
	)
	if snapshot.ScheduleError != "" {
		t.Fatalf("ScheduleError = %q, want none", snapshot.ScheduleError)
	}
	if len(snapshot.Rows) != 3 || !snapshot.Rows[0].ScheduleFired || snapshot.Rows[1].ScheduleFired ||
		snapshot.Rows[2].ScheduleFired {
		t.Fatalf("snapshot rows ScheduleFired = %v, want only idA", scheduleFiredFlags(snapshot.Rows))
	}
	for index, row := range output.Rows {
		if row.ScheduleFired {
			t.Fatalf("buildSnapshot mutated the composed output: row %d is ScheduleFired", index)
		}
	}
}

func TestBuildSnapshotReportsScheduleReadFailure(t *testing.T) {
	output := compose.Output{Rows: []compose.Row{{ID: "idA"}, {ID: "idB"}}}
	snapshot := buildSnapshot(
		context.Background(),
		fleet.Env{},
		scanRequest{Schedules: fakeScheduleReader{err: errors.New("database unavailable")}},
		output,
	)
	if !strings.Contains(snapshot.ScheduleError, "read schedule flags") ||
		!strings.Contains(snapshot.ScheduleError, "database unavailable") {
		t.Fatalf("ScheduleError = %q, want the wrapped read failure", snapshot.ScheduleError)
	}
	if len(snapshot.Rows) != 2 || snapshot.Rows[0].ScheduleFired || snapshot.Rows[1].ScheduleFired {
		t.Fatalf(
			"rows after a failed read = %v, want both present and none ScheduleFired",
			scheduleFiredFlags(snapshot.Rows),
		)
	}
}

// TestBuildSnapshotReportsDegradedScheduleStore proves a state database that
// cannot open reads as an error at the snapshot, never as "no schedules".
func TestBuildSnapshotReportsDegradedScheduleStore(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("a regular file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, paths.Values{StateDB: filepath.Join(blocker, "state.db")})
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Errorf("close degraded shared state: %v", err)
		}
	})
	snapshot := buildSnapshot(
		ctx, fleet.Env{}, scanRequest{Schedules: state}, compose.Output{Rows: []compose.Row{{ID: "idA"}}},
	)
	if snapshot.ScheduleError == "" {
		t.Fatal("a degraded schedule store produced no ScheduleError: a broken database reads as no schedules")
	}
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].ScheduleFired {
		t.Fatalf(
			"rows with a degraded store = %v, want the row present, not ScheduleFired",
			scheduleFiredFlags(snapshot.Rows),
		)
	}
}

func scheduleFiredFlags(rows []compose.Row) []bool {
	flags := make([]bool, len(rows))
	for index := range rows {
		flags[index] = rows[index].ScheduleFired
	}
	return flags
}

// countingStoreReaders stands for the two store reads buildSnapshot makes: the
// comms ledger and the schedule flags.
type countingStoreReaders struct{ comms, schedules int }

func (readers *countingStoreReaders) CommsSince(context.Context, int64, int) ([]fleetdb.CommsEvent, error) {
	readers.comms++
	return nil, nil
}

func (readers *countingStoreReaders) UnseenScheduleSessionIDs(context.Context) (map[string]bool, error) {
	readers.schedules++
	return map[string]bool{"live": true}, nil
}

// TestRereadFactsRefreshesWorkingStateWithoutAStoreRead pins what a parked
// poll costs when it only re-reads the rows' working state: a stat per
// transcript. The ledger and the schedule flags are not read again, the last
// pass's schedule flags and cosmos stand, and an unchanged re-read is not a
// change.
func TestRereadFactsRefreshesWorkingStateWithoutAStoreRead(t *testing.T) {
	output := factsFixture(t)
	transcript := output.Rows[0].Path
	readers := &countingStoreReaders{}
	request := scanRequest{Facts: rowfacts.NewReader(t.TempDir()), Comms: readers, Schedules: readers}
	environment := fleet.Env{NowNS: statNowNS(t, transcript)}
	last := sentRefresh{output: output, snapshot: buildSnapshot(context.Background(), environment, request, output)}
	if !last.snapshot.Rows[0].Working || !last.snapshot.Rows[0].ScheduleFired {
		t.Fatalf("the pass's snapshot lacks the working state or the schedule flag: %+v", last.snapshot.Rows[0])
	}
	if readers.comms != 1 || readers.schedules != 1 {
		t.Fatalf(
			"a full pass reads the ledger and the flags once: comms=%d schedules=%d",
			readers.comms,
			readers.schedules,
		)
	}

	if again := rereadFacts(environment, request, last); rowFactsMoved(last.snapshot, again) {
		t.Fatalf("an unchanged re-read must not look like a change: %+v", again.Rows[0])
	}

	done := `{"type":"assistant","message":{"model":"claude-opus-5-5","role":"assistant","content":[],"stop_reason":"end_turn"}}` + "\n"
	file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open transcript to append: %v", err)
	}
	if _, err := file.WriteString(done); err != nil {
		t.Fatalf("append the finished turn: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close transcript: %v", err)
	}
	environment.NowNS = statNowNS(t, transcript)
	idle := rereadFacts(environment, request, last)
	if idle.Rows[0].Working || !rowFactsMoved(last.snapshot, idle) {
		t.Fatalf("a chat that finished its turn must re-read idle and count as a change: %+v", idle.Rows[0])
	}
	if !idle.Rows[0].ScheduleFired {
		t.Error("the re-read dropped the schedule flag the last pass read")
	}
	if idle.NowNS != environment.NowNS {
		t.Errorf("NowNS = %d, want the re-read's clock %d", idle.NowNS, environment.NowNS)
	}
	if readers.comms != 1 || readers.schedules != 1 {
		t.Fatalf("a facts re-read must not touch the store: comms=%d schedules=%d, want 1 and 1",
			readers.comms, readers.schedules)
	}
}

func TestSendRefreshIncludesNativeSeats(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	environment := fleet.Env{
		Paths:  paths.Values{Home: home, StateDB: filepath.Join(home, "state.db")},
		Config: config.Defaults(home, nil),
	}
	state := fleetdb.OpenSharedState(ctx, environment.Paths)
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := state.PutSeat(
		ctx,
		fleetdb.Seat{
			SeatID:      "seat1",
			SessionID:   "sid1",
			Engine:      "cc",
			Backend:     "herdr",
			State:       "lost",
			Name:        "REVIEW",
			HerdrSocket: filepath.Join(home, "dead.sock"),
			HerdrPane:   "w6:p3",
		},
	); err != nil {
		t.Fatal(err)
	}
	for _, refreshing := range []bool{true, false} {
		updates := make(chan ui.Snapshot, 1)
		last, ok := sendRefresh(
			ctx,
			nil,
			environment,
			scanRequest{View: compose.DefaultView},
			fleet.Data{},
			gather.Snapshot{},
			refreshing,
			updates,
		)
		if !ok {
			t.Fatal("refresh not published")
		}
		snapshot := <-updates
		var native []compose.Row
		for _, row := range snapshot.Rows {
			if row.Backend == "herdr" {
				native = append(native, row)
			}
		}
		if len(native) != 1 || native[0].SeatID != "seat1" || native[0].BackendError == "" {
			t.Fatalf("refreshing=%t native snapshot=%+v", refreshing, snapshot.Rows)
		}
		if snapshot.Refreshing != refreshing || len(last.output.Rows) != len(snapshot.Rows) {
			t.Fatalf("refresh state lost: %+v", snapshot)
		}
	}
}

func TestSendRefreshReportsNativeReadFailure(t *testing.T) {
	ctx := context.Background()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	updates := make(chan ui.Snapshot, 1)
	last, ok := sendRefresh(
		ctx,
		nil,
		fleet.Env{Paths: paths.Values{StateDB: filepath.Join(blocker, "state.db")}},
		scanRequest{},
		fleet.Data{},
		gather.Snapshot{},
		false,
		updates,
	)
	if !ok {
		t.Fatal("failure not published")
	}
	if !last.failed {
		t.Fatal("failed native pass must remain retryable without becoming last good state")
	}
	snapshot := <-updates
	if snapshot.RefreshFailure == nil || !strings.Contains(snapshot.RefreshFailure.Err, "durable seats") {
		t.Fatalf("native read failure rendered successful: %+v", snapshot)
	}
}

func TestNativeSnapshotsRetainCappedBackgroundMetadata(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := config.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	machine := config.Defaults(values.Home, values.Roots["cc"])
	runtime := config.Runtime{Config: machine, Paths: values}
	environment, err := fleet.ResolveEnv(fleet.Request{Runtime: &runtime})
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	state := fleetdb.OpenSharedState(ctx, values)
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"visible", "background"} {
		if err := db.UpsertTranscript(
			ctx,
			store.Transcript{
				UUID:        id,
				Path:        filepath.Join(values.Home, id+".jsonl"),
				CustomTitle: id,
				Size:        42,
				PromptCount: 3,
				IsBG:        id == "background",
			},
		); err != nil {
			t.Fatal(err)
		}
		if err := state.PutSeat(
			ctx,
			fleetdb.Seat{
				SeatID:      id,
				SessionID:   id,
				Engine:      "cc",
				Backend:     "herdr",
				State:       "lost",
				Name:        id,
				HerdrSocket: filepath.Join(values.Home, "dead.sock"),
				HerdrPane:   "w6:" + id,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	data, err := fleet.LoadDefaultData(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Transcripts) != 1 {
		t.Fatalf("capped fixture=%+v, want one foreground candidate", data.Transcripts)
	}
	check := func(t *testing.T, rows []compose.Row) {
		t.Helper()
		var native []compose.Row
		for _, row := range rows {
			if row.Backend == "herdr" {
				native = append(native, row)
			}
		}
		if len(native) != 1 || native[0].SeatID != "visible" || native[0].PromptCount != 3 ||
			native[0].Path != filepath.Join(values.Home, "visible.jsonl") {
			t.Fatalf("canonical native metadata=%+v, want only the foreground native seat", native)
		}
	}
	t.Run("cached", func(t *testing.T) {
		result, err := fleet.ScanCached(ctx, db, fleet.Request{Runtime: &runtime, View: compose.DefaultView})
		if err != nil {
			t.Fatal(err)
		}
		check(t, result.Output.Rows)
	})

	t.Run("native enrichment preserves capped candidates", func(t *testing.T) {
		if err := db.UpsertTranscript(
			ctx,
			store.Transcript{
				UUID:        "unrelated",
				Path:        filepath.Join(values.Home, "unrelated.jsonl"),
				Size:        42,
				PromptCount: 1,
			},
		); err != nil {
			t.Fatal(err)
		}
		updates := make(chan ui.Snapshot, 1)
		last, ok := sendRefresh(
			ctx,
			db,
			environment,
			scanRequest{View: compose.DefaultView},
			data,
			gather.Snapshot{},
			false,
			updates,
		)
		if !ok || last.failed {
			t.Fatalf("native refresh failed: %+v", last)
		}
		snapshot := <-updates
		check(t, snapshot.Rows)
		for _, row := range snapshot.Rows {
			if row.ID == "unrelated" {
				t.Fatalf("native enrichment replaced capped candidates with unrelated full-store row: %+v", row)
			}
		}
	})

	for _, refreshing := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream refreshing=%t", refreshing), func(t *testing.T) {
			updates := make(chan ui.Snapshot, 1)
			_, ok := sendRefresh(
				ctx,
				db,
				environment,
				scanRequest{View: compose.DefaultView},
				data,
				gather.Snapshot{},
				refreshing,
				updates,
			)
			if !ok {
				t.Fatal("native stream failed")
			}
			check(t, (<-updates).Rows)
		})
	}
}
