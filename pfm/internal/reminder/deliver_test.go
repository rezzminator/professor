package reminder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

type deliveryRig struct {
	state      string
	openErr    error
	captures   []string
	captured   int
	injectErr  error
	injected   []string
	socketPath string
}

func (rig *deliveryRig) deliverer() *chatDeliverer {
	return &chatDeliverer{
		tmuxDir: "/tmux",
		open: func(context.Context, string) (action.OpenResult, error) {
			return action.OpenResult{State: rig.state, Socket: "cc-revived"}, rig.openErr
		},
		capture: func(_ context.Context, socketPath, _ string) (string, error) {
			rig.socketPath = socketPath
			index := min(rig.captured, len(rig.captures)-1)
			rig.captured++
			return rig.captures[index], nil
		},
		inject: func(_ context.Context, _ fleetdb.Reminder, target, _ string) error {
			rig.injected = append(rig.injected, target)
			return rig.injectErr
		},
	}
}

func shortenComposerWait(t *testing.T, poll, wait time.Duration) {
	t.Helper()
	oldPoll, oldWait := reminderComposerPoll, reminderComposerWait
	reminderComposerPoll, reminderComposerWait = poll, wait
	t.Cleanup(func() { reminderComposerPoll, reminderComposerWait = oldPoll, oldWait })
}

var testReminder = fleetdb.Reminder{
	ID: 7, SessionID: "sess-7", Label: "seven", SetByID: "setter-id", SetByLabel: "setter",
}

func TestReminderDelivererInjectsALiveChatByItsSessionId(t *testing.T) {
	rig := &deliveryRig{state: "live", captures: []string{""}}
	if err := rig.deliverer().Deliver(context.Background(), testReminder, "msg"); err != nil {
		t.Fatal(err)
	}
	if len(rig.injected) != 1 || rig.injected[0] != "sess-7" || rig.captured != 0 {
		t.Fatalf("injected=%v captured=%d, want one inject at the session id and no composer wait",
			rig.injected, rig.captured)
	}
}

func TestReminderDelivererWaitsForThreeConsecutiveComposerReads(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := &deliveryRig{state: "opened", captures: []string{"booting", "❯ ", "booting", "❯ ", "❯ ", "❯ "}}
	if err := rig.deliverer().Deliver(context.Background(), testReminder, "msg"); err != nil {
		t.Fatal(err)
	}
	if rig.captured != 6 || len(rig.injected) != 1 || rig.injected[0] != "cc-revived" ||
		rig.socketPath != "/tmux/cc-revived" {
		t.Fatalf("captured=%d injected=%v socket=%q, want 6 reads, inject at the fresh socket",
			rig.captured, rig.injected, rig.socketPath)
	}
}

func TestReminderDelivererRefusesAFolderTrustDialog(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := &deliveryRig{state: "opened", captures: []string{"Yes, I trust this folder\nNo, exit\n❯ 1. Yes"}}
	err := rig.deliverer().Deliver(context.Background(), testReminder, "msg")
	if err == nil || !strings.Contains(err.Error(), "folder-trust dialog; nothing typed") || len(rig.injected) != 0 {
		t.Fatalf("err=%v injected=%v, want the trust-dialog refusal and nothing typed", err, rig.injected)
	}
}

func TestReminderDelivererTimesOutWithoutAComposer(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 30*time.Millisecond)
	rig := &deliveryRig{state: "opened", captures: []string{"still booting"}}
	err := rig.deliverer().Deliver(context.Background(), testReminder, "msg")
	if err == nil || !strings.Contains(err.Error(), "showed no composer within") ||
		!strings.Contains(err.Error(), "cc-revived") || len(rig.injected) != 0 {
		t.Fatalf("err=%v injected=%v", err, rig.injected)
	}
}

func TestReminderDelivererReportsResolveAndInjectFailures(t *testing.T) {
	rig := &deliveryRig{openErr: errors.New("chat is not indexed")}
	err := rig.deliverer().Deliver(context.Background(), testReminder, "msg")
	if err == nil || !strings.Contains(err.Error(), "resolve chat sess-7 (seven)") {
		t.Fatalf("open failure err=%v", err)
	}
	rig = &deliveryRig{state: "live", injectErr: errors.New("pane gone")}
	err = rig.deliverer().Deliver(context.Background(), testReminder, "msg")
	if err == nil || !strings.Contains(err.Error(), "pane gone") {
		t.Fatalf("inject failure err=%v", err)
	}
}
