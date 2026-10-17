package picker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// versionedLedger is a comms reader with a change stamp, counting its reads.
type versionedLedger struct {
	events     []fleetdb.CommsEvent
	stamp      fleetdb.CommsStamp
	stampErr   error
	reads      int
	stampReads int
}

func (ledger *versionedLedger) CommsSince(_ context.Context, sinceNS int64, limit int) ([]fleetdb.CommsEvent, error) {
	ledger.reads++
	var out []fleetdb.CommsEvent
	for index := range ledger.events {
		if ledger.events[index].AtNS >= sinceNS && len(out) < limit {
			out = append(out, ledger.events[index])
		}
	}
	return out, nil
}

func (ledger *versionedLedger) CommsVersion(context.Context) (fleetdb.CommsStamp, error) {
	ledger.stampReads++
	return ledger.stamp, ledger.stampErr
}

// plainLedger is a comms reader with no change stamp.
type plainLedger struct{ reads int }

func (ledger *plainLedger) CommsSince(context.Context, int64, int) ([]fleetdb.CommsEvent, error) {
	ledger.reads++
	return nil, nil
}

func atTimes(times ...int64) []fleetdb.CommsEvent {
	events := make([]fleetdb.CommsEvent, 0, len(times))
	for _, at := range times {
		events = append(events, fleetdb.CommsEvent{AtNS: at})
	}
	return events
}

// TestCosmosSamplerReadsTheLedgerOnlyWhenItsStampMoved pins the removed work:
// a 24h ledger read every two seconds for a ledger nobody wrote to. An
// unchanged stamp serves the last read, narrowed to the window that has slid
// since; a moved stamp reads again, so a new event shows within one sample.
func TestCosmosSamplerReadsTheLedgerOnlyWhenItsStampMoved(t *testing.T) {
	ctx := context.Background()
	ledger := &versionedLedger{events: atTimes(30, 20, 10), stamp: fleetdb.CommsStamp{DataVersion: 1}}
	sampler := &cosmosSampler{reader: ledger}

	first, err := sampler.Sample(ctx, 5)
	if err != nil || len(first) != 3 || ledger.reads != 1 {
		t.Fatalf("first sample = %d events, err %v, reads %d; want 3 events from 1 read", len(first), err, ledger.reads)
	}
	again, err := sampler.Sample(ctx, 5)
	if err != nil || len(again) != 3 || ledger.reads != 1 {
		t.Fatalf(
			"unchanged ledger: %d events, err %v, reads %d; want the last read, no new read",
			len(again),
			err,
			ledger.reads,
		)
	}
	slid, err := sampler.Sample(ctx, 15)
	if err != nil || len(slid) != 2 || slid[0].AtNS != 30 || slid[1].AtNS != 20 || ledger.reads != 1 {
		t.Fatalf("slid window: %+v err %v reads %d; want events 30, 20 and no new read", slid, err, ledger.reads)
	}

	ledger.events = atTimes(40, 30, 20, 10)
	ledger.stamp = fleetdb.CommsStamp{DataVersion: 2}
	moved, err := sampler.Sample(ctx, 5)
	if err != nil || len(moved) != 4 || moved[0].AtNS != 40 || ledger.reads != 2 {
		t.Fatalf("moved stamp: %+v err %v reads %d; want the new event from a second read", moved, err, ledger.reads)
	}
	ledger.stamp = fleetdb.CommsStamp{DataVersion: 2, OwnWrites: 1}
	if _, err := sampler.Sample(ctx, 5); err != nil || ledger.reads != 3 {
		t.Fatalf("a write of the store's own: err %v reads %d; want a third read", err, ledger.reads)
	}
}

// TestCosmosSamplerWithoutAStampReadsEveryTime keeps a reader that cannot say
// whether it changed on the plain read: no gate is better than a wrong one.
func TestCosmosSamplerWithoutAStampReadsEveryTime(t *testing.T) {
	reader := &plainLedger{}
	sampler := &cosmosSampler{reader: reader}
	for range 3 {
		if _, err := sampler.Sample(context.Background(), 0); err != nil {
			t.Fatalf("sample: %v", err)
		}
	}
	if reader.reads != 3 {
		t.Fatalf("an unstamped reader was read %d times for 3 samples, want 3", reader.reads)
	}
}

// TestCosmosSamplerNamesAStampFailureInsteadOfServingTheCache pins that a
// ledger whose change stamp cannot be read is reported, never answered from a
// cache that may be stale.
func TestCosmosSamplerNamesAStampFailureInsteadOfServingTheCache(t *testing.T) {
	ctx := context.Background()
	ledger := &versionedLedger{events: atTimes(10)}
	sampler := &cosmosSampler{reader: ledger}
	if _, err := sampler.Sample(ctx, 0); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	ledger.stampErr = errors.New("database is locked")
	events, err := sampler.Sample(ctx, 0)
	if err == nil || !strings.Contains(err.Error(), "database is locked") || events != nil {
		t.Fatalf("sample = %+v, %v; want the stamp failure named and no events", events, err)
	}
}

// TestCosmosSamplerSeesAnotherProcessesEventsOnTheRealLedger runs the gate over
// the real WAL-mode ledger: a second connection (another pfm process) records
// an event and the very next sample returns it with no checkpoint between (the
// WAL-truncate half of the contract is pinned on the stamp itself, in fleetdb).
func TestCosmosSamplerSeesAnotherProcessesEventsOnTheRealLedger(t *testing.T) {
	ctx := context.Background()
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "cc", "pfm.db"), Home: t.TempDir()}
	reader := fleetdb.OpenSharedState(ctx, values)
	writer := fleetdb.OpenSharedState(ctx, values)
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close reading ledger: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Errorf("close writing ledger: %v", err)
		}
	})
	for _, state := range []*fleetdb.Store{reader, writer} {
		if err := state.Degraded(); err != nil {
			t.Fatalf("open the ledger: %v", err)
		}
	}
	sampler := &cosmosSampler{reader: reader}
	record := func(at int64) {
		t.Helper()
		event := fleetdb.CommsEvent{AtNS: at, Kind: fleetdb.KindCall, Target: "t", Message: "m"}
		if err := writer.RecordComms(ctx, event); err != nil {
			t.Fatalf("record comms from the other connection: %v", err)
		}
	}
	sample := func(want int) {
		t.Helper()
		events, err := sampler.Sample(ctx, 0)
		if err != nil || len(events) != want {
			t.Fatalf("sample = %d events, %v; want %d", len(events), err, want)
		}
	}

	sample(0)
	record(10)
	sample(1)
	record(20)
	sample(2)
	record(30)
	sample(3)
}
