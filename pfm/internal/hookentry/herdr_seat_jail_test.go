package hookentry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrNativeSeatReadOnlyReports(t *testing.T) {
	for _, event := range []string{"working", "idle", "blocked", "notification", "legacy", "older start", "newer", "corrupt"} {
		t.Run(event, func(t *testing.T) {
			root := t.TempDir()
			testjail.DatabaseFiles(t, root)
			server := newNativeReporterHerdr(t, "native-seat", "w6:p3", "native-terminal")
			// A shell pane is the one pfm reports to, so the report proves the hook ran.
			shellFrontNativeReporterPane(t, server, "w6:p3")
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
			case "older start":
				// A start's own writable open migrates the file, so its seat read must not refuse it.
				statement = fmt.Sprintf("PRAGMA user_version=%d", fleetdb.SchemaVersion-1)
				reportEvent = "start"
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
			if statement == "BEGIN IMMEDIATE" {
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

			if event == "older start" {
				var version int
				if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				if version != fleetdb.SchemaVersion {
					t.Errorf(
						"native start left schema at %d, want %d (%s)",
						version,
						fleetdb.SchemaVersion,
						values.StateDB,
					)
				}
			}
			state, message := "idle", ""
			switch event {
			case "working":
				state = "working"
			case "blocked":
				state, message = "blocked", "waiting for approval: Bash"
			}
			want := []string{heldClaudeCall("bound", state, message)}
			if got := server.calls(); !slices.Equal(got, want) || stderr.Len() != 0 {
				t.Fatalf(
					"read-only native %s: Herdr received\n%s\nwant\n%s\nstderr=%s",
					event,
					strings.Join(got, "\n"),
					strings.Join(want, "\n"),
					&stderr,
				)
			}
		})
	}
}
