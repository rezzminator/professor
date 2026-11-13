package reload

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	scheduleOldID = "11111111-1111-4111-8111-111111111111"
	scheduleMidID = "22222222-2222-4222-8222-222222222222"
)

// scheduleJail points the shared state database at a fresh temp file.
func scheduleJail(t *testing.T) {
	t.Helper()
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
}

func openScheduleState(t *testing.T) *fleetdb.Store {
	t.Helper()
	values, err := pfmconfig.ResolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	return state
}

func seedSchedule(t *testing.T, sessionID string, engine pfmengine.ID) {
	t.Helper()
	if _, err := openScheduleState(t).CreateSchedule(context.Background(), fleetdb.Schedule{
		SessionID: sessionID, Engine: string(engine), Prompt: "check the build",
		Interval: time.Hour, Created: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

// scheduleSessions is the session id of every stored schedule.
func scheduleSessions(t *testing.T) []string {
	t.Helper()
	schedules, err := openScheduleState(t).Schedules(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range schedules {
		ids = append(ids, schedules[i].SessionID)
	}
	return ids
}

// TestRunNewCodexTellsTheSchedulesStayBehind pins the one door with no new id
// to move to: a Codex --new conversation has none until it answers.
func TestRunNewCodexTellsTheSchedulesStayBehind(t *testing.T) {
	scheduleJail(t)
	seedSchedule(t, scheduleOldID, pfmengine.Codex)
	dir := t.TempDir()
	request := Request{
		Engine: pfmengine.Codex, SocketPath: "/tmp/cx-schedule", Pane: "%7",
		New: true, LeftBehind: scheduleOldID, CWD: dir, Account: 1, AccountIDs: []int{1},
		Machine: pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: dir}}},
	}
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	want := "1 schedule(s) of session " + scheduleOldID + " stay on the conversation left behind"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q lacks %q", stderr.String(), want)
	}
	if got := scheduleSessions(t); len(got) != 1 || got[0] != scheduleOldID {
		t.Fatalf("schedules on %v, want on %s", got, scheduleOldID)
	}
}
