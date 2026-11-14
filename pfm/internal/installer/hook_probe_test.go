package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// stageOpenCodeGuard stages home's OpenCode engine-guard plugin as install
// does, so a probe test of another hook sees only that hook's rows.
func stageOpenCodeGuard(t *testing.T, home string) {
	t.Helper()
	path, binary := openCodeEngineGuardStaging(home, OpenCodeConfigPath(home))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := workbench.StageOpenCodeEngineGuardPlugin(path, binary); err != nil {
		t.Fatal(err)
	}
}

func TestReportHooksChecksLaunchExecutableOnce(t *testing.T) {
	home := t.TempDir()
	stageOpenCodeGuard(t, home)
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

// TestReportHooksOutput pins the whole doctor report ReportHooks prints for
// each hook state a host can hold, with its warning and failure counts.
func TestReportHooksOutput(t *testing.T) {
	const claudeNone = "doctor: hook claude none — no Claude config dir is configured in the machine config\n"
	const untrusted = " UNTRUSTED no Codex trust is recorded for the hook, so Codex may refuse to run it — run pfm install --yes\n"
	const missing = " MISSING — run pfm install --yes\n"
	stageBinaryHome := func(t *testing.T) string {
		home := t.TempDir()
		stageOpenCodeGuard(t, home)
		binary := filepath.Join(home, ".local", "bin", "pfm")
		writeFixture(t, binary, "binary")
		if err := os.Chmod(binary, 0o700); err != nil {
			t.Fatal(err)
		}
		return home
	}
	for _, tc := range []struct {
		name         string
		stage        func(t *testing.T) (string, pfmconfig.Config)
		wantWarnings int
		wantFailures int
		want         string
	}{
		{
			name:         "no claude account and no codex residue",
			stage:        func(t *testing.T) (string, pfmconfig.Config) { return stageBinaryHome(t), pfmconfig.Config{} },
			wantWarnings: 1,
			want:         claudeNone,
		},
		{
			// The receipt carries history; its unknown native trust and the stale
			// clear-hide handler (put in after install would remove it) are both
			// visible, beside the seven Herdr handlers and the engine guard it does
			// not record.
			name: "codex residue",
			stage: func(t *testing.T) (string, pfmconfig.Config) {
				home := stageBinaryHome(t)
				stageRetiredLists(t, home, fixtureRetiredHooks)
				codex := filepath.Join(home, ".codex")
				body := strings.Replace(ownedHooksBody(t, home, ""), `"SessionStart": [`,
					`"SessionStart": [{"matcher":"","hooks":[{"type":"command","command":"pfm internal clear-hide"}]},`, 1)
				writeFixture(t, filepath.Join(codex, "hooks.json"), body)
				writeFixture(t, filepath.Join(codex, ".professor-hook-trust.json"),
					`{"`+resumeUnkillKey(t, codex, body)+`":"sha256:abc"}`)
				return home, pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codex}}}
			},
			wantWarnings: 2,
			wantFailures: 9,
			want: claudeNone +
				"doctor: hook codex[1] hooks.json SessionStart resume-unkill NATIVE-TRUST-UNKNOWN native trust is unknown: Codex binary is not configured\n" +
				"doctor: hook codex[1] hooks.json SessionStart herdr-start-SessionStart" + untrusted +
				"doctor: hook codex[1] hooks.json UserPromptSubmit herdr-working-UserPromptSubmit" + untrusted +
				"doctor: hook codex[1] hooks.json PostToolUse herdr-working-PostToolUse" + untrusted +
				"doctor: hook codex[1] hooks.json PermissionRequest herdr-blocked-PermissionRequest" + untrusted +
				"doctor: hook codex[1] hooks.json Stop herdr-idle-Stop" + untrusted +
				"doctor: hook codex[1] hooks.json Interrupt herdr-idle-Interrupt" + untrusted +
				"doctor: hook codex[1] hooks.json SessionEnd herdr-end-SessionEnd" + untrusted +
				"doctor: hook codex[1] hooks.json PreToolUse engine-guard" + untrusted +
				"doctor: hook codex[1] hooks.json SessionStart clear-hide STALE clear-hide — run pfm install\n",
		},
		{
			name: "every herdr handler missing",
			stage: func(t *testing.T) (string, pfmconfig.Config) {
				home, machine, hooksPath := stageCodexProbeHome(t, "")
				writeFixture(t, hooksPath, resumeUnkillHooksBody(home))
				return home, machine
			},
			wantWarnings: 1,
			wantFailures: 9,
			want: claudeNone +
				"doctor: hook codex[1] hooks.json SessionStart herdr-start-SessionStart" + missing +
				"doctor: hook codex[1] hooks.json UserPromptSubmit herdr-working-UserPromptSubmit" + missing +
				"doctor: hook codex[1] hooks.json PostToolUse herdr-working-PostToolUse" + missing +
				"doctor: hook codex[1] hooks.json PermissionRequest herdr-blocked-PermissionRequest" + missing +
				"doctor: hook codex[1] hooks.json Stop herdr-idle-Stop" + missing +
				"doctor: hook codex[1] hooks.json Interrupt herdr-idle-Interrupt" + missing +
				"doctor: hook codex[1] hooks.json SessionEnd herdr-end-SessionEnd" + missing +
				"doctor: hook codex[1] hooks.json PreToolUse engine-guard" + missing +
				"doctor: hook codex[1] hooks.json SessionStart resume-unkill" + untrusted,
		},
		{
			// A hook the operator disabled is a warning that promises no install repair.
			name: "disabled hook",
			stage: func(t *testing.T) (string, pfmconfig.Config) {
				old := HookProbeOverride
				t.Cleanup(func() { HookProbeOverride = old })
				HookProbeOverride = func(string, pfmconfig.Config) []HookProbeResult {
					return []HookProbeResult{{Hook: ExpectedHook{Target: "codex[1]", File: "hooks.json"}, State: "disabled"}}
				}
				return t.TempDir(), pfmconfig.Config{}
			},
			wantWarnings: 1,
			want:         "doctor: hook codex[1] hooks.json   DISABLED — operator choice preserved; re-enable in Codex only if wanted\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, machine := tc.stage(t)
			var output bytes.Buffer
			warnings, failures := ReportHooks(&output, home, machine, false)
			if warnings != tc.wantWarnings || failures != tc.wantFailures || output.String() != tc.want {
				t.Fatalf("warnings=%d failures=%d output=\n%s\nwant warnings=%d failures=%d output=\n%s",
					warnings, failures, output.String(), tc.wantWarnings, tc.wantFailures, tc.want)
			}
		})
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

// stageCodexProbeHome stages an executable pfm and one Codex account whose
// hooks.json is the given body ("" stages none); it returns the machine config
// and the account's hooks.json path.
func stageCodexProbeHome(t *testing.T, hooksBody string) (home string, machine pfmconfig.Config, hooksPath string) {
	t.Helper()
	home = t.TempDir()
	stageOpenCodeGuard(t, home)
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
	body, _, _, err := updateCodexHooks(fixtureRetiredHooks, []byte(extra), home, false, nil)
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
		"no resume handler": {`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`, 9},
		"wrong matcher": {`{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[` +
			`{"type":"command","command":"@CMD@"}]}]}}`, 9},
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

func TestProbeCodexHooksReportsUntrustedAndUnknownNativeTrust(t *testing.T) {
	t.Parallel()
	home, machine, _ := stageCodexProbeHome(t, "")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	writeFixture(t, hooksPath, ownedHooksBody(t, home, ""))

	// Codex refuses to run an untrusted handler, so every owned one is a named failure.
	var output bytes.Buffer
	warnings, failures := ReportHooks(&output, home, machine, false)
	// The one warning is the missing Claude config of this machine config.
	if warnings != 1 || failures != 9 ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json SessionStart resume-unkill UNTRUSTED") ||
		!strings.Contains(output.String(), "doctor: hook codex[1] hooks.json PreToolUse engine-guard UNTRUSTED") ||
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
	); len(rows) != 9 || rows[0].Hook.Name != "resume-unkill" || rows[0].State != "native-trust-unknown" ||
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
	if warnings != 2 || failures != 8 || unknownLine == "" || strings.Contains(unknownLine, "pfm install --yes") {
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
	rows := probeCodexHooks(home, machine, filepath.Join(home, ".local", "bin", "pfm"), fixtureRetiredHooks)
	want := "inspect hook trust receipt " + receipt + ": stat " + receipt + ": too many levels of symbolic links"
	if len(rows) != 1 || rows[0].State != stateUnreadable || rows[0].Error != want {
		t.Fatalf("rows=%+v, want one unreadable trust receipt row with error %q, not one per handler", rows, want)
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

// withHerdrTimeout is a pfm-owned hooks.json whose Herdr handler of event
// carries timeout instead of the one pfm writes; nil removes the key.
func withHerdrTimeout(t *testing.T, home, event string, timeout any) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(ownedHooksBody(t, home, "")), &document); err != nil {
		t.Fatal(err)
	}
	hooks, _ := document["hooks"].(map[string]any)
	entries, _ := hooks[event].([]any)
	edited := false
	for _, entryValue := range entries {
		entry, _ := entryValue.(map[string]any)
		handlers, _ := entry["hooks"].([]any)
		for _, handlerValue := range handlers {
			handler, _ := handlerValue.(map[string]any)
			if command, _ := handler["command"].(string); !strings.Contains(command, "herdr-state") {
				continue
			}
			if timeout == nil {
				delete(handler, "timeout")
			} else {
				handler["timeout"] = timeout
			}
			edited = true
		}
	}
	if !edited {
		t.Fatalf("no Herdr handler of %s in the owned hooks.json", event)
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// An installed Herdr handler whose timeout is not the one pfm writes is a
// drift row naming the event, the timeout found and the one expected; a
// healthy file has none, and a file that cannot be read stays UNREADABLE.
func TestProbeCodexHooksReportsAHerdrHandlerTimeoutDrift(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		body func(t *testing.T, home string) string
		want []string
	}{
		"healthy": {func(t *testing.T, home string) string { return ownedHooksBody(t, home, "") }, nil},
		"an Interrupt timeout of 5": {
			func(t *testing.T, home string) string { return withHerdrTimeout(t, home, "Interrupt", 5) },
			[]string{"doctor: hook codex[1] hooks.json Interrupt herdr-idle-Interrupt DRIFT what=timeout want=3 got=5"},
		},
		"a Stop timeout of 3": {
			func(t *testing.T, home string) string { return withHerdrTimeout(t, home, "Stop", 3) },
			[]string{"doctor: hook codex[1] hooks.json Stop herdr-idle-Stop DRIFT what=timeout want=5 got=3"},
		},
		"a SessionEnd with no timeout": {
			func(t *testing.T, home string) string { return withHerdrTimeout(t, home, "SessionEnd", nil) },
			[]string{"doctor: hook codex[1] hooks.json SessionEnd herdr-end-SessionEnd DRIFT what=timeout want=3 got=absent"},
		},
		"an unreadable file": {
			func(*testing.T, string) string { return `{"hooks":` },
			[]string{"doctor: hook codex[1] hooks.json UNREADABLE"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, machine, hooksPath := stageCodexProbeHome(t, "")
			writeFixture(t, hooksPath, test.body(t, home))
			var output bytes.Buffer
			ReportHooks(&output, home, machine, false)
			var drifts []string
			for _, line := range strings.Split(output.String(), "\n") {
				if strings.Contains(line, "what=timeout") || strings.Contains(line, "UNREADABLE") {
					drifts = append(drifts, line)
				}
			}
			if len(drifts) != len(test.want) {
				t.Fatalf("timeout and unreadable rows = %q, want %q:\n%s", drifts, test.want, output.String())
			}
			for index, want := range test.want {
				if !strings.HasPrefix(drifts[index], want) {
					t.Fatalf("row %q, want it to open %q", drifts[index], want)
				}
			}
		})
	}
}
