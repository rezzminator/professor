package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptCLIEntry(t *testing.T) {
	jail := jailTest(t)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	record := `{"type":"user","sessionId":"cli-entry","timestamp":"2026-09-29T20:00:00Z",` +
		`"message":{"content":"cli entry prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, args := range [][]string{
		{"transcript", "show", path}, {"transcript", "locate", "no-such-session-anywhere"}, {"transcript", "list", "x"},
	} {
		var out, errs bytes.Buffer
		fmt.Fprintf(&got, "%d %s%s", run(args, &out, &errs), out.String(), errs.String())
	}
	if got, want := rooted(
		rooted(got.String(), filepath.Dir(path)),
		jail,
	), "0 TRANSCRIPT claude · cli-entry · model ?\nFILE {root}/session.jsonl · 116 bytes · 1 records · cwd ? (shown as ./)\nSPAN 2026-09-29 20:00:00Z → 20:00:00Z · 0s wall\nCALLS 0, 0 err, 0 without result: none\nFILTER only prompt,reply,call,note,final · results brief · text 500 · shown 1 of 1 events\nL1 20:00:00 PROMPT cli entry prompt\nUNFINISHED — the last turn has no final reply: the run was cut off or is still running\nSKIPPED 0 records\n1 TRANSCRIPT FAILED — NOT FOUND no-such-session-anywhere — searched {root}/claude for an id or prefix; the name lookup over 0 session files (the last 256 KB of each) found no chat whose last title is 'no-such-session-anywhere' (a seat name resolves to its id with `pfm chat resolve no-such-session-anywhere`, third column)\n2 TRANSCRIPT FAILED — argument verb: invalid choice: 'list' (choose from 'show', 'locate', 'agents')\n"; got != want {
		t.Fatalf("transcript verbs = %q, want %q", got, want)
	}
}
