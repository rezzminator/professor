package hookentry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrNativeSeatReporter(t *testing.T) {
	for _, event := range []string{"start", "working", "idle", "attach", "detach", "sync", "invalid resume", "delayed working"} {
		t.Run(event, func(t *testing.T) {
			root := t.TempDir()
			server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
			server.addPane("w6:p3", "native")
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
			t.Setenv("TMUX", "")
			for k, v := range herdrPaneEnv(server, "w6:p3") {
				t.Setenv(k, v)
			}
			values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			seat := fleetdb.Seat{
				SeatID:      "native-seat",
				SessionID:   "old-session",
				Backend:     "herdr",
				State:       "live",
				Engine:      "cc",
				HerdrSocket: server.path,
				HerdrPane:   "w6:p3",
			}
			stampNativeReporterSeat(t, &seat)
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			reportedSession := "new-session"
			reportEvent := event
			if event == "invalid resume" {
				reportedSession = "bad'session"
				reportEvent = "start"
			}
			payload, _ := json.Marshal(map[string]string{"session_id": reportedSession, "source": "startup"})
			deps := realHerdrDeps()
			if event == "delayed working" {
				reportEvent = "working"
				reads := 0
				deps.now = func() time.Time {
					reads++
					if reads == 1 {
						return time.Unix(0, 100)
					}
					return time.Unix(0, 300)
				}
			}
			rc := reportToHerdr([]string{reportEvent}, strings.NewReader(string(payload)), &stderr, deps)
			if rc != 0 || (stderr.Len() != 0 && event != "invalid resume") {
				t.Fatalf("report rc=%d stderr=%s", rc, &stderr)
			}
			if event == "invalid resume" && !strings.Contains(stderr.String(), "resume") {
				t.Fatalf("invalid resume warning=%s", &stderr)
			}
			got, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || !found {
				t.Fatalf("seat=%+v found=%v err=%v", got, found, err)
			}
			if event == "attach" || event == "detach" || event == "sync" {
				if got.SessionID != "old-session" {
					t.Fatalf("bridge event changed native binding: %+v", got)
				}
				return
			}
			if got.SessionID != reportedSession {
				t.Fatalf("native seat binding = %q, want %s", got.SessionID, reportedSession)
			}
			lines := server.requests()
			if len(lines) != 1 {
				t.Fatalf("native reports=%q", lines)
			}
			var request struct {
				Method string `json:"method"`
				Params struct {
					Seq    uint64   `json:"seq"`
					Pane   string   `json:"pane_id"`
					State  string   `json:"state"`
					Resume []string `json:"resume_argv"`
				} `json:"params"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &request); err != nil {
				t.Fatal(err)
			}
			if event == "delayed working" && request.Params.Seq != 100 {
				t.Fatalf("native report reordered event by send time: seq=%d want ingress=100", request.Params.Seq)
			}
			wantState := "idle"
			if reportEvent == "working" {
				wantState = "working"
			}
			wantResume := []string{herdr.ResumeCommand, "chat", "open", "--restored", "new-session"}
			if event == "invalid resume" {
				wantResume = nil
			}
			if request.Method != "pane.report_agent" || request.Params.Pane != "w6:p3" ||
				request.Params.State != wantState ||
				!reflect.DeepEqual(request.Params.Resume, wantResume) {
				t.Fatalf("native report = %s", lines[0])
			}
		})
	}
}

func TestHerdrTmuxSeatLifecycle(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() { _ = db.Close() }()
	seat := fleetdb.Seat{SeatID: "cc-native-test", SessionID: "old", Engine: "cc", Backend: "tmux", State: "live"}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	reporter := herdrReporter{
		options: herdrOptions{engine: "cc", event: herdr.EventStart},
		deps: herdrDeps{seatPaths: &values, getenv: func(key string) string {
			if key == "TMUX" {
				return filepath.Join(root, seat.SeatID) + ",12,0"
			}
			return ""
		}},
	}
	if err := reporter.tmuxSeatLifecycle(context.Background(), herdrPayload{SessionID: "bound"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.Seat(context.Background(), seat.SeatID)
	if err != nil || !found || got.SessionID != "bound" {
		t.Fatalf("tmux binding=%+v found=%v err=%v", got, found, err)
	}
	reporter.options.event = herdr.EventEnd
	if err := reporter.tmuxSeatLifecycle(context.Background(), herdrPayload{SessionID: "bound"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.Seat(context.Background(), seat.SeatID); err != nil || found {
		t.Fatalf("ended tmux seat found=%v err=%v", found, err)
	}
}

// seatStateSQL stages real shared state, then exposes only the existing SQL fixture facade.
func seatStateSQL(t *testing.T, values paths.Values, seat fleetdb.Seat) *sql.DB {
	t.Helper()
	store := fleetdb.OpenSharedState(context.Background(), values)
	if err := store.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sqlitedb.OpenReadWrite(values.StateDB, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close seat SQL fixture: %v", err)
		}
	})
	return database
}

func TestHerdrTmuxSeatFailurePreservesBridge(t *testing.T) {
	for _, test := range []struct{ failure, event string }{
		{"corrupt", "start"},
		{"corrupt", "end"},
		{"newer", "start"},
		{"newer", "end"},
		{"locked", "start"},
		{"locked", "end"},
		{"update", "start"},
		{"delete", "end"},
	} {
		t.Run(test.failure+"/"+test.event, func(t *testing.T) {
			rig := newChatRig(t)
			rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
			database := seatStateSQL(t, values, fleetdb.Seat{
				SeatID: filepath.Base(rigSocket), SessionID: "old", Engine: "cc", Backend: "tmux", State: "live",
			})
			var statement string
			switch test.failure {
			case "corrupt":
				if err := os.WriteFile(values.StateDB, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "newer":
				statement = fmt.Sprintf("PRAGMA user_version=%d", fleetdb.SchemaVersion+1)
			case "locked":
				statement = "BEGIN IMMEDIATE"
				defer func() {
					if _, err := database.Exec("ROLLBACK"); err != nil {
						t.Errorf("release durable writer: %v", err)
					}
				}()
			case "update", "delete":
				statement = "CREATE TRIGGER refuse_seat BEFORE " + strings.ToUpper(test.failure) +
					" ON seat BEGIN SELECT RAISE(ABORT, 'durable write refused'); END"
			}
			if statement != "" {
				if _, err := database.Exec(statement); err != nil {
					t.Fatalf("stage %s: %v", test.failure, err)
				}
			}
			deps := rig.jail.deps(rig.env, &rig.clock)
			deps.tmux, deps.seatPaths = rig.tmux.run, &values
			var stderr bytes.Buffer
			rc := reportToHerdr([]string{test.event}, strings.NewReader(`{"session_id":"s1"}`), &stderr, deps)
			if rc != 0 || !strings.Contains(stderr.String(), "seat") ||
				!strings.Contains(stderr.String(), "fail-open") {
				t.Fatalf("durable failure must warn: rc=%d stderr=%s", rc, &stderr)
			}
			if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, "seat") {
				t.Errorf("durable failure log=%q stderr=%s", logged, &stderr)
			}
			if test.event == "start" {
				rig.wantCalls(listCall, reportCall("w1:p1", "idle", ""))
			} else {
				rig.wantCalls(append([]string{listCall}, releaseCalls("w1:p1")...)...)
			}
		})
	}
}

func TestHerdrNativeSeatReadOnlyReports(t *testing.T) {
	for _, event := range []string{"working", "idle", "blocked", "notification", "legacy", "newer", "corrupt"} {
		t.Run(event, func(t *testing.T) {
			root := t.TempDir()
			server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
			server.addPane("w6:p3", "native")
			values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
			seat := fleetdb.Seat{
				SeatID: "native-seat", SessionID: "bound", Backend: "herdr", State: "live", Engine: "cc",
				HerdrSocket: server.path, HerdrPane: "w6:p3",
			}
			stampNativeReporterSeat(t, &seat)
			database := seatStateSQL(t, values, seat)
			reportEvent := event
			statement := "BEGIN IMMEDIATE"
			wantWarning := ""
			switch event {
			case "legacy":
				statement, reportEvent, wantWarning = "PRAGMA user_version=5", "working", "schema"
			case "newer":
				statement = fmt.Sprintf("PRAGMA user_version=%d", fleetdb.SchemaVersion+1)
				reportEvent, wantWarning = "start", "schema"
			case "corrupt":
				statement, reportEvent, wantWarning = "", "start", "resolve native seat"
				if err := os.WriteFile(values.StateDB, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if statement != "" {
				if _, err := database.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if wantWarning == "" {
				defer func() {
					if _, err := database.Exec("ROLLBACK"); err != nil {
						t.Errorf("release native writer: %v", err)
					}
				}()
			}
			environment := herdrPaneEnv(server, "w6:p3")
			deps := newHerdrJail(t).deps(environment, new(int64))
			deps.seatPaths = &values
			deps.getpid = os.Getpid
			deps.getenv = func(key string) string {
				if key == paths.EnvProcRoot {
					return os.Getenv(key)
				}
				return environment[key]
			}
			var stderr bytes.Buffer
			rc := reportToHerdr(
				[]string{reportEvent},
				strings.NewReader(
					`{"session_id":"bound","source":"startup","notification_type":"idle_prompt","tool_name":"Bash"}`,
				),
				&stderr,
				deps,
			)
			if rc != 0 {
				t.Fatalf("report rc=%d stderr=%s", rc, &stderr)
			}
			if wantWarning != "" {
				if !strings.Contains(stderr.String(), wantWarning) || !strings.Contains(stderr.String(), "fail-open") {
					t.Errorf("native schema failure must warn: %s", &stderr)
				}
				if event == "legacy" {
					var version int
					if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
						t.Fatal(err)
					}
					if version != 5 {
						t.Errorf("unrelated native hook migrated schema: got %d want5", version)
					}
				}
				return
			}
			wantState := event
			if event == "notification" {
				wantState = "idle"
			}
			lines := server.requests()
			if len(lines) != 1 || !strings.Contains(lines[0], `"state":"`+wantState+`"`) || stderr.Len() != 0 {
				t.Fatalf("read-only native %s report=%q stderr=%s", event, lines, &stderr)
			}
		})
	}
}

func TestNativeLossNoteClearsOnlyOnActualPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, event, payload string
		engine               pfmengine.ID
		clear, pending       bool
	}{
		{name: "resume", event: "start", payload: `{"session_id":"sid","source":"resume"}`},
		{name: "idle", event: "idle", payload: `{"session_id":"sid"}`},
		{name: "tool working", event: "working", payload: `{"session_id":"sid","hook_event_name":"PostToolUse","tool_name":"Bash"}`},
		{name: "prompt", event: "working", payload: `{"session_id":"sid","hook_event_name":"UserPromptSubmit","prompt":"private"}`, clear: true},
		{name: "end reload", event: "end", payload: `{"session_id":"sid","reason":"prompt_input_exit"}`, pending: true},
		{name: "opencode state", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid"}`},
		{name: "opencode replayed user message", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"message.updated","role":"user"}`},
		{name: "opencode prompt", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`, clear: true},
		{name: "opencode interrupted prompt", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message","is_interrupt":true}`},
		{name: "claude rejects opencode marker", event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`},
		{name: "opencode idle marker", engine: pfmengine.OpenCode, event: "idle", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newNativeReporterHerdr(t, "seat-note", "w6:p3", "native-terminal")
			server.addPane("w6:p3", "native")
			root := t.TempDir()
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
			t.Setenv("TMUX", "")
			for k, v := range herdrPaneEnv(server, "w6:p3") {
				t.Setenv(k, v)
			}
			db := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: filepath.Join(root, "pfm.db")})
			defer func() { _ = db.Close() }()
			note := "your last message was not answered — re-send it"
			engine := tc.engine
			if engine == "" {
				engine = pfmengine.Claude
			}
			seat := fleetdb.Seat{
				SeatID:        "seat-note",
				SessionID:     "sid",
				Engine:        string(engine),
				Backend:       "herdr",
				State:         "live",
				HerdrSocket:   server.path,
				HerdrPane:     "w6:p3",
				LostNote:      note,
				ReloadPending: tc.pending,
			}
			stampNativeReporterSeat(t, &seat)
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			if rc := reportToHerdr(
				[]string{"--engine", string(engine), tc.event},
				strings.NewReader(tc.payload),
				&stderr,
				realHerdrDeps(),
			); rc != 0 ||
				stderr.Len() != 0 {
				t.Fatalf("hook rc=%d stderr=%s", rc, &stderr)
			}
			current, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || !found {
				t.Fatalf("read note: %v %v", found, err)
			}
			want := note
			if tc.clear {
				want = ""
			}
			if current.LostNote != want || current.ReloadPending != tc.pending {
				t.Fatalf("note/pending after %s: %+v want note=%q", tc.name, current, want)
			}
			if tc.pending || strings.Contains(tc.payload, `"is_interrupt":true`) {
				if len(server.requests()) != 0 {
					t.Fatalf("pending reload released pane: %v", server.requests())
				}
			} else {
				if len(server.requests()) != 1 ||
					!strings.Contains(server.requests()[0], `"method":"pane.report_agent"`) {
					t.Fatalf("native report=%v", server.requests())
				}
			}
		})
	}
}

func TestBareHerdrDegradedSeatStoreRefusesUnprovedReport(t *testing.T) {
	for _, failure := range []string{"corrupt", "newer", "no owner"} {
		t.Run(failure, func(t *testing.T) {
			server := newFakeHerdr(t)
			server.addPane("w6:p3", "bare")
			root := t.TempDir()
			t.Setenv(paths.EnvHome, root)
			t.Setenv(paths.EnvStateDB, filepath.Join(root, "pfm.db"))
			t.Setenv("TMUX", "")
			for k, v := range herdrPaneEnv(server, "w6:p3") {
				t.Setenv(k, v)
			}
			values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
			database := seatStateSQL(
				t,
				values,
				fleetdb.Seat{SeatID: "other", Backend: "tmux", State: "live", Engine: string(pfmengine.Claude)},
			)
			switch failure {
			case "corrupt":
				if err := os.WriteFile(values.StateDB, []byte("not SQLite"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "newer":
				if _, err := database.Exec(fmt.Sprintf("PRAGMA user_version=%d", fleetdb.SchemaVersion+1)); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			if rc := reportToHerdr(
				[]string{"start"},
				strings.NewReader(`{"session_id":"sid","source":"startup"}`),
				&stderr,
				realHerdrDeps(),
			); rc != 0 || !strings.Contains(stderr.String(), "seat") ||
				!strings.Contains(stderr.String(), "fail-open") {
				t.Fatalf("degraded rc=%d stderr=%s", rc, &stderr)
			}
			lines := server.requests()
			reported := false
			for _, line := range lines {
				if strings.Contains(line, `"method":"pane.report_agent"`) {
					reported = true
				}
			}
			if reported || !strings.Contains(stderr.String(), "ownership unproved") {
				t.Fatalf("unproved native report must be refused visibly: %v stderr=%s", lines, &stderr)
			}
		})
	}
}

// This producer-faithful native fixture records mutations separately from proof reads.
func newNativeReporterHerdr(t *testing.T, seatID, pane, terminal string, beforeSend ...func()) *fakeHerdr {
	return newNativeReporterHerdrCount(t, seatID, pane, terminal, nil, nil, beforeSend...)
}

func newNativeReporterHerdrCount(
	t *testing.T,
	seatID, pane, terminal string,
	onProof func(),
	location func() (string, string),
	beforeSend ...func(),
) *fakeHerdr {
	t.Helper()
	root := testjail.ShortRoot(t)
	proc := filepath.Join(root, "proc")
	if err := os.MkdirAll(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(pid, parent int, argv string) {
		dir := filepath.Join(proc, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := make([]string, 24)
		for i := range fields {
			fields[i] = "0"
		}
		fields[0] = "S"
		fields[1] = strconv.Itoa(parent)
		fields[19] = "100"
		if err := os.WriteFile(
			filepath.Join(dir, "stat"),
			[]byte(fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(900001, 1, "/fixture/pfm\x00internal\x00seat-host\x00"+seatID+"\x00")
	write(os.Getpid(), 900001, "/fixture/pfm\x00internal\x00herdr-state\x00working\x00")
	t.Setenv(paths.EnvProcRoot, proc)
	server := &fakeHerdr{path: filepath.Join(root, "h.sock")}
	listener, err := net.Listen("unix", server.path)
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
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string                     `json:"id"`
					Method string                     `json:"method"`
					Params map[string]json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				var result any
				if (req.Method == "pane.list" || req.Method == "pane.process_info") && onProof != nil {
					onProof()
				}
				if location != nil {
					pane, terminal = location()
				}
				switch req.Method {
				case "pane.list":
					fields := testjail.HerdrPaneInfo(pane)["pane"].(map[string]any)
					fields["terminal_id"] = terminal
					result = map[string]any{"type": "pane_list", "panes": []any{fields}}
				case "pane.process_info":
					result = map[string]any{
						"type":         "pane_process_info",
						"process_info": map[string]any{"pane_id": pane, "shell_pid": 900001},
					}
				default:
					for _, check := range beforeSend {
						check()
					}
					line, _ := json.Marshal(req)
					server.mu.Lock()
					server.lines = append(server.lines, string(line))
					server.mu.Unlock()
					result = map[string]any{"type": "ok"}
					switch req.Method {
					case "pane.rename":
						result = map[string]any{"pane": map[string]any{"pane_id": pane, "label": req.Params["label"]}}
					case "agent.rename":
						result = map[string]any{"agent": map[string]any{"pane_id": pane, "name": req.Params["name"]}}
					case "pane.get":
						result = testjail.HerdrPaneInfo(pane)
					case "tab.get":
						result = map[string]any{
							"type": "tab_info",
							"tab":  map[string]any{"tab_id": "w6:t1", "label": "Previous", "pane_count": 1},
						}
					}
				}
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
					t.Error(err)
				}
			}()
		}
	}()
	return server
}

func stampNativeReporterSeat(t *testing.T, seat *fleetdb.Seat) {
	t.Helper()
	if err := gather.StampHerdrHost(gather.NewProcFS(os.Getenv(paths.EnvProcRoot)), seat, 900001); err != nil {
		t.Fatal(err)
	}
	seat.HerdrTerminalID = "native-terminal"
	seat.HerdrWorkspace = "w6"
}

func TestNativeReporterProvesItsSupervisorAndCanonicalTarget(t *testing.T) {
	for _, mode := range []string{"moved", "already-moved", "recycled", "unrelated", "dead", "unknown", "address-only", "reservation-error"} {
		t.Run(mode, func(t *testing.T) {
			pane, terminal := "w8:p7", "native-terminal"
			if mode == "recycled" {
				pane, terminal = "w6:p3", "other-terminal"
			}
			if mode == "address-only" {
				pane = "w6:p3"
			}
			lockDir := t.TempDir()
			var server *fakeHerdr
			ownerPath := func() string {
				key := sha256.Sum256([]byte(server.path + "\x00" + terminal))
				return filepath.Join(lockDir, fmt.Sprintf("pfm-herdr-pane-%x.owner", key))
			}
			server = newNativeReporterHerdr(t, "native-seat", pane, terminal, func() {
				var owner struct {
					Client string `json:"client"`
					Seq    uint64 `json:"seq"`
				}
				raw, err := os.ReadFile(ownerPath())
				if err != nil || json.Unmarshal(raw, &owner) != nil ||
					!strings.HasPrefix(owner.Client, "native\x00native-seat\x00"+terminal+"\x00") ||
					owner.Seq == 0 {
					t.Errorf("native wire send preceded proved ownership reservation: owner=%+v err=%v", owner, err)
				}
			})
			values := paths.Values{
				StateDB:  filepath.Join(t.TempDir(), "state.db"),
				ProcRoot: os.Getenv(paths.EnvProcRoot),
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			seat := fleetdb.Seat{
				SeatID:      "native-seat",
				SessionID:   "bound",
				Backend:     "herdr",
				Engine:      "cc",
				State:       "live",
				HerdrSocket: server.path,
				HerdrPane:   "w6:p3",
			}
			stampNativeReporterSeat(t, &seat)
			if mode == "already-moved" || mode == "reservation-error" {
				seat.HerdrPane = pane
				seat.HerdrWorkspace = "w8"
			}
			if mode == "address-only" {
				seat.HerdrTerminalID, seat.SeatHostPID, seat.SeatHostStart, seat.SeatHostBirth = "", 0, 0, 0
				if err := os.WriteFile(
					filepath.Join(values.ProcRoot, "900001", "cmdline"),
					[]byte("/fixture/pfm\x00internal\x00seat-host\x00another-seat\x00"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reservation-error" {
				if err := os.Mkdir(ownerPath(), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated" {
				file := filepath.Join(values.ProcRoot, strconv.Itoa(os.Getpid()), "stat")
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					file,
					[]byte(strings.Replace(string(raw), "S 900001 ", "S 1 ", 1)),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "dead" {
				if err := os.RemoveAll(filepath.Join(values.ProcRoot, "900001")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unknown" {
				if err := os.WriteFile(
					filepath.Join(values.ProcRoot, "900001", "stat"),
					[]byte("malformed"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			deps := realHerdrDeps()
			deps.lockDir = lockDir
			deps.seatPaths = &values
			inherited := herdrPaneEnv(server, "w6:p3")
			deps.getenv = func(key string) string {
				if key == paths.EnvProcRoot {
					return values.ProcRoot
				}
				return inherited[key]
			}
			var stderr bytes.Buffer
			rc := reportToHerdr([]string{"working"}, strings.NewReader(`{"session_id":"bound"}`), &stderr, deps)
			current, found, err := db.Seat(context.Background(), seat.SeatID)
			allowed := mode == "moved" || mode == "already-moved"
			if allowed {
				if rc != 0 || stderr.Len() != 0 || err != nil || !found || current.HerdrPane != pane ||
					len(server.requests()) != 1 ||
					!strings.Contains(server.requests()[0], `"pane_id":"`+pane+`"`) {
					t.Fatalf(
						"canonical reporter rc=%d stderr=%s seat=%+v found=%v err=%v reports=%v",
						rc,
						&stderr,
						current,
						found,
						err,
						server.requests(),
					)
				}
				// The C consumer is a required landing dependency. A fresh delayed
				// legacy release must not clear the native generation, even after a move.
				legacy := &herdrReporter{
					deps:    deps,
					options: herdrOptions{socket: "old-tmux"},
					session: "old-session",
					seq:     ^uint64(0),
				}
				unlockSession, lockErr := legacy.lock(context.Background(), legacy.session)
				if lockErr != nil {
					t.Fatal(lockErr)
				}
				bridgeDeps := deps
				bridgeDeps.lockDir = filepath.Join(lockDir, "pfm-herdr-bridge")
				unlockBridge, lockErr := (&herdrReporter{deps: bridgeDeps}).lock(context.Background(), "transaction")
				if lockErr != nil {
					unlockSession()
					t.Fatal(lockErr)
				}
				releaseErr := legacy.releaseAll(
					context.Background(),
					"claude",
					map[string]herdrClient{"42": {Pane: "w6:p3", Terminal: terminal, Socket: server.path}},
				)
				unlockBridge()
				unlockSession()
				if releaseErr != nil || len(server.requests()) != 1 {
					t.Fatalf(
						"delayed legacy release mutated proved native terminal: err=%v reports=%v",
						releaseErr,
						server.requests(),
					)
				}
			} else if stderr.Len() == 0 || len(server.requests()) != 0 || err != nil || !found || !reflect.DeepEqual(current, seat) {
				t.Fatalf(
					"unproved reporter mutated: stderr=%s before=%+v after=%+v reports=%v",
					&stderr,
					seat,
					current,
					server.requests(),
				)
			}
		})
	}
}

func TestNativeReporterUsesTwoRPCProofForEachEvent(t *testing.T) {
	var calls atomic.Int32
	count := func() { calls.Add(1) }
	server := newNativeReporterHerdrCount(t, "native-seat", "w6:p3", "native-terminal", count, nil)
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "state.db")}
	seat := fleetdb.Seat{
		SeatID:      "native-seat",
		SessionID:   "bound",
		Engine:      "cc",
		Backend:     "herdr",
		State:       "live",
		HerdrSocket: server.path,
		HerdrPane:   "w6:p3",
	}
	stampNativeReporterSeat(t, &seat)
	seatStateSQL(t, values, seat)
	env := herdrPaneEnv(server, "w6:p3")
	deps := newHerdrJail(t).deps(env, new(int64))
	deps.seatPaths = &values
	deps.getpid = os.Getpid
	deps.getenv = func(key string) string {
		if key == paths.EnvProcRoot {
			return os.Getenv(key)
		}
		return env[key]
	}
	report := func() {
		var stderr bytes.Buffer
		reportToHerdr([]string{"working"}, strings.NewReader(`{"session_id":"bound"}`), &stderr, deps)
		if stderr.Len() != 0 {
			t.Fatal(&stderr)
		}
	}
	report()
	calls.Store(0)
	report()
	if calls.Load() != 2 {
		t.Fatalf("each event needs pane.list and pane.process_info proof: calls=%d want 2", calls.Load())
	}
	lines := server.requests()
	if len(lines) != 2 {
		t.Fatalf("reports=%v want warm and repeated report", lines)
	}
	for _, line := range lines {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Pane   string   `json:"pane_id"`
				State  string   `json:"state"`
				Resume []string `json:"resume_argv"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "pane.report_agent" || req.Params.Pane != "w6:p3" || req.Params.State != "working" ||
			!reflect.DeepEqual(
				req.Params.Resume,
				[]string{herdr.ResumeCommand, "chat", "open", "--restored", "bound"},
			) {
			t.Fatalf("proved event report=%s", line)
		}
	}
}

func TestNativeReservationIsMonotonic(t *testing.T) {
	deps := newHerdrJail(t).deps(nil, new(int64))
	reporter := &herdrReporter{deps: deps}
	seat := fleetdb.Seat{
		SeatID:          "owner",
		HerdrSocket:     "fixture",
		HerdrPane:       "w1:p1",
		HerdrTerminalID: "terminal",
		SeatHostPID:     20,
		SeatHostStart:   9,
	}
	if err := reporter.reserveNativeOwnership(seat, 100); err != nil {
		t.Fatal(err)
	}
	if err := reporter.reserveNativeOwnership(seat, 99); err == nil {
		t.Fatal("native owner accepted decreasing Seq")
	}
	owner, err := reporter.paneOwner(
		herdrTarget{socket: seat.HerdrSocket, terminal: seat.HerdrTerminalID, pane: seat.HerdrPane},
	)
	if err != nil || owner.Seq != 100 {
		t.Fatalf("reservation=%+v err=%v", owner, err)
	}
}

func TestNativeReporterReprovesMovedOrRecycledCachedAddress(t *testing.T) {
	for _, mode := range []string{"moved", "recycled"} {
		t.Run(mode, func(t *testing.T) {
			var moved atomic.Bool
			server := newNativeReporterHerdrCount(
				t,
				"native-seat",
				"w6:p3",
				"native-terminal",
				nil,
				func() (string, string) {
					if moved.Load() {
						if mode == "moved" {
							return "w9:p2", "native-terminal"
						}
						return "w6:p3", "other-terminal"
					}
					return "w6:p3", "native-terminal"
				},
			)
			values := paths.Values{StateDB: filepath.Join(t.TempDir(), "state.db")}
			seat := fleetdb.Seat{
				SeatID:      "native-seat",
				SessionID:   "bound",
				Engine:      "cc",
				Backend:     "herdr",
				State:       "live",
				HerdrSocket: server.path,
				HerdrPane:   "w6:p3",
			}
			stampNativeReporterSeat(t, &seat)
			seatStateSQL(t, values, seat)
			env := herdrPaneEnv(server, "w6:p3")
			deps := newHerdrJail(t).deps(env, new(int64))
			deps.seatPaths = &values
			deps.getpid = os.Getpid
			deps.getenv = func(key string) string {
				if key == paths.EnvProcRoot {
					return os.Getenv(key)
				}
				return env[key]
			}
			var first bytes.Buffer
			reportToHerdr([]string{"working"}, strings.NewReader(`{"session_id":"bound"}`), &first, deps)
			if first.Len() != 0 {
				t.Fatal(&first)
			}
			moved.Store(true)
			var second bytes.Buffer
			reportToHerdr([]string{"working"}, strings.NewReader(`{"session_id":"bound"}`), &second, deps)
			requests := server.requests()
			if mode == "recycled" {
				if second.Len() == 0 || len(requests) != 1 {
					t.Fatalf("recycled cached address painted: err=%s requests=%v", &second, requests)
				}
				return
			}
			if second.Len() != 0 || len(requests) != 2 || !strings.Contains(requests[1], `"pane_id":"w9:p2"`) {
				t.Fatalf("cached move not canonical: err=%s requests=%v", &second, requests)
			}
		})
	}
}
