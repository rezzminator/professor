package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/archive"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmstore "github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type deliveryRig struct {
	state      string
	openErr    error
	captures   []string
	captured   int
	injectErr  error
	injected   []string
	socketPath string

	hidden    bool // the chat is hidden until the unhide seam lifts it
	unhideErr error
	events    []string // "inject" and "unhide" in the order they ran
	unhides   []string
	stdout    bytes.Buffer
}

// unhideSeam stands in for the kill store: it lifts a hidden chat once, as the store does.
func (rig *deliveryRig) unhideSeam(_ context.Context, sessionID string) (bool, error) {
	rig.events = append(rig.events, "unhide")
	rig.unhides = append(rig.unhides, sessionID)
	if rig.unhideErr != nil {
		return false, rig.unhideErr
	}
	removed := rig.hidden
	rig.hidden = false
	return removed, nil
}

func (rig *deliveryRig) deliverer() *chatDeliverer {
	return &chatDeliverer{
		stdout:  &rig.stdout,
		unhide:  rig.unhideSeam,
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
		inject: func(_ context.Context, _ fleetdb.Schedule, target, _ string) error {
			rig.events = append(rig.events, "inject")
			rig.injected = append(rig.injected, target)
			return rig.injectErr
		},
	}
}

func shortenComposerWait(t *testing.T, poll, wait time.Duration) {
	t.Helper()
	oldPoll, oldWait := scheduleComposerPoll, scheduleComposerWait
	scheduleComposerPoll, scheduleComposerWait = poll, wait
	t.Cleanup(func() { scheduleComposerPoll, scheduleComposerWait = oldPoll, oldWait })
}

var testSchedule = fleetdb.Schedule{
	ID: 7, SessionID: "sess-7", Label: "seven", SetByID: "setter-id", SetByLabel: "setter",
}

func TestScheduleDelivererInjectsALiveChatByItsSessionId(t *testing.T) {
	rig := &deliveryRig{state: "live", captures: []string{""}}
	if err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg"); err != nil {
		t.Fatal(err)
	}
	if len(rig.injected) != 1 || rig.injected[0] != "sess-7" || rig.captured != 0 {
		t.Fatalf("injected=%v captured=%d, want one inject at the session id and no composer wait",
			rig.injected, rig.captured)
	}
}

func TestScheduleDelivererWaitsForThreeConsecutiveComposerReads(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := &deliveryRig{state: "opened", captures: []string{"booting", "❯ ", "booting", "❯ ", "❯ ", "❯ "}}
	if err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg"); err != nil {
		t.Fatal(err)
	}
	if rig.captured != 6 || len(rig.injected) != 1 || rig.injected[0] != "cc-revived" ||
		rig.socketPath != "/tmux/cc-revived" {
		t.Fatalf("captured=%d injected=%v socket=%q, want 6 reads, inject at the fresh socket",
			rig.captured, rig.injected, rig.socketPath)
	}
}

func TestScheduleDelivererRefusesAFolderTrustDialog(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := &deliveryRig{state: "opened", captures: []string{"Yes, I trust this folder\nNo, exit\n❯ 1. Yes"}}
	err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg")
	if err == nil || !strings.Contains(err.Error(), "folder-trust dialog; nothing typed") || len(rig.injected) != 0 {
		t.Fatalf("err=%v injected=%v, want the trust-dialog refusal and nothing typed", err, rig.injected)
	}
}

func TestScheduleDelivererTimesOutWithoutAComposer(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 30*time.Millisecond)
	rig := &deliveryRig{state: "opened", captures: []string{"still booting"}}
	err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg")
	if err == nil || !strings.Contains(err.Error(), "showed no composer within") ||
		!strings.Contains(err.Error(), "cc-revived") || len(rig.injected) != 0 {
		t.Fatalf("err=%v injected=%v", err, rig.injected)
	}
}

func TestScheduleDelivererReportsResolveAndInjectFailures(t *testing.T) {
	rig := &deliveryRig{openErr: errors.New("chat is not indexed")}
	err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg")
	if err == nil || !strings.Contains(err.Error(), "resolve chat sess-7 (seven)") {
		t.Fatalf("open failure err=%v", err)
	}
	rig = &deliveryRig{state: "live", injectErr: errors.New("pane gone")}
	err = rig.deliverer().Deliver(context.Background(), testSchedule, "msg")
	if err == nil || !strings.Contains(err.Error(), "pane gone") {
		t.Fatalf("inject failure err=%v", err)
	}
}

func TestScheduleDelivererUnhidesAHiddenChatAfterTheInject(t *testing.T) {
	rig := &deliveryRig{state: "live", captures: []string{""}, hidden: true}
	if err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rig.events, ","); got != "inject,unhide" || rig.unhides[0] != "sess-7" {
		t.Fatalf("events=%q unhides=%v, want the prompt typed first, then the session id un-hidden", got, rig.unhides)
	}
	if want := "unhid chat sess-7\n"; rig.stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", rig.stdout.String(), want)
	}
}

func TestScheduleDelivererLeavesAVisibleChatAlone(t *testing.T) {
	rig := &deliveryRig{state: "live", captures: []string{""}}
	if err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg"); err != nil {
		t.Fatal(err)
	}
	if rig.stdout.Len() != 0 || len(rig.injected) != 1 {
		t.Fatalf("stdout=%q injected=%v, want a delivery and no unhid line for a visible chat",
			rig.stdout.String(), rig.injected)
	}
}

func TestScheduleDelivererDoesNotUnhideAChatItFailedToDeliverTo(t *testing.T) {
	rig := &deliveryRig{state: "live", injectErr: errors.New("pane gone"), hidden: true}
	if err := rig.deliverer().Deliver(context.Background(), testSchedule, "msg"); err == nil {
		t.Fatal("want the inject failure")
	}
	if len(rig.unhides) != 0 || !rig.hidden || rig.stdout.Len() != 0 {
		t.Fatalf(
			"unhides=%v hidden=%v stdout=%q, want the undelivered chat left hidden",
			rig.unhides,
			rig.hidden,
			rig.stdout.String(),
		)
	}
}

// failingWriter is a stdout whose every write fails.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestScheduleDelivererUnhideFailureStillDeliversAndIsReportedWithIdAndCause(t *testing.T) {
	cases := []struct {
		name       string
		removed    bool
		unhideErr  error
		stdoutFail bool
		wantStdout string
		wantIn     []string
	}{
		{
			name: "store refuses", unhideErr: errors.New("database is locked"),
			wantIn: []string{"unhide chat sess-7", "database is locked"},
		},
		{
			name:       "kill removed, then the store fails",
			removed:    true,
			unhideErr:  errors.New("close the kill store: busy"),
			wantStdout: "unhid chat sess-7\n",
			wantIn:     []string{"unhide chat sess-7", "close the kill store: busy"},
		},
		{
			name: "kill removed, stdout fails", removed: true, stdoutFail: true,
			wantIn: []string{"report unhid chat sess-7", "broken pipe"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := &deliveryRig{state: "live", hidden: true}
			deliverer := rig.deliverer()
			deliverer.unhide = func(context.Context, string) (bool, error) { return tc.removed, tc.unhideErr }
			if tc.stdoutFail {
				deliverer.stdout = failingWriter{}
			}
			err := deliverer.Deliver(context.Background(), testSchedule, "msg")
			var warning *DeliveryWarning
			if !errors.As(err, &warning) || len(rig.injected) != 1 || rig.stdout.String() != tc.wantStdout {
				t.Fatalf("err=%v injected=%v stdout=%q, want a delivery warning after one inject and stdout %q",
					err, rig.injected, rig.stdout.String(), tc.wantStdout)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("warning = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestScheduleDelivererRealUnhideLiftsAKillInTheFleetStore runs the production
// seam over a scratch fleet store: the same operation `pfm chat unkill` runs.
func TestScheduleDelivererRealUnhideLiftsAKillInTheFleetStore(t *testing.T) {
	testjail.Fleet(t)
	const id = "22222222-2222-4222-8222-222222222222"
	ctx := context.Background()
	database, err := pfmstore.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, pfmstore.Killed{ID: id, KilledAt: 1_800_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	deliverer := newChatDeliverer(&pfmconfig.Runtime{}, &bytes.Buffer{}, &stderr)
	for _, want := range []bool{true, false} {
		removed, err := deliverer.unhide(ctx, id)
		if err != nil || removed != want {
			t.Fatalf("unhide removed=%v err=%v stderr=%q, want removed=%v", removed, err, stderr.String(), want)
		}
	}
}

func TestWriteReportPrintsAWarnedScheduleOnStderrAndExitsOne(t *testing.T) {
	hidden := fleetdb.Schedule{ID: 4, SessionID: "sess-hidden", Label: "hidden"}
	report := Report{
		Fired:  []fleetdb.Schedule{hidden},
		Warned: []Failure{{Schedule: hidden, Err: errors.New("unhide chat sess-hidden: database is locked")}},
	}
	var stdout, stderr bytes.Buffer
	code := writeReport(report, &stdout, &stderr)
	wantStderr := "pfm internal schedule-fire: schedule 4 (hidden, sess-hidden): delivered, " +
		"but unhide chat sess-hidden: database is locked\n"
	if code != 1 || stdout.String() != "fired schedule 4 → hidden\n" || stderr.String() != wantStderr {
		t.Fatalf("code=%d stdout=%q stderr=%q, want the fired line, the warning on stderr and rc 1",
			code, stdout.String(), stderr.String())
	}
}

// archiveRig is a deliverer over a real archive in a temp dir. Its open seam
// stands in for the index: a chat is "indexed" exactly when its transcript sits
// at its original path, so the order of restore and open is decided by real files.
type archiveRig struct {
	values   paths.Values
	id       string
	original string
	opens    int
	openErr  error
	reopen   error // every open after the first fails with it
	indexed  bool  // the index holds the chat whatever the files say
	hidden   bool  // the chat is hidden until the unhide seam lifts it
	events   []string
	injected []string
	messages []string
	stdout   bytes.Buffer
}

type noProcesses struct{}

func (noProcesses) PIDs() ([]int, error)                   { return nil, nil }
func (noProcesses) Cmdline(int) ([]string, error)          { return nil, nil }
func (noProcesses) Environ(int) (map[string]string, error) { return nil, nil }
func (noProcesses) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (noProcesses) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }

type killedSet struct{ rows []archive.KilledChat }

func (set *killedSet) Killed(context.Context) ([]archive.KilledChat, error) { return set.rows, nil }
func (set *killedSet) Unkill(context.Context, string) error                 { return nil }

// newArchiveRig lays a Claude transcript in a temp tree. With archived, the
// archive package itself moves it out of the tree and records the manifest.
func newArchiveRig(t *testing.T, archived bool) *archiveRig {
	t.Helper()
	root := t.TempDir()
	values := paths.Values{
		Home: filepath.Join(root, "home"),
		Roots: map[pfmengine.ID][]string{
			pfmengine.Claude: {filepath.Join(root, "home", ".cc", "1", "projects")},
			pfmengine.Codex:  {filepath.Join(root, "home", ".codex")},
		},
		SIDDir:     filepath.Join(root, "sid"),
		ArchiveDir: filepath.Join(root, "home", ".claude-archive"),
		TmuxDir:    filepath.Join(root, "tmux"),
		ProcRoot:   filepath.Join(root, "proc"),
	}
	const id = "11111111-1111-4111-8111-111111111111"
	original := filepath.Join(values.Roots[pfmengine.Claude][0], "-work-x", id+".jsonl")
	for _, directory := range []string{filepath.Dir(original), values.SIDDir, filepath.Join(values.Home, ".claude")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(original, []byte("{\"type\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if archived {
		archiveChat(t, values, id, original)
	}
	return &archiveRig{values: values, id: id, original: original}
}

// archiveChat moves a killed chat's transcript out of the tree through the
// archive package itself, recording its manifest row.
func archiveChat(t *testing.T, values paths.Values, id, original string) {
	t.Helper()
	runner, err := archive.New(archive.Dependencies{
		Paths: values, Proc: noProcesses{},
		Kills: &killedSet{rows: []archive.KilledChat{{ID: id, Engine: pfmengine.Claude}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), archive.Options{Apply: true})
	if err != nil || len(report.Moves) != 1 || !report.Moves[0].Applied {
		t.Fatalf("archive the chat: report=%+v err=%v", report, err)
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archived chat still at %s: %v", original, err)
	}
}

func (rig *archiveRig) deliverer() *chatDeliverer {
	return &chatDeliverer{
		stdout:     &rig.stdout,
		tmuxDir:    "/tmux",
		archiveDir: rig.values.ArchiveDir,
		unhide: func(_ context.Context, _ string) (bool, error) {
			rig.events = append(rig.events, "unhide")
			removed := rig.hidden
			rig.hidden = false
			return removed, nil
		},
		open: func(_ context.Context, sessionID string) (action.OpenResult, error) {
			rig.opens++
			rig.events = append(rig.events, "open")
			if rig.openErr != nil {
				return action.OpenResult{}, rig.openErr
			}
			if rig.reopen != nil && rig.opens > 1 {
				return action.OpenResult{}, rig.reopen
			}
			if _, err := os.Stat(rig.original); err != nil && !rig.indexed {
				return action.OpenResult{}, fmt.Errorf("chat %q is %w", sessionID, pfmchat.ErrNotIndexed)
			}
			return action.OpenResult{State: "opened", Socket: "cc-revived"}, nil
		},
		capture: func(context.Context, string, string) (string, error) { return "❯ ", nil },
		inject: func(_ context.Context, _ fleetdb.Schedule, target, message string) error {
			rig.events = append(rig.events, "inject")
			rig.injected = append(rig.injected, target)
			rig.messages = append(rig.messages, message)
			return nil
		},
	}
}

func (rig *archiveRig) schedule() fleetdb.Schedule {
	return fleetdb.Schedule{ID: 9, SessionID: rig.id, Label: "nine"}
}

func TestScheduleDelivererRestoresAnArchivedChatThenDeliversIt(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := newArchiveRig(t, true)
	if err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, err := os.Stat(rig.original); err != nil {
		t.Fatalf("the archived chat was not put back at %s: %v", rig.original, err)
	}
	if rig.opens != 2 || len(rig.injected) != 1 || rig.injected[0] != "cc-revived" || rig.messages[0] != "msg" {
		t.Fatalf("opens=%d injected=%v messages=%v, want a restore between two opens and the prompt unchanged",
			rig.opens, rig.injected, rig.messages)
	}
	if want := "restored archived chat " + rig.id + "\n"; rig.stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", rig.stdout.String(), want)
	}
}

func TestScheduleDelivererUnhidesAnArchivedChatItRestored(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := newArchiveRig(t, true)
	rig.hidden = true
	if err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if got := strings.Join(rig.events, ","); got != "open,open,inject,unhide" || rig.hidden {
		t.Fatalf("events=%q hidden=%v, want restore between two opens, then inject, then unhide", got, rig.hidden)
	}
	if want := "restored archived chat " + rig.id + "\nunhid chat " + rig.id + "\n"; rig.stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", rig.stdout.String(), want)
	}
}

func TestScheduleDelivererRestoreFailureIsARecordedFailureAndTheScheduleStaysDue(t *testing.T) {
	rig := newArchiveRig(t, true)
	rows, err := archive.ReadManifest(rig.values.ArchiveDir)
	if err != nil || len(rows) != 1 {
		t.Fatalf("manifest rows=%v err=%v", rows, err)
	}
	if err := os.Remove(rows[0].Archived); err != nil {
		t.Fatal(err)
	}
	store := openStore(t)
	now := time.Unix(1_800_000_000, 0)
	id, err := store.CreateSchedule(context.Background(), fleetdb.Schedule{
		SessionID: rig.id, Engine: "claude", Label: "nine", Prompt: "ping", Interval: time.Hour,
		Created: now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := Fire(context.Background(), store, rig.deliverer(), filepath.Join(t.TempDir(), "fire.lock"), now)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}
	if len(report.Fired) != 0 || len(report.Failed) != 1 || len(rig.injected) != 0 {
		t.Fatalf("report=%+v injected=%v, want one failure and nothing typed", report, rig.injected)
	}
	stuck := mustSchedule(t, store, id)
	for _, want := range []string{"restore archived chat " + rig.id, "archived file for " + rig.id + " is missing"} {
		if !strings.Contains(stuck.LastError, want) {
			t.Fatalf("LastError = %q, want it to contain %q", stuck.LastError, want)
		}
	}
	if !stuck.NextFire.Before(now.Add(time.Second)) || rig.stdout.Len() != 0 {
		t.Fatalf("schedule=%+v stdout=%q, want still due and no restored line", stuck, rig.stdout.String())
	}
}

func TestScheduleDelivererNamesAChatInNeitherTheIndexNorTheArchive(t *testing.T) {
	rig := newArchiveRig(t, false)
	if err := os.Remove(rig.original); err != nil {
		t.Fatal(err)
	}
	err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg")
	if err == nil || !strings.Contains(err.Error(), "resolve chat "+rig.id+" (nine)") ||
		!strings.Contains(err.Error(), "is not indexed") || !strings.Contains(err.Error(), "not in the archive") ||
		len(rig.injected) != 0 || rig.stdout.Len() != 0 {
		t.Fatalf("err=%v injected=%v stdout=%q, want the not-indexed failure that also says not in the archive",
			err, rig.injected, rig.stdout.String())
	}
}

func TestScheduleDelivererLeavesTheArchiveAloneUnlessTheChatIsNotIndexed(t *testing.T) {
	for name, openErr := range map[string]error{
		"scan failure": errors.New("open index: disk I/O error"),
		"indexed":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			shortenComposerWait(t, time.Millisecond, 5*time.Second)
			rig := newArchiveRig(t, true)
			rig.openErr, rig.indexed = openErr, openErr == nil
			err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg")
			if (openErr != nil) != (err != nil) {
				t.Fatalf("err = %v for open error %v", err, openErr)
			}
			rows, readErr := archive.ReadManifest(rig.values.ArchiveDir)
			if readErr != nil || len(rows) != 1 {
				t.Fatalf("manifest rows=%v err=%v", rows, readErr)
			}
			if _, statErr := os.Stat(rows[0].Archived); statErr != nil || rig.stdout.Len() != 0 {
				t.Fatalf("archive touched: stat=%v stdout=%q", statErr, rig.stdout.String())
			}
		})
	}
}

// TestScheduleDelivererRestoredButUnopenedChatDeliversOnTheNextFire pins a
// restore whose reopen fails: the failure names both facts, nothing is typed,
// and the next fire finds the chat back in place and delivers without
// restoring it a second time.
func TestScheduleDelivererRestoredButUnopenedChatDeliversOnTheNextFire(t *testing.T) {
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	rig := newArchiveRig(t, true)
	rig.reopen = errors.New("open the fleet database: database is locked")
	err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg")
	restored := "restored archived chat " + rig.id + " but it still cannot be opened"
	if err == nil || !strings.Contains(err.Error(), restored) ||
		!strings.Contains(err.Error(), "database is locked") || len(rig.injected) != 0 {
		t.Fatalf("err=%v injected=%v, want the reopen failure after the restore and nothing typed", err, rig.injected)
	}
	if _, statErr := os.Stat(rig.original); statErr != nil {
		t.Fatalf("the restore was not kept at %s: %v", rig.original, statErr)
	}
	rig.reopen = nil
	if err := rig.deliverer().Deliver(context.Background(), rig.schedule(), "msg"); err != nil {
		t.Fatalf("next fire: %v", err)
	}
	if rig.opens != 3 || len(rig.injected) != 1 || rig.stdout.String() != "restored archived chat "+rig.id+"\n" {
		t.Fatalf("opens=%d injected=%v stdout=%q, want one restore and one delivery on the next fire",
			rig.opens, rig.injected, rig.stdout.String())
	}
}
