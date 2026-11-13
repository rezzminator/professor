package schedule

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A schedule's wake of a dead tmux-born chat revives it in tmux, its own
// home, whatever chat.backend says: with no caller and no recorded Herdr host,
// a configured Herdr is no reason to fail the wake. It runs the real door —
// Deliver → OpenDetachedID → the action executor — on a jailed tmux, with a
// fake engine and only the composer read and the typing faked.
func TestScheduleWakeRevivesATmuxChatInTmuxUnderConfiguredHerdr(t *testing.T) {
	root := testjail.Fleet(t)
	// The wake runs where no chat is: a timer, a daemon.
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	// The fake engine lays its own entry in the jailed process table, on the
	// pane's terminal, as a real engine has one in /proc, and boots before it
	// paints, as a real one loads its runtime first: the launch watch sees it
	// on the pane before its screen and calls it up without waiting out its
	// quiet rule.
	engine := filepath.Join(root, "home", "claude")
	script := `#!/bin/sh
entry="$PFM_PROC_ROOT/$$"
mkdir -p "$entry/fd" && printf 'claude\000' > "$entry/cmdline" && ln -s "$(readlink /proc/self/fd/0)" "$entry/fd/0"
sleep 0.5
printf '❯ \n'
exec sleep 30
`
	if err := testjail.WriteExecutable(engine, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"version":2,"chat":{"backend":"herdr"},"claude":{"binary":%q}}`, engine)
	if err := os.WriteFile(filepath.Join(root, "home", "pfm.config.json"), []byte(config+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "66666666-6666-4666-8666-666666666666"
	transcript := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"user","cwd":%q,"message":{"content":"wake me later"}}`, filepath.Join(root, "home"))
	if err := os.WriteFile(transcript, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sockets, _ := filepath.Glob(filepath.Join(root, "tmux", "*"))
		for _, socket := range sockets {
			kill := exec.Command("tmux", "-S", socket, "kill-server")
			kill.Env = append(os.Environ(), "TMUX=")
			_ = kill.Run()
		}
	})
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	shortenComposerWait(t, time.Millisecond, 5*time.Second)
	var stdout, stderr bytes.Buffer
	deliverer := newChatDeliverer(&runtime, &stdout, &stderr)
	deliverer.capture = func(context.Context, string) (string, error) { return "❯ ", nil }
	var called []string
	deliverer.call = func(_ context.Context, _ fleetdb.Schedule, request pfmcall.Request) (pfmcall.Result, error) {
		called = append(called, request.Target)
		return pfmcall.Result{Status: "delivered", Typed: true}, nil
	}
	deliverer.unhide = func(context.Context, string) (bool, error) { return false, nil }
	if err := deliverer.Deliver(
		context.Background(),
		fleetdb.Schedule{ID: 7, SessionID: id, Label: "seven"},
		"msg",
	); err != nil {
		t.Fatalf("Deliver: %v (stderr %q)", err, stderr.String())
	}
	if len(called) != 1 || called[0] != id {
		t.Fatalf("called=%v, want the wake typed once into %s", called, id)
	}
	sockets, err := filepath.Glob(filepath.Join(root, "tmux", "cc-*"))
	if err != nil || len(sockets) != 1 {
		t.Fatalf("tmux chat servers=%v err=%v, want the chat revived on one fresh tmux server", sockets, err)
	}
}
