package digest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// invokeOpenCode runs args over an env whose only root is OpenCode's root.
func invokeOpenCode(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Invoke(context.Background(), args, &stdout, &stderr, Env{OpenCodeRoots: []string{root}, Home: t.TempDir()})
	return code, stdout.String(), stderr.String()
}

// A session named in a store that does not exist is a failure naming the
// missing store, never an empty transcript.
func TestOpenCodeMissingStoreIsAFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "opencode")
	store := filepath.Join(root, "opencode.db")
	want := "TRANSCRIPT FAILED — cannot read " + store + "#ses_1a2b3c4d5e6fGarden0001: no opencode store at " + store
	for _, verb := range []string{"show", "locate"} {
		code, stdout, stderr := invokeOpenCode(t, root, verb, store+"#ses_1a2b3c4d5e6fGarden0001")
		if code != 1 || stdout != "" || strings.TrimSpace(stderr) != want {
			t.Errorf("%s: exit %d stdout %q stderr %q, want 1, nothing, %q", verb, code, stdout, stderr, want)
		}
	}
	seeded := filepath.Join(t.TempDir(), "opencode")
	seedOpenCodeStore(t, seeded, "garden")
	locator := filepath.Join(seeded, "opencode.db") + "#ses_typo"
	code, stdout, stderr := invokeOpenCode(t, seeded, "locate", locator)
	want = "TRANSCRIPT FAILED — cannot read " + locator + ": no such opencode session ses_typo in " +
		filepath.Join(seeded, "opencode.db")
	if code != 1 || stdout != "" || strings.TrimSpace(stderr) != want {
		t.Errorf("locate an unknown session: exit %d stdout %q stderr %q, want 1, nothing, %q", code, stdout, stderr,
			want)
	}
}

// A Claude id resolves cleanly beside an unreadable OpenCode store: no
// OpenCode session id is shaped like it, so the store is no place it could be.
func TestClaudeIDResolvesBesideAnUnreadableOpenCodeStore(t *testing.T) {
	t.Chdir(t.TempDir())
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode.db"), []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(t.TempDir(), "projects")
	chat := filepath.Join(claude, "-work-repo", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(chat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chat, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Invoke(context.Background(), []string{"locate", "11111111-2222"}, &stdout, &stderr,
		Env{ClaudeRoots: []string{claude}, OpenCodeRoots: []string{broken}, Home: t.TempDir()})
	if code != 0 || strings.TrimSpace(stdout.String()) != resolvedPath(chat) || stderr.String() != "" {
		t.Errorf("exit %d stdout %q stderr %q, want 0, %s, nothing", code, stdout.String(), stderr.String(),
			resolvedPath(chat))
	}
}

// A store that cannot be read makes the lookup INCOMPLETE and names the store:
// an error, never NOT FOUND and never an empty transcript.
func TestOpenCodeUnreadableStoreFailsTheLookup(t *testing.T) {
	t.Chdir(t.TempDir())
	root := t.TempDir()
	store := filepath.Join(root, "opencode.db")
	if err := os.WriteFile(store, []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, verb := range []string{"show", "agents", "locate"} {
		code, stdout, stderr := invokeOpenCode(t, root, verb, "ses_1a2b3c4d5e6f")
		fmt.Fprintf(
			&got,
			"%s: exit %d stdout %q stderr %s",
			verb,
			code,
			stdout,
			strings.ReplaceAll(stderr, root, "{root}"),
		)
	}
	if want := "show: exit 1 stdout \"\" stderr TRANSCRIPT FAILED — INCOMPLETE name lookup for 'ses_1a2b3c4d5e6f' — 1 discovery/read errors: {root}/opencode.db (open opencode store read-only: sqlite database {root}/opencode.db is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles)\nagents: exit 1 stdout \"\" stderr TRANSCRIPT FAILED — INCOMPLETE name lookup for 'ses_1a2b3c4d5e6f' — 1 discovery/read errors: {root}/opencode.db (open opencode store read-only: sqlite database {root}/opencode.db is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles)\nlocate: exit 1 stdout \"\" stderr TRANSCRIPT FAILED — INCOMPLETE name lookup for 'ses_1a2b3c4d5e6f' — 1 discovery/read errors: {root}/opencode.db (open opencode store read-only: sqlite database {root}/opencode.db is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles)\n"; got.String() != want {
		t.Errorf("lookups = %q, want %q", got.String(), want)
	}
}

// A target is an OpenCode locator only when its store part names opencode.db.
func TestSplitOpenCodeLocator(t *testing.T) {
	cases := []struct {
		target, store, id string
		ok                bool
	}{
		{"/x/opencode.db#ses_1", "/x/opencode.db", "ses_1", true},
		{"rel/opencode.db#ses_1", "rel/opencode.db", "ses_1", true},
		{"/x/opencode.db#", "", "", false},
		{"/x/other.db#ses_1", "", "", false},
		{"/x/session.jsonl", "", "", false},
		{"ses_1", "", "", false},
	}
	for _, c := range cases {
		store, id, ok := splitOpenCodeLocator(c.target)
		if store != c.store || id != c.id || ok != c.ok {
			t.Errorf("splitOpenCodeLocator(%q) = %q, %q, %v; want %q, %q, %v", c.target, store, id, ok, c.store, c.id,
				c.ok)
		}
	}
}

// A part whose message row cannot be read still shows, its role unknown: its
// text is a reply, not a lost record; so does a part with a member of another
// type than its field's, which reads as absent.
func TestOpenCodePartOfAnUnreadableMessageStillShows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "opencode")
	seedOpenCodeStore(t, root, "garden")
	db, err := sqlitedb.OpenReadWrite(filepath.Join(root, "opencode.db"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the store: %v", err)
		}
	}()
	for _, statement := range []string{
		`INSERT INTO message VALUES ('msg_bad', 'ses_1a2b3c4d5e6fGarden0001', 1790762500000, 1790762500000, 'not json')`,
		`INSERT INTO part VALUES ('prt_bad', 'msg_bad', 'ses_1a2b3c4d5e6fGarden0001', 1790762500000, 1790762500000,
			'{"type":"text","text":"lost parent reply"}')`,
		`INSERT INTO part VALUES ('prt_odd', 'msg_bad', 'ses_1a2b3c4d5e6fGarden0001', 1790762500001, 1790762500001,
			'{"type":"text","text":"odd member reply","synthetic":"yes"}')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed %s: %v", statement, err)
		}
	}
	locator := filepath.Join(root, "opencode.db") + "#ses_1a2b3c4d5e6fGarden0001"
	code, stdout, stderr := invokeOpenCode(t, root, "show", locator)
	if got, want := fmt.Sprintf("exit %d stderr %q\n%s", code, stderr, strings.ReplaceAll(stdout, root, "{root}")),
		"exit 0 stderr \"\"\nTRANSCRIPT opencode · ses_1a2b3c4d5e6fGarden0001 · openai/gpt-5.6-sol high\nSTORE {root}/opencode.db#ses_1a2b3c4d5e6fGarden0001 · 26 records: 6 messages, 20 parts (L = record number in store order; no file lines or bytes) · cwd /work/garden (shown as ./)\nNAME Tidy the garden beds\nSPAN 2026-09-30 10:00:00Z → 10:01:40Z · 100s wall\nCALLS 5, 1 err, 1 without result: bash 1 · read 1 (1 err) · task 1 · edit 1 · webfetch 1\nFILTER only prompt,reply,call,note,final · results brief · text 500 · shown 15 of 15 events\nL2 10:00:00 PROMPT Tidy the beds and list what changed\nL3 10:00:00 PROMPT [file notes.txt]\nL4 10:00:00 NOTE Called the Read tool with the following input: {\"filePath\":\"./notes.txt\"}\nL8 10:00:06 bash ls ./beds → ok 11B ‹tulip›\nL9 10:00:07 read ./missing.txt → ERR 40B\n    | File not found: ./missing.txt\nL10 10:00:08 task explore: Survey the beds → ok 18B · 12s\nL14 10:00:21 SAY Tidying now.\nL15 10:00:22 edit ./beds/rose (-2 +1 lines) → ok 13B\nL16 10:00:23 FINAL\n  Both beds are tidy; rose was edited.\nL19 10:01:00 PROMPT thanks, one more: compact please\nL20 10:01:00 NOTE COMPACTED — the context was compacted here\nL21 10:01:05 NOTE ENGINE ERROR — APIError: rate limited\nL22 10:01:06 webfetch https://example.invalid/guide → NO RESULT\nL25 10:01:40 SAY lost parent reply\nL26 10:01:40 SAY odd member reply\nUNFINISHED — the last turn has no final reply: the run was cut off or is still running\nSKIPPED 7 records: part.step-start 2 · part.step-finish 2 · part.reasoning 1 · part.mystery 1 · message (unparseable) 1 | HEADER 4: message.user 2 · message.assistant 2\n"; got != want {
		t.Errorf("show = %q, want %q", got, want)
	}
}
