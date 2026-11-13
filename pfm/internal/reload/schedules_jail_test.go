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

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
