package reload

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
)

// TestRunRecordsEveryStateTransition: the reload coordinator walks its states
// through the state door — one comp=state record per transition with the
// prior and next state — and a refused run ends in `failed` at ERROR. Pane
// captures never reach the file.
func TestRunRecordsEveryStateTransition(t *testing.T) {
	ctx, recorder := obs.Test(t)
	tmux := &fakeReloadTmux{}
	if _, err := Run(ctx, Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-reload", Pane: "%7", PanePID: 700,
		SessionID: "11111111-1111-4111-8111-111111111111", CWD: "/jail/project", Account: 2, AccountIDs: []int{2},
		Machine: reloadTestMachine("", "/jail/home"),
	}, Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, nil); err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "state" {
			t.Fatalf("comp = %v, want state", comp)
		}
		if kind, _ := record.Field("kind"); kind != "reload" {
			t.Fatalf("kind = %v, want reload", kind)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
		next, _ := record.Field("next")
		path = append(path, next.(string))
	}
	want := "locked,idle,exit-typed,dead,respawned,done"
	if got := strings.Join(path, ","); got != want {
		t.Fatalf("state path = %s, want %s: %s", got, want, recorder.Raw())
	}
	if strings.Contains(recorder.Raw(), "❯") || strings.Contains(recorder.Raw(), "Claude\n") {
		t.Fatalf("pane content reached the file: %s", recorder.Raw())
	}

	refused := &fakeReloadTmux{}
	_, err := Run(ctx, Request{
		Engine:     pfmengine.OpenCode,
		SocketPath: "/tmp/ox-session",
		Pane:       "%7",
		PanePID:    700,
		Account:    1,
		AccountIDs: []int{1},
		CWD:        "/work",
	}, Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 1}, refused, nil, nil)
	if err == nil {
		t.Fatal("OpenCode reload was not refused")
	}
	records := recorder.Records()
	last := records[len(records)-1]
	if next, _ := last.Field("next"); last.Message != "state.transition" || next != "failed" || last.Level != "ERROR" {
		t.Fatalf("a refused run did not end failed at ERROR: %v", last)
	}
	if prior, _ := last.Field("prior"); prior != "requested" {
		t.Fatalf("failure attributed to %v, want requested (refused before any state was reached)", prior)
	}
}
