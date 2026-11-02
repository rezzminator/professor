package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// One engine's pass failing leaves the others indexed: pfm index names the
// failure, exits 1 and still prints the counters of the passes that ran.
func TestIndexPrintsCountersAndFailsOnAPartialIndex(t *testing.T) {
	root := jailTest(t)
	transcriptDir := filepath.Join(root, "claude", "project")
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","cwd":"/work/project","message":{"content":"Wired prompt"}}` + "\n"
	if err := os.WriteFile(filepath.Join(transcriptDir, "11111111-1111-4111-8111-111111111111.jsonl"),
		[]byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	openCodeRoot := filepath.Join(root, "opencode")
	if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(openCodeRoot, "opencode.db"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvOpenCodeRoot, openCodeRoot)

	var stdout, stderr bytes.Buffer
	code := run([]string{"index"}, &stdout, &stderr)
	wantStdout := "files=1 skipped=0 delta=0 full=1 deleted=0 touched=2 bytes=75 cx_names=true\n"
	wantStderr := "pfm index: index engine ox: read opencode sessions: query opencode sessions: file is not a database (26)\n"
	if code != 1 || stdout.String() != wantStdout || stderr.String() != wantStderr {
		t.Fatalf(
			"code %d stdout %q stderr %q; want 1, %q, %q",
			code,
			stdout.String(),
			stderr.String(),
			wantStdout,
			wantStderr,
		)
	}
}
