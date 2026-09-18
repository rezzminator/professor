package reap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

type quietBusy struct{}

func (quietBusy) BusySessions(context.Context) (map[string]struct{}, error) { return nil, nil }

// TestRunRecordsTheSweepsStateTransitions: the reap coordinator walks
// probed → planned → applied → done through the state door, one comp=state
// record per phase with dur_ms; a sweep of an empty fleet has no decision
// records because it made no decision.
func TestRunRecordsTheSweepsStateTransitions(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	values := paths.Values{
		TmuxDir: filepath.Join(root, "tmux"), SIDDir: filepath.Join(root, "sid"), ProcRoot: filepath.Join(root, "proc"),
		FleetDB: filepath.Join(root, "fleet.db"), Home: filepath.Join(root, "home"),
	}
	for _, dir := range []string{values.TmuxDir, values.SIDDir, values.ProcRoot} {
		if err := mkdir(dir); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := New(Dependencies{
		Paths: values, Tmux: fakeClientIdleTmux{}, Proc: gather.NewProcFS(values.ProcRoot), Busy: quietBusy{},
		KillServer: func(context.Context, string) error { return nil }, Now: func() time.Time { return time.Unix(600, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message != "state.transition" || kind != "reap" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "state" {
			t.Fatalf("comp = %v", comp)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
		next, _ := record.Field("next")
		path = append(path, next.(string))
	}
	if got := strings.Join(path, ","); got != "probed,planned,applied,done" {
		t.Fatalf("state path = %s: %s", got, recorder.Raw())
	}
}

func mkdir(dir string) error { return os.MkdirAll(dir, 0o700) }
