package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// serveNativeHerdr is a fake Herdr server for a native birth: every method
// answers the caller's pane w6:p3 in workspace w6, with this test process in
// its foreground; layout.apply lays out the new tab's pane w6:p9, agent.focus
// focuses its target, and each method asked is sent on methods.
func serveNativeHerdr(t *testing.T) (socket string, methods <-chan string) {
	t.Helper()
	socket = filepath.Join(testjail.ShortRoot(t), "native.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	asked := make(chan string, 64)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var request struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						Pane   string `json:"pane_id"`
						Label  string `json:"label"`
						Target string `json:"target"`
					} `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				asked <- request.Method
				pane := request.Params.Pane
				if pane == "" {
					pane = "w6:p3"
				}
				reply := any(testjail.HerdrPaneInfo(pane))
				switch request.Method {
				case "ping":
					reply = map[string]any{"type": "pong", "version": "0.9.5", "protocol": 22}
				case "pane.process_info":
					// The test process is the caller in the foreground of its pane.
					reply = map[string]any{"type": "pane_process_info", "process_info": map[string]any{
						"pane_id":              pane,
						"shell_pid":            41,
						"foreground_processes": []any{map[string]any{"pid": os.Getpid()}},
					}}
				case "agent.focus":
					reply = map[string]any{"type": "pane_focused", "pane_id": request.Params.Target}
				case "layout.apply":
					reply = map[string]any{"layout": map[string]any{
						"workspace_id": "w6", "root": map[string]any{"type": "pane", "pane_id": "w6:p9"},
					}}
				}
				placed, handled := testjail.HerdrWorkspaceReply(request.Method, request.Params.Label, "w6")
				if handled {
					reply = placed
				}
				body, err := json.Marshal(reply)
				if err != nil {
					t.Errorf("encode Herdr %s fixture: %v", request.Method, err)
					return
				}
				_, _ = fmt.Fprintf(conn, "{\"id\":%q,\"result\":%s}\n", request.ID, body)
			}()
		}
	}()
	return socket, asked
}

// `cx` typed in a Herdr pane births through `pfm chat new --engine codex`'s own
// door: one placement (shim.cx), one native Codex seat in a new Herdr tab
// carrying the typed prompt, no tmux server, and the born-natively status that
// tells the shim there is nothing to attach.
func TestChatServerBirthsANativeCodexSeatForAHerdrCaller(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	socket, methods := serveNativeHerdr(t)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	_, recorder := obs.Test(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	name := "cx-1800000060-1-1"
	var stdout, stderr bytes.Buffer
	code := runInternal(
		[]string{"chat-server", name, jail.root, "sleep 1", "fix the bug"},
		&stdout, &stderr, runtime,
	)

	// 3 is the shim's contract (pfm.zsh cx): born natively, nothing to attach.
	if code != 3 {
		t.Fatalf("code=%d, want 3 (born natively); stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), string(pfmengine.Codex)+"\t"+name+"\therdr\t") {
		t.Fatalf("stdout=%q, want the native birth's report line", stdout.String())
	}
	var placements []string
	for _, record := range recorder.Records() {
		if record.Message == "chat.placement" {
			door, _ := record.Field("door")
			chosen, _ := record.Field("chosen")
			placements = append(placements, fmt.Sprint(door, "=", chosen))
		}
	}
	if !slices.Equal(placements, []string{"shim.cx=herdr"}) {
		t.Fatalf("placements=%q, want one shim.cx=herdr", placements)
	}
	asked := map[string]int{}
	for len(methods) > 0 {
		asked[<-methods]++
	}
	if asked["layout.apply"] != 1 || asked["agent.focus"] != 1 {
		t.Fatalf("asked=%v, want one new tab, focused once", asked)
	}
	state := fleetdb.OpenSharedState(t.Context(), runtime.Paths)
	defer func() { _ = state.Close() }()
	seats, err := state.Seats(t.Context())
	if err != nil || len(seats) != 1 || seats[0].Engine != string(pfmengine.Codex) || seats[0].Name != name {
		t.Fatalf("seats=%+v err=%v, want one Codex seat named %s", seats, err, name)
	}
	recipe, err := state.ReadSeatRun(seats[0].SeatID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(recipe.Args, "fix the bug") || recipe.CWD != jail.root {
		t.Fatalf("recipe=%+v, want the typed prompt in %s", recipe, jail.root)
	}
	if entries, err := os.ReadDir(jail.tmuxDir); err != nil || len(entries) != 0 {
		t.Fatalf("tmux dir entries=%v err=%v, want no server", entries, err)
	}
}

// Arguments a native seat cannot carry (flags, subcommands, more than one
// prompt word) are refused before any birth, naming the doors that take them.
func TestChatServerRefusesArgumentsANativeSeatCannotCarry(t *testing.T) {
	jail := newRunJail(t)
	defer jail.killSockets(t)
	socket, methods := serveNativeHerdr(t)
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--model", "o9"}, {"resume"}, {"exec", "hi"}, {"one", "two"}} {
		var stdout, stderr bytes.Buffer
		code := runInternal(
			append([]string{"chat-server", "cx-1800000061-1-1", jail.root, "sleep 1"}, args...),
			&stdout, &stderr, runtime,
		)
		if code != 1 || !strings.Contains(stderr.String(), "pfm chat new") || stdout.Len() != 0 {
			t.Fatalf("args %q: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
	for len(methods) > 0 {
		if method := <-methods; method == "layout.apply" {
			t.Fatalf("a refused cx still laid out a tab")
		}
	}
}
