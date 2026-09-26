package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
)

// Each broken state of the Codex fleet-prompt channel names ITSELF: absent,
// someone else's value, an unreadable config and a missing config are four
// different lines, and only the installed prompt earns "ok". A check that
// answered the same way for all of them would be a coincidence detector.
func TestCodexDeveloperInstructionsNameEveryBrokenState(t *testing.T) {
	prompt, err := codexgen.FleetPrompt()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		config    string
		absent    bool
		directory bool
		want      string
		wantAlso  string
		failures  int
	}{
		{
			name:     "installed",
			config:   "developer_instructions = '''\n" + prompt + "'''\n",
			want:     "developer_instructions=ok",
			failures: 0,
		},
		{
			name:     "key absent",
			config:   "model = 'personal'\n",
			want:     "developer_instructions=MISSING",
			failures: 1,
		},
		{
			name:     "someone else's value",
			config:   "developer_instructions = 'Keep my rules.'\n",
			want:     "developer_instructions=MISMATCH",
			failures: 1,
		},
		{
			name:     "an older prompt",
			config:   "developer_instructions = '''\n" + strings.TrimSuffix(prompt, "\n") + "'''\n",
			want:     "developer_instructions=MISMATCH",
			failures: 1,
		},
		{
			name:     "unparseable config",
			config:   "developer_instructions = [\n",
			want:     "developer_instructions=CHECK FAILED",
			failures: 1,
		},
		{
			// A config known to be absent was looked at and is not there:
			// that is the MISSING state install repairs, not a failed look.
			name:     "no config at all",
			absent:   true,
			want:     "developer_instructions=MISSING",
			wantAlso: "config=absent — run pfm install --yes",
			failures: 1,
		},
		{
			// A config that exists but cannot be read is a failed look. A
			// directory in its place is the read error (the fence runs as
			// root, so a chmod-000 file would still read).
			name:      "unreadable config",
			directory: true,
			want:      "developer_instructions=CHECK FAILED",
			wantAlso:  "UNKNOWN, not clean",
			failures:  1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.directory {
				if err := os.Mkdir(filepath.Join(home, "config.toml"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else if !testCase.absent {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(testCase.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if failures := PrintCodexDeveloperInstructions(&out, []string{home}); failures != testCase.failures {
				t.Fatalf("failures=%d want=%d: %s", failures, testCase.failures, out.String())
			}
			for _, want := range []string{testCase.want, testCase.wantAlso} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("line does not name %q: %s", want, out.String())
				}
			}
		})
	}
}

// A host with no Codex account has nothing to check — named, never a silent
// pass and never a failure.
func TestCodexDeveloperInstructionsNameAHostWithNoCodexAccount(t *testing.T) {
	var out bytes.Buffer
	if failures := PrintCodexDeveloperInstructions(&out, nil); failures != 0 {
		t.Fatalf("failures=%d: %s", failures, out.String())
	}
	if !strings.Contains(out.String(), "no-accounts") {
		t.Fatalf("line does not name the absent accounts: %s", out.String())
	}
}
