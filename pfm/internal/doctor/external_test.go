package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexprompt"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// installedCodexConfig is a Codex config carrying what `pfm install` writes
// into it — the fleet prompt — plus whatever the fixture is really about. A
// fixture that left it out would also trip doctor's developer_instructions
// row, which is a different failure than the one under test.
func installedCodexConfig(t *testing.T, rest string) string {
	t.Helper()
	prompt, err := codexprompt.FleetPrompt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return "developer_instructions = '''\n" + prompt + "'''\n" + rest
}

func writeDoctorCodexAuth(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"tokens":{"access_token":"fixture-access-token","account_id":"fixture-account-id"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// stageExternalDoctorAccount keeps fleet scratch files outside the account scanned by host checks.
func stageExternalDoctorAccount(t *testing.T, home string) string {
	t.Helper()
	account := pfmconfig.DefaultAccountDir(home, 42)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	testjail.StageGlobalAgents(t, home)
	dirs, files := storeLayout()
	testjail.StageAccountLinks(t, home, account, dirs, files)
	stageStorePlugins(t, home)
	return account
}

func TestDoctorEnumeratesExternalDependencies(t *testing.T) {
	jailTest(t)
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	account := stageExternalDoctorAccount(t, runtime.Paths.Home)
	runtime.Config.Accounts = []pfmconfig.Account{{ID: 1, ConfigDir: account}}
	var stdout, stderr bytes.Buffer
	if code := runDoctor(nil, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"doctor code=%d, want clean injected probes\nstdout=%s\nstderr=%s",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	for _, wanted := range []string{"doctor: dep tmux "} {
		if !strings.Contains(stdout.String(), wanted) {
			t.Fatalf("doctor output missing %q:\n%s", wanted, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "doctor: hook codex[") {
		t.Fatalf("doctor invented a Codex hook for an empty roster:\n%s", stdout.String())
	}
}

func TestDependencyDoctorRowsKeepMissingBrokenAndSkippedDistinct(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entries := []deps.Entry{
		{Name: "tmux", Required: true, MinVersion: "1.8"},
		{Name: "ps", Required: true, Platforms: []string{"darwin"}},
		{Name: "codex", Required: true, InstallHint: "install configured Codex"},
		{Name: "claude", Required: true},
	}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{
			{Entry: entries[0], State: deps.StateOK, Path: "/fixture/tmux", Version: "3.4"},
			{Entry: entries[1], State: deps.StateSkipped, Error: "not this platform"},
			{Entry: entries[2], State: deps.StateMissing},
			{
				Entry: entries[3],
				State: deps.StateBroken,
				Path:  "/fixture/claude",
				Error: "exit status 1",
				Raw:   "damaged install\nmore",
			},
		}
	}
	var output bytes.Buffer
	if _, failures, _ := PrintDependencies(
		context.Background(),
		&output,
		"",
		entries,
		deps.ProbeOptions{},
	); failures != 2 {
		t.Fatalf("failures=%d, want 2\n%s", failures, output.String())
	}
	want := strings.Join([]string{
		"doctor: dep tmux path=/fixture/tmux version=3.4 min=1.8 ok",
		"doctor: dep ps platform=darwin skipped (not this platform)",
		"doctor: dep codex path=(none) MISSING required — install: install configured Codex",
		`doctor: dep claude path=/fixture/claude broken error=exit status 1 raw="damaged install"`,
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("dependency rows:\n%s\nwant:\n%s", output.String(), want)
	}
}

// TestDependencyDoctorClaudeAbsenceIsNamedNotWarned pins the ruling: pfm's own
// launcher exiting 127 (its "no real Claude binary" contract, assets/bin/claude)
// is absence — MISSING optional, no warning — while the SAME exit code from a
// binary that is not pfm's launcher, and pfm's launcher exiting anything else,
// both stay broken and counted. The identity check is installer.ClaudeAbsent,
// never a string match on stderr.
func TestDependencyDoctorClaudeAbsenceIsNamedNotWarned(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	home := t.TempDir()
	launcher := filepath.Join(home, ".local", "bin", pfmengine.MustLookup(pfmengine.Claude).Binary)
	// Required:true here (unlike the real registry's optional claude entry) is
	// what makes the "still counted" half of this test meaningful: it proves
	// absence overrides the warning even for a dependency that would
	// otherwise count one, and that a merely-broken claude still gets it.
	entry := deps.Entry{Name: "claude", Engine: pfmengine.Claude, Required: true}
	nonPfm := filepath.Join(home, "opt", "claude")

	cases := []struct {
		name                     string
		path                     string
		exitCode                 int
		wantMissed               bool
		probeHomeErr, verboseErr string
		wantWarnings             int
	}{
		{name: "pfms launcher absent", path: launcher, exitCode: 127, wantMissed: true},
		{name: "pfms launcher broken", path: launcher, exitCode: 1},
		{name: "non-pfm claude at 127", path: nonPfm, exitCode: 127},
		{
			name: "absent with probe home residue", path: launcher, exitCode: 127, wantMissed: true,
			probeHomeErr: "remove /srv/sid/pfm-probe-home-1: device busy", wantWarnings: 1,
		},
		{
			name: "absent with verbose failure", path: launcher, exitCode: 127, wantMissed: true,
			verboseErr: "write /srv/sid/pfm-doctor/claude.log: device busy", wantWarnings: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
				return []deps.Result{{
					Entry: entry, State: deps.StateBroken, Path: testCase.path,
					ExitCode: testCase.exitCode, Error: fmt.Sprintf("exit status %d", testCase.exitCode),
					ProbeHomeErr: testCase.probeHomeErr, VerboseErr: testCase.verboseErr,
				}}
			}
			var output bytes.Buffer
			warnings, failures, claudeAbsent := PrintDependencies(
				context.Background(),
				&output,
				home,
				[]deps.Entry{entry},
				deps.ProbeOptions{},
			)
			if claudeAbsent != testCase.wantMissed || warnings != testCase.wantWarnings {
				t.Fatalf("claudeAbsent=%v warnings=%d, want %v/%d", claudeAbsent, warnings,
					testCase.wantMissed, testCase.wantWarnings)
			}
			if testCase.wantMissed {
				if failures != 0 {
					t.Fatalf("failures=%d, want 0\n%s", failures, output.String())
				}
				want := "doctor: dep claude path=" + testCase.path + " MISSING optional — install: install Claude Code (the pfm launcher has no real binary to run)\n"
				if testCase.verboseErr != "" {
					want += "doctor: dep claude verbose broken error=" + testCase.verboseErr + "\n"
				}
				if testCase.probeHomeErr != "" {
					want += "doctor: dep claude probe-home residue error=" + testCase.probeHomeErr + "\n"
				}
				if output.String() != want {
					t.Fatalf("output=%q, want %q", output.String(), want)
				}
			} else {
				if failures != 1 {
					t.Fatalf("failures=%d, want 1 (still broken, still counted)\n%s", failures, output.String())
				}
				if !strings.Contains(output.String(), "broken") {
					t.Fatalf("output never called it broken:\n%s", output.String())
				}
			}
		})
	}
}

func TestDependencyDoctorTimeoutRowNamesTimeoutNotBroken(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entries := []deps.Entry{
		{Name: "tmux", Required: true},
	}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{
			{Entry: entries[0], State: deps.StateTimeout, Path: "/fixture/tmux", Error: "timeout (5s)"},
		}
	}
	var output bytes.Buffer
	_, failures, _ := PrintDependencies(context.Background(), &output, "", entries, deps.ProbeOptions{})
	if failures != 1 {
		t.Fatalf(
			"failures=%d, want 1 — a required timed-out dep still contributes its failure\n%s",
			failures,
			output.String(),
		)
	}
	if !strings.Contains(output.String(), "timeout") {
		t.Fatalf("dependency row missing %q:\n%s", "timeout", output.String())
	}
	if strings.Contains(output.String(), "broken") {
		t.Fatalf("dependency row must not call an unanswered probe broken:\n%s", output.String())
	}
}

func TestDependencyDoctorCancellationRowNamesCallerStopNotBroken(t *testing.T) {
	saved := DependencyProbeOverride
	t.Cleanup(func() { DependencyProbeOverride = saved })
	entry := deps.Entry{Name: "tmux", Required: true}
	DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return []deps.Result{{
			Entry: entry, State: deps.StateCancelled, Path: "/fixture/tmux",
			Error: "cancelled by parent context",
		}}
	}
	var output bytes.Buffer
	_, failures, _ := PrintDependencies(context.Background(), &output, "", []deps.Entry{entry}, deps.ProbeOptions{})
	if failures != 1 {
		t.Fatalf("failures=%d, want 1 for a required unanswered probe\n%s", failures, output.String())
	}
	if want := "doctor: dep tmux path=/fixture/tmux cancelled error=cancelled by parent context — probe stopped by its caller; unverified, no fault established"; !strings.Contains(
		output.String(),
		want,
	) {
		t.Fatalf("output=%q, want caller cancellation row %q", output.String(), want)
	}
	if strings.Contains(output.String(), "broken") {
		t.Fatalf("caller cancellation must not be diagnosed as broken:\n%s", output.String())
	}
}
