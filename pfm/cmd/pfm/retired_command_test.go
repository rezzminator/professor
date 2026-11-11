package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// stageRetiredClone points the jail's source-repo marker at a clone holding
// only raw as its retired lists.
func stageRetiredClone(t *testing.T, home, raw string) {
	t.Helper()
	clone := t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(retired.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
}

// A command the clone's retired list retires answers with the one line
// naming its successor and the port step, exits 2 and runs no engine-sync —
// a config pfm cannot load included, since the answer reads no config. A
// command the list does not retire is unknown, and a list that cannot be
// read says so instead of passing for an empty one — except before dispatch
// under a config pfm cannot load, where the command may be a registered one.
func TestRetiredCommandsExitTwoNamingTheirSuccessor(t *testing.T) {
	const lists = `{"names":[{"name":"pfm sketch","kind":"command","successor":"sketcher draw","match":"exact"}]}`
	retiredLine := `pfm: "pfm sketch" is retired — run "sketcher draw ..."; ` +
		`port this project's files with "pfm doctor --project-updates"` + "\n"
	for _, testCase := range []struct {
		name, lists  string
		args         []string
		brokenConfig bool
		prefix       string
		code         int
	}{
		{name: "retired", lists: lists, args: []string{"sketch", "build", "."}, prefix: retiredLine},
		{name: "retired bare", lists: lists, args: []string{"sketch"}, prefix: retiredLine},
		{name: "retired under a malformed config", lists: lists, args: []string{"sketch"}, brokenConfig: true, prefix: retiredLine},
		{name: "not retired", lists: lists, args: []string{"doodle"}, prefix: `pfm: unknown command "doodle"` + "\n"},
		{
			name: "unreadable list", lists: `{"names":`, args: []string{"sketch"},
			prefix: `pfm: retired commands unread: retired lists `,
		},
		{
			name: "unreadable list under a malformed config", lists: `{"names":`, args: []string{"sketch"},
			brokenConfig: true, prefix: `pfm: config: `, code: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := jailTest(t)
			stageRetiredClone(t, filepath.Join(root, "home"), testCase.lists)
			record := testjail.FakeEngineSync(t, "", 0)
			args := testCase.args
			if testCase.brokenConfig {
				path := filepath.Join(t.TempDir(), "machine.json")
				if err := os.WriteFile(path, []byte(`{"version":1`), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append([]string{"--config", path}, args...)
			}
			var stdout, stderr bytes.Buffer
			wantCode := testCase.code
			if wantCode == 0 {
				wantCode = 2
			}
			if code := run(args, &stdout, &stderr); code != wantCode {
				t.Fatalf("run(%q) code = %d, want %d; stderr=%q", args, code, wantCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("run(%q) stdout = %q, want empty", args, stdout.String())
			}
			got := stderr.String()
			if testCase.prefix == retiredLine && got != retiredLine || !strings.HasPrefix(got, testCase.prefix) {
				t.Fatalf("run(%q) stderr = %q, want %q", args, got, testCase.prefix)
			}
			if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a retired command ran engine-sync (record %s: %v)", record, err)
			}
		})
	}
}
