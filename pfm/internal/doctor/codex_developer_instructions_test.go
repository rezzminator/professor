package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Each broken state of the Codex fleet-prompt channel names ITSELF: absent,
// someone else's value, an unreadable config and a missing config are four
// different lines, and only the installed prompt earns "ok". A check that
// answered the same way for all of them would be a coincidence detector.
func TestCodexDeveloperInstructionsNameEveryBrokenState(t *testing.T) {
	prompt, err := codexprompt.FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		config       string
		absent       bool
		directory    bool
		uncomposable bool
		want         string
		wantAlso     string
		failures     int
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
		{
			// A prompt that cannot be composed leaves the comparison unknown,
			// never the accounts unread: a value there is UNVERIFIED.
			name:         "a value while the prompt cannot be composed",
			uncomposable: true,
			config:       "developer_instructions = 'Keep my rules.'\n",
			want: "doctor: codex developer_instructions=UNVERIFIED file={file} bytes=14 — the Codex prompt could " +
				"not be composed, so whether this value is the fleet prompt is UNKNOWN\n",
			wantAlso: "developer_instructions=CHECK FAILED error=",
			failures: 1,
		},
		{
			name:         "key absent while the prompt cannot be composed",
			uncomposable: true,
			config:       "model = 'personal'\n",
			want: "doctor: codex developer_instructions=MISSING file={file} — every Codex session from this " +
				"account starts with no fleet prompt; run pfm install --yes\n",
			wantAlso: "developer_instructions=CHECK FAILED error=",
			failures: 2,
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
			pfmHome := t.TempDir()
			if testCase.uncomposable {
				// A directory where the source-repo marker belongs: the clone is unreadable.
				if err := os.MkdirAll(paths.SourceRepoPath(pfmHome), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			if failures := PrintCodexDeveloperInstructions(
				&out,
				pfmHome,
				[]string{home},
			); failures != testCase.failures {
				t.Fatalf("failures=%d want=%d: %s", failures, testCase.failures, out.String())
			}
			want := strings.ReplaceAll(testCase.want, "{file}", filepath.Join(home, "config.toml"))
			for _, want := range []string{want, testCase.wantAlso} {
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
	if failures := PrintCodexDeveloperInstructions(&out, t.TempDir(), nil); failures != 0 {
		t.Fatalf("failures=%d: %s", failures, out.String())
	}
	if !strings.Contains(out.String(), "no-accounts") {
		t.Fatalf("line does not name the absent accounts: %s", out.String())
	}
}
