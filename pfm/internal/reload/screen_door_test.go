package reload

import (
	"context"
	"errors"
	"io"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// doorRecordingTmux records the target of every key, type and read Run makes.
type doorRecordingTmux struct {
	fakeReloadTmux
	targets []pfmcall.Target
	verbs   []string
}

func (tmux *doorRecordingTmux) CaptureScreen(ctx context.Context, target pfmcall.Target) (string, error) {
	tmux.targets = append(tmux.targets, target)
	tmux.verbs = append(tmux.verbs, "read")
	return tmux.fakeReloadTmux.CaptureScreen(ctx, target)
}

func (tmux *doorRecordingTmux) LeaveCopyMode(ctx context.Context, target pfmcall.Target) (bool, error) {
	tmux.targets = append(tmux.targets, target)
	tmux.verbs = append(tmux.verbs, "leave copy mode")
	return tmux.fakeReloadTmux.LeaveCopyMode(ctx, target)
}

func (tmux *doorRecordingTmux) SendKey(ctx context.Context, target pfmcall.Target, key string) error {
	tmux.targets = append(tmux.targets, target)
	tmux.verbs = append(tmux.verbs, "key "+key)
	return tmux.fakeReloadTmux.SendKey(ctx, target, key)
}

func (tmux *doorRecordingTmux) SendLiteral(ctx context.Context, target pfmcall.Target, value string) error {
	tmux.targets = append(tmux.targets, target)
	tmux.verbs = append(tmux.verbs, "type "+value)
	return tmux.fakeReloadTmux.SendLiteral(ctx, target, value)
}

// TestRunTypesAndReadsThroughTheScreenDoor: the tmux worker reads, types /exit
// and presses Enter only through call's Screen door, every call addressed to
// the one pane the worker was handed.
func TestRunTypesAndReadsThroughTheScreenDoor(t *testing.T) {
	tmux := &doorRecordingTmux{}
	request := Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/probe-door", Pane: "%7",
		SessionID: "11111111-1111-4111-8111-111111111111", Account: 1,
		AccountIDs: []int{1}, Machine: reloadTestMachine("", t.TempDir()),
	}
	options := Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}
	if _, err := Run(context.Background(), request, options, tmux, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := pfmcall.Target{SocketPath: request.SocketPath, Pane: request.Pane, Engine: string(pfmengine.Claude)}
	if len(tmux.verbs) == 0 || tmux.verbs[0] != "leave copy mode" {
		t.Fatalf("door calls %v, want the copy-mode exit first", tmux.verbs)
	}
	typed, entered := false, false
	for index, target := range tmux.targets {
		if target != want {
			t.Fatalf("door call %d (%s) addressed %+v, want %+v", index, tmux.verbs[index], target, want)
		}
		typed = typed || tmux.verbs[index] == "type /exit"
		entered = entered || typed && tmux.verbs[index] == "key Enter"
	}
	if !typed || !entered {
		t.Fatalf("door calls %v, want /exit typed then Enter", tmux.verbs)
	}
}

// TestRunRefusesWhenTheCopyModeReadFails: a pane whose copy-mode state cannot
// be read is never typed into — /exit pressed into copy mode moves its cursor.
func TestRunRefusesWhenTheCopyModeReadFails(t *testing.T) {
	tmux := &doorRecordingTmux{fakeReloadTmux: fakeReloadTmux{leaveErr: errors.New("server not responding")}}
	request := Request{
		Engine: pfmengine.Claude, SocketPath: "/tmp/probe-door", Pane: "%7",
		SessionID: "11111111-1111-4111-8111-111111111111", Account: 1,
		AccountIDs: []int{1}, Machine: reloadTestMachine("", t.TempDir()),
	}
	options := Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}
	_, err := Run(context.Background(), request, options, tmux, nil, io.Discard)
	if err == nil || err.Error() != "server not responding" {
		t.Fatalf("Run error = %v, want the failed copy-mode read", err)
	}
	for _, verb := range tmux.verbs {
		if verb != "leave copy mode" {
			t.Fatalf("door calls %v: typed into a pane whose mode was unread", tmux.verbs)
		}
	}
}
