package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// TestChatKeysRefusesATokenTmuxWouldType guards the silent failure the whole
// verb exists to avoid: tmux types an unrecognised key name as text, so a
// caller who means "press escape" and writes `Esc` puts three letters into the
// composer and is told it worked.
func TestChatKeysRefusesATokenTmuxWouldType(t *testing.T) {
	for _, token := range []string{"Esc", "Return", "ctrl-c", ""} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"chat", "keys", "any-target", token}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("%q exit=%d, want 2 (stdout=%q stderr=%q)",
				token, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "is not a tmux key") ||
			!strings.Contains(stderr.String(), "--literal") {
			t.Fatalf("%q refusal = %q, want the reason and the way to mean it",
				token, stderr.String())
		}
	}
}

// TestChatKeysDrivesALiveChat proves the verb reaches the engine, not just
// tmux: text typed with --literal and submitted with Enter has to come back
// out of the chat's own transcript.
func TestChatKeysDrivesALiveChat(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "keys worker",
		"--cwd", jail.root + "/work",
		"first orders",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{
		"chat", "keys", "--delay", "10ms", "--literal", socket, "typed by keys",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("literal exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{
		"chat", "keys", "--delay", "10ms", socket, "Enter",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("Enter exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 key(s)") {
		t.Fatalf("receipt = %q, want the count of keys pressed", stdout.String())
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		var transcript, transcriptErrors bytes.Buffer
		run([]string{"chat", "read", socket, "--tail", "10", "--condensed"},
			&transcript, &transcriptErrors)
		if strings.Contains(transcript.String(), "typed by keys") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keys never reached the engine: %q", transcript.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestChatKeysRefusesADeadChat keeps the verb from reporting a press into a
// pane that is not there.
func TestChatKeysRefusesADeadChat(t *testing.T) {
	// Hermetic by law: without a jail override this verb would open the
	// OPERATOR'S live database — which a newer mirror may already have
	// migrated past this build's schema, turning an expected codeUnknownChat
	// into a schema refusal.
	t.Setenv("PFM_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "keys", "no-such-chat-here", "Enter"}, &stdout, &stderr)
	if code != codeUnknownChat {
		t.Fatalf("exit=%d, want %d (stderr=%q)", code, codeUnknownChat, stderr.String())
	}
}

// TestRunRescuesAPromptStrandedByAnOverlay is the failure that stranded a
// launch for twenty minutes: the prompt was typed, the startup overlay ate the
// Enter, and the chat sat idle looking alive. The launch has to press the keys
// a human would and prove the result against the engine's transcript.
func TestRunRescuesAPromptStrandedByAnOverlay(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CC_STUB_OVERLAY", "1")
	restoreGrace, restoreWindow := launchGrace, launchProofWindow
	launchGrace, launchProofWindow = 50*time.Millisecond, 250*time.Millisecond
	t.Cleanup(func() { launchGrace, launchProofWindow = restoreGrace, restoreWindow })

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "stranded worker",
		"--cwd", jail.root + "/work",
		"a prompt the overlay swallowed",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d, want the rescue to succeed (stdout=%q stderr=%q)",
			code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unsent in the composer") ||
		!strings.Contains(stderr.String(), "the model recorded it") {
		t.Fatalf("rescue report = %q, want it to say what it did", stderr.String())
	}
}

func TestChatNativeKeysAndCaptureAdapters(t *testing.T) {
	jailTest(t)
	values := jailPaths(t)
	socket := filepath.Join(t.TempDir(), "h.sock")
	keys := make(chan string, 32)
	serveNativeAdapter(t, socket, map[string]string{
		"agent.get": `{"type":"agent_info","agent":{"pane_id":"p3","terminal_id":"w6:p3","agent":"claude","agent_status":"idle"}}`,
		"pane.process_info": fmt.Sprintf(
			`{"process_info":{"pane_id":"w6:p3","foreground_processes":[{"pid":%d,"argv0":"claude"}]}}`,
			os.Getpid(),
		),
		"pane.read":      `{"type":"pane_read","read":{"pane_id":"p3","workspace_id":"w6","text":"native screen\n🔖 🥇 Captured Label │ project","truncated":false}}`,
		"pane.send_keys": `{"type":"ok"}`,
		"pane.send_text": `{"type":"ok"}`,
	}, keys)
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := state.PutSeat(
		context.Background(),
		fleetdb.Seat{
			SeatID:         "native-adapter",
			SessionID:      "native-session",
			Engine:         "cc",
			Backend:        "herdr",
			State:          "live",
			Name:           "NATIVE",
			HerdrSocket:    socket,
			HerdrPane:      "w6:p3",
			HerdrWorkspace: "w6",
		},
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p3")
	for _, args := range [][]string{{"chat", "keys", "--delay", "0s", "NATIVE", "Enter", "Escape", "C-c", "Down", "Tab"}, {"chat", "keys", "--literal", "--capture", "NATIVE", "native text"}, {"chat", "capture", "NATIVE"}, {"chat", "whoami", "--label"}, {"chat", "modal", "NATIVE", "deny", "1"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("native adapter %v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		if args[1] == "whoami" && strings.TrimSpace(stdout.String()) != "🥇 Captured Label" {
			t.Errorf("native label=%q", stdout.String())
		}
		if args[1] == "capture" && !strings.Contains(stdout.String(), "native screen") {
			t.Errorf("native capture=%q", stdout.String())
		}
	}
	var got []string
	for len(keys) > 0 {
		got = append(got, <-keys)
	}
	if len(got) < 2 || strings.Join(got[len(got)-2:], ",") != "Down,Enter" {
		t.Fatalf("native modal keys=%v", got)
	}
}

func serveNativeAdapter(t *testing.T, path string, table map[string]string, recorded ...chan string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Error(err)
					return
				}
				var request map[string]any
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					return
				}
				if len(recorded) > 0 && request["method"] == "pane.send_keys" {
					params, _ := request["params"].(map[string]any)
					keys, _ := params["keys"].([]any)
					for _, key := range keys {
						recorded[0] <- fmt.Sprint(key)
					}
				}
				_ = json.NewEncoder(conn).
					Encode(map[string]any{"id": request["id"], "result": json.RawMessage(table[fmt.Sprint(request["method"])])})
			}()
		}
	}()
}
