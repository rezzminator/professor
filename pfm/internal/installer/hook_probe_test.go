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
	stageGptHookProbeAssets(t, filepath.Join(home, ".claude"))
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
	stageGptHookProbeAssets(t, filepath.Join(home, ".claude"))
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

// resumeUnkillKey is the native hook key of the resume-unkill handler in a
// hooks.json body.
func resumeUnkillKey(t *testing.T, account, body string) string {
	t.Helper()
	for _, line := range strings.Split(nativeListing(t, account, []byte(body)), "\n") {
		if strings.Contains(line, " internal resume-unkill") {
			start := strings.Index(line, `"key":"`) + len(`"key":"`)
			return line[start : start+strings.Index(line[start:], `"`)]
		}
	}
	t.Fatalf("no resume-unkill handler in %s", body)
	return ""
}

func TestReportHooksStillChecksCodexResidue(t *testing.T) {
	home := t.TempDir()
	stageGptHookProbeAssets(t, filepath.Join(home, ".claude"))
	binary := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, binary, "binary")
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	codex := filepath.Join(home, ".codex")
	// The receipt carries history; both its unknown native trust and the stale handler are visible.
	// The install would remove the retired clear-hide handler, so it is put in after.
	body := strings.Replace(ownedHooksBody(t, home, ""), `"SessionStart": [`,
		`"SessionStart": [{"matcher":"","hooks":[{"type":"command","command":"pfm internal clear-hide"}]},`, 1)
	writeFixture(t, filepath.Join(codex, "hooks.json"), body)
	writeFixture(
		t,
		filepath.Join(codex, ".professor-hook-trust.json"),
		`{"`+resumeUnkillKey(t, codex, body)+`":"sha256:abc"}`,
	)
	machine := pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codex}}}
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, machine, false)
	// Eight failures: the stale clear-hide handler and the seven Herdr handlers the receipt does not record.
	if warnings != 2 || failures != 8 ||
		!strings.Contains(output.String(), "native trust is unknown") ||
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
	stageGptHookProbeAssets(t, filepath.Join(home, ".claude"))
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

// ownedHooksBody is a hooks.json carrying every handler pfm owns (resume-unkill
// and the Herdr reporter's), after the personal entries of extra, if any.
func ownedHooksBody(t *testing.T, home, extra string) string {
	t.Helper()
	if extra == "" {
		extra = `{"hooks":{}}`
	}
	body, _, _, err := updateCodexHooks([]byte(extra), home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
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
	for name, test := range map[string]struct {
		body string
		// rows is how many owned handlers the probe reports MISSING: one row for
		// an absent file, one per owned handler a present file lacks.
		rows int
	}{
		"no hooks.json":     {"", 1},
		"no resume handler": {`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`, 8},
		"wrong matcher": {`{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[` +
			`{"type":"command","command":"@CMD@"}]}]}}`, 8},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, machine, hooksPath := stageCodexProbeHome(t, "")
			if test.body != "" {
				writeFixture(t, hooksPath, strings.ReplaceAll(test.body, "@CMD@", resumeUnkillCommand(home)))
			}
			rows := codexProbeRows(home, machine)
			if len(rows) != test.rows ||
				rows[0].State != string(HostOverlayMissing) ||
				rows[0].Hook.Name != "resume-unkill" ||
				rows[0].Hook.Target != "codex[1]" ||
				rows[0].Hook.File != hooksPath ||
				rows[0].Hook.Event != "SessionStart" ||
				rows[0].Hook.Matcher != "resume" ||
				rows[0].Hook.Command != resumeUnkillCommand(home) {
				t.Fatalf("rows=%+v, want %d MISSING rows led by resume-unkill", rows, test.rows)
			}
			for _, row := range rows {
				if row.State != string(HostOverlayMissing) {
					t.Fatalf("row %+v is not MISSING", row)
				}
			}
			var output bytes.Buffer
			missing := "doctor: hook codex[1] hooks.json SessionStart resume-unkill MISSING"
			if _, failures := ReportHooks(&output, home, machine, false); failures != test.rows ||
				!strings.Contains(output.String(), missing) || !strings.Contains(output.String(), "pfm install --yes") {
				t.Fatalf("failures=%d output=%s", failures, output.String())
			}
		})
	}
}

func TestProbeCodexHooksReportsEveryHerdrHandlerMissing(t *testing.T) {
	t.Parallel()
	home, machine, hooksPath := stageCodexProbeHome(t, "")
	writeFixture(t, hooksPath, resumeUnkillHooksBody(home))
	var output bytes.Buffer
	_, failures := ReportHooks(&output, home, machine, false)
	for _, name := range []string{
		"SessionStart herdr-start-SessionStart", "UserPromptSubmit herdr-working-UserPromptSubmit",
		"PostToolUse herdr-working-PostToolUse", "PermissionRequest herdr-blocked-PermissionRequest",
		"Stop herdr-idle-Stop", "Interrupt herdr-idle-Interrupt",
	} {
		if !strings.Contains(output.String(), "doctor: hook codex[1] hooks.json "+name+" MISSING") {
			t.Fatalf("no MISSING row for %s:\n%s", name, output.String())
		}
	}
	if failures < 6 {
		t.Fatalf("failures=%d, want a failure per missing Herdr handler:\n%s", failures, output.String())
	}
}

func TestProbeCodexHooksReportsUntrustedAndUnknownNativeTrust(t *testing.T) {
	t.Parallel()
	home, machine, _ := stageCodexProbeHome(t, "")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	writeFixture(t, hooksPath, ownedHooksBody(t, home, ""))

	// Codex refuses to run an untrusted handler, so every owned one is a named failure.
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, machine, false)
	// The one warning is the missing Claude config of this machine config.
	if warnings != 1 || failures != 8 ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json SessionStart resume-unkill UNTRUSTED") ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json Stop herdr-idle-Stop UNTRUSTED") ||
		!strings.Contains(output.String(), "pfm install --yes") {
		t.Fatalf("warnings=%d failures=%d output=%s", warnings, failures, output.String())
	}

	writeFixture(
		t,
		filepath.Join(home, ".codex", ".professor-hook-trust.json"),
		`{"`+filepath.Join(home, ".codex")+`/hooks.json:session_start:0:0":"sha256:abc"}`,
	)
	ledger, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physicalSettingsPath(hooksPath): {
			settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(ledger))
	if rows := codexProbeRows(
		home,
		machine,
	); len(rows) != 8 || rows[0].Hook.Name != "resume-unkill" || rows[0].State != "native-trust-unknown" ||
		!strings.Contains(rows[0].Error, "native trust is unknown") {
		t.Fatalf("historical trust must stay visibly unknown: %+v", rows)
	}
	output.Reset()
	warnings, failures = ReportHooks(&output, home, machine, false)
	unknownLine := ""
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.Contains(line, "NATIVE-TRUST-UNKNOWN") {
			unknownLine = line
		}
	}
	if warnings != 2 || failures != 7 || unknownLine == "" || strings.Contains(unknownLine, "pfm install --yes") {
		t.Fatalf(
			"unknown native trust must be an explicit warning, without promising readback: warnings=%d failures=%d output=%s",
			warnings,
			failures,
			output.String(),
		)
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
	writeFixture(t, hooksPath, ownedHooksBody(t, home, ""))
	receipt := filepath.Join(filepath.Dir(hooksPath), ".professor-hook-trust.json")
	if err := os.Symlink(receipt, receipt); err != nil {
		t.Fatal(err)
	}
	rows := probeCodexHooks(home, machine, filepath.Join(home, ".local", "bin", "pfm"))
	if len(rows) != 1 || rows[0].State != stateUnreadable || !strings.Contains(rows[0].Error, receipt) {
		t.Fatalf("rows=%+v, want one unreadable trust receipt row, not one per handler", rows)
	}
}

func TestHookTrustReceiptMustNameExpectedHandler(t *testing.T) {
	for _, suffix := range []string{"1:0", "0:1", "wrong:0"} {
		t.Run(suffix, func(t *testing.T) {
			home, machine, hooksPath := stageCodexProbeHome(t, "")
			writeFixture(t, hooksPath, resumeUnkillHooksBody(home))
			writeFixture(
				t,
				filepath.Join(filepath.Dir(hooksPath), ".professor-hook-trust.json"),
				`{"`+hooksPath+`:session_start:`+suffix+`":"opaque-native-hash"}`,
			)
			rows := ProbeExpectedHooks(home, machine)
			found := false
			for _, row := range rows {
				if row.State == stateUnreadable && strings.Contains(row.Error, "fingerprint") {
					found = true
				}
			}
			if !found {
				t.Fatalf("wrong-handler receipt accepted: %+v", rows)
			}
		})
	}
}

func TestDoctorDisabledHookDoesNotPromiseInstallRepair(t *testing.T) {
	old := HookProbeOverride
	t.Cleanup(func() { HookProbeOverride = old })
	HookProbeOverride = func(string, pfmconfig.Config) []HookProbeResult {
		return []HookProbeResult{{Hook: ExpectedHook{Target: "codex[1]", File: "hooks.json"}, State: "disabled"}}
	}
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, t.TempDir(), pfmconfig.Config{}, false)
	if warnings != 1 || failures != 0 || !strings.Contains(output.String(), "DISABLED") ||
		strings.Contains(output.String(), "pfm install") {
		t.Fatalf("disabled advice warnings=%d failures=%d: %s", warnings, failures, output.String())
	}
}
