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
	reminderOldID = "11111111-1111-4111-8111-111111111111"
	reminderMidID = "22222222-2222-4222-8222-222222222222"
)

// reminderJail points the shared state database at a fresh temp file.
func reminderJail(t *testing.T) {
	t.Helper()
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
}

func openReminderState(t *testing.T) *fleetdb.Store {
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

func seedReminder(t *testing.T, sessionID string, engine pfmengine.ID) {
	t.Helper()
	if _, err := openReminderState(t).CreateReminder(context.Background(), fleetdb.Reminder{
		SessionID: sessionID, Engine: string(engine), Prompt: "check the build",
		Interval: time.Hour, Created: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatal(err)
	}
}

// reminderSessions is the session id of every stored reminder.
func reminderSessions(t *testing.T) []string {
	t.Helper()
	reminders, err := openReminderState(t).Reminders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range reminders {
		ids = append(ids, reminders[i].SessionID)
	}
	return ids
}

func claudeReminderRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-reminder", Pane: "%7",
		CWD: "/jail/project", Account: 2, AccountIDs: []int{2},
		Machine: reloadTestMachine("", t.TempDir()),
	}
}

func TestRunNewCarriesTheChatsRemindersToTheRebornSession(t *testing.T) {
	reminderJail(t)
	seedReminder(t, reminderOldID, pfmengine.Claude)
	request := claudeReminderRequest(t)
	// What `pfm chat reload --new` hands Run: no session, the old one left behind.
	request.New, request.LeftBehind = true, reminderOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	result, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr)
	if err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	reborn := parsedReloadShell(t, tmux.respawn).SessionID
	if !result.New || reborn == "" || reborn == reminderOldID {
		t.Fatalf("result=%+v reborn=%q", result, reborn)
	}
	if got := reminderSessions(t); len(got) != 1 || got[0] != reborn {
		t.Fatalf("reminders on %v, want on the reborn session %s; stderr=%q", got, reborn, stderr.String())
	}
}

func TestRunInPlaceKeepsTheRemindersOnTheResumedSession(t *testing.T) {
	reminderJail(t)
	seedReminder(t, reminderOldID, pfmengine.Claude)
	request := claudeReminderRequest(t)
	request.SessionID, request.LeftBehind = reminderOldID, reminderOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	if resume := parsedReloadShell(t, tmux.respawn).Resume; resume != reminderOldID {
		t.Fatalf("respawn resume=%q", resume)
	}
	if got := reminderSessions(t); len(got) != 1 || got[0] != reminderOldID {
		t.Fatalf("reminders on %v, want on %s", got, reminderOldID)
	}
}

// TestRunQueuedNewCarriesRemindersFromTheHandoffSession pins the handoff door:
// a --new reload queued behind an earlier --new continues from the session
// that reboot left in the pane — where the earlier reload put the reminders —
// not from the one it resolved before it queued.
func TestRunQueuedNewCarriesRemindersFromTheHandoffSession(t *testing.T) {
	reminderJail(t)
	seedReminder(t, reminderMidID, pfmengine.Claude)
	dir := t.TempDir()
	request := claudeReminderRequest(t)
	request.New, request.LeftBehind = true, reminderOldID
	content, err := json.Marshal(handoffRecord{
		Engine: pfmengine.Claude, SessionID: reminderMidID, LeftBehind: reminderOldID,
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
	if result.LeftBehind != reminderMidID || reborn == "" || reborn == reminderMidID {
		t.Fatalf("result=%+v reborn=%q stderr=%q", result, reborn, stderr.String())
	}
	if got := reminderSessions(t); len(got) != 1 || got[0] != reborn {
		t.Fatalf("reminders on %v, want on the reborn session %s; stderr=%q", got, reborn, stderr.String())
	}
}

// TestRunNewTellsWhenTheRemindersCannotMove pins the failure path: the reboot
// already happened, so it stands, and the move that failed is told with both
// session ids and the cause.
func TestRunNewTellsWhenTheRemindersCannotMove(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(notDir, "pfm.db"))
	request := claudeReminderRequest(t)
	request.New, request.LeftBehind = true, reminderOldID
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	reborn := parsedReloadShell(t, tmux.respawn).SessionID
	want := "reminders of session " + reminderOldID + " did NOT move to " + reborn + ":"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q lacks %q", stderr.String(), want)
	}
}

// TestRunNewCodexTellsTheRemindersStayBehind pins the one door with no new id
// to move to: a Codex --new conversation has none until it answers.
func TestRunNewCodexTellsTheRemindersStayBehind(t *testing.T) {
	reminderJail(t)
	seedReminder(t, reminderOldID, pfmengine.Codex)
	dir := t.TempDir()
	request := Request{
		Engine: pfmengine.Codex, SocketPath: "/tmp/cx-reminder", Pane: "%7",
		New: true, LeftBehind: reminderOldID, CWD: dir, Account: 1, AccountIDs: []int{1},
		Machine: pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: dir}}},
	}
	tmux := &fakeReloadTmux{}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), request,
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, &stderr); err != nil {
		t.Fatalf("run: %v stderr=%q", err, stderr.String())
	}
	want := "1 reminder(s) of session " + reminderOldID + " stay on the conversation left behind"
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr %q lacks %q", stderr.String(), want)
	}
	if got := reminderSessions(t); len(got) != 1 || got[0] != reminderOldID {
		t.Fatalf("reminders on %v, want on %s", got, reminderOldID)
	}
}
