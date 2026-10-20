package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The caller seam supplies only the transport outcome. The real deliverer,
// transcript parser, schedule store and fire loop decide whether a wake fired.
func scheduleProofRig(t *testing.T) (*chatDeliverer, *fleetdb.Store, fleetdb.Schedule, string) {
	t.Helper()
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	const id = "77777777-7777-4777-8777-777777777777"
	path := filepath.Join(values.Roots[pfmengine.Claude][0], "-fixture", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(
			`{"type":"user","sessionId":"`+id+`","cwd":"/fixture","message":{"role":"user","content":"original request"}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	store := fleetdb.OpenSharedState(context.Background(), values)
	if err := store.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC().Add(-time.Hour)
	key, err := store.CreateSchedule(context.Background(), fleetdb.Schedule{
		SessionID: id, Engine: "claude", Prompt: "check the build", Once: true,
		Interval: time.Minute, Created: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := mustSchedule(t, store, key)
	d := newChatDeliverer(&pfmconfig.Runtime{Paths: values}, &bytes.Buffer{}, &bytes.Buffer{})
	d.open = func(context.Context, string) (action.OpenResult, error) {
		return action.OpenResult{State: "live"}, nil
	}
	d.unhide = func(context.Context, string) (bool, error) { return false, nil }
	return d, store, r, path
}

func appendSchedulePrompt(t *testing.T, path, text string) {
	t.Helper()
	line, err := json.Marshal(
		map[string]any{"type": "user", "message": map[string]string{"role": "user", "content": text}},
	)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(line, '\n'))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleUnprovenSubmissionNeedsTranscriptProof(t *testing.T) {
	for _, test := range []struct {
		name      string
		result    pfmcall.Result
		proof     bool
		wantFired bool
	}{
		{name: "unproven is not delivered", result: pfmcall.Result{Status: "delivered_unproven", Typed: true}},
		{name: "unproven with transcript", result: pfmcall.Result{Status: "delivered_unproven", Typed: true}, proof: true, wantFired: true},
		{name: "uncertain submit with transcript", result: pfmcall.Result{Status: "typed_unconfirmed", Typed: true, Code: pfmcall.CodeUndelivered}, proof: true, wantFired: true},
		{name: "queued counts as delivered", result: pfmcall.Result{Status: "queued", Typed: true}, wantFired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, store, r, path := scheduleProofRig(t)
			calls := 0
			d.call = func(_ context.Context, _ fleetdb.Schedule, request pfmcall.Request) (pfmcall.Result, error) {
				calls++
				if test.proof {
					appendSchedulePrompt(t, path, request.Message)
				}
				return test.result, nil
			}
			report, err := Fire(context.Background(), store, d, filepath.Join(t.TempDir(), "fire.lock"), time.Now())
			if err != nil || (len(report.Fired) == 1) != test.wantFired || calls != 1 {
				t.Fatalf("Fire=%+v err=%v calls=%d; want fired=%v", report, err, calls, test.wantFired)
			}
			got := mustSchedule(t, store, r.ID)
			if got.LastFired.IsZero() == test.wantFired {
				t.Fatalf("schedule consumed without expected proof: %+v", got)
			}
		})
	}
}

func TestScheduleRetryFindsPriorSubmissionBeforeTyping(t *testing.T) {
	for _, status := range []string{"delivered_unproven", "typed_unconfirmed", "reschedule_failed"} {
		t.Run(status, func(t *testing.T) {
			d, store, r, path := scheduleProofRig(t)
			calls := 0
			d.call = func(_ context.Context, _ fleetdb.Schedule, request pfmcall.Request) (pfmcall.Result, error) {
				calls++
				result := pfmcall.Result{Status: status, Typed: true}
				if status == "typed_unconfirmed" {
					result.Code = pfmcall.CodeUndelivered
				}
				if status == "reschedule_failed" {
					result.Status = "delivered"
					appendSchedulePrompt(t, path, request.Message)
				}
				return result, nil
			}
			lock := filepath.Join(t.TempDir(), "fire.lock")
			var firstStore Store = store
			if status == "reschedule_failed" {
				firstStore = markFailingStore{Store: store, err: errors.New("disk temporarily unavailable")}
			}
			_, err := Fire(context.Background(), firstStore, d, lock, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if status != "reschedule_failed" {
				appendSchedulePrompt(t, path, Message(r))
			}
			report, err := Fire(context.Background(), store, d, lock, time.Now().Add(5*time.Minute))
			if err != nil || len(report.Fired) != 1 || calls != 1 {
				t.Fatalf("retry=%+v err=%v submissions=%d; want mark-only, one total submission", report, err, calls)
			}
		})
	}
}

func TestScheduleUnreadableProofIsVisibleAndNeverRetyped(t *testing.T) {
	d, store, r, path := scheduleProofRig(t)
	ctx, activity := obs.Test(t)
	calls := 0
	d.call = func(context.Context, fleetdb.Schedule, pfmcall.Request) (pfmcall.Result, error) {
		calls++
		return pfmcall.Result{Status: "typed_unconfirmed", Code: pfmcall.CodeUndelivered, Typed: true}, nil
	}
	lock := filepath.Join(t.TempDir(), "fire.lock")
	if _, err := Fire(ctx, store, d, lock, time.Now()); err != nil {
		t.Fatal(err)
	}
	// A directory is unreadable as a transcript even when the fence runs as root.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := Fire(ctx, store, d, lock, time.Now().Add(5*time.Minute))
	if err != nil || len(report.Failed) != 1 || calls != 1 {
		t.Fatalf("unreadable proof retry=%+v err=%v typed=%d; want stays due, no duplicate", report, err, calls)
	}
	got := mustSchedule(t, store, r.ID)
	var listed bytes.Buffer
	if err := writeScheduleTable(&listed, []fleetdb.Schedule{got}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.String(), "unproven since") ||
		!strings.Contains(listed.String(), "transcript unreadable") {
		t.Fatalf("schedule ls hid the stuck wake: %s", listed.String())
	}
	logged := false
	for _, record := range activity.Records() {
		chat, _ := record.Field(obs.FieldChat)
		logged = logged ||
			record.Level == "ERROR" && chat == r.SessionID && strings.Contains(record.Message, "schedule 1") &&
				strings.Contains(record.Message, "transcript unreadable")
	}
	if !logged {
		t.Fatalf("activity log hid unproven schedule %d: %s", r.ID, activity.Raw())
	}
}

func TestScheduleReadableMissingTagRetriesAfterOneTick(t *testing.T) {
	d, store, _, _ := scheduleProofRig(t)
	calls := 0
	d.call = func(_ context.Context, _ fleetdb.Schedule, request pfmcall.Request) (pfmcall.Result, error) {
		calls++
		if calls == 1 {
			return pfmcall.Result{Status: "delivered_unproven", Typed: true}, nil
		}
		if !request.RequireIdle {
			t.Fatal("retry could queue a duplicate into a busy target instead of requiring an idle empty composer")
		}
		return pfmcall.Result{Status: "delivered", Typed: true}, nil
	}
	lock, now := filepath.Join(t.TempDir(), "fire.lock"), time.Now()
	ctx, _ := obs.Test(t, obs.WithTestClock(clock.NewFake(now)))
	for _, tick := range []time.Time{now, now.Add(time.Minute)} {
		report, err := Fire(ctx, store, d, lock, tick)
		if err != nil || len(report.Failed) != 1 || calls != 1 {
			t.Fatalf("early retry=%+v err=%v calls=%d; want stays due, one attempted submission", report, err, calls)
		}
	}
	report, err := Fire(ctx, store, d, lock, now.Add(5*time.Minute))
	if err != nil || len(report.Fired) != 1 || calls != 2 {
		t.Fatalf("readable missing-tag retry=%+v err=%v calls=%d; want one idle-only retry", report, err, calls)
	}
	report, err = Fire(ctx, store, d, lock, now.Add(10*time.Minute))
	if err != nil || len(report.Fired) != 0 || calls != 2 {
		t.Fatalf("completed wake typed again: report=%+v err=%v calls=%d", report, err, calls)
	}
}

func TestScheduleBusyRetryStaysDueUntilProofArrives(t *testing.T) {
	d, store, r, path := scheduleProofRig(t)
	calls := 0
	d.call = func(_ context.Context, _ fleetdb.Schedule, request pfmcall.Request) (pfmcall.Result, error) {
		calls++
		if calls == 1 {
			return pfmcall.Result{Status: "typed_unconfirmed", Code: pfmcall.CodeUndelivered, Typed: true}, nil
		}
		if !request.RequireIdle {
			t.Fatal("busy retry was permitted to queue")
		}
		return pfmcall.Result{Code: pfmcall.CodeBusy, Busy: true, Message: "target is working"}, nil
	}
	lock, now := filepath.Join(t.TempDir(), "fire.lock"), time.Now()
	ctx, _ := obs.Test(t, obs.WithTestClock(clock.NewFake(now)))
	for _, tick := range []time.Time{now, now.Add(5 * time.Minute), now.Add(10 * time.Minute)} {
		report, err := Fire(ctx, store, d, lock, tick)
		if err != nil || len(report.Failed) != 1 || len(report.Fired) != 0 {
			t.Fatalf("busy/unconfirmed wake=%+v err=%v; want stays due", report, err)
		}
	}
	if got := mustSchedule(
		t,
		store,
		r.ID,
	); !strings.Contains(got.LastError, "unproven since") ||
		!strings.Contains(got.LastError, "target is working") {
		t.Fatalf("busy stuck state hidden: %+v", got)
	}
	appendSchedulePrompt(t, path, Message(r))
	report, err := Fire(ctx, store, d, lock, now.Add(15*time.Minute))
	if err != nil || len(report.Fired) != 1 || calls != 3 {
		t.Fatalf("late transcript proof=%+v err=%v calls=%d; want mark-only", report, err, calls)
	}
}

func TestScheduleRetryGraceStartsAtSubmission(t *testing.T) {
	d, store, r, _ := scheduleProofRig(t)
	now := time.Now()
	timing := clock.NewFake(now)
	ctx, _ := obs.Test(t, obs.WithTestClock(timing))
	opened := false
	d.open = func(context.Context, string) (action.OpenResult, error) {
		if !opened {
			timing.Advance(4 * time.Minute)
			opened = true
		}
		return action.OpenResult{State: "live"}, nil
	}
	calls := 0
	d.call = func(context.Context, fleetdb.Schedule, pfmcall.Request) (pfmcall.Result, error) {
		calls++
		return pfmcall.Result{Status: "delivered_unproven", Typed: true}, nil
	}
	lock := filepath.Join(t.TempDir(), "fire.lock")
	if _, err := Fire(ctx, store, d, lock, now); err != nil {
		t.Fatal(err)
	}
	receipt, err := readReceipt(scheduleReceiptPath(d.runtime.Paths.StateDB, r.ID), fireTag(r))
	if err != nil || !receipt.Attempted.Equal(now.Add(4*time.Minute)) {
		t.Fatalf(
			"attempt timestamp=%s err=%v; want actual submission at %s",
			receipt.Attempted,
			err,
			now.Add(4*time.Minute),
		)
	}
	timing.Advance(time.Minute)
	if _, err := Fire(ctx, store, d, lock, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("retried after only one minute since submission: %d calls", calls)
	}
}

func TestScheduleTranscriptProofSurvivesCorruptReceipt(t *testing.T) {
	d, store, r, path := scheduleProofRig(t)
	appendSchedulePrompt(t, path, Message(r))
	receiptPath := scheduleReceiptPath(d.runtime.Paths.StateDB, r.ID)
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	d.call = func(context.Context, fleetdb.Schedule, pfmcall.Request) (pfmcall.Result, error) {
		t.Fatal("transcript-proven wake was submitted again")
		return pfmcall.Result{}, nil
	}
	report, err := Fire(context.Background(), store, d, filepath.Join(t.TempDir(), "fire.lock"), time.Now())
	if err != nil || len(report.Fired) != 1 {
		t.Fatalf("proven wake stuck on corrupt receipt: report=%+v err=%v", report, err)
	}
}

func TestRemovingUnprovenScheduleRemovesItsReceipt(t *testing.T) {
	d, store, r, _ := scheduleProofRig(t)
	d.call = func(context.Context, fleetdb.Schedule, pfmcall.Request) (pfmcall.Result, error) {
		return pfmcall.Result{Status: "typed_unconfirmed", Code: pfmcall.CodeUndelivered, Typed: true}, nil
	}
	if err := d.Deliver(context.Background(), r, Message(r)); err == nil {
		t.Fatal("uncertain wake was consumed")
	}
	path := scheduleReceiptPath(d.runtime.Paths.StateDB, r.ID)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("attempt was not persisted: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runScheduleRm([]string{fmt.Sprint(r.ID)}, &stdout, &stderr, d.runtime); code != 0 {
		t.Fatalf("rm code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, exists, err := store.Schedule(context.Background(), r.ID); err != nil || exists {
		t.Fatalf("schedule survived rm: exists=%v err=%v", exists, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt survived rm: %v", err)
	}
}
