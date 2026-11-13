package reload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ownerExitTmux is a chat whose owner may have typed /exit while the reload
// waited: its pane can be dead (remain-on-exit kept it), gone from a live
// server, or its server gone, and a dead pane renders nothing typed into it.
type ownerExitTmux struct {
	fakeReloadTmux
	panes      []Pane
	listErr    error
	remainErr  error
	remains    []bool
	killed     []string
	markPath   string
	markAtExit bool
	enterErr   error
}

// SendKey presses the key, then answers enterErr: tmux delivered the key and
// still reported a failure.
func (tmux *ownerExitTmux) SendKey(ctx context.Context, target pfmcall.Target, key string) error {
	if err := tmux.fakeReloadTmux.SendKey(ctx, target, key); err != nil {
		return err
	}
	return tmux.enterErr
}

func (tmux *ownerExitTmux) ListPanes(context.Context, string) ([]Pane, error) {
	if tmux.panes == nil {
		return tmux.fakeReloadTmux.ListPanes(context.Background(), "")
	}
	return tmux.panes, tmux.listErr
}

func (tmux *ownerExitTmux) SetRemain(_ context.Context, _, _ string, on bool) error {
	tmux.remains = append(tmux.remains, on)
	return tmux.remainErr
}

func (tmux *ownerExitTmux) KillPane(_ context.Context, _, pane string) error {
	tmux.killed = append(tmux.killed, pane)
	return nil
}

func (tmux *ownerExitTmux) SendLiteral(ctx context.Context, target pfmcall.Target, value string) error {
	if value == "/exit" {
		_, err := os.Stat(tmux.markPath)
		tmux.markAtExit = err == nil
	}
	return tmux.fakeReloadTmux.SendLiteral(ctx, target, value)
}

// TestRunYieldsToTheOwnersExit pins that a /exit the owner types while the
// reload waits for its turn ends the chat: the reload types no /exit of its
// own, respawns nothing, closes a dead pane its remain-on-exit kept, and
// reports the chat ended. A live chat still reboots, its exit mark written
// exactly while its own /exit is typed; a pane read that fails is an error.
func TestRunYieldsToTheOwnersExit(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		tmux        ownerExitTmux
		wantResult  Result
		wantErr     string
		wantKilled  []string
		wantRemains []bool
		wantLiteral string
		wantRespawn bool
		wantMark    bool
	}{
		{
			name:        "owner exited while the reload waited",
			tmux:        ownerExitTmux{panes: []Pane{{ID: "%7", Dead: true, PID: 700}}},
			wantResult:  Result{Ended: true},
			wantKilled:  []string{"%7"},
			wantRemains: []bool{true},
		},
		{
			name: "owner exited before remain-on-exit, the server went with the pane",
			tmux: ownerExitTmux{
				panes: []Pane{}, listErr: fmt.Errorf("list panes: %w", gather.ErrServerGone),
				remainErr: errors.New("exit status 1"),
			},
			wantResult:  Result{Ended: true},
			wantRemains: []bool{true},
		},
		{
			name:        "the pane left a server that lives on",
			tmux:        ownerExitTmux{panes: []Pane{{ID: "%9", PID: 900}}},
			wantResult:  Result{Ended: true},
			wantRemains: []bool{true},
		},
		{
			name: "a failed step on a pane that cannot be read stays an error",
			tmux: ownerExitTmux{
				panes: []Pane{}, listErr: errors.New("list panes: server unresponsive"),
				remainErr: errors.New("exit status 1"),
			},
			wantErr: "set pane remain-on-exit: exit status 1\n" +
				"check whether the chat ended before the reload's /exit: list panes: server unresponsive",
			wantRemains: []bool{true},
		},
		{
			name:        "an Enter that went out with an error still reboots",
			tmux:        ownerExitTmux{enterErr: errors.New("send-keys: exit status 1")},
			wantResult:  Result{Account: 2},
			wantRemains: []bool{true, false},
			wantLiteral: "/exit",
			wantRespawn: true,
			wantMark:    true,
		},
		{
			name:        "a live chat reboots",
			tmux:        ownerExitTmux{panes: nil},
			wantResult:  Result{Account: 2},
			wantRemains: []bool{true, false},
			wantLiteral: "/exit",
			wantRespawn: true,
			wantMark:    true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(paths.EnvHome, t.TempDir())
			t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
			sidDir := t.TempDir()
			tmux := testCase.tmux
			tmux.markPath = ExitMarkPath(sidDir, "probe-reload", "%7")
			result, err := Run(
				context.Background(),
				Request{
					Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-reload", Pane: "%7",
					SessionID: "11111111-1111-4111-8111-111111111111", CWD: "/jail/project",
					Account: 2, AccountIDs: []int{2}, Machine: reloadTestMachine("", t.TempDir()),
				},
				Options{SIDDir: sidDir, Delay: -1, Poll: -1, ExitTries: 2},
				&tmux,
				nil,
				nil,
			)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != testCase.wantErr || result != testCase.wantResult {
				t.Fatalf("Run() = %+v, %q; want %+v, %q", result, gotErr, testCase.wantResult, testCase.wantErr)
			}
			if !reflect.DeepEqual(tmux.killed, testCase.wantKilled) ||
				!reflect.DeepEqual(tmux.remains, testCase.wantRemains) {
				t.Fatalf("killed=%q remains=%v; want killed=%q remains=%v",
					tmux.killed, tmux.remains, testCase.wantKilled, testCase.wantRemains)
			}
			if tmux.literal != testCase.wantLiteral || (tmux.respawn != "") != testCase.wantRespawn ||
				tmux.markAtExit != testCase.wantMark {
				t.Fatalf("typed=%q respawned=%v exit mark at /exit=%v; want %q %v %v",
					tmux.literal, tmux.respawn != "", tmux.markAtExit,
					testCase.wantLiteral, testCase.wantRespawn, testCase.wantMark)
			}
			if _, err := os.Stat(tmux.markPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("exit mark %s after Run: stat err=%v, want it removed", tmux.markPath, err)
			}
		})
	}
}

// TestResultReceiptNamesWhatTheReloadDid pins the worker's receipt line.
func TestResultReceiptNamesWhatTheReloadDid(t *testing.T) {
	for result, want := range map[Result]string{
		{}:            "respawned in place",
		{Ended: true}: "the chat ended before its reboot, nothing respawned",
	} {
		if got := result.Receipt(); got != want {
			t.Fatalf("Result%+v.Receipt() = %q, want %q", result, got, want)
		}
	}
}

// TestExitTypedNeedsTheLockAndTheMark pins the probe exit-close asks: a
// reload's own /exit is in flight only while the reload holds the pane mutex
// and has written its exit mark; the mutex alone (the reload still waits for
// the turn, so a /exit is the owner's) or a leftover mark is not, and a lock
// that cannot be probed is an error.
func TestExitTypedNeedsTheLockAndTheMark(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		held, marked bool
		lockIsDir    bool
		want         bool
		wantErr      string
	}{
		{name: "no reload", wantErr: "<nil>"},
		{name: "a leftover mark, no lock held", marked: true, wantErr: "<nil>"},
		{name: "the reload waits for the turn", held: true, wantErr: "<nil>"},
		{name: "the reload typed its /exit", held: true, marked: true, want: true, wantErr: "<nil>"},
		{name: "a lock that cannot be probed", lockIsDir: true, marked: true, wantErr: "open reload lock: open {dir}/.cc-1-1-1.%0.reloadlock: is a directory"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			lockPath := LockPath(dir, "cc-1-1-1", "%0")
			if testCase.lockIsDir {
				if err := os.Mkdir(lockPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.held {
				lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := lock.Close(); err != nil {
						t.Errorf("close lock: %v", err)
					}
				})
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.marked {
				if err := os.WriteFile(ExitMarkPath(dir, "cc-1-1-1", "%0"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ExitTyped(dir, "cc-1-1-1", "%0")
			if errText := strings.ReplaceAll(
				fmt.Sprint(err),
				dir,
				"{dir}",
			); got != testCase.want ||
				errText != testCase.wantErr {
				t.Fatalf(
					"ExitTyped() error = %q, want %q (typed %v, want %v)",
					errText,
					testCase.wantErr,
					got,
					testCase.want,
				)
			}
		})
	}
}
