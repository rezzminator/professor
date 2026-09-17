package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/fleet"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/shared"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
)

type fakeCommsReader struct {
	events  []shared.CommsEvent
	err     error
	sinceNS int64
	limit   int
}

func (reader *fakeCommsReader) CommsSince(_ context.Context, sinceNS int64, limit int) ([]shared.CommsEvent, error) {
	reader.sinceNS = sinceNS
	reader.limit = limit
	return reader.events, reader.err
}

func TestComposeFleetPacksCosmosLedgerState(t *testing.T) {
	const nowNS = int64(48 * time.Hour)
	t.Run("healthy", func(t *testing.T) {
		reader := &fakeCommsReader{events: []shared.CommsEvent{{
			AtNS: nowNS - 1, Kind: shared.KindInject,
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
		events := make([]shared.CommsEvent, compose.CosmosEventCap)
		for index := range events {
			events[index] = shared.CommsEvent{
				AtNS: int64(index + 1), Kind: shared.KindInject,
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

type slowIndexRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mutex   sync.Mutex
	options []fleetindex.Options
}

type immediateIndexRunner struct {
	mutex   sync.Mutex
	options []fleetindex.Options
}

func TestCanceledPickerRefreshExitsWithoutReportingARefreshFailure(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	updates := make(chan ui.Snapshot)
	var stderr bytes.Buffer
	streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{},
	)

	if _, ok := <-updates; ok {
		t.Fatal("canceled refresh left its update channel open")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("canceled refresh wrote stderr=%q, want a quiet picker shutdown", got)
	}
}

func (runner *immediateIndexRunner) Run(
	_ context.Context,
	options fleetindex.Options,
) (fleetindex.Counters, error) {
	runner.mutex.Lock()
	runner.options = append(runner.options, options)
	runner.mutex.Unlock()
	return fleetindex.Counters{}, nil
}

func TestPickerRefreshStreamRepeatsAtTheBaseInterval(t *testing.T) {
	shortenRefreshIntervals(t)
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan ui.Snapshot, 1)
	var stderr bytes.Buffer
	runner := &immediateIndexRunner{}
	go streamFleetRefreshesWith(
		ctx,
		database,
		scanRequest{},
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{newIndexer: func(*store.Store) (indexRunner, error) {
			return runner, nil
		}},
	)
	for {
		snapshot, ok := <-updates
		if !ok {
			t.Fatalf("refresh stream closed after initial scan: %s", stderr.String())
		}
		if !snapshot.Refreshing {
			break
		}
	}
	// The base interval plus headroom: a nil activity clock never backs off,
	// so a second pass is due one fleetRefreshInterval after the first.
	deadline := time.After(fleetRefreshInterval + 4*time.Second)
	for {
		select {
		case snapshot, ok := <-updates:
			if !ok {
				t.Fatalf("refresh stream closed before recurring scan: %s", stderr.String())
			}
			if !snapshot.Refreshing {
				goto refreshed
			}
		case <-deadline:
			t.Fatalf("picker did not refresh again within %s", fleetRefreshInterval+4*time.Second)
		}
	}

refreshed:
	cancel()
	for range updates {
	}
	runner.mutex.Lock()
	options := append([]fleetindex.Options(nil), runner.options...)
	runner.mutex.Unlock()
	if len(options) < 2 {
		t.Fatalf("routine picker index stages = %#v, want initial and recurring priority passes", options)
	}
	for index, option := range options {
		if !option.PriorityOnly || option.Full {
			t.Fatalf("routine picker index stage %d walked the whole corpus: %#v", index, option)
		}
	}
}

func shortenRefreshIntervals(t *testing.T) {
	t.Helper()
	previousInterval := fleetRefreshInterval
	previousParkThreshold := fleetRefreshParkThreshold
	previousParkPoll := fleetRefreshParkPollInterval
	previousCodexPoll := fleetRefreshCodexPollInterval
	fleetRefreshInterval = 10 * time.Millisecond
	fleetRefreshParkThreshold = 20 * time.Millisecond
	fleetRefreshParkPollInterval = 5 * time.Millisecond
	fleetRefreshCodexPollInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		fleetRefreshInterval = previousInterval
		fleetRefreshParkThreshold = previousParkThreshold
		fleetRefreshParkPollInterval = previousParkPoll
		fleetRefreshCodexPollInterval = previousCodexPoll
	})
}

func (runner *slowIndexRunner) Run(
	ctx context.Context,
	options fleetindex.Options,
) (fleetindex.Counters, error) {
	runner.mutex.Lock()
	runner.options = append(runner.options, options)
	runner.mutex.Unlock()
	blocked := false
	runner.once.Do(func() {
		close(runner.started)
		blocked = true
	})
	if blocked {
		select {
		case <-runner.release:
		case <-ctx.Done():
			return fleetindex.Counters{}, ctx.Err()
		}
	}
	return fleetindex.Counters{}, nil
}

func TestCachedFirstPaintWhileIndexRefreshIsSlow(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const rows = 5_000
	if err := database.Batch(
		context.Background(),
		rows,
		func(tx *store.ImmediateTx, start, end int) error {
			for index := start; index < end; index++ {
				if err := tx.UpsertTranscript(context.Background(), store.Transcript{
					UUID:        fmt.Sprintf("cached-%08d", index),
					Path:        fmt.Sprintf("/jail/cached-%08d.jsonl", index),
					Size:        1024,
					MTimeNS:     int64(index + 1),
					CWD:         cwd,
					FirstPrompt: fmt.Sprintf("cached row %08d", index),
					PromptCount: 1,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	limit := 2 * time.Second
	if strict {
		limit = 100 * time.Millisecond
	}
	request := scanRequest{Cache1H: true}
	started := time.Now()
	cached, err := scanFleetCached(context.Background(), database, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached.Snapshot.Rows) != 31 ||
		cached.Snapshot.KilledCount != 0 ||
		cached.Snapshot.SuppressedCount != rows-30 {
		t.Fatalf(
			"cached snapshot rows/killed/empty=%d/%d/%d, want 31/0/%d",
			len(cached.Snapshot.Rows),
			cached.Snapshot.KilledCount,
			cached.Snapshot.SuppressedCount,
			rows-30,
		)
	}
	scanReady := time.Since(started)
	modelStarted := time.Now()
	model := ui.NewModel(cached.Snapshot)
	modelReady := time.Since(modelStarted)
	viewStarted := time.Now()
	if view := model.View(); view.Content == "" {
		t.Fatal("cached first frame is empty")
	}
	viewReady := time.Since(viewStarted)
	firstPaint := time.Since(started)
	if firstPaint >= limit {
		t.Fatalf(
			"cached 5k first paint=%s (scan=%s model=%s view=%s), want <%s (strict=%t)",
			firstPaint,
			scanReady,
			modelReady,
			viewReady,
			limit,
			strict,
		)
	}

	runner := &slowIndexRunner{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	updates := make(chan ui.Snapshot, 1)
	refreshContext, cancel := context.WithCancel(context.Background())
	before := runtime.NumGoroutine()
	var stderr bytes.Buffer
	go streamFleetRefreshesWith(
		refreshContext,
		database,
		request,
		fleet.PrintWarn(&stderr),
		&stderr,
		updates,
		refreshDependencies{
			newIndexer: func(*store.Store) (indexRunner, error) {
				return runner, nil
			},
		},
	)
	gathered, ok := <-updates
	if !ok {
		t.Fatalf("refresh ended before gather: %s", stderr.String())
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow project index did not start asynchronously")
	}
	follow := model.SelectedKey()
	updated, command := model.Update(ui.RefreshMsg{Snapshot: gathered})
	if command != nil {
		t.Fatal("gather refresh returned a command")
	}
	model = updated.(ui.Model)
	if model.SelectedKey() != follow {
		t.Fatalf("gather refresh moved cursor from %q to %q", follow, model.SelectedKey())
	}
	close(runner.release)
	for {
		snapshot, ok := <-updates
		if !ok {
			t.Fatalf("refresh stream ended before indexed frame: %s", stderr.String())
		}
		updated, _ = model.Update(ui.RefreshMsg{Snapshot: snapshot})
		model = updated.(ui.Model)
		if model.SelectedKey() != follow {
			t.Fatalf("index refresh moved cursor from %q to %q", follow, model.SelectedKey())
		}
		if !snapshot.Refreshing {
			break
		}
	}
	cancel()
	for range updates {
	}
	runner.mutex.Lock()
	options := append([]fleetindex.Options(nil), runner.options...)
	runner.mutex.Unlock()
	if len(options) != 1 ||
		!options[0].PriorityOnly ||
		options[0].PriorityCWD != cwd {
		t.Fatalf("async index stages = %#v", options)
	}
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("async refresh leaked goroutines: before=%d after=%d", before, after)
	}
	t.Logf(
		"STRESS cached_rows=%d first_paint=%s scan=%s model=%s view=%s slow_index_async=true refreshes=3 priority_only=true cursor_follow=true goroutines=%d/%d strict=%t",
		rows,
		firstPaint,
		scanReady,
		modelReady,
		viewReady,
		before,
		after,
		strict,
	)
}

func TestAsyncCallerRefreshStormPreservesCursorAndGoroutines(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const rows = 256
	if err := database.Batch(
		context.Background(),
		rows,
		func(tx *store.ImmediateTx, start, end int) error {
			for index := start; index < end; index++ {
				if err := tx.UpsertTranscript(context.Background(), store.Transcript{
					UUID:        fmt.Sprintf("storm-%08d", index),
					Path:        fmt.Sprintf("/jail/storm-%08d.jsonl", index),
					Size:        1024,
					MTimeNS:     int64(index + 1),
					CWD:         cwd,
					FirstPrompt: fmt.Sprintf("storm row %08d", index),
					PromptCount: 1,
				}); err != nil {
					return err
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	request := scanRequest{}
	cached, err := scanFleetCached(context.Background(), database, request)
	if err != nil {
		t.Fatal(err)
	}
	cached.Snapshot.InitialCursorID = cached.Snapshot.Rows[len(cached.Snapshot.Rows)/2].ID
	model := ui.NewModel(cached.Snapshot)
	follow := model.SelectedKey()
	before := runtime.NumGoroutine()
	const storms = 100
	for storm := 0; storm < storms; storm++ {
		stormContext, cancelStorm := context.WithCancel(context.Background())
		updates := make(chan ui.Snapshot, 1)
		stormStderr := &bytes.Buffer{}
		go streamFleetRefreshesWith(
			stormContext,
			database,
			request,
			fleet.PrintWarn(stormStderr),
			stormStderr,
			updates,
			refreshDependencies{
				newIndexer: func(*store.Store) (indexRunner, error) {
					return &immediateIndexRunner{}, nil
				},
			},
		)
		for {
			snapshot, ok := <-updates
			if !ok {
				t.Fatalf("storm %d refresh stream ended early: %s", storm, stormStderr.String())
			}
			updated, command := model.Update(ui.RefreshMsg{Snapshot: snapshot})
			if command != nil {
				t.Fatalf("storm %d refresh returned command", storm)
			}
			model = updated.(ui.Model)
			if model.SelectedKey() != follow {
				t.Fatalf(
					"storm %d moved cursor from %q to %q",
					storm,
					follow,
					model.SelectedKey(),
				)
			}
			if !snapshot.Refreshing {
				break
			}
		}
		cancelStorm()
		for range updates {
		}
	}
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Fatalf("refresh storm leaked goroutines: before=%d after=%d", before, after)
	}
	t.Logf(
		"STRESS async_caller_storms=%d messages=%d cursor_follow=true goroutines=%d/%d",
		storms,
		storms*3,
		before,
		after,
	)
}

// TestInternalPrimarySetGetDispatch is F5's regression test: the retired
// TestShimLaunchPosture (shim/shim_test.go) was the only place that ran a
// primary-account switch through `pfm internal primary-set` / `primary-get`
// end to end, and it drove those subcommands through a HAND-WRITTEN fake
// shell-script "pfm" fixture, never the real Go dispatch — the shell
// functions it exercised (_cc_run, cc-swap, _cc_primary) are exactly what
// this PR retires. TestPrimaryAccountGoesThroughTheStateStore above already
// proves fleet.SetPrimaryAccount/fleet.PrimaryAccount persist through the shared
// store; this test proves the thin CLI argv layer around them — runInternal
// itself, main.go's "primary-set" (~419-450) and "primary-get" (~421)
// cases — actually reaches those functions with the right parsing, output,
// and exit codes.
func TestInternalPrimarySetGetDispatch(t *testing.T) {
	home := t.TempDir()
	values := paths.Values{
		Home:     home,
		SharedDB: filepath.Join(home, ".cc", "fleet.db"),
	}
	machine := config.Defaults(home, []string{
		filepath.Join(home, ".cc", "1", "projects"),
		filepath.Join(home, ".cc", "2", "projects"),
	})
	rt := commandRuntime{Config: machine, Paths: values}

	var stdout, stderr bytes.Buffer
	if code := runInternal([]string{"primary-set", "2"}, &stdout, &stderr, rt); code != 0 {
		t.Fatalf("runInternal(primary-set 2) = %d, stderr=%q", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("primary-set printed to stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runInternal([]string{"primary-get"}, &stdout, &stderr, rt); code != 0 {
		t.Fatalf("runInternal(primary-get) = %d, stderr=%q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "2" {
		t.Fatalf("primary-get printed %q, want the persisted account 2", got)
	}

	// An off-roster account is refused, and refused BEFORE anything
	// persists — the CLI reports the failure rather than the shim's old
	// fail-open silence.
	stdout.Reset()
	stderr.Reset()
	if code := runInternal([]string{"primary-set", "9"}, &stdout, &stderr, rt); code == 0 {
		t.Fatalf("runInternal(primary-set 9) accepted an off-roster account")
	} else if !strings.Contains(stderr.String(), "primary-set") {
		t.Fatalf("primary-set failure did not name itself: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runInternal([]string{"primary-get"}, &stdout, &stderr, rt); code != 0 {
		t.Fatalf("runInternal(primary-get) after refused switch = %d, stderr=%q", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "2" {
		t.Fatalf("primary-get after a refused switch = %q, want the still-persisted account 2", got)
	}
}

// TestPrimaryWritebackIgnoresTheUnsetSentinel fixtures the crash `pfm ls`
// hit live: 0 is ui.Outcome's zero value for PrimaryAccount, never a real
// account (accounts start at 1), so it must never reach fleet.SetPrimaryAccount
// — which correctly rejects it, but aborting the whole `pfm ls` run over a
// value nobody chose is the bug. A cancelled picker and a no-op reselect of
// the already-current account must skip the write for the same reason: there
// is nothing deliberate to persist.
func TestPrimaryWritebackIgnoresTheUnsetSentinel(t *testing.T) {
	cases := []struct {
		name        string
		kind        ui.OutcomeKind
		account     int
		current     int
		wantShould  bool
		wantAccount int
	}{
		{"unset sentinel on an otherwise-real outcome", ui.OutcomeSelected, 0, 2, false, 0},
		{"cancelled never writes, even with a real account", ui.OutcomeCancelled, 3, 1, false, 0},
		{"unchanged primary has nothing to persist", ui.OutcomeSelected, 2, 2, false, 0},
		{"a deliberate switch persists", ui.OutcomeSelected, 3, 1, true, 3},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			account, should := primaryWriteback(testCase.kind, testCase.account, testCase.current)
			if should != testCase.wantShould || account != testCase.wantAccount {
				t.Fatalf(
					"primaryWriteback(%v, %d, %d) = (%d, %v), want (%d, %v)",
					testCase.kind, testCase.account, testCase.current,
					account, should,
					testCase.wantAccount, testCase.wantShould,
				)
			}
		})
	}
}

// TestPrimaryWritebackSentinelNeverHitsTheRosterCheck proves the failure mode
// end to end: routing the unset sentinel through fleet.SetPrimaryAccount (what
// runLS did before primaryWriteback existed) reproduces the exact live
// error, "primary account 0 is not in the configured roster" — and confirms
// primaryWriteback's whole point is keeping that call from ever happening.
func TestPrimaryWritebackSentinelNeverHitsTheRosterCheck(t *testing.T) {
	home := t.TempDir()
	values := paths.Values{Home: home, SharedDB: filepath.Join(home, ".cc", "fleet.db")}
	machine := config.Defaults(home, []string{
		filepath.Join(home, ".cc", "1", "projects"),
		filepath.Join(home, ".cc", "2", "projects"),
		filepath.Join(home, ".cc", "3", "projects"),
	})

	if err := fleet.SetPrimaryAccount(values, machine, 0); err == nil {
		t.Fatal("fleet.SetPrimaryAccount(0) accepted the sentinel — roster check regressed")
	}

	if _, should := primaryWriteback(ui.OutcomeSelected, 0, fleet.PrimaryAccount(values, machine)); should {
		t.Fatal("primaryWriteback let the unset sentinel through — runLS would still crash")
	}
}
