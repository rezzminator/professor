package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestReportHooksChecksLaunchExecutableOnce(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	machine := pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: filepath.Join(home, ".claude")}}}
	for _, testCase := range []struct {
		name  string
		setup func(t *testing.T)
		got   string
	}{
		{"absent", func(*testing.T) {}, "absent"},
		{"not executable", func(t *testing.T) { writeFixture(t, binary, "binary") }, "not-executable"},
		{"not regular", func(t *testing.T) {
			if err := os.MkdirAll(binary, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "not-regular-file"},
		{"stat failure", func(t *testing.T) {
			if err := os.Symlink(binary, binary); err != nil {
				t.Fatal(err)
			}
		}, "stat-failed("},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.RemoveAll(binary); err != nil {
				t.Fatal(err)
			}
			testCase.setup(t)
			var output bytes.Buffer
			_, failures := ReportHooks(&output, home, machine, false)
			if failures != 1 || strings.Count(output.String(), "what=executable") != 1 ||
				!strings.Contains(output.String(), "want="+binary+" got="+testCase.got) ||
				strings.Contains(output.String(), " MISSING") {
				t.Fatalf("failures=%d output=%s", failures, output.String())
			}
		})
	}
}

func TestReportHooksNoClaudeAccountAndCodexResidue(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, pfmconfig.Config{}, false)
	if warnings != 1 || failures != 0 || !strings.Contains(output.String(), "doctor: hook claude none —") {
		t.Fatalf("warnings=%d failures=%d output=%s", warnings, failures, output.String())
	}
}

func TestReportHooksStillChecksCodexResidue(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(home, ".codex")
	writeFixture(
		t,
		filepath.Join(codex, "hooks.json"),
		`{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"pfm internal clear-hide"}]}]}}`,
	)
	machine := pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codex}}}
	var output bytes.Buffer
	_, failures := ReportHooks(&output, home, machine, false)
	if failures != 1 ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json SessionStart clear-hide STALE") {
		t.Fatalf("failures=%d output=%s", failures, output.String())
	}
}
