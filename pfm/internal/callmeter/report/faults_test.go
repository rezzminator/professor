package report

import (
	"context"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

func TestFaultsCountsByStageAndStatusThenLatest(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	for i, stage := range []string{callmeter.StagePayload, callmeter.StagePayload, callmeter.StageParse} {
		if err := store.AddFault(ctx, callmeter.Fault{
			TS: ms(time.Duration(3-i) * time.Hour), Stage: stage, Error: stage + " broke",
		}); err != nil {
			t.Fatalf("AddFault: %v", err)
		}
	}
	seed(t, store, bash("b1", "s", "", ms(time.Hour), workDir(t), "node -e 'x'", 5))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Faults(ctx, store, Filter{Limit: 2}, nil)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	counts := map[string]string{}
	var latest []string
	for _, row := range table.Rows {
		switch row[0] {
		case "stage", "parse":
			counts[row[0]+":"+row[1]] = row[2]
		case "fault":
			latest = append(latest, row[6])
		}
	}
	for key, want := range map[string]string{"stage:payload": "2", "stage:parse": "1", "stage:store": "0", "parse:unparsed": "1"} {
		if counts[key] != want {
			t.Errorf("count %s = %q, want %s (rows %v)", key, counts[key], want, table.Rows)
		}
	}
	if len(latest) != 2 || latest[0] != "parse broke" {
		t.Errorf("latest faults = %v, want 2, newest first", latest)
	}
}

func TestFaultsEmptyWindow(t *testing.T) {
	table, err := Faults(context.Background(), openStore(t), Filter{}, nil)
	if err != nil {
		t.Fatalf("Faults: %v", err)
	}
	if len(table.Rows) != 0 {
		t.Errorf("empty store rows = %v, want none", table.Rows)
	}
}
