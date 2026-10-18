package hookentry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestHerdrNativeSeatReporter(t *testing.T) {
	for _, event := range []string{"start", "working", "idle", "attach", "detach", "sync", "invalid resume", "delayed working"} {
		t.Run(event, func(t *testing.T) {
			root := t.TempDir()
			server := newFakeHerdr(t)
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
			server := newFakeHerdr(t)
			server.addPane("w6:p3", "native")
			values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
			database := seatStateSQL(t, values, fleetdb.Seat{
				SeatID: "native-seat", SessionID: "bound", Backend: "herdr", State: "live", Engine: "cc",
				HerdrSocket: server.path, HerdrPane: "w6:p3",
			})
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
			server := newFakeHerdr(t)
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

func TestBareHerdrDegradedSeatStoreStillReportsWithVisibleError(t *testing.T) {
	for _, failure := range []string{"corrupt", "newer"} {
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
			if failure == "corrupt" {
				if err := os.WriteFile(values.StateDB, []byte("not SQLite"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
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
			if !reported {
				t.Fatalf("legacy report missing after visible degradation: %v stderr=%s", lines, &stderr)
			}
		})
	}
}
