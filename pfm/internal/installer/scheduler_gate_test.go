package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scheduler gate: a mutating install refuses while the name-sync job is
// mid-execution (systemd on Linux, launchd on macOS), and says so when it could
// not ask.

// TestReachableIdleUserManagerAllowsMutatingModes is behavior (b): the
// systemctl is-active probe ran to completion and genuinely reported the
// service inactive (a commandExitError with a positive code, via fakeRunner's
// nameSyncIdle).
// That is the proceed-silently case: install must not refuse, and — unlike
// the probe-could-not-run case — must never claim the gate was unprobed.
func TestReachableIdleUserManagerAllowsMutatingModes(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				Mode:   mode,
				Home:   home,
				Stdout: &output,
				Runner: &outputRunner{
					fakeRunner:  fakeRunner{manager: true, nameSyncIdle: true},
					printOutput: "state = not running\n",
				},
			})
			if err != nil {
				t.Fatalf("mode %d refused an idle reachable manager: %v\n%s", mode, err, output.String())
			}
			if strings.Contains(output.String(), "gate NOT probed") {
				t.Fatalf(
					"mode %d claimed the gate was unprobed for a genuinely idle service:\n%s",
					mode,
					output.String(),
				)
			}
		})
	}
}

// TestUnprobedNameSyncGateProceedsButSaysSo is behavior (c): the systemctl
// is-active probe never got an answer at all — modeled by fakeRunner's
// default is-active response, a plain error with no positive coded exit
// (as would come from systemctl missing, a dead user bus, or permission
// denied). Install must still proceed (this gate only ever refuses a
// CONFIRMED running service), but it must say the gate was not probed rather
// than silently reading that ambiguity as safe — the entire point of the fix.
func TestUnprobedNameSyncGateProceedsButSaysSo(t *testing.T) {
	if schedulerIsLaunchd {
		t.Skip("the systemd name-sync gate is Linux-only")
	}
	home := t.TempDir()
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("an unprobed gate refused the install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "name-sync gate NOT probed") {
		t.Fatalf("an unprobed name-sync gate was silent:\n%s", output.String())
	}
}

func TestRunningNameSyncRefusesMutatingModesBeforeWriting(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			var runner CommandRunner = &fakeRunner{nameSyncActive: true}
			expected := ErrNameSyncRunning
			if schedulerIsLaunchd {
				runner = &outputRunner{printOutput: "state = running\n"}
				expected = ErrLaunchAgentRunning
			}
			_, err := Run(context.Background(), Options{
				Mode: mode, Home: home, Runner: runner,
			})
			if !errors.Is(err, expected) {
				t.Fatalf("Run() error = %v, want %v", err, expected)
			}
			if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
				t.Fatalf("running-service refusal wrote files: entries=%v err=%v", entries, readErr)
			}
		})
	}
}

// stateRunner answers every Output call with a fixed state line or error, so
// nameSyncServiceRunning can be unit-tested directly against every answer
// `systemctl show -p ActiveState --value` gives and every way it can fail.
type stateRunner struct {
	state string
	err   error
}

func (r stateRunner) Run(context.Context, string, ...string) error { return r.err }

func (r stateRunner) Output(context.Context, string, ...string) ([]byte, error) {
	return []byte(r.state), r.err
}

// runOnlyRunner cannot read a command's output, so it cannot be probed.
type runOnlyRunner struct{}

func (runOnlyRunner) Run(context.Context, string, ...string) error { return nil }

// TestNameSyncServiceRunningClassifiesProbeAnswers is a direct pin on
// nameSyncServiceRunning. pfm-name-sync.service is Type=oneshot: mid-run its
// state is "activating", and `is-active` exits 3 for that exactly as it does for
// "inactive" — so the gate reads the state by name. Any state in which the unit
// is doing work refuses; inactive/failed is probed-idle; anything the gate
// cannot read (a coded exit, a plain error, an unknown state, a runner that
// cannot read output) is unprobed, never idle.
func TestNameSyncServiceRunningClassifiesProbeAnswers(t *testing.T) {
	cases := []struct {
		name                    string
		runner                  CommandRunner
		wantRunning, wantProbed bool
	}{
		{"active", stateRunner{state: "active\n"}, true, true},
		{"oneshot mid-run is activating", stateRunner{state: "activating\n"}, true, true},
		{"deactivating", stateRunner{state: "deactivating\n"}, true, true},
		{"reloading", stateRunner{state: "reloading\n"}, true, true},
		{"inactive (also an unknown unit)", stateRunner{state: "inactive\n"}, false, true},
		{"failed", stateRunner{state: "failed\n"}, false, true},
		{"unknown state text", stateRunner{state: "maintenance\n"}, false, false},
		{"empty answer", stateRunner{state: ""}, false, false},
		{"bus unreachable coded exit", stateRunner{err: commandExitError{name: "systemctl", code: 1}}, false, false},
		{"plain error", stateRunner{err: errors.New("dead user bus")}, false, false},
		{"runner cannot read output", runOnlyRunner{}, false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			running, probed := nameSyncServiceRunning(context.Background(), testCase.runner)
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"nameSyncServiceRunning() = (%v, %v), want (%v, %v)",
					running, probed, testCase.wantRunning, testCase.wantProbed,
				)
			}
		})
	}
}

// TestNameSyncServiceRunningProductionShape runs nameSyncServiceRunning
// through the real execCommandRunner against a systemctl script on PATH (none
// when script is empty). The scripts answer `show` with a state and exit 3 for
// `is-active`, as real systemd does for a oneshot mid-run: a gate that trusts
// the is-active exit reads a running job as idle.
func TestNameSyncServiceRunningProductionShape(t *testing.T) {
	answer := func(state string) string {
		return "#!/bin/sh\ncase \"$*\" in *show*) echo " + state + "; exit 0;; esac\nexit 3\n"
	}
	for _, testCase := range []struct {
		name, script            string
		wantRunning, wantProbed bool
	}{
		{"oneshot mid-run reports activating", answer("activating"), true, true},
		{"systemctl reports inactive", answer("inactive"), false, true},
		{"systemctl could not reach the user bus", "#!/bin/sh\necho 'Failed to connect to bus' >&2\nexit 1\n", false, false},
		{"systemctl cannot be found", "", false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			if testCase.script != "" {
				if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(testCase.script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			running, probed := nameSyncServiceRunning(context.Background(), execCommandRunner{})
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"nameSyncServiceRunning() = (%v, %v), want (%v, %v)",
					running, probed, testCase.wantRunning, testCase.wantProbed,
				)
			}
		})
	}
}

// TestLaunchAgentRunningClassifiesProbeAnswers is a direct pin on
// launchAgentRunning: an unknown-label error with a positive coded exit still
// counts as probed=true (nothing installed, so nothing can be mid-execution),
// while a plain Output error means the probe never got an answer.
func TestLaunchAgentRunningClassifiesProbeAnswers(t *testing.T) {
	cases := []struct {
		name                    string
		output                  string
		err                     error
		wantRunning, wantProbed bool
	}{
		{"running", "state = running\n", nil, true, true},
		{"not running", "state = not running\n", nil, false, true},
		{"unknown label coded exit", "", commandExitError{name: "launchctl", code: 5}, false, true},
		{"plain error", "", errors.New("boom"), false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &outputRunner{printOutput: testCase.output, printErr: testCase.err}
			running, probed := launchAgentRunning(context.Background(), runner)
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"launchAgentRunning() = (%v, %v), want (%v, %v)",
					running, probed, testCase.wantRunning, testCase.wantProbed,
				)
			}
		})
	}
}
