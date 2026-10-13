package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestClaudeTitleRecordsFollowTheSharedTitleRule: the index names a chat by
// transcript.TitleOf — the last title record of either kind whose own member
// is a non-empty name; an empty record or a name in the other kind's member
// names nothing (INTENDED: it used to clear the title or borrow the member).
func TestClaudeTitleRecordsFollowTheSharedTitleRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "titled.jsonl")
	content := `{"type":"custom-title","customTitle":"Desk"}` + "\n" +
		`{"type":"agent-name","agentName":"Agent"}` + "\n" +
		`{"type":"custom-title","customTitle":""}` + "\n" +
		`{"type":"custom-title","agentName":"wrong member"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file := diskFile{ID: "titled", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	transcript, _, err := parseClaudeTranscriptFile(file, 0, store.Transcript{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript.CustomTitle != "Agent" {
		t.Fatalf("CustomTitle = %q, want %q", transcript.CustomTitle, "Agent")
	}
}

// TestClaudeTranscriptCWDIsWhereTheSessionRan pins the directory the index
// records for a Claude session: the first record that carries one, whether or
// not it is a prompt worth counting. Claude's /clear starts a successor
// session whose only record is the /clear itself; recording a directory only
// from a real prompt left that session with none, and an open of it ran in
// whatever directory the opener happened to stand in.
func TestClaudeTranscriptCWDIsWhereTheSessionRan(t *testing.T) {
	const clearRecord = `{"type":"user","cwd":"/work/ledger","timestamp":"2026-01-02T03:04:05Z",` +
		`"message":{"content":"<command-name>/clear</command-name>\n` +
		`<command-message>clear</command-message>\n<command-args></command-args>"}}` + "\n"
	for _, test := range []struct {
		name       string
		content    string
		prior      store.Transcript
		wantCWD    string
		wantPrompt int64
	}{
		{
			name:    "a /clear successor whose only record is the /clear",
			content: clearRecord,
			wantCWD: "/work/ledger",
		},
		{
			name: "a real prompt's directory",
			content: `{"type":"user","cwd":"/work/orbit","timestamp":"2026-01-02T03:04:05Z",` +
				`"message":{"content":"plan the orbit"}}` + "\n",
			wantCWD:    "/work/orbit",
			wantPrompt: 1,
		},
		{
			name:    "an incremental parse keeps the directory it already holds",
			content: clearRecord,
			prior:   store.Transcript{CWD: "/work/kept"},
			wantCWD: "/work/kept",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			file := diskFile{ID: "session", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
			transcript, _, err := parseClaudeTranscriptFile(file, 0, test.prior)
			if err != nil {
				t.Fatal(err)
			}
			if transcript.CWD != test.wantCWD || transcript.PromptCount != test.wantPrompt {
				t.Fatalf("CWD = %q, prompts = %d; want %q, %d",
					transcript.CWD, transcript.PromptCount, test.wantCWD, test.wantPrompt)
			}
		})
	}
}

// TestClaudeCWDFillsRowsAPreviousParserIndexed pins the upgrade path of the
// directory rule: a database an older pfm indexed already holds a /clear
// successor with no directory, its file unchanged since. The parser version
// bump is the only thing that makes this pfm read it again; without it the
// row keeps opening wherever its opener stands.
func TestClaudeCWDFillsRowsAPreviousParserIndexed(t *testing.T) {
	project, database, indexer := continuationJail(t)
	ctx := context.Background()
	const session = "bbbbbbbb-0000-4000-8000-000000000001"
	rewriteJSONLines(t, filepath.Join(project, session+".jsonl"), []any{
		map[string]any{
			"type": "user",
			"cwd":  "/work/app",
			"message": map[string]any{
				"role": "user",
				"content": "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n" +
					"<command-args></command-args>",
			},
		},
	})
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	// Rewind to what the previous parser left: the row without a directory,
	// and that parser's version stamp.
	stale := mustTranscript(t, database, session)
	stale.CWD = ""
	if err := database.Batch(ctx, 1, func(tx *store.ImmediateTx, _, _ int) error {
		return tx.UpsertTranscript(ctx, stale)
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetMeta(ctx, claudeParserVersionKey, preFirstRecordCWDParserVersion); err != nil {
		t.Fatal(err)
	}

	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("upgrade Run() = %v", err)
	}
	if filled := mustTranscript(t, database, session); filled.CWD != "/work/app" {
		t.Fatalf("/clear successor's CWD after upgrade = %q, want %q — claudeParserVersion must move past %q "+
			"so rows indexed before the first record's directory was read are reparsed",
			filled.CWD, "/work/app", preFirstRecordCWDParserVersion)
	}
}
