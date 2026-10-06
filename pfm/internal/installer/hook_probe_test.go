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
	// The owned hook is wired and trusted, so the stale handler is the only red.
	writeFixture(
		t,
		filepath.Join(codex, "hooks.json"),
		`{"hooks":{"SessionStart":[{"matcher":"","hooks":[{"type":"command","command":"pfm internal clear-hide"}]},`+
			`{"matcher":"resume","hooks":[{"type":"command","command":"`+resumeUnkillCommand(home)+`"}]}]}}`,
	)
	writeFixture(t, filepath.Join(codex, ".professor-hook-trust.json"), `{"k":"sha256:abc"}`)
	machine := pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codex}}}
	var output bytes.Buffer
	_, failures := ReportHooks(&output, home, machine, false)
	if failures != 1 ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json SessionStart clear-hide STALE") {
		t.Fatalf("failures=%d output=%s", failures, output.String())
	}
}

// stageCodexProbeHome stages an executable pfm and one Codex account whose
// hooks.json is the given body ("" stages none); it returns the machine config
// and the account's hooks.json path.
func stageCodexProbeHome(t *testing.T, hooksBody string) (home string, machine pfmconfig.Config, hooksPath string) {
	t.Helper()
	home = t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(home, ".codex")
	hooksPath = filepath.Join(codex, "hooks.json")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	if hooksBody != "" {
		writeFixture(t, hooksPath, hooksBody)
	}
	return home, pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codex}}}, hooksPath
}

func resumeUnkillHooksBody(home string) string {
	return `{"hooks":{"SessionStart":[{"matcher":"resume","hooks":[{"type":"command","command":"` +
		resumeUnkillCommand(home) + `","timeout":30}]}]}}`
}

func codexProbeRows(home string, machine pfmconfig.Config) []HookProbeResult {
	var rows []HookProbeResult
	probed := ProbeExpectedHooks(home, machine)
	for index := range probed {
		if strings.HasPrefix(probed[index].Hook.Target, "codex[") {
			rows = append(rows, probed[index])
		}
	}
	return rows
}

func TestProbeCodexHooksReportsTheResumeUnkillHookMissing(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"no hooks.json":     "",
		"no resume handler": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`,
		"wrong matcher": `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[` +
			`{"type":"command","command":"@CMD@"}]}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, machine, hooksPath := stageCodexProbeHome(t, "")
			if body != "" {
				writeFixture(t, hooksPath, strings.ReplaceAll(body, "@CMD@", resumeUnkillCommand(home)))
			}
			rows := codexProbeRows(home, machine)
			if len(rows) != 1 || rows[0].State != string(HostOverlayMissing) || rows[0].Hook.Name != "resume-unkill" ||
				rows[0].Hook.Target != "codex[1]" || rows[0].Hook.File != hooksPath ||
				rows[0].Hook.Event != "SessionStart" || rows[0].Hook.Matcher != "resume" ||
				rows[0].Hook.Command != resumeUnkillCommand(home) {
				t.Fatalf("rows=%+v, want one MISSING resume-unkill row", rows)
			}
			var output bytes.Buffer
			missing := "doctor: hook codex[1] hooks.json SessionStart resume-unkill MISSING"
			if _, failures := ReportHooks(&output, home, machine, false); failures != 1 ||
				!strings.Contains(output.String(), missing) || !strings.Contains(output.String(), "pfm install --yes") {
				t.Fatalf("failures=%d output=%s", failures, output.String())
			}
		})
	}
}

func TestProbeCodexHooksReportsAnUntrustedResumeUnkillHookAndPassesATrustedOne(t *testing.T) {
	t.Parallel()
	home, machine, _ := stageCodexProbeHome(t, "")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	writeFixture(t, hooksPath, resumeUnkillHooksBody(home))

	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, machine, false)
	if warnings != 2 || failures != 0 ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json SessionStart resume-unkill UNTRUSTED") ||
		!strings.Contains(output.String(), "pfm install --yes") {
		t.Fatalf("warnings=%d failures=%d output=%s", warnings, failures, output.String())
	}

	writeFixture(t, filepath.Join(home, ".codex", ".professor-hook-trust.json"), `{"k":"sha256:abc"}`)
	ledger, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physicalSettingsPath(hooksPath): {
			settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(ledger))
	if rows := codexProbeRows(home, machine); len(rows) != 0 {
		t.Fatalf("a wired, trusted hook produced rows: %+v", rows)
	}
	probed := ProbeExpectedHooks(home, machine)
	for index := range probed {
		if probed[index].Hook.Target == "ownership" {
			t.Fatalf("the ledger row of the pfm-owned Codex hook reads as drift: %+v", probed[index].Hook)
		}
	}
}

func TestProbeCodexHooksReportsUnreadableTrustReceipt(t *testing.T) {
	home, machine, hooksPath := stageCodexProbeHome(t, "")
	writeFixture(t, hooksPath, resumeUnkillHooksBody(home))
	receipt := filepath.Join(filepath.Dir(hooksPath), ".professor-hook-trust.json")
	if err := os.Symlink(receipt, receipt); err != nil {
		t.Fatal(err)
	}
	rows := probeCodexHooks(home, machine, filepath.Join(home, ".local", "bin", "pfm"))
	if len(rows) != 1 || rows[0].State != stateUnreadable || !strings.Contains(rows[0].Error, receipt) {
		t.Fatalf("rows=%+v, want one unreadable trust receipt row", rows)
	}
}
