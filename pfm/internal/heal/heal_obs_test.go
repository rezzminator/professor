package heal

import (
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestHealRecordsEveryForeignStatement: Sweep's reads against the state and
// history stores, and Delete's three-table removal, each write one comp=db
// record under kind=codex — verb and table only, never the rollout path or
// the thread id bound into the statement.
func TestHealRecordsEveryForeignStatement(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)

	ctx, recorder := obs.Test(t)
	if _, err := Sweep(ctx, jail.stores, ""); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if err := Delete(ctx, jail.stores, wedged); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	counts := map[string]int{}
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "codex" {
			t.Fatalf("kind = %v, want codex", kind)
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		counts[op.(string)+" "+table.(string)]++
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	for _, want := range []string{
		"select thread_history_projection_state",
		"select threads",
		"delete thread_history_projection_state",
		"delete thread_items",
		"delete thread_turns",
	} {
		if counts[want] == 0 {
			t.Fatalf("no record for %q; saw %v in %s", want, counts, recorder.Raw())
		}
	}
	// db.open already records its own store path by design (obs.SQLOpen);
	// what must never appear is the bound thread id DELETE binds by argument.
	if strings.Contains(recorder.Raw(), wedged) {
		t.Fatalf("a bound thread id reached the file: %s", recorder.Raw())
	}
}

// TestRunRecordsTheHealsStateTransitions: the heal coordinator walks
// requested → scanned → planned → repaired → done through the state door,
// and Delete records its own present → deleted transition for the thread it
// heals.
func TestRunRecordsTheHealsStateTransitions(t *testing.T) {
	jail := newCodexJail(t)
	const (
		healthy = "22222222-2222-4222-8222-222222222222"
		wedged  = "33333333-3333-4333-8333-333333333333"
	)
	healthyOffsets := jail.addThread(t, healthy, 3)
	wedgedOffsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, healthy, healthyOffsets[1], 1)
	jail.setCursor(t, wedged, wedgedOffsets[2], 1)

	runner, err := New(jail.root, func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}

	ctx, recorder := obs.Test(t)
	if _, err := runner.Run(ctx, Options{Apply: true}); err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}

	var path []string
	deleted := false
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		kind, _ := record.Field("kind")
		if kind != "heal" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "state" {
			t.Fatalf("comp = %v, want state", comp)
		}
		prior, _ := record.Field("prior")
		next, _ := record.Field("next")
		if prior == "present" && next == "deleted" {
			deleted = true
			continue
		}
		path = append(path, next.(string))
	}
	if got := strings.Join(path, ","); got != "scanned,planned,repaired,done" {
		t.Fatalf("state path = %s: %s", got, recorder.Raw())
	}
	if !deleted {
		t.Fatalf("no present->deleted record for the healed thread: %s", recorder.Raw())
	}
}
