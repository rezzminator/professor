package reload

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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

func claudeScheduleRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-schedule", Pane: "%7",
		CWD: "/jail/project", Account: 2, AccountIDs: []int{2},
		Machine: reloadTestMachine("", t.TempDir()),
	}
}

func TestRunNewCarriesTheChatsSchedulesToTheRebornSession(t *testing.T) {
	scheduleJail(t)
	seedSchedule(t, scheduleOldID, pfmengine.Claude)
	request := claudeScheduleRequest(t)
	// What `pfm chat reload --new` hands Run: no session, the old one left behind.
	request.New, request.LeftBehind = true, scheduleOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	result, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr)
	if err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	reborn := parsedReloadShell(t, tmux.respawn).SessionID
	if !result.New || reborn == "" || reborn == scheduleOldID {
		t.Fatalf("result=%+v reborn=%q", result, reborn)
	}
	if got := scheduleSessions(t); len(got) != 1 || got[0] != reborn {
		t.Fatalf("schedules on %v, want on the reborn session %s; stderr=%q", got, reborn, stderr.String())
	}
}

func TestRunInPlaceKeepsTheSchedulesOnTheResumedSession(t *testing.T) {
	scheduleJail(t)
	seedSchedule(t, scheduleOldID, pfmengine.Claude)
	request := claudeScheduleRequest(t)
	request.SessionID, request.LeftBehind = scheduleOldID, scheduleOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	if resume := parsedReloadShell(t, tmux.respawn).Resume; resume != scheduleOldID {
		t.Fatalf("respawn resume=%q", resume)
	}
	if got := scheduleSessions(t); len(got) != 1 || got[0] != scheduleOldID {
		t.Fatalf("schedules on %v, want on %s", got, scheduleOldID)
	}
}

// TestRunQueuedNewCarriesSchedulesFromTheHandoffSession pins the handoff door:
// a --new reload queued behind an earlier --new continues from the session
// that reboot left in the pane — where the earlier reload put the schedules —
// not from the one it resolved before it queued.
func TestRunQueuedNewCarriesSchedulesFromTheHandoffSession(t *testing.T) {
	scheduleJail(t)
	seedSchedule(t, scheduleMidID, pfmengine.Claude)
	dir := t.TempDir()
	request := claudeScheduleRequest(t)
	request.New, request.LeftBehind = true, scheduleOldID
	content, err := json.Marshal(handoffRecord{
		Engine: pfmengine.Claude, SessionID: scheduleMidID, LeftBehind: scheduleOldID,
		Account: 2, CWD: t.TempDir(), WrittenAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(dir, filepath.Base(request.SocketPath), request.Pane), content, 0o600); err != nil {
		t.Fatal(err)
	}
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	result, err := Run(context.Background(), request,
		Options{SIDDir: dir, Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr)
	if err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	reborn := parsedReloadShell(t, tmux.respawn).SessionID
	if result.LeftBehind != scheduleMidID || reborn == "" || reborn == scheduleMidID {
		t.Fatalf("result=%+v reborn=%q stderr=%q", result, reborn, stderr.String())
	}
	if got := scheduleSessions(t); len(got) != 1 || got[0] != reborn {
		t.Fatalf("schedules on %v, want on the reborn session %s; stderr=%q", got, reborn, stderr.String())
	}
}

// TestRunNewTellsWhenTheSchedulesCannotMove pins the failure path: the reboot
// already happened, so it stands, and the move that failed is told with both
// session ids and the cause.
func TestRunNewTellsWhenTheSchedulesCannotMove(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(notDir, "pfm.db"))
	request := claudeScheduleRequest(t)
	request.New, request.LeftBehind = true, scheduleOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	reborn := parsedReloadShell(t, tmux.respawn).SessionID
	want := "schedules of session " + scheduleOldID + " did NOT move to " + reborn + ":"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q lacks %q", stderr.String(), want)
	}
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
