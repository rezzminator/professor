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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// socketFromRun reads the socket out of a run report line, which is the handle
// every later verb addresses the chat by.
func socketFromRun(t *testing.T, report string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 && (strings.HasPrefix(fields[2], "cc-") ||
			strings.HasPrefix(fields[2], "cx-")) {
			return fields[2]
		}
	}
	t.Fatalf("no socket in run report: %q", report)
	return ""
}

// TestRunAwaitsTheAnswerItAskedFor is the launch half of two-way: one command
// starts a chat, gives it work, and comes back with the reply — with the
// answer alone on stdout, so a caller can capture it.
func TestRunAwaitsTheAnswerItAskedFor(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "await worker",
		"--cwd", jail.root + "/work",
		"--await", "--timeout", "60", "--settle", "1",
		"audit the firewall",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "ack: audit the firewall" {
		t.Fatalf("stdout = %q, want the answer alone", got)
	}
	if !strings.Contains(stderr.String(), "attach: tmux -L cc-") {
		t.Fatalf("the launch summary did not move to stderr: %q", stderr.String())
	}
}

// TestAskHoldsATwoWayConversation is the other half: a chat that is already
// running answers one question, then another, and each answer belongs to its
// own question.
func TestAskHoldsATwoWayConversation(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	previous := askAwaitTimings
	askAwaitTimings.Poll = 10 * time.Millisecond
	askAwaitTimings.Settle = 200 * time.Millisecond
	t.Cleanup(func() { askAwaitTimings = previous })
	jail := newRunJail(t)
	statedTestSender(t)
	t.Setenv("CHAT_CALL_POLL", "0.01")
	t.Setenv("CHAT_CALL_ENTER_SETTLE", "0.02")
	t.Setenv("CHAT_CALL_PROOF_SETTLE", "0.02")
	defer jail.killSockets(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "talker",
		"--cwd", jail.root + "/work",
		"open the shop",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	for _, question := range []string{"first question", "second question"} {
		var out, errors bytes.Buffer
		code := run([]string{
			"chat", "ask", "--timeout", "60", "--settle", "0",
			socket, question,
		}, &out, &errors)
		if code != 0 {
			t.Fatalf("ask %q exit=%d out=%q err=%q", question, code, out.String(), errors.String())
		}
		answer := strings.TrimSpace(out.String())
		if !strings.HasPrefix(answer, "ack: "+question) {
			t.Fatalf("answer to %q = %q, want the reply to THAT question", question, answer)
		}
	}
}

// TestAskReportsATimeoutWithoutLosingDelivery: a chat that hears the question
// and says nothing is a different outcome from a chat that never heard it, and
// the exit code has to tell them apart.
func TestAskReportsATimeoutWithoutLosingDelivery(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	statedTestSender(t)
	defer jail.killSockets(t)
	t.Setenv("STUB_MUTE", "1")
	// A chat still at work when the reply watch closes is a launch that
	// worked: the watch is shortened, never skipped.
	restoreReply := launchReplyWindow
	launchReplyWindow = 250 * time.Millisecond
	t.Cleanup(func() { launchReplyWindow = restoreReply })

	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"chat", "new",
		"--name", "mute worker",
		"--cwd", jail.root + "/work",
		"say nothing",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exit=%d stderr=%q", code, stderr.String())
	}
	socket := socketFromRun(t, stdout.String())

	var out, errorsOut bytes.Buffer
	code := run([]string{
		"chat", "ask", "--timeout", "1", "--settle", "0",
		socket, "answer me",
	}, &out, &errorsOut)
	if code != codeAwaitTimeout {
		t.Fatalf("ask exit=%d, want %d (out=%q err=%q)",
			code, codeAwaitTimeout, out.String(), errorsOut.String())
	}
	if !strings.Contains(errorsOut.String(), "the message was delivered") {
		t.Fatalf("timeout report = %q", errorsOut.String())
	}
	// The question itself must still have landed in the chat's record.
	var transcript, transcriptErrors bytes.Buffer
	if code := run([]string{
		"chat", "read", socket, "--tail", "5", "--condensed",
	}, &transcript, &transcriptErrors); code != 0 {
		t.Fatalf("transcript exit=%d err=%q", code, transcriptErrors.String())
	}
	if !strings.Contains(transcript.String(), "answer me") {
		t.Fatalf("transcript = %q, want the delivered question", transcript.String())
	}
}

// TestRunRefusesToCallAnUnheardPromptDelivered is the whole point of the
// verification: an engine that never records the prompt must not be reported
// as a working chat that got its orders.
func TestRunRefusesToCallAnUnheardPromptDelivered(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newRunJail(t)
	defer jail.killSockets(t)
	t.Setenv("CC_STUB_DEAF", "1")
	restoreGrace, restoreWindow := launchGrace, launchProofWindow
	restoreRescue := launchRescueWindow
	launchGrace, launchProofWindow = 50*time.Millisecond, 250*time.Millisecond
	launchRescueWindow = 250 * time.Millisecond
	t.Cleanup(func() {
		launchGrace, launchProofWindow = restoreGrace, restoreWindow
		launchRescueWindow = restoreRescue
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"chat", "new",
		"--name", "deaf worker",
		"--cwd", jail.root + "/work",
		"a prompt that will be eaten",
	}, &stdout, &stderr)
	if code != codeUndelivered {
		t.Fatalf("run exit=%d, want %d (stdout=%q stderr=%q)",
			code, codeUndelivered, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "never recorded the prompt") ||
		!strings.Contains(stderr.String(), "attach it and look") {
		t.Fatalf("refusal = %q, want it to name the failure and how to look", stderr.String())
	}
	// The retry is reported whether or not it worked: a rescue that silently
	// failed would read as a launch that was never retried.
	if !strings.Contains(stderr.String(), "retry did not") {
		t.Fatalf("refusal = %q, want it to say the retry was tried", stderr.String())
	}
	// The chat itself is still up: an undelivered prompt is not a reason to
	// kill a seat the user may want to drive by hand.
	if !strings.Contains(stdout.String(), "\tnamed\t") {
		t.Fatalf("run report = %q, want the live chat still reported", stdout.String())
	}
}

// Two seats deliberately share one socket. Delivery and the subsequent await
// must retain the selected conversation, for both native and raw engines.
func TestAskNativeSharedSocketKeepsConversation(t *testing.T) {
	root := jailTest(t)
	writeJailedCodexAuth(t, root)
	statedTestSender(t)
	values := jailPaths(t)
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")
	previous := askAwaitTimings
	askAwaitTimings.Poll, askAwaitTimings.Settle = time.Millisecond, time.Millisecond
	t.Cleanup(func() { askAwaitTimings = previous })
	socket := filepath.Join(testjail.ShortRoot(t), "ask.sock")
	state := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	var seats []fleetdb.Seat
	files := map[string]string{}
	for i, engine := range []string{"cc", "cx"} {
		id := fmt.Sprintf("11111111-1111-4111-8111-%012d", i+1)
		pane := fmt.Sprintf("w6:p%d", i+1)
		seat := fleetdb.Seat{
			SeatID: "ask-" + engine, SessionID: id, Name: "ASK_" + engine, Engine: engine,
			Backend: "herdr", State: "live", HerdrSocket: socket, HerdrPane: pane, HerdrWorkspace: "w6",
			HerdrTerminalID: "fixture-" + pane, SeatHostPID: 900001 + i, SeatHostStart: 100,
		}
		seats = append(seats, seat)
		path := filepath.Join(root, "claude", "project", id+".jsonl")
		body := `{"type":"user","cwd":"/work/native","message":{"content":"initial"}}` + "\n"
		if engine == "cx" {
			path = filepath.Join(
				root,
				"codex",
				"sessions",
				"2030",
				"01",
				"02",
				"rollout-2030-01-02T03-04-05-"+id+".jsonl",
			)
			body = `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"/work/native","thread_source":"user"}}` + "\n" + `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"initial"}]}}` + "\n"
		}
		files[pane] = path
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := state.PutSeat(context.Background(), seat); err != nil {
			t.Fatal(err)
		}
		writeNativeAskHost(t, values.ProcRoot, seat)
	}
	var trust, failRead atomic.Bool
	var deliveries atomic.Int32
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = listener.Close(); <-done })
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string         `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				pane, _ := req.Params["pane_id"].(string)
				if pane == "" {
					pane, _ = req.Params["target"].(string)
				}
				var result any
				switch req.Method {
				case "pane.list":
					result = map[string]any{
						"type": "pane_list",
						"panes": []any{
							testjail.HerdrPaneInfo("w6:p1")["pane"],
							testjail.HerdrPaneInfo("w6:p2")["pane"],
						},
					}
				case "pane.get":
					result = testjail.HerdrPaneInfo(pane)
				case "pane.process_info":
					pid := 900001
					if pane == "w6:p2" {
						pid++
					}
					result = map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id":              pane,
							"shell_pid":            pid,
							"foreground_processes": []any{map[string]any{"pid": pid, "argv0": "pfm"}},
						},
					}
				case "pane.read":
					if failRead.Load() {
						if err := json.NewEncoder(conn).
							Encode(map[string]any{"id": req.ID, "error": map[string]string{"code": "read_failed", "message": "fixture capture failed"}}); err != nil {
							t.Error(err)
						}
						return
					}
					screen := "❯ \n────────\n"
					if pane == "w6:p2" {
						screen = "› \n"
					}
					if trust.Load() && pane == "w6:p1" {
						screen = "Quick safety check\nYes, I trust this folder\nNo, exit"
					}
					result = map[string]any{
						"type": "pane_read",
						"read": map[string]any{
							"pane_id":      pane,
							"workspace_id": "w6",
							"text":         screen,
							"truncated":    false,
							"format":       req.Params["format"],
						},
					}
				case "agent.get", "agent.prompt":
					kind, status := "agent_info", "idle"
					if trust.Load() {
						status = "blocked"
					}
					if req.Method == "agent.prompt" {
						kind, status = "agent_prompted", "working"
					}
					result = map[string]any{
						"type": kind,
						"agent": map[string]any{
							"pane_id":          pane,
							"terminal_id":      "fixture-" + pane,
							"agent_status":     status,
							"state_change_seq": 1,
						},
					}
				case "pane.send_text", "pane.send_input", "pane.send_keys":
					result = map[string]any{"type": "ok"}
				default:
					t.Errorf("unexpected native method %s", req.Method)
					return
				}
				if req.Method == "agent.prompt" || req.Method == "pane.send_keys" {
					deliveries.Add(1)
					body := `{"type":"user","message":{"content":"question"}}` + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"native answer cc"}]}}` + "\n"
					if pane == "w6:p2" {
						body = `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"question"}]}}` + "\n" + `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native answer cx"}]}}` + "\n"
					}
					file, err := os.OpenFile(files[pane], os.O_APPEND|os.O_WRONLY, 0o600)
					if err != nil {
						t.Error(err)
						return
					}
					if _, err := file.WriteString(body); err != nil {
						t.Error(err)
					}
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				}
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
					t.Error(err)
				}
			}()
		}
	}()
	for _, seat := range seats {
		for _, target := range []string{seat.Name, seat.SessionID} {
			t.Run(seat.Engine+"/"+target, func(t *testing.T) {
				var out, errout bytes.Buffer
				code := run([]string{"chat", "ask", "--timeout", "3", target, "answer only"}, &out, &errout)
				if code != 0 || strings.TrimSpace(out.String()) != "native answer "+seat.Engine {
					t.Fatalf("ask target=%s exit=%d out=%q err=%q", target, code, out.String(), errout.String())
				}
			})
		}
	}
	beforeTrust := deliveries.Load()
	trust.Store(true)
	for _, verb := range []string{"ask", "stream"} {
		var out, errout bytes.Buffer
		args := []string{"chat", verb, seats[0].Name}
		if verb == "ask" {
			args = append(args, "answer only")
		}
		code := run(args, &out, &errout)
		if code != codeUndelivered || !strings.Contains(errout.String(), "folder-trust dialog") ||
			!strings.Contains(errout.String(), "w6:p1") ||
			strings.Contains(errout.String(), "gone") {
			t.Errorf("%s trust exit=%d out=%q err=%q", verb, code, out.String(), errout.String())
		}
	}
	if deliveries.Load() != beforeTrust {
		t.Fatal("trust refusal sent input to a live seat")
	}
	failRead.Store(true)
	var failedOut, failedErr bytes.Buffer
	if code := run(
		[]string{"chat", "stream", seats[0].Name},
		&failedOut,
		&failedErr,
	); code != 1 || !strings.Contains(failedErr.String(), "fixture capture failed") ||
		strings.Contains(failedErr.String(), "gone") {
		t.Fatalf("failed stream probe exit=%d err=%q", code, failedErr.String())
	}
	failRead.Store(false)
	// A genuinely ambiguous name remains a refusal, not an arbitrary delivery.
	seats[1].Name = seats[0].Name
	if err := state.PutSeat(context.Background(), seats[1]); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if code := run(
		[]string{"chat", "ask", seats[0].Name, "answer only"},
		&out,
		&errout,
	); code == 0 ||
		!strings.Contains(errout.String(), "matches 2 chats") {
		t.Fatalf("ambiguous ask exit=%d err=%q", code, errout.String())
	}
}

func writeNativeAskHost(t *testing.T, root string, seat fleetdb.Seat) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, strconv.Itoa(seat.SeatHostPID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields("S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
	if err := os.WriteFile(
		filepath.Join(dir, "stat"),
		[]byte(fmt.Sprintf("%d (pfm) %s", seat.SeatHostPID, strings.Join(fields, " "))),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "cmdline"),
		[]byte("/fixture/pfm\x00internal\x00seat-host\x00"+seat.SeatID+"\x00"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}
