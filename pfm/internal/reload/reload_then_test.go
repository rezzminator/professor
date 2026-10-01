package reload

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

type failedThenDisplayTmux struct {
	fakeReloadTmux
	displayErr error
}

func (tmux *failedThenDisplayTmux) Display(ctx context.Context, socket, pane, message string) error {
	_ = tmux.fakeReloadTmux.Display(ctx, socket, pane, message)
	return tmux.displayErr
}

func TestRunMarksFailedThenOnlyWhenPaneWasTold(t *testing.T) {
	for _, displayErr := range []error{nil, errors.New("display failed")} {
		dir := t.TempDir()
		tmux := &failedThenDisplayTmux{displayErr: displayErr}
		_, err := Run(context.Background(), Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/probe-then", Pane: "%7",
			SessionID: "11111111-1111-4111-8111-111111111111", Account: 1,
			AccountIDs: []int{1}, Then: "follow up",
		}, Options{SIDDir: dir, Delay: -1, Poll: -1, ExitTries: 2, ThenTries: 1},
			tmux, fakeReloadProc{}, io.Discard)
		if err == nil || PaneTold(err) != (displayErr == nil) {
			t.Fatalf("displayErr=%v runErr=%v paneTold=%t", displayErr, err, PaneTold(err))
		}
		if content, readErr := os.ReadFile(
			filepath.Join(dir, "probe-then.then-failed"),
		); readErr != nil ||
			string(content) != "follow up\n" {
			t.Fatalf("sentinel=%q error=%v", content, readErr)
		}
	}
}

func TestDeliverThenRecognizesTheCodexComposerMarker(t *testing.T) {
	tmux := &delayedThenTmux{marker: "›"}
	tmux.respawn = "codex"
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"codex"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	err := deliverThen(
		context.Background(),
		Request{
			Engine: pfmengine.Codex, SocketPath: "/tmp/tmux-1000/probe-codex-then", Pane: "%7",
			Then: "continue the task",
		},
		Options{ThenTries: 2},
		tmux,
		proc,
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !tmux.ready || !tmux.submitted {
		t.Fatalf("ready=%t submitted=%t", tmux.ready, tmux.submitted)
	}
}

func TestRunRefreshesThePanePIDAfterRespawnBeforeSubmittingThen(t *testing.T) {
	tmux := &respawnPIDTmux{oldPID: 700, newPID: 900}
	_, err := Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/probe-reload-then-pid",
			Pane:       "%7",
			SessionID:  "11111111-1111-4111-8111-111111111111",
			CWD:        "/jail/project",
			Account:    2,
			AccountIDs: []int{2},
			Then:       "continue the task",
		},
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, ThenTries: 2},
		tmux,
		respawnPromptProc{tmux: tmux},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !tmux.submitted {
		t.Fatal("--then was not submitted after the pane process changed")
	}
}

func TestFailedThenWritesTheRecoverableSentinel(t *testing.T) {
	dir := t.TempDir()
	tmux := &fakeReloadTmux{}
	request := Request{
		SocketPath: "/tmp/tmux-1000/probe-reload",
		Pane:       "%7",
		Then:       "continue the task",
	}
	if err := failThen(context.Background(), request, dir, tmux, "input box missing"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "probe-reload.then-failed"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "continue the task\n" || len(tmux.displays) != 1 {
		t.Fatalf("sentinel=%q displays=%q", content, tmux.displays)
	}
}

// A wrapped prompt's tail must prove delivery even though the marker is only
// on the first composer row.
func TestDeliverThenSubmitsAPromptThatWrapsAcrossComposerLines(t *testing.T) {
	const then = "Continue the flight: read the run ledger end to end, " +
		"execute the remaining tasks, and write the zero-gap task file to the " +
		"flight directory before presenting the user gate."
	tmux := &delayedThenTmux{}
	tmux.respawn = "claude"
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"claude"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	err := deliverThen(
		context.Background(),
		Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-wrapped-then", Pane: "%7",
			Then: then,
		},
		Options{ThenTries: 2},
		tmux,
		proc,
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !tmux.submitted {
		t.Fatal("a wrapped --then prompt was never submitted — Enter was withheld from a prompt that had fully landed")
	}
}

// busyThenIdleTmux shows the caller's own turn still running for the first
// busyCaptures captures — the state the worker actually wakes up in, since the
// Bash call that scheduled it is part of that turn — and idle afterwards. It
// records how many captures had happened when /exit was typed.
type busyThenIdleTmux struct {
	fakeReloadTmux
	busyCaptures int
	captures     int
	typedAfter   int
}

func (tmux *busyThenIdleTmux) Capture(context.Context, string, string) (string, error) {
	tmux.captures++
	if tmux.captures <= tmux.busyCaptures {
		return "Claude\n✻ Thinking… (12s · ↓ 1.2k tokens · esc to interrupt)\n❯ ", nil
	}
	return tmux.fakeReloadTmux.Capture(context.Background(), "", "")
}

func (tmux *busyThenIdleTmux) SendLiteral(ctx context.Context, socket, pane, value string) error {
	if value == "/exit" {
		tmux.typedAfter = tmux.captures
	}
	return tmux.fakeReloadTmux.SendLiteral(ctx, socket, pane, value)
}

// stuckExitTmux renders the typed /exit in the composer and never dies on
// Enter — the incident shape: a chat that did not take the /exit and sat with
// it in the input box. Backspaces erase the typed text one rune at a time.
type stuckExitTmux struct {
	fakeReloadTmux
	keys []string
}

func (tmux *stuckExitTmux) Capture(context.Context, string, string) (string, error) {
	return "Claude\n❯ " + tmux.literal, nil
}

func (tmux *stuckExitTmux) SendKey(_ context.Context, _, _, key string) error {
	tmux.keys = append(tmux.keys, key)
	if key == "BSpace" && tmux.literal != "" {
		tmux.literal = tmux.literal[:len(tmux.literal)-1]
	}
	return nil
}

func reloadIdleWaitRequest(socket string) Request {
	return Request{
		Engine: pfmengine.Claude, SocketPath: socket, Pane: "%7",
		SessionID: "11111111-1111-4111-8111-111111111111", CWD: "/jail/project",
		Account: 2, AccountIDs: []int{2}, Machine: reloadTestMachine("", "/jail/home"),
	}
}
