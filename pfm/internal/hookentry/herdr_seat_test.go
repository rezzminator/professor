package hookentry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestHerdrNativeClaudeDefersLifecycle(t *testing.T) {
	for _, event := range []string{"working", "idle", "blocked", "notification", "agent-start", "agent-stop"} {
		t.Run(event, func(t *testing.T) {
			server := newFakeHerdr(t)
			jail := newHerdrJail(t)
			now := int64(time.Second)
			payload := `{"session_id":"s1","notification_type":"permission_prompt"}`
			if event == "agent-start" {
				payload = `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`
			}
			if event == "agent-stop" {
				payload = `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStop"}`
			}
			got := runHerdrIn(t, jail, herdrPaneEnv(server, "w1:p1"), nil, &now, []string{event}, payload)
			if got.rc != 0 {
				t.Fatal(got)
			}
			for _, line := range server.requests() {
				if strings.Contains(line, `"method":"pane.report_agent"`) {
					t.Fatalf("native lifecycle overrides detection: %s", line)
				}
			}
		})
	}
}

func TestHerdrNativeSeatReporter(t *testing.T) {
	events := []string{
		"start", "working", "idle", "attach", "detach", "sync",
		"shell start", "shell idle", "shell invalid resume", "shell delayed working", "unread idle", "empty idle",
	}
	for _, event := range events {
		t.Run(event, func(t *testing.T) {
			root := t.TempDir()
			server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
			reportEvent, shellFront := strings.CutPrefix(event, "shell ")
			reportEvent, unread := strings.CutPrefix(reportEvent, "unread ")
			reportEvent, empty := strings.CutPrefix(reportEvent, "empty ")
			shellFront = shellFront || unread || empty
			// name is the row's hook without its pane's fate.
			name := reportEvent
			switch {
			case empty:
				// Herdr lists no foreground: it cannot say Claude runs there, so pfm reports.
				noFrontNativeReporterPane(server, "w6:p3")
			case shellFront:
				shellFrontNativeReporterPane(t, server, "w6:p3")
				if unread {
					// The foreground read after the seat's proof fails: the state still
					// reaches the pane, under pfm's source.
					server.processInfoAnswers = 1
				}
			default:
				server.addPane("w6:p3", "native")
			}
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
			if name == "invalid resume" {
				reportedSession = "bad'session"
				reportEvent = "start"
			}
			payload, _ := json.Marshal(map[string]string{"session_id": reportedSession, "source": "startup"})
			deps := realHerdrDeps()
			deps.spawn = nil // the transcript watch is herdr_watch_test.go's
			if name == "delayed working" {
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
			wantStderr := ""
			if unread {
				wantStderr = "pfm internal herdr-state idle: native Claude seat native-seat: read pane w6:p3 " +
					"foreground (any report goes under pfm's own source): herdr refused pane.process_info pfm-seat pane w6:p3: " +
					"server_error: refused by fake herdr\n"
			}
			if empty {
				wantStderr = "pfm internal herdr-state idle: native Claude seat native-seat: read pane w6:p3 " +
					"foreground (any report goes under pfm's own source): herdr foreground evidence unavailable in " +
					"w6:p3; engine exit is unproved\n"
			}
			if name == "invalid resume" {
				wantStderr = `pfm internal herdr-state start: no resume command for session "bad'session" ` +
					`(state still reported): resume_argv: word "bad'session--restored" holds an apostrophe, ` +
					"which Herdr refuses\n"
			}
			if rc != 0 || stderr.String() != wantStderr {
				t.Fatalf("report rc=%d stderr=%q, want %q", rc, stderr.String(), wantStderr)
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
			if !shellFront {
				// Herdr detects Claude in the pane and owns it: its screen detection reads
				// the state and its own integration reports the session.
				if calls := server.calls(); len(calls) != 0 {
					t.Fatalf("detected pane: Herdr received\n%s\nwant nothing", strings.Join(calls, "\n"))
				}
				return
			}
			// Herdr lists no Claude in the pane's foreground: pfm reports
			// the state, or the pane reads unknown.
			want := map[string]any{
				"pane_id": "w6:p3", "source": "pfm", "agent": "claude", "state": "idle",
				"agent_session_id": reportedSession,
				"resume_argv":      []string{herdr.ResumeCommand, "chat", "open", "--restored", reportedSession},
			}
			switch name {
			case "invalid resume":
				// No restore command for this id: the state still reaches Herdr without one.
				delete(want, "resume_argv")
			case "delayed working":
				want["state"] = "working"
			}
			lines := server.requests()
			if len(lines) != 1 {
				t.Fatalf("shell pane requests=%q, want one pane.report_agent %v", lines, want)
			}
			var request struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &request); err != nil {
				t.Fatal(err)
			}
			// A delayed hook reports at its ingress, never at the later clock.
			if seq := request.Params["seq"]; name == "delayed working" && seq != float64(100) {
				t.Fatalf("shell pane report seq=%v, want the hook's ingress 100", seq)
			}
			delete(request.Params, "seq")
			wantJSON, _ := json.Marshal(want)
			gotJSON, _ := json.Marshal(request.Params)
			if request.Method != "pane.report_agent" || !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("shell pane report = %s %s, want pane.report_agent %s", request.Method, gotJSON, wantJSON)
			}
		})
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
			t.Parallel()
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
			deps.tmux, deps.listPanes, deps.seatPaths = rig.tmux.run, rig.tmux.listPanes, &values
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

// heldClaudeCall is what Herdr receives from a native Claude in a pane where
// Herdr lists no Claude in front, as fakeHerdr.calls records it (sorted keys, no seq): the
// state under pfm's own source, with the session and its restore command.
func heldClaudeCall(session, state, message string) string {
	if message != "" {
		message = `"message":"` + message + `",`
	}
	return `pane.report_agent {"agent":"claude","agent_session_id":"` + session + `",` + message +
		`"pane_id":"w6:p3","resume_argv":["pfm","chat","open","--restored","` + session + `"],` +
		`"source":"pfm","state":"` + state + `"}`
}

func TestNativeLossNoteClearsOnlyOnActualPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, event, payload string
		engine               pfmengine.ID
		clear, pending       bool
		// state is a Claude row's reported state.
		state string
	}{
		{name: "resume", event: "start", payload: `{"session_id":"sid","source":"resume"}`, state: "idle"},
		{name: "idle", event: "idle", payload: `{"session_id":"sid"}`, state: "idle"},
		{name: "tool working", event: "working", payload: `{"session_id":"sid","hook_event_name":"PostToolUse","tool_name":"Bash"}`, state: "working"},
		{name: "prompt", event: "working", payload: `{"session_id":"sid","hook_event_name":"UserPromptSubmit","prompt":"private"}`, clear: true, state: "working"},
		{name: "task notification turn", event: "working", payload: `{"session_id":"sid","hook_event_name":"UserPromptSubmit","prompt":"<task-notification>\n<task-id>t1</task-id>\n<status>completed</status>\n</task-notification>"}`, state: "working"},
		{name: "end reload", event: "end", payload: `{"session_id":"sid","reason":"prompt_input_exit"}`, pending: true},
		{name: "opencode state", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid"}`},
		{name: "opencode replayed user message", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"message.updated","role":"user"}`},
		{name: "opencode prompt", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`, clear: true},
		{name: "opencode interrupted prompt", engine: pfmengine.OpenCode, event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message","is_interrupt":true}`},
		{name: "claude rejects opencode marker", event: "working", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`, state: "working"},
		{name: "opencode idle marker", engine: pfmengine.OpenCode, event: "idle", payload: `{"session_id":"sid","hook_event_name":"chat.message"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newNativeReporterHerdr(t, "seat-note", "w6:p3", "native-terminal")
			engine := tc.engine
			if engine == "" {
				engine = pfmengine.Claude
				// A shell pane is the one pfm reports a Claude to, so the report proves the hook ran.
				shellFrontNativeReporterPane(t, server, "w6:p3")
			} else {
				server.addPane("w6:p3", "native")
			}
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
			deps := realHerdrDeps()
			deps.spawn = nil // the transcript watch is herdr_watch_test.go's
			if rc := reportToHerdr(
				[]string{"--engine", string(engine), tc.event},
				strings.NewReader(tc.payload),
				&stderr,
				deps,
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
			switch {
			case tc.pending || strings.Contains(tc.payload, `"is_interrupt":true`):
				if len(server.requests()) != 0 {
					t.Fatalf("pending reload released pane: %v", server.requests())
				}
			case engine == pfmengine.Claude:
				want := []string{heldClaudeCall("sid", tc.state, "")}
				if got := server.calls(); !slices.Equal(got, want) {
					t.Fatalf(
						"native Claude: Herdr received\n%s\nwant\n%s",
						strings.Join(got, "\n"),
						strings.Join(want, "\n"),
					)
				}
			default:
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
			deps := realHerdrDeps()
			spawned := 0
			deps.spawn = func([]string) error { spawned++; return nil }
			var stderr bytes.Buffer
			if rc := reportToHerdr(
				[]string{"start"},
				strings.NewReader(`{"session_id":"sid","source":"startup"}`),
				&stderr,
				deps,
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
			// The unproved pane got no report, so its stopped turn holding an agent
			// leaves no watchdog behind to settle it.
			for _, event := range [][2]string{
				{"agent-start", `{"session_id":"sid","agent_id":"a","hook_event_name":"SubagentStart"}`},
				{"idle", `{"session_id":"sid"}`},
			} {
				rc := reportToHerdr([]string{event[0]}, strings.NewReader(event[1]), new(bytes.Buffer), deps)
				if rc != 0 {
					t.Fatalf("%s rc=%d", event[0], rc)
				}
			}
			activity, err := (&herdrReporter{deps: deps}).readActivity("sid")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(activity.Stored.Agents, map[string]bool{"a": true}) ||
				activity.Stored.MainStoppedAt == 0 || spawned != 0 {
				t.Fatalf(
					"stopped turn agents=%v stopped=%d watchdogs=%d, want agent a held, no watchdog",
					activity.Stored.Agents,
					activity.Stored.MainStoppedAt,
					spawned,
				)
			}
		})
	}
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
				Engine:      "cx",
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
					[]byte(strings.Replace(string(raw), "S 900002 ", "S 1 ", 1)),
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
			rc := reportToHerdr(
				[]string{"--engine", "codex", "working"},
				strings.NewReader(`{"session_id":"bound"}`),
				&stderr,
				deps,
			)
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
		Engine:      "cx",
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
		reportToHerdr(
			[]string{"--engine", "codex", "working"},
			strings.NewReader(`{"session_id":"bound"}`),
			&stderr,
			deps,
		)
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
				Engine:      "cx",
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
			reportToHerdr(
				[]string{"--engine", "codex", "working"},
				strings.NewReader(`{"session_id":"bound"}`),
				&first,
				deps,
			)
			if first.Len() != 0 {
				t.Fatal(&first)
			}
			moved.Store(true)
			var second bytes.Buffer
			reportToHerdr(
				[]string{"--engine", "codex", "working"},
				strings.NewReader(`{"session_id":"bound"}`),
				&second,
				deps,
			)
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
