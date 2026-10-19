package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReportLineIsExactlyHerdrsRequest(t *testing.T) {
	line, err := Report{
		Pane: "w1:p1", Agent: "claude", State: StateBlocked, Message: "needs\napproval",
		SessionID: "s1", ResumeArgv: []string{"pfm", "chat", "open", "s1"}, Seq: 42,
	}.Line()
	want := `{"id":"pfm-42","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"blocked","message":"needs approval","agent_session_id":"s1","resume_argv":["pfm","chat","open","s1"],"seq":42}}`
	if err != nil || string(line) != want {
		t.Fatalf("Line = %s, %v\nwant   %s", line, err, want)
	}
	bare, err := Report{Pane: "w1:p1", Agent: "codex", State: StateIdle, Seq: 7}.Line()
	wantBare := `{"id":"pfm-7","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"codex","state":"idle","seq":7}}`
	if err != nil || string(bare) != wantBare {
		t.Fatalf("Line = %s, %v\nwant   %s", bare, err, wantBare)
	}
	shell, err := Report{Pane: "w1:p3", Source: "pfm-shell", Agent: "shell", State: StateWorking, Seq: 8}.Line()
	wantShell := `{"id":"pfm-8","method":"pane.report_agent","params":{"pane_id":"w1:p3","source":"pfm-shell","agent":"shell","state":"working","seq":8}}`
	if err != nil || string(shell) != wantShell {
		t.Fatalf("Line = %s, %v\nwant   %s", shell, err, wantShell)
	}
	for _, report := range []Report{{Agent: "a", State: StateIdle}, {Pane: "p", State: StateIdle}, {Pane: "p", Agent: "a"}} {
		if line, err := report.Line(); err == nil {
			t.Errorf("Line(%+v) = %s, want an error for a missing pane, agent or state", report, line)
		}
	}
}

func TestReleaseLineIsExactlyHerdrsRequest(t *testing.T) {
	line, err := Release{Pane: "w1:p1", Agent: "opencode", Seq: 9}.Line()
	want := `{"id":"pfm-9","method":"pane.release_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"opencode","seq":9}}`
	if err != nil || string(line) != want {
		t.Fatalf("Line = %s, %v\nwant   %s", line, err, want)
	}
	shell, err := Release{Pane: "w1:p3", Source: "pfm-shell", Agent: "shell", Seq: 10}.Line()
	wantShell := `{"id":"pfm-10","method":"pane.release_agent","params":{"pane_id":"w1:p3","source":"pfm-shell","agent":"shell","seq":10}}`
	if err != nil || string(shell) != wantShell {
		t.Fatalf("Line = %s, %v\nwant   %s", shell, err, wantShell)
	}
	if line, err := (Release{Agent: "a"}).Line(); err == nil {
		t.Errorf("Line without a pane = %s, want an error", line)
	}
}

func TestMessageIsCappedAtTwoHundredFortyRunes(t *testing.T) {
	line, err := Report{Pane: "p", Agent: "a", State: StateBlocked, Message: strings.Repeat("é", 500), Seq: 1}.Line()
	if err != nil || !strings.Contains(string(line), strings.Repeat("é", 239)+"…") ||
		strings.Contains(string(line), strings.Repeat("é", 240)) {
		t.Fatalf("Line = %s, %v, want 239 runes and an ellipsis", line, err)
	}
}

func serve(t *testing.T, respond func(request string) string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if _, err := conn.Write([]byte(respond(strings.TrimSuffix(line, "\n")) + "\n")); err != nil {
						return
					}
				}
			}()
		}
	}()
	return path
}

// serveOnePerConnection is Herdr's real socket: it reads one request line,
// answers it and closes the connection (a second line on it is never read).
func serveOnePerConnection(t *testing.T, connections *int, mu *sync.Mutex) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			*connections++
			mu.Unlock()
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				request := strings.TrimSuffix(line, "\n")
				_, _ = conn.Write([]byte(`{"id":"x","result":{"type":"echo","line":"` + request + `"}}` + "\n"))
			}()
		}
	}()
	return path
}

func TestCallOpensOneConnectionPerRequestLikeHerdr(t *testing.T) {
	var mu sync.Mutex
	connections := 0
	path := serveOnePerConnection(t, &connections, &mu)
	results, err := Call(context.Background(), path, []byte("release"), []byte("clear-title"), []byte("third"))
	if err != nil {
		t.Fatalf("Call over a one-request-per-connection socket: %v, want every line answered", err)
	}
	if len(results) != 3 || !strings.Contains(string(results[0]), `"release"`) ||
		!strings.Contains(string(results[1]), `"clear-title"`) || !strings.Contains(string(results[2]), `"third"`) {
		t.Fatalf("Call = %s, want the results of release, clear-title, third in order", results)
	}
	mu.Lock()
	defer mu.Unlock()
	if connections != 3 {
		t.Fatalf("Call dialed %d connections for 3 lines, want 3", connections)
	}
}

func TestSendDeliversEachLineAndReadsItsResponse(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	path := serve(t, func(request string) string {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, request)
		return `{"id":"x","result":{"type":"ok"}}`
	})
	if err := Send(context.Background(), path, []byte("one"), []byte("two")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(seen, "|") != "one|two" {
		t.Fatalf("server saw %q, want one|two", seen)
	}
}

func TestSendNamesAHerdrRefusal(t *testing.T) {
	path := serve(t, func(string) string {
		return `{"id":"x","error":{"code":"resume_not_accepted","message":"source does not hold the pane"}}`
	})
	err := Send(context.Background(), path, []byte(`{"id":"x"}`))
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != "resume_not_accepted" || !RefusedResume(err) {
		t.Fatalf("Send = %v, want a Refusal with code resume_not_accepted that RefusedResume recognises", err)
	}
	other := serve(t, func(string) string { return `{"id":"x","error":{"code":"pane_not_found","message":"no pane"}}` })
	if err := Send(
		context.Background(),
		other,
		[]byte("x"),
	); err == nil || RefusedResume(err) ||
		!strings.Contains(err.Error(), "pane_not_found") {
		t.Fatalf("Send = %v, want a pane_not_found refusal that is not a resume refusal", err)
	}
}

func TestSendReportsEveryTransportFailure(t *testing.T) {
	if err := Send(
		context.Background(),
		filepath.Join(t.TempDir(), "missing.sock"),
		[]byte("x"),
	); err == nil ||
		!strings.Contains(err.Error(), "dial") {
		t.Errorf("Send to a missing socket = %v, want a dial error naming the socket", err)
	}
	garbage := serve(t, func(string) string { return "not json" })
	if err := Send(
		context.Background(),
		garbage,
		[]byte("x"),
	); err == nil ||
		!strings.Contains(err.Error(), "decode response") {
		t.Errorf("Send with an unparseable response = %v, want a decode error", err)
	}
}

// rawServe runs handle on every connection to a fresh Herdr socket.
func rawServe(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				handle(conn)
			}()
		}
	}()
	return path
}

// A hook's whole budget is its context: a silent Herdr ends the exchange at
// the context's deadline, and a response that never ends a line stops at the
// read cap, not at the I/O deadline.
func TestSendIsBoundedByItsContextAndAResponseCap(t *testing.T) {
	silent := rawServe(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := Send(ctx, silent, []byte("x")); err == nil || time.Since(started) > 500*time.Millisecond {
		t.Errorf(
			"Send to a silent Herdr = %v after %s, want an error at the 50ms context deadline",
			err,
			time.Since(started),
		)
	}
	endless := rawServe(t, func(conn net.Conn) {
		chunk := []byte(strings.Repeat("x", 4096))
		for {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	})
	err := Send(context.Background(), endless, []byte("x"))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("Send with an endless response = %v, want the read cap to end it before the I/O deadline", err)
	}
}

func TestAgentAndTitleLinesAreExactlyHerdrsRequests(t *testing.T) {
	long := strings.Repeat("é", 100)
	for _, test := range []struct {
		name string
		line func() ([]byte, error)
		want string
	}{
		{"agent.list", AgentList{Seq: 3}.Line, `{"id":"pfm-3","method":"agent.list","params":{}}`},
		{"agent.rename", AgentRename{Target: "w1:p1", Name: "my-chat", Seq: 4}.Line, `{"id":"pfm-4","method":"agent.rename","params":{"target":"w1:p1","name":"my-chat"}}`},
		{"title", Metadata{Pane: "w1:p1", Title: " Her\tDer\nChat ", Seq: 5}.Line, `{"id":"pfm-5","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm","title":"Her Der Chat"}}`},
		{"title capped on a rune boundary", Metadata{Pane: "p", Title: long, Seq: 6}.Line, `{"id":"pfm-6","method":"pane.report_metadata","params":{"pane_id":"p","source":"pfm","title":"` + strings.Repeat("é", 80) + `"}}`},
		{"clear_title", Metadata{Pane: "w1:p1", ClearTitle: true, Seq: 7}.Line, `{"id":"pfm-7","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm","clear_title":true}}`},
		{"title under its own source", Metadata{Pane: "w1:p1", Source: "pfm-shell", Title: "make exit 0 · 12s", Seq: 8}.Line, `{"id":"pfm-8","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm-shell","title":"make exit 0 · 12s"}}`},
		{"pane.list", PaneList{Seq: 8}.Line, `{"id":"pfm-8","method":"pane.list","params":{}}`},
		{"pane.process_info", ProcessInfo{Pane: "w2:p1", Seq: 9}.Line, `{"id":"pfm-9","method":"pane.process_info","params":{"pane_id":"w2:p1"}}`},
		{"tab.get", TabGet{Tab: "w6:t3", Seq: 10}.Line, `{"id":"pfm-10","method":"tab.get","params":{"tab_id":"w6:t3"}}`},
		{"tab.rename", TabRename{Tab: "w6:t3", Label: " XC\tAWS\n", Seq: 11}.Line, `{"id":"pfm-11","method":"tab.rename","params":{"tab_id":"w6:t3","label":"XC AWS"}}`},
		{"tab label capped on a rune boundary", TabRename{Tab: "t", Label: long, Seq: 12}.Line, `{"id":"pfm-12","method":"tab.rename","params":{"tab_id":"t","label":"` + strings.Repeat("é", 80) + `"}}`},
	} {
		if line, err := test.line(); err != nil || string(line) != test.want {
			t.Errorf("%s: Line = %s, %v\nwant %s", test.name, line, err, test.want)
		}
	}
	for name, line := range map[string]func() ([]byte, error){
		"rename without a name":   AgentRename{Target: "p"}.Line,
		"title of only controls":  Metadata{Pane: "p", Title: " \n\t "}.Line,
		"metadata without a pane": Metadata{Title: "x"}.Line,
		"process info no pane":    ProcessInfo{}.Line,
		"tab.get without a tab":   TabGet{}.Line,
		"tab label of controls":   TabRename{Tab: "t", Label: " \n\t "}.Line,
		"tab.rename without tab":  TabRename{Label: "x"}.Line,
	} {
		if got, err := line(); err == nil {
			t.Errorf("%s: Line = %s, want an error", name, got)
		}
	}
}

func TestResultParsersReadHerdrsShapesAndRefuseOthers(t *testing.T) {
	agents, err := ParseAgentList(
		json.RawMessage(
			`{"type":"agent_list","agents":[{"name":null,"pane_id":"w1:p1"},{"name":"chat-a","pane_id":"w1:p2",` +
				`"workspace_id":"w1","tab_id":"w1:t2","agent":"claude","agent_status":"working"}]}`,
		),
	)
	if err != nil || len(agents) != 2 || agents[0] != (Agent{PaneID: "w1:p1"}) ||
		agents[1] != (Agent{
			PaneID: "w1:p2", WorkspaceID: "w1", TabID: "w1:t2", Agent: "claude", Status: "working", Name: "chat-a",
		}) {
		t.Errorf("ParseAgentList = %+v, %v, want an unnamed and a named agent", agents, err)
	}
	panes, err := ParsePanes(
		json.RawMessage(`{"type":"pane_list","panes":[{"pane_id":"w6:p3","terminal_id":"term_1","focused":true}]}`),
	)
	if err != nil || len(panes) != 1 || panes[0] != (Pane{PaneID: "w6:p3", TerminalID: "term_1"}) {
		t.Errorf("ParsePanes = %+v, %v", panes, err)
	}
	pids, err := ParseProcessInfo(
		json.RawMessage(
			`{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","foreground_processes":[{"pid":4242,"name":"tmux"},{"pid":7,"name":"x"}]}}`,
		),
	)
	if err != nil || len(pids) != 2 || pids[0] != 4242 || pids[1] != 7 {
		t.Errorf("ParseProcessInfo = %v, %v", pids, err)
	}
	empty, err := ParseProcessInfo(
		json.RawMessage(`{"type":"pane_process_info","process_info":{"foreground_processes":[]}}`),
	)
	if err != nil || len(empty) != 0 {
		t.Errorf("explicit empty foreground = %v, %v", empty, err)
	}
	// The shape a live Herdr answered for a plain zsh pane.
	shellPID, err := ParseShellPID(json.RawMessage(
		`{"type":"pane_process_info","process_info":{"foreground_process_group_id":51234,"foreground_processes":` +
			`[{"argv":["-zsh"],"argv0":"-zsh","cmdline":"-zsh","cwd":"/tmp","name":"zsh","pid":51234}],` +
			`"pane_id":"wD:p2","shell_pid":51234}}`,
	))
	if err != nil || shellPID != 51234 {
		t.Errorf("ParseShellPID = %d, %v, want 51234", shellPID, err)
	}
	// The shape a live Herdr answered for the tab of an agent's pane.
	tab, err := ParseTab(json.RawMessage(
		`{"type":"tab_info","tab":{"agent_status":"idle","focused":false,"label":"1","number":3,` +
			`"pane_count":1,"tab_id":"w6:t3","workspace_id":"w6"}}`,
	))
	if err != nil || tab != (Tab{TabID: "w6:t3", Label: "1", PaneCount: 1}) {
		t.Errorf("ParseTab = %+v, %v, want w6:t3 labelled 1 holding one pane", tab, err)
	}
	for name, parse := range map[string]func() error{
		"tab of another type": func() error {
			_, err := ParseTab(json.RawMessage(`{"type":"ok","tab":{"tab_id":"w6:t3","label":"1","pane_count":1}}`))
			return err
		},
		"tab garbage":        func() error { _, err := ParseTab(json.RawMessage(`[1]`)); return err },
		"tab without its id": func() error { _, err := ParseTab(json.RawMessage(`{"type":"tab_info","tab":{}}`)); return err },
		"shell pid of another type": func() error {
			_, err := ParseShellPID(json.RawMessage(`{"type":"ok","process_info":{"shell_pid":7}}`))
			return err
		},
		"shell pid garbage":      func() error { _, err := ParseShellPID(json.RawMessage(`[1]`)); return err },
		"agents of another type": func() error { _, err := ParseAgentList(json.RawMessage(`{"type":"ok"}`)); return err },
		"panes of another type":  func() error { _, err := ParsePanes(json.RawMessage(`{"type":"agent_list"}`)); return err },
		"process info garbage":   func() error { _, err := ParseProcessInfo(json.RawMessage(`[1]`)); return err },
		"missing foreground": func() error {
			_, err := ParseProcessInfo(json.RawMessage(`{"type":"pane_process_info","process_info":{}}`))
			return err
		},
		"null foreground": func() error {
			_, err := ParseProcessInfo(json.RawMessage(`{"type":"pane_process_info","process_info":{"foreground_processes":null}}`))
			return err
		},
		"process info of another type": func() error {
			_, err := ParseProcessInfo(json.RawMessage(`{"type":"ok","process_info":{"foreground_processes":[{"pid":7,"name":"x"}]}}`))
			return err
		},
		"panes without a result": func() error { _, err := ParsePanes(nil); return err },
	} {
		if err := parse(); err == nil {
			t.Errorf("%s: parsed without an error", name)
		}
	}
}

func TestCallReturnsEachResultInRequestOrder(t *testing.T) {
	path := serve(
		t,
		func(request string) string { return `{"id":"x","result":{"type":"echo","line":"` + request + `"}}` },
	)
	results, err := Call(context.Background(), path, []byte("one"), []byte("two"))
	if err != nil || len(results) != 2 || !strings.Contains(string(results[0]), `"one"`) ||
		!strings.Contains(string(results[1]), `"two"`) {
		t.Fatalf("Call = %s, %v, want the results of one then two", results, err)
	}
	big := serve(t, func(string) string { return `{"id":"x","result":{"pad":"` + strings.Repeat("p", 200<<10) + `"}}` })
	if results, err := Call(context.Background(), big, []byte("x")); err != nil || len(results) != 1 {
		t.Fatalf("Call of a 200 KiB answer = %d results, %v, want it read whole (a big pane.list)", len(results), err)
	}
}
