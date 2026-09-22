package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/testjail"
)

// The record shapes below are copied from a real Claude Code 2.1.275 chat
// that was sent to the background: the host keeps its launch id, Claude
// appends a continued-in record to that transcript and carries the whole
// conversation on in a new session, which the user then renamed. Ids, names
// and paths are invented.
const (
	parkedPredecessorID = "11111111-2222-4333-8444-555555555555"
	parkedSuccessorID   = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	parkedOldName       = "INTEGRATION"
	parkedNewName       = "SUITE:DESIGNER"
	parkedSocket        = "cc-1700000000-4242-77"
	parkedCWD           = "/work/app"
)

func parkedPrompt(session, uuid, stamp, text string) string {
	const record = `{"type":"user","message":{"role":"user","content":%q},"uuid":%q,` +
		`"timestamp":%q,"cwd":%q,"sessionId":%q,"entrypoint":"cli","promptSource":"typed"}`
	return fmt.Sprintf(record, text, uuid, stamp, parkedCWD, session)
}

// writeParkedChat lays down both transcripts exactly as Claude leaves them
// and returns the predecessor's path — the one the host pane's crumb names.
func writeParkedChat(t *testing.T, claudeRoot string) string {
	t.Helper()
	project := filepath.Join(claudeRoot, "-work-app")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const (
		firstUUID   = "a0000000-0000-4000-8000-000000000001"
		firstStamp  = "2026-09-18T01:45:00.000Z"
		firstText   = "design the integration lanes"
		secondUUID  = "a0000000-0000-4000-8000-000000000002"
		secondStamp = "2026-09-18T02:22:26.117Z"
		secondText  = "see how others handled these cases"
	)
	titled := func(session, name string) string {
		return fmt.Sprintf(`{"type":"custom-title","customTitle":%q,"sessionId":%q}`, name, session)
	}
	predecessor := strings.Join([]string{
		fmt.Sprintf(`{"type":"mode","mode":"normal","sessionId":%q}`, parkedPredecessorID),
		parkedPrompt(parkedPredecessorID, firstUUID, firstStamp, firstText),
		titled(parkedPredecessorID, parkedOldName),
		parkedPrompt(parkedPredecessorID, secondUUID, secondStamp, secondText),
		fmt.Sprintf(
			`{"type":"continued-in","timestamp":"2026-09-18T08:50:25.188Z","sessionId":%q,"continuedInSessionId":%q}`,
			parkedPredecessorID, parkedSuccessorID,
		),
	}, "\n") + "\n"
	successor := strings.Join([]string{
		fmt.Sprintf(
			`{"type":"history-suppression","sessionId":%q,"cause":"fork_inherit","ts":"2026-09-18T08:50:27.891Z"}`,
			parkedSuccessorID,
		),
		titled(parkedSuccessorID, parkedNewName),
		fmt.Sprintf(`{"type":"agent-name","agentName":%q,"sessionId":%q}`, parkedNewName, parkedSuccessorID),
		parkedPrompt(parkedSuccessorID, firstUUID, firstStamp, firstText),
		parkedPrompt(parkedSuccessorID, secondUUID, secondStamp, secondText),
		fmt.Sprintf(
			`{"type":"system","subtype":"local_command","content":"<command-name>/rename</command-name>",`+
				`"timestamp":"2026-09-18T13:20:53.834Z","sessionId":%q}`,
			parkedSuccessorID,
		),
	}, "\n") + "\n"
	predecessorPath := filepath.Join(project, parkedPredecessorID+".jsonl")
	if err := os.WriteFile(predecessorPath, []byte(predecessor), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, parkedSuccessorID+".jsonl"), []byte(successor), 0o600); err != nil {
		t.Fatal(err)
	}
	return predecessorPath
}

func indexParkedChat(t *testing.T) (*store.Store, string) {
	t.Helper()
	testjail.Fleet(t)
	claudeRoot := os.Getenv(paths.EnvClaudeRoots)
	predecessorPath := writeParkedChat(t, claudeRoot)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var counters index.Counters
	if err := index.SyncClaude(context.Background(), database, []string{claudeRoot}, &counters); err != nil {
		t.Fatalf("index.SyncClaude() = %v", err)
	}
	return database, predecessorPath
}

// composeParked runs the picker's own data path — the capped default load
// or the full load, enriched with the live snapshot — and composes it.
func composeParked(t *testing.T, database *store.Store, view compose.View, live gather.Snapshot) compose.Output {
	t.Helper()
	ctx := context.Background()
	var data Data
	var err error
	if view == compose.DefaultView {
		data, err = LoadDefaultData(ctx, database)
	} else {
		data, err = LoadData(ctx, database)
	}
	if err != nil {
		t.Fatalf("load data: %v", err)
	}
	data, err = EnrichLive(ctx, database, data, live)
	if err != nil {
		t.Fatalf("EnrichLive() = %v", err)
	}
	return ComposeFleet(
		Env{Config: pfmconfig.Defaults(t.TempDir(), nil)},
		view,
		data,
		live,
	)
}

// chatRows are the rows of the parked conversation, found by either id or
// either name — every way it could surface twice.
func chatRows(output compose.Output) []compose.Row {
	var rows []compose.Row
	for position := range output.Rows {
		row := &output.Rows[position]
		if row.ID == parkedPredecessorID || row.ID == parkedSuccessorID ||
			row.Name == parkedOldName || row.Name == parkedNewName {
			rows = append(rows, *row)
		}
	}
	return rows
}

func describeRows(rows []compose.Row) string {
	parts := make([]string, 0, len(rows))
	for position := range rows {
		row := &rows[position]
		parts = append(parts, fmt.Sprintf("{kind=%v id=%s name=%q socket=%s}", row.Kind, row.ID, row.Name, row.Socket))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// TestParkedClaudeChatIsOneChatUnderItsCurrentSession pins the picker bug a
// backgrounded Claude chat produced: the host pane's crumb still named the
// launch session, so the pane was labeled with the OLD name while showing
// the successor, and the successor — owned by nothing pfm could see — was
// offered for resume, which started a second process on the same session.
// Whatever hosts it, the conversation must be exactly one row, under its
// current session id and its current name.
func TestParkedClaudeChatIsOneChatUnderItsCurrentSession(t *testing.T) {
	database, predecessorPath := indexParkedChat(t)

	parkedPane := gather.Snapshot{
		Panes: []gather.ProbePane{{
			Socket: parkedSocket, SessionName: parkedSocket, PaneID: "%0", PID: 4242, CurrentPath: parkedCWD,
		}},
		Crumbs: []gather.Crumb{
			{Filename: parkedSocket, Socket: parkedSocket, TranscriptPath: predecessorPath},
			{Filename: parkedSocket + ".%0", Socket: parkedSocket, PaneID: "%0", TranscriptPath: predecessorPath},
		},
	}
	daemonJob := gather.Snapshot{
		Agents: []gather.Agent{{PID: 5151, SessionID: parkedSuccessorID, ConfigDir: "/jail/.cc/3"}},
	}

	cases := []struct {
		name     string
		live     gather.Snapshot
		wantKind compose.Kind
		wantSock string
	}{
		{"host pane still open", parkedPane, compose.LiveClaude, parkedSocket},
		{"daemon job, no pane", daemonJob, compose.Agent, ""},
		{"nothing running", gather.Snapshot{}, compose.ResumeClaude, ""},
	}
	for _, test := range cases {
		for _, view := range []compose.View{compose.DefaultView, compose.AllView} {
			t.Run(fmt.Sprintf("%s/view=%d", test.name, view), func(t *testing.T) {
				rows := chatRows(composeParked(t, database, view, test.live))
				if len(rows) != 1 {
					t.Fatalf("parked chat composed as %d rows %s, want exactly one", len(rows), describeRows(rows))
				}
				row := rows[0]
				if row.ID != parkedSuccessorID || row.Name != parkedNewName ||
					row.Kind != test.wantKind || row.Socket != test.wantSock {
					t.Fatalf(
						"parked chat row = %s, want {kind=%v id=%s name=%q socket=%s}",
						describeRows(rows), test.wantKind, parkedSuccessorID, parkedNewName, test.wantSock,
					)
				}
			})
		}
	}
}
