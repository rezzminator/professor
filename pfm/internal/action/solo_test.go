package action

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/gather/gathertest"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Solo trail names what the pass closed, so a reader of the trail sees
// whether it killed anything; each kill is also an obs record.
func TestSoloTrailCauseNamesWhatItKilled(t *testing.T) {
	id := "66666666-6666-4666-8666-666666666666"
	for _, test := range []struct {
		name       string
		duplicate  bool
		stray      bool
		wantCause  string
		wantRecord []string
	}{
		{name: "nothing killed", wantCause: "no duplicates"},
		{
			name:       "one pane and one stray process",
			duplicate:  true,
			stray:      true,
			wantCause:  "closed 1 duplicate panes, 1 stray processes",
			wantRecord: []string{"solo.kill_pane", "solo.kill_process"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jailAction(t)
			sidDir := os.Getenv("PFM_SID_DIR")
			ctx, recorder := obs.Test(t)
			tmux := &fakeActionTmux{
				alive: map[string]bool{"cc-100-1-1": true},
				panes: map[string][]ActionPane{
					"cc-100-1-1": {{PaneID: "%1", TTY: "pts/1"}},
				},
			}
			writeActionFile(t, filepath.Join(sidDir, "cc-100-1-1"), "/tx/"+id+".jsonl", 0o600)
			processes := &fakeProcesses{}
			if test.duplicate {
				writeActionFile(t, filepath.Join(sidDir, "cc-200-1-1.%2"), "/tx/"+id+".jsonl", 0o600)
				tmux.alive["cc-200-1-1"] = true
				tmux.panes["cc-200-1-1"] = []ActionPane{
					{PaneID: "%2", TTY: "pts/2"},
					{PaneID: "%9", TTY: "pts/9"},
				}
			}
			if test.stray {
				processes.processes = []Process{
					{PID: 10, Argv: []string{"claude", "--resume", id}, TTY: "pts/1"},
					{PID: 11, Argv: []string{"claude", "--resume", id}, TTY: "pts/8"},
				}
			}
			executor, err := New(Dependencies{
				Tmux:      tmux,
				Processes: processes,
				Gate:      fixedGate(false),
				Runner:    &captureRunner{},
				Stderr:    io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := executor.Solo(ctx, id, "cc-100-1-1", false); err != nil {
				t.Fatal(err)
			}
			var causes []string
			for _, record := range recorder.Records() {
				if record.Message != "state.transition" {
					continue
				}
				if kind, _ := record.Field("kind"); kind != "action" {
					continue
				}
				if next, _ := record.Field("next"); next != "solo" {
					continue
				}
				cause, _ := record.Field("cause")
				causes = append(causes, fmt.Sprint(cause))
			}
			if !reflect.DeepEqual(causes, []string{test.wantCause}) {
				t.Fatalf("solo causes = %q, want %q: %s", causes, test.wantCause, recorder.Raw())
			}
			for _, message := range test.wantRecord {
				var found bool
				for _, record := range recorder.Records() {
					found = found || record.Message == message
				}
				if !found {
					t.Fatalf("no %s record: %s", message, recorder.Raw())
				}
			}
		})
	}
}

// doorProcesses is a ProcessTable over a gathertest table whose Terminate is
// gather's signal door, so a solo test sees which signals the door lets
// through. afterScan changes the table between the scan and the kill.
type doorProcesses struct {
	table        *gathertest.Table
	kernel       *gathertest.Kernel
	tty          string
	unidentified error
	afterScan    func(*gathertest.Table)
	scans        int
}

func (processes *doorProcesses) Processes(context.Context) ([]Process, error) {
	processes.scans++
	pids, err := processes.table.PIDs()
	if err != nil {
		return nil, err
	}
	result := make([]Process, 0, len(pids))
	for _, pid := range pids {
		identity := processes.table.Identity(pid)
		result = append(result, Process{
			PID: pid, Start: identity.Start, UID: identity.UID, Argv: identity.Argv,
			Executable: identity.Executable, TTY: processes.tty, Unidentified: processes.unidentified,
		})
	}
	if processes.afterScan != nil {
		processes.afterScan(processes.table)
	}
	return result, nil
}

func (processes *doorProcesses) Terminate(target gather.Target) (gather.Outcome, error) {
	const uid, self = 501, 9
	return gathertest.Door(processes.table, processes.kernel, uid, self).Signal(target, syscall.SIGTERM)
}

// The stray sweep signals a Claude holding the session only through the door:
// a stray whose pid was reused, whose argv or owner changed, or that the scan
// could not identify is never signalled, and the sweep says which.
func TestSoloStrayKillGoesThroughTheDoor(t *testing.T) {
	id := "44444444-4444-4444-8444-444444444444"
	const pid = 4100
	stray := func() *gathertest.Process {
		return &gathertest.Process{Start: 77, UID: 501, Argv: []string{"claude", "--resume", id}}
	}
	for _, test := range []struct {
		name         string
		process      *gathertest.Process
		unidentified error
		afterScan    func(*gathertest.Table)
		wantSent     []gathertest.Sent
		wantMessage  string
		wantOutcome  string
	}{
		{
			name:        "the stray still holding the session is signalled",
			process:     stray(),
			wantSent:    []gathertest.Sent{{PID: pid, Sig: syscall.SIGTERM}},
			wantMessage: "solo.kill_process",
		},
		{
			name:    "a pid reused after the scan is not signalled",
			process: stray(),
			afterScan: func(table *gathertest.Table) {
				table.Processes[pid] = &gathertest.Process{Start: 78, UID: 501, Argv: []string{"claude", "--resume", id}}
			},
			wantMessage: "solo.kill_process_skipped",
			wantOutcome: "gone",
		},
		{
			name:    "an argv changed after the scan is not signalled",
			process: stray(),
			afterScan: func(table *gathertest.Table) {
				table.Processes[pid].Argv = []string{"claude", "--resume", "55555555-5555-4555-8555-555555555555"}
			},
			wantMessage: "solo.kill_process_skipped",
			wantOutcome: "changed",
		},
		{
			name:        "another user's Claude is not signalled",
			process:     &gathertest.Process{Start: 77, UID: 0, Argv: []string{"claude", "--resume", id}},
			wantMessage: "solo.kill_process_failed",
		},
		{
			name:         "a process the scan could not identify is not signalled",
			process:      stray(),
			unidentified: errors.New("identify process 4100: the process table cannot read owners"),
			wantMessage:  "solo.kill_process_failed",
		},
		{
			name: "a session id inside another argument is no stray",
			process: &gathertest.Process{
				Start: 77, UID: 501, Argv: []string{"claude", "--add-dir", "/work/" + id},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jailAction(t)
			ctx, recorder := obs.Test(t)
			processes := &doorProcesses{
				table:        &gathertest.Table{Processes: map[int]*gathertest.Process{pid: test.process}},
				kernel:       &gathertest.Kernel{},
				tty:          "pts/8",
				unidentified: test.unidentified,
				afterScan:    test.afterScan,
			}
			executor, err := New(Dependencies{
				Tmux:      &fakeActionTmux{alive: map[string]bool{}},
				Processes: processes,
				Gate:      fixedGate(false),
				Runner:    &captureRunner{},
				Stderr:    io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := executor.Solo(ctx, id, "", false); err != nil {
				t.Fatalf("Solo() error = %v", err)
			}
			if !reflect.DeepEqual(processes.kernel.Sent, test.wantSent) {
				t.Fatalf("signals sent = %v, want %v", processes.kernel.Sent, test.wantSent)
			}
			if processes.scans != 1 {
				t.Fatalf("Solo() scanned the process table %d times, want 1", processes.scans)
			}
			var messages []string
			var outcome any
			for _, record := range recorder.Records() {
				if strings.HasPrefix(record.Message, "solo.kill_process") {
					messages = append(messages, record.Message)
					outcome, _ = record.Field("state")
				}
			}
			var wantMessages []string
			if test.wantMessage != "" {
				wantMessages = []string{test.wantMessage}
			}
			if !reflect.DeepEqual(messages, wantMessages) {
				t.Fatalf("kill records = %q, want %q: %s", messages, wantMessages, recorder.Raw())
			}
			if test.wantOutcome != "" && outcome != test.wantOutcome {
				t.Fatalf("skipped outcome = %v, want %q: %s", outcome, test.wantOutcome, recorder.Raw())
			}
		})
	}
}

// A failed pane probe is not an empty server. The socket may still be live
// but temporarily unreadable; removing its crumb here makes the next sweep
// treat a working chat as unowned. Cleanup is safe only after a probe that
// returned no panes or found no server behind the socket.
func TestSoloCrumbAfterPaneProbeError(t *testing.T) {
	id := "55555555-5555-4555-8555-555555555555"
	socket := "cc-900-1-1"
	for _, test := range []struct {
		name string
		// unreadable makes the crumb a directory: its read fails, so whether
		// it names this chat is unknown and it stays for a later pass.
		unreadable bool
		// listErr is what the pane probe of the crumb's socket answers.
		listErr    error
		wantCrumb  bool
		wantStderr func(crumb string) string
	}{
		{
			// L1-F14: the pane probe failure has a sibling below (the
			// keep-socket probe) that already logs; this one was silent.
			name:      "pane probe fails",
			wantCrumb: true,
			wantStderr: func(string) string {
				return "cc: solo — pane probe on " + socket + " failed; leaving its crumb: dead socket\n"
			},
		},
		{
			name:       "crumb cannot be read",
			unreadable: true,
			wantCrumb:  true,
			wantStderr: func(crumb string) string {
				return "cc: solo — read crumb " + crumb + " failed; leaving it: read " + crumb + ": is a directory\n"
			},
		},
		{
			// A chat whose Claude died at launch leaves its socket file
			// behind its exited server: an ended chat, removed in silence.
			name:       "server behind the socket is gone",
			listErr:    fmt.Errorf("%w: %s", gather.ErrServerGone, socket),
			wantStderr: func(string) string { return "" },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jailAction(t)
			crumb := filepath.Join(os.Getenv("PFM_SID_DIR"), socket)
			if test.unreadable {
				if err := os.MkdirAll(crumb, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeActionFile(t, crumb, "/tx/"+id+".jsonl", 0o600)
			}
			tmux := &fakeActionTmux{
				alive:   map[string]bool{socket: false},
				listErr: map[string]error{socket: test.listErr},
			}
			var stderr bytes.Buffer
			executor, err := New(Dependencies{
				Tmux:      tmux,
				Processes: &fakeProcesses{},
				Gate:      fixedGate(false),
				Runner:    &captureRunner{},
				Stderr:    &stderr,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := executor.Solo(context.Background(), id, "", true); err != nil {
				t.Fatalf("Solo() error = %v", err)
			}
			if _, err := os.Stat(crumb); (err == nil) != test.wantCrumb {
				t.Fatalf("Solo() crumb %s present = %t, want %t (stat: %v)", crumb, err == nil, test.wantCrumb, err)
			}
			if want := test.wantStderr(crumb); stderr.String() != want {
				t.Fatalf("Solo() stderr = %q, want %q", stderr.String(), want)
			}
		})
	}
}
