package hookentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHerdrStateOutsideHerdrIsASilentNoOp(t *testing.T) {
	server := newFakeHerdr(t)
	clock := int64(1_000_000)
	for _, env := range []map[string]string{
		{},
		{"HERDR_ENV": "1"},
		{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1"},
		{"HERDR_PANE_ID": "w1:p1", "HERDR_SOCKET_PATH": server.path},
	} {
		got := runHerdr(t, env, nil, &clock, []string{"--engine", "claude", "working"}, `{"session_id":"s1"}`)
		if got.rc != 0 || got.stderr != "" {
			t.Fatalf("env=%v: rc=%d stderr=%q, want a silent exit 0", env, got.rc, got.stderr)
		}
	}
	if lines := server.requests(); len(lines) != 0 {
		t.Fatalf("requests outside Herdr = %q, want none", lines)
	}
}

func TestHerdrStateReportsTheExactRequestLines(t *testing.T) {
	const session = "6b1a2c3d-0000-4000-8000-000000000001"
	payload := `{"session_id":"` + session + `","hook_event_name":"x","cwd":"/work"}`
	resume := `"agent_session_id":"` + session + `","resume_argv":["pfm","chat","open","` + session + `"]`
	for _, test := range []struct {
		name   string
		args   []string
		stdin  string
		reason string
		want   []string
	}{
		{
			"start is idle with session and resume",
			[]string{"--engine", "claude", "start"},
			payload,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"idle",` + resume + `,"seq":1001000}}`},
		},
		{
			"working",
			[]string{"--engine", "codex", "working"},
			payload,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"codex","state":"working",` + resume + `,"seq":1001000}}`},
		},
		{
			"idle",
			[]string{"--engine", "opencode", "idle"},
			payload,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"opencode","state":"idle",` + resume + `,"seq":1001000}}`},
		},
		{
			"blocked carries its message",
			[]string{"--engine", "claude", "blocked"},
			`{"session_id":"` + session + `","message":"needs approval"}`,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"blocked","message":"needs approval",` + resume + `,"seq":1001000}}`},
		},
		{
			"permission_prompt notification is blocked",
			[]string{"--engine", "claude", "notification"},
			`{"session_id":"` + session + `","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"blocked","message":"Claude needs your permission to use Bash",` + resume + `,"seq":1001000}}`},
		},
		{
			"elicitation_dialog notification is blocked",
			[]string{"--engine", "claude", "notification"},
			`{"session_id":"` + session + `","notification_type":"elicitation_dialog","message":"pick one"}`,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"blocked","message":"pick one",` + resume + `,"seq":1001000}}`},
		},
		{
			"idle_prompt notification is idle",
			[]string{"--engine", "claude", "notification"},
			`{"session_id":"` + session + `","notification_type":"idle_prompt","message":"waiting"}`,
			"",
			[]string{`{"id":"pfm-1001000","method":"pane.report_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","state":"idle",` + resume + `,"seq":1001000}}`},
		},
		{
			"end releases the pane and clears its title",
			[]string{"--engine", "claude", "end"},
			`{"session_id":"` + session + `","reason":"prompt_input_exit"}`,
			"",
			[]string{
				`{"id":"pfm-1001000","method":"pane.release_agent","params":{"pane_id":"w1:p1","source":"pfm","agent":"claude","seq":1001000}}`,
				`{"id":"pfm-1001001","method":"pane.report_metadata","params":{"pane_id":"w1:p1","source":"pfm","clear_title":true}}`,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeHerdr(t)
			clock := int64(1_000_000)
			got := runHerdr(t, herdrPaneEnv(server, "w1:p1"), nil, &clock, test.args, test.stdin)
			if got.rc != 0 || got.stderr != "" {
				t.Fatalf("rc=%d stderr=%q, want a quiet exit 0", got.rc, got.stderr)
			}
			if lines := server.requests(); !slices.Equal(lines, test.want) {
				t.Fatalf("requests = %q\nwant      %q", lines, test.want)
			}
		})
	}
}

func TestHerdrStateSendsNothingForEventsThatDoNotChangeThePane(t *testing.T) {
	const session = "6b1a2c3d-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{"sub-agent event", []string{"--engine", "claude", "working"}, `{"session_id":"` + session + `","agent_id":"agent-7"}`},
		{"sub-agent stop", []string{"--engine", "claude", "idle"}, `{"session_id":"` + session + `","agent_id":"agent-7"}`},
		{"session swap by /clear", []string{"--engine", "claude", "end"}, `{"session_id":"` + session + `","reason":"clear"}`},
		{"session swap by resume", []string{"--engine", "claude", "end"}, `{"session_id":"` + session + `","reason":"resume"}`},
		{"unmapped notification", []string{"--engine", "claude", "notification"}, `{"session_id":"` + session + `","notification_type":"auth_success"}`},
		// A compaction restarts the session mid-turn: the pane stays working.
		{"compaction restart", []string{"--engine", "claude", "start"}, `{"session_id":"` + session + `","source":"compact"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newFakeHerdr(t)
			clock := int64(1_000_000)
			got := runHerdr(t, herdrPaneEnv(server, "w1:p1"), nil, &clock, test.args, test.stdin)
			if got.rc != 0 || got.stderr != "" {
				t.Fatalf("rc=%d stderr=%q, want a quiet exit 0", got.rc, got.stderr)
			}
			if lines := server.requests(); len(lines) != 0 {
				t.Fatalf("requests = %q, want none", lines)
			}
		})
	}
}

func TestHerdrStateSeqIsStrictlyIncreasingAcrossProcesses(t *testing.T) {
	server := newFakeHerdr(t)
	clock := int64(1_000_000)
	env := herdrPaneEnv(server, "w1:p1")
	for _, event := range []string{"start", "working", "working", "blocked", "idle", "end"} {
		runHerdr(t, env, nil, &clock, []string{"--engine", "claude", event}, `{"session_id":"s1","message":"m"}`)
	}
	last := uint64(0)
	lines := server.requests()
	seen := 0
	for _, line := range lines {
		var request struct {
			Method string `json:"method"`
			Params struct {
				Seq uint64 `json:"seq"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if request.Method != "pane.report_agent" && request.Method != "pane.release_agent" {
			continue
		}
		seen++
		if request.Params.Seq <= last {
			t.Fatalf("seq %d after %d in %q, want strictly increasing", request.Params.Seq, last, lines)
		}
		last = request.Params.Seq
	}
	if seen != 6 {
		t.Fatalf("seq-carrying requests = %d (%q), want 6", seen, lines)
	}
}

func TestHerdrStateOmitsAResumeCommandHerdrWouldRefuse(t *testing.T) {
	for _, session := range []string{"it's", "a\nb", ""} {
		server := newFakeHerdr(t)
		clock := int64(1_000_000)
		payload, _ := json.Marshal(map[string]string{"session_id": session})
		got := runHerdr(
			t,
			herdrPaneEnv(server, "w1:p1"),
			nil,
			&clock,
			[]string{"--engine", "claude", "working"},
			string(payload),
		)
		lines := server.requests()
		if got.rc != 0 || len(lines) != 1 || strings.Contains(lines[0], "resume_argv") {
			t.Fatalf("session %q: rc=%d requests=%q, want one report without resume_argv", session, got.rc, lines)
		}
		if session != "" && !strings.Contains(got.stderr, "resume_argv") {
			t.Fatalf("session %q: stderr=%q, want the omitted resume command named", session, got.stderr)
		}
	}
}

func TestHerdrStateCapsALongMessage(t *testing.T) {
	server := newFakeHerdr(t)
	clock := int64(1_000_000)
	payload, _ := json.Marshal(map[string]string{"session_id": "s1", "message": strings.Repeat("é", 1000)})
	runHerdr(t, herdrPaneEnv(server, "w1:p1"), nil, &clock, []string{"--engine", "claude", "blocked"}, string(payload))
	var request struct {
		Params struct {
			Message string `json:"message"`
		} `json:"params"`
	}
	lines := server.requests()
	if len(lines) != 1 {
		t.Fatalf("requests = %q, want one", lines)
	}
	if err := json.Unmarshal([]byte(lines[0]), &request); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(request.Params.Message)); got != 240 {
		t.Fatalf("message runes = %d, want 240", got)
	}
}

func TestHerdrStateNeverFailsATurn(t *testing.T) {
	server := newFakeHerdr(t)
	clock := int64(1_000_000)
	t.Run("socket gone", func(t *testing.T) {
		env := herdrPaneEnv(server, "w1:p1")
		env["HERDR_SOCKET_PATH"] = filepath.Join(os.TempDir(), "pfm-no-such-herdr.sock")
		got := runHerdr(t, env, nil, &clock, []string{"--engine", "claude", "working"}, `{"session_id":"s1"}`)
		if got.rc != 0 || !strings.Contains(got.stderr, "pfm-no-such-herdr.sock") ||
			!strings.Contains(got.stderr, "herdr-state") {
			t.Fatalf(
				"rc=%d stderr=%q, want exit 0 and a stderr line naming the verb and the socket",
				got.rc,
				got.stderr,
			)
		}
	})
	t.Run("herdr refuses", func(t *testing.T) {
		server.refuse["pane.report_agent"] = "resume_not_accepted"
		got := runHerdr(
			t,
			herdrPaneEnv(server, "w1:p1"),
			nil,
			&clock,
			[]string{"--engine", "claude", "working"},
			`{"session_id":"s1"}`,
		)
		if got.rc != 0 || !strings.Contains(got.stderr, "resume_not_accepted") {
			t.Fatalf("rc=%d stderr=%q, want exit 0 and the refusal code named", got.rc, got.stderr)
		}
	})
	// An unreadable payload cannot say whether a sub-agent sent it, so it
	// reports nothing; a payload over the read cap decodes as garbage.
	for _, test := range []struct{ name, stdin, named string }{
		{"garbage payload", `not json`, "decode"},
		{"empty payload", ``, "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			quiet := newFakeHerdr(t)
			got := runHerdr(
				t,
				herdrPaneEnv(quiet, "w1:p1"),
				nil,
				&clock,
				[]string{"--engine", "claude", "idle"},
				test.stdin,
			)
			if got.rc != 0 || !strings.Contains(got.stderr, test.named) {
				t.Fatalf("rc=%d stderr=%q, want exit 0 and the %s payload named", got.rc, got.stderr, test.named)
			}
			if lines := quiet.requests(); len(lines) != 0 {
				t.Fatalf("requests = %q, want none for an unreadable payload", lines)
			}
		})
	}
	t.Run("usage errors exit 1, never the blocking 2", func(t *testing.T) {
		for _, args := range [][]string{{}, {"bogus"}, {"--engine", "vim", "working"}, {"working", "extra"}} {
			got := runHerdr(t, herdrPaneEnv(server, "w1:p1"), nil, &clock, args, "")
			if got.rc != 1 || !strings.Contains(got.stderr, "usage") {
				t.Fatalf("args=%v: rc=%d stderr=%q, want exit 1 with a usage line", args, got.rc, got.stderr)
			}
		}
	})
}

const rigSocket = "/tmp/tmux-1/cc-1"

// chatRig is one pfm chat server seen by a reporter: a Herdr with pane w1:p1
// (terminal term_1, running the tmux client pid 4242), the chat's tmux session
// cc-1 whose pane process is the engine, and a jail around both.
type chatRig struct {
	t     *testing.T
	herdr *fakeHerdr
	tmux  *fakeTmux
	jail  *herdrJail
	env   map[string]string
	clock int64
}

func newChatRig(t *testing.T) *chatRig {
	t.Helper()
	server := newFakeHerdr(t)
	server.addPane("w1:p1", "term_1", 4242)
	return &chatRig{
		t: t, herdr: server, tmux: newFakeTmux("cc-1"), jail: newHerdrJail(t), clock: 1_000_000,
		env: map[string]string{
			"TMUX": rigSocket + ",1,0", "TMUX_PANE": "%0", "HERDR_BIN_PATH": "/opt/herdr",
			// What the shell that created the chat server left in the server's own
			// environment: the pane of a long gone attach.
			"HERDR_ENV": "1", "HERDR_PANE_ID": "w9:p9", "HERDR_SOCKET_PATH": server.path,
		},
	}
}

func (rig *chatRig) client(pane, terminal string) herdrClient {
	return herdrClient{Pane: pane, Terminal: terminal, Socket: rig.herdr.path}
}

// seed stores the chat's @pfm_herdr; every client it registers is attached.
func (rig *chatRig) seed(state herdrStored) {
	rig.t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		rig.t.Fatal(err)
	}
	rig.tmux.stored = string(encoded)
	rig.tmux.clients = nil
	for pid := range state.Clients {
		rig.tmux.clients = append(rig.tmux.clients, pid)
	}
	slices.Sort(rig.tmux.clients)
}

// registered is the usual stored chat: Claude session s1 in the given state,
// attached from the given clients.
func (rig *chatRig) registered(state string, clients map[string]herdrClient) herdrStored {
	return herdrStored{Engine: "claude", SessionID: "s1", State: state, Seq: 500, Clients: clients}
}

func (rig *chatRig) sessionEnv(pane string) {
	rig.tmux.env = fmt.Sprintf(
		"HERDR_ENV=1\nHERDR_PANE_ID=%s\nHERDR_SOCKET_PATH=%s\nHERDR_BIN_PATH=/opt/herdr\nTERM=xterm\n",
		pane, rig.herdr.path,
	)
}

func (rig *chatRig) run(args []string, stdin string) herdrRun {
	rig.t.Helper()
	return runHerdrIn(rig.t, rig.jail, rig.env, rig.tmux, &rig.clock, args, stdin)
}

func (rig *chatRig) engine(event, stdin string) herdrRun {
	rig.t.Helper()
	return rig.run([]string{"--engine", "claude", event}, stdin)
}

// hook runs a tmux-fired verb (sync, detach or name) the way the tmux hooks do.
func (rig *chatRig) hook(verb string, extra ...string) herdrRun {
	rig.t.Helper()
	return rig.run(append([]string{verb, "--socket", rigSocket, "--session", "cc-1"}, extra...), "")
}

func (rig *chatRig) wantQuiet(got herdrRun) {
	rig.t.Helper()
	if got.rc != 0 || got.stderr != "" {
		rig.t.Fatalf("rc=%d stderr=%q, want a quiet exit 0", got.rc, got.stderr)
	}
}

func (rig *chatRig) wantCalls(want ...string) {
	rig.t.Helper()
	if got := rig.herdr.calls(); !slices.Equal(got, want) {
		rig.t.Fatalf("herdr requests =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

const listCall = `pane.list {}`

func reportCall(pane, state, message string) string {
	if message != "" {
		message = `,"message":"` + message + `"`
	}
	return `pane.report_agent {"pane_id":"` + pane + `","source":"pfm","agent":"claude","state":"` + state + `"` + message +
		`,"agent_session_id":"s1","resume_argv":["pfm","chat","open","s1"]}`
}

func releaseCalls(pane string) []string {
	return []string{
		`pane.release_agent {"pane_id":"` + pane + `","source":"pfm","agent":"claude"}`,
		`pane.report_metadata {"pane_id":"` + pane + `","source":"pfm","clear_title":true}`,
	}
}

func renameCall(pane, name string) string {
	return `agent.rename {"target":"` + pane + `","name":"` + name + `"}`
}

func titleCall(pane, title string) string {
	return `pane.report_metadata {"pane_id":"` + pane + `","source":"pfm","title":"` + title + `"}`
}

// Inside a pfm chat tmux server the process environment keeps the Herdr
// variables of whatever shell CREATED the server: events go to the panes of the
// clients registered for the session, and to nothing on a host without Herdr.
func TestHerdrStateInsideTmuxFollowsTheSessionEnvironment(t *testing.T) {
	const payload = `{"session_id":"s1"}`
	t.Run("no Herdr on the host: nothing stored, nothing locked, exit 0", func(t *testing.T) {
		rig := newChatRig(t)
		delete(rig.env, "HERDR_BIN_PATH")
		rig.wantQuiet(rig.engine("working", payload))
		if calls := rig.tmux.setOptionCalls(); len(calls) != 0 {
			t.Fatalf("tmux set-option calls = %q, want none without a Herdr", calls)
		}
		if files := rig.jail.lockFiles(); len(files) != 0 {
			t.Fatalf("lock files = %q, want none", files)
		}
		rig.wantCalls()
	})
	for name, arrange := range map[string]func(*chatRig){
		"HERDR_BIN_PATH names the Herdr": func(*chatRig) {},
		"herdr is on PATH":               func(rig *chatRig) { delete(rig.env, "HERDR_BIN_PATH"); rig.jail.onPath = true },
	} {
		t.Run(name+": the state is stored for a late attach", func(t *testing.T) {
			rig := newChatRig(t)
			arrange(rig)
			rig.wantQuiet(rig.engine("working", payload))
			if state := rig.tmux.storedState(t); state.State != "working" || state.SessionID != "s1" {
				t.Fatalf("stored @pfm_herdr = %q, want the working state and session kept", rig.tmux.stored)
			}
			if files := rig.jail.lockFiles(); len(files) != 1 {
				t.Fatalf("lock files = %q, want the session's lock", files)
			}
			rig.wantCalls()
		})
	}
	t.Run("an event reaches the registered client's pane, never the process environment's", func(t *testing.T) {
		rig := newChatRig(t)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		rig.wantQuiet(rig.engine("working", payload))
		rig.wantCalls(listCall, reportCall("w1:p1", "working", ""))
	})
	for _, test := range []struct{ failing, session string }{
		{"display-message", "no-session"},
		{"show-options", "cc-1"},
	} {
		t.Run("a failing tmux "+test.failing+" is named and logged, never read as no Herdr", func(t *testing.T) {
			rig := newChatRig(t)
			rig.tmux.failOn = test.failing
			got := rig.engine("working", payload)
			if got.rc != 0 || !strings.Contains(got.stderr, test.failing) {
				t.Fatalf("rc=%d stderr=%q, want exit 0 and the failed tmux call named", got.rc, got.stderr)
			}
			if logged := rig.jail.logged(test.session); !strings.Contains(logged, test.failing) {
				t.Fatalf("%s.log = %q, want the failed tmux call named", test.session, logged)
			}
		})
	}
}

// A stored seq above the event's means a later event already wrote.
func TestHerdrStateKeepsTheHigherSeqAndSendsNothing(t *testing.T) {
	rig := newChatRig(t)
	state := rig.registered("blocked", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")})
	state.Seq = 12000
	rig.seed(state)
	before := rig.tmux.stored
	rig.clock = 10_000 // this event's seq is 11000
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	if rig.tmux.stored != before || len(rig.tmux.setOptionCalls()) != 0 {
		t.Fatalf(
			"stored = %q after %q, set-option calls %q, want the state untouched",
			rig.tmux.stored,
			before,
			rig.tmux.setOptionCalls(),
		)
	}
	rig.wantCalls()
}

// Two reporters of one session started together: whichever the lock lets in
// first, the higher seq is what stays stored. The lower event's tmux write is
// slow, so without the lock it lands last.
func TestHerdrStateRacingReportersEndWithTheHigherSeq(t *testing.T) {
	rig := newChatRig(t)
	rig.tmux.beforeWrite = func(value string) {
		if strings.Contains(value, `"state":"working"`) {
			time.Sleep(150 * time.Millisecond)
		}
	}
	lowClock, highClock := int64(1_000_000), int64(9_000_000)
	jobs := []struct {
		event string
		clock *int64
	}{{"working", &lowClock}, {"idle", &highClock}}
	results := make([]herdrRun, len(jobs))
	start := make(chan struct{})
	var group sync.WaitGroup
	for index, job := range jobs {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results[index] = runHerdrIn(
				t,
				rig.jail,
				rig.env,
				rig.tmux,
				job.clock,
				[]string{"--engine", "claude", job.event},
				`{"session_id":"s1"}`,
			)
		}()
	}
	close(start)
	group.Wait()
	for index, got := range results {
		if got.rc != 0 || got.stderr != "" {
			t.Fatalf("reporter %d: rc=%d stderr=%q, want a quiet exit 0", index, got.rc, got.stderr)
		}
	}
	if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != 9_001_000 {
		t.Fatalf("stored = %q, want the higher-seq idle event (seq 9001000)", rig.tmux.stored)
	}
}

// A lock still held at the hook's 3 s deadline fails the run: stderr names the
// lock file and nothing is written.
func TestHerdrStateGivesUpOnALockHeldPastTheDeadline(t *testing.T) {
	t.Parallel()
	rig := newChatRig(t)
	if err := os.MkdirAll(rig.jail.lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(filepath.Join(rig.jail.lockDir, "cc-1.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold the session lock: %v", err)
	}
	started := time.Now()
	got := rig.engine("working", `{"session_id":"s1"}`)
	if waited := time.Since(started); waited < herdrDeadline-200*time.Millisecond {
		t.Fatalf("gave up after %v, want the full %v deadline", waited, herdrDeadline)
	}
	if got.rc != 0 || !strings.Contains(got.stderr, "cc-1.lock") {
		t.Fatalf("rc=%d stderr=%q, want exit 0 and the held cc-1.lock named", got.rc, got.stderr)
	}
	if rig.tmux.stored != "" || len(rig.tmux.setOptionCalls()) != 0 {
		t.Fatalf("stored = %q, set-option calls %q, want nothing written", rig.tmux.stored, rig.tmux.setOptionCalls())
	}
	rig.wantCalls()
}

// Only the engine running in the chat's pane reports: a `codex exec` or
// `claude -p` the chat's own Bash tool started inherits $TMUX and stays silent.
func TestHerdrStateReportsOnlyForThePanesOwnEngine(t *testing.T) {
	for _, test := range []struct {
		name      string
		procs     map[int]fakeProc
		panePID   string
		reports   bool
		logsAbout string
	}{
		{"the engine is the pane process", map[int]fakeProc{100: {50, "pfm"}, 50: {1, "claude"}}, "50", true, ""},
		{"a hook shell between pfm and the engine", map[int]fakeProc{100: {90, "sh"}, 90: {50, "claude"}, 50: {1, "zsh"}}, "50", true, ""},
		{"an engine started from the pane's shell", map[int]fakeProc{100: {70, "pfm"}, 70: {50, "claude"}, 50: {1, "-zsh"}}, "50", true, ""},
		{"opencode under node, hook through sh", map[int]fakeProc{100: {90, "sh"}, 90: {60, "node"}, 60: {1, "zsh"}}, "60", true, ""},
		{"opencode as the pane process", map[int]fakeProc{100: {50, "pfm"}, 50: {1, "node"}}, "50", true, ""},
		{
			"codex exec from the pane engine's Bash tool",
			map[int]fakeProc{100: {90, "sh"}, 90: {70, "codex"}, 70: {60, "bash"}, 60: {50, "claude"}, 50: {1, "zsh"}},
			"50", false, "",
		},
		{
			"claude -p from the pane engine's Bash tool",
			map[int]fakeProc{100: {70, "pfm"}, 70: {60, "claude"}, 60: {50, "bash"}, 50: {1, "claude"}},
			"50", false, "",
		},
		{
			"codex exec under an engine started from the shell",
			map[int]fakeProc{100: {80, "pfm"}, 80: {70, "codex"}, 70: {60, "bash"}, 60: {55, "claude"}, 55: {50, "sh"}, 50: {1, "login"}},
			"50", false, "",
		},
		{
			"codex under its npm node launcher",
			map[int]fakeProc{100: {90, "sh"}, 90: {80, "codex"}, 80: {70, "node"}, 70: {60, "pfm"}, 60: {1, "zsh"}},
			"60", true, "",
		},
		{
			"an engine under env and timeout wrappers",
			map[int]fakeProc{100: {90, "sh"}, 90: {80, "claude"}, 80: {70, "timeout"}, 70: {60, "env"}, 60: {1, "zsh"}},
			"60", true, "",
		},
		{
			"codex exec under a wrapper from the pane engine's Bash tool",
			map[int]fakeProc{100: {90, "codex"}, 90: {80, "timeout"}, 80: {70, "bash"}, 70: {50, "claude"}, 50: {1, "zsh"}},
			"50", false, "",
		},
		{
			"claude -p the Bash tool's shell exec'd into",
			map[int]fakeProc{100: {90, "sh"}, 90: {80, "claude"}, 80: {50, "codex"}, 50: {1, "zsh"}},
			"50", false, "",
		},
		{
			"a hook that does not run under the chat pane",
			map[int]fakeProc{100: {90, "sh"}, 90: {1, "codex"}},
			"50", false, "not under",
		},
		{"a process table that cannot be read", map[int]fakeProc{100: {50, "pfm"}}, "50", false, "walk up"},
		{"a pane pid that is not a pid", map[int]fakeProc{100: {50, "pfm"}, 50: {1, "claude"}}, "", false, "pane pid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rig := newChatRig(t)
			rig.jail.procs = test.procs
			rig.tmux.panePID = test.panePID
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			before := rig.tmux.stored
			got := rig.engine("idle", `{"session_id":"s1"}`)
			if test.logsAbout != "" {
				if got.rc != 0 || !strings.Contains(got.stderr, test.logsAbout) ||
					!strings.Contains(rig.jail.logged("cc-1"), test.logsAbout) {
					t.Fatalf(
						"rc=%d stderr=%q log=%q, want exit 0 and %q on stderr and in the log",
						got.rc,
						got.stderr,
						rig.jail.logged("cc-1"),
						test.logsAbout,
					)
				}
			} else {
				rig.wantQuiet(got)
			}
			if test.reports {
				rig.wantCalls(listCall, reportCall("w1:p1", "idle", ""))
				return
			}
			rig.wantCalls()
			if rig.tmux.stored != before {
				t.Fatalf("stored = %q, want the state untouched by a foreign engine (was %q)", rig.tmux.stored, before)
			}
		})
	}
}

// One dispatch branch in cmd/pfm serves both stdin-payload hooks.
func TestPayloadHookRoutesEachVerbToItsEntry(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if rc := PayloadHook(
		[]string{"herdr-state", "bogus"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); rc != 1 ||
		!strings.Contains(stderr.String(), "herdr-state") {
		t.Fatalf("herdr-state: rc=%d stderr=%q, want its usage error", rc, stderr.String())
	}
	stderr.Reset()
	payload := `{"tool_input":{"subagent_type":"Explore","model":"sonnet"}}`
	if rc := PayloadHook(
		[]string{"explore-deny"},
		strings.NewReader(payload),
		&stdout,
		&stderr,
	); rc != 0 ||
		!strings.Contains(stdout.String(), "deny") {
		t.Fatalf("explore-deny: rc=%d stdout=%q, want its deny answer", rc, stdout.String())
	}
}

// Claude's PermissionRequest hook (blocked, async) names the tool; the
// Notification that follows it carries Claude's generic text and never replaces
// a stored message that names a tool. The generic text stands only when no tool
// name is known.
func TestHerdrStateKeepsAToolNamedBlockedMessage(t *testing.T) {
	const generic = "Claude needs your permission to use Bash"
	const permission = `{"session_id":"s1","notification_type":"permission_prompt","message":"` + generic + `"}`
	bashRequest := `{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":"Bash"}`
	idleRig := func(t *testing.T) *chatRig {
		t.Helper()
		rig := newChatRig(t)
		rig.seed(rig.registered("idle", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
		return rig
	}
	t.Run("blocked(tool Bash) then the generic permission notification: tool name stays", func(t *testing.T) {
		rig := idleRig(t)
		rig.wantQuiet(rig.engine("blocked", bashRequest))
		rig.wantCalls(listCall, reportCall("w1:p1", "blocked", "waiting for approval: Bash"))
		sent := len(rig.herdr.calls())
		rig.wantQuiet(rig.engine("notification", permission))
		if got := rig.herdr.calls()[sent:]; len(got) != 0 {
			t.Fatalf("requests after the notification = %q, want none: the pane keeps its message", got)
		}
		state := rig.tmux.storedState(t)
		if state.State != "blocked" || state.Message != "waiting for approval: Bash" {
			t.Fatalf("stored = %q, want blocked and the tool-named message", rig.tmux.stored)
		}
	})
	t.Run("the permission notification alone: its own message", func(t *testing.T) {
		rig := idleRig(t)
		rig.wantQuiet(rig.engine("notification", permission))
		rig.wantCalls(listCall, reportCall("w1:p1", "blocked", generic))
	})
	t.Run("idle after the kept message: idle", func(t *testing.T) {
		rig := idleRig(t)
		rig.wantQuiet(rig.engine("blocked", bashRequest))
		rig.wantQuiet(rig.engine("notification", permission))
		rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
		if got := rig.herdr.calls(); got[len(got)-1] != reportCall("w1:p1", "idle", "") {
			t.Fatalf("requests = %q, want the idle report last", got)
		}
		if state := rig.tmux.storedState(t); state.State != "idle" || state.Message != "" {
			t.Fatalf("stored = %q, want idle and no message", rig.tmux.stored)
		}
	})
	t.Run("a blocked with no tool name is replaced by the notification's text", func(t *testing.T) {
		rig := idleRig(t)
		rig.wantQuiet(rig.engine("blocked", `{"session_id":"s1"}`))
		rig.wantQuiet(rig.engine("notification", permission))
		if got := rig.herdr.calls(); got[len(got)-1] != reportCall("w1:p1", "blocked", generic) {
			t.Fatalf("requests = %q, want the notification's report last", got)
		}
	})
	t.Run("a second tool request replaces the first tool's message", func(t *testing.T) {
		rig := idleRig(t)
		rig.wantQuiet(rig.engine("blocked", bashRequest))
		rig.wantQuiet(rig.engine("blocked", `{"session_id":"s1","tool_name":"Edit"}`))
		if got := rig.herdr.calls(); got[len(got)-1] != reportCall("w1:p1", "blocked", "waiting for approval: Edit") {
			t.Fatalf("requests = %q, want the Edit report last", got)
		}
	})
}
