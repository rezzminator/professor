package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// `pfm codex` and `pfm opencode` are retired, and an adopter's agents still
// run them: each prints the one line naming the successor and the port step,
// exits 2 and runs no engine-sync — a config pfm cannot load included, since
// the answer reads no config.
func TestRetiredEngineCommandsExitTwoNamingTheirSuccessor(t *testing.T) {
	for _, testCase := range []struct {
		args         []string
		line         string
		brokenConfig bool
	}{
		{
			[]string{"codex", "build", "."},
			`pfm: "pfm codex" is retired — run "engine-sync codex ..."; ` +
				`port this project's files with "pfm doctor --project-updates"` + "\n",
			false,
		},
		{
			[]string{"codex"},
			`pfm: "pfm codex" is retired — run "engine-sync codex ..."; ` +
				`port this project's files with "pfm doctor --project-updates"` + "\n",
			false,
		},
		{
			[]string{"opencode", "check", "."},
			`pfm: "pfm opencode" is retired — run "engine-sync opencode ..."; ` +
				`port this project's files with "pfm doctor --project-updates"` + "\n",
			false,
		},
		{
			[]string{"codex", "check", "."},
			`pfm: "pfm codex" is retired — run "engine-sync codex ..."; ` +
				`port this project's files with "pfm doctor --project-updates"` + "\n",
			true,
		},
	} {
		name := strings.Join(testCase.args, " ")
		if testCase.brokenConfig {
			name += " under a malformed config"
		}
		t.Run(name, func(t *testing.T) {
			jailTest(t)
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
			if code := run(args, &stdout, &stderr); code != 2 {
				t.Fatalf("run(%q) code = %d, want 2; stderr=%q", args, code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("run(%q) stdout = %q, want empty", args, stdout.String())
			}
			if stderr.String() != testCase.line {
				t.Fatalf("run(%q) stderr = %q, want %q", args, stderr.String(), testCase.line)
			}
			if _, err := os.Stat(record); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a retired command ran engine-sync (record %s: %v)", record, err)
			}
		})
	}
}
