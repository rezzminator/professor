package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeRunner is the package tests' systemd/launchd runner: Run answers the
// manager calls, Output the ActiveState probes.
type fakeRunner struct {
	manager        bool
	nameSyncActive bool
	// nameSyncActive makes the state probe for pfm-name-sync.service answer
	// "activating" (a oneshot mid-run); nameSyncIdle makes it answer
	// "inactive". Leaving both false models a probe that could not run at all
	// (systemctl missing, dead bus, permission denied) via a plain error.
	nameSyncIdle bool
	// reminderActive makes the state probe for pfm-reminder.service answer
	// "activating" (a reminder fire mid-run). The same idle flags make it answer
	// "inactive"; otherwise the probe fails like an unanswered one.
	reminderActive bool
	reminderStates []string
	// mcpState is pfm-mcp.service's ActiveState after its restart ("" is
	// active).
	mcpState string
	failSudo bool
	calls    []string
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if name == "sudo" && runner.failSudo {
		return os.ErrPermission
	}
	if call == "systemctl --user show-environment" && runner.manager {
		return nil
	}
	if strings.Contains(call, "is-active") || strings.Contains(call, "is-enabled") ||
		strings.Contains(call, "is-failed") {
		return errors.New("not loaded")
	}
	if runner.manager || name == "launchctl" {
		return nil
	}
	return errors.New("dead user bus")
}

// nameSyncStateProbe is the exact argv schedulerServiceRunning runs.
const nameSyncStateProbe = "systemctl --user show --property=ActiveState --value pfm-name-sync.service"

// reminderStateProbe is the exact argv schedulerServiceRunning runs for the
// reminder fire.
const reminderStateProbe = "systemctl --user show --property=ActiveState --value pfm-reminder.service"

func (runner *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if len(runner.reminderStates) != 0 {
		if call == reminderStateProbe || (name == "launchctl" && strings.HasSuffix(call, "/"+reminderLaunchdLabel)) {
			state := runner.reminderStates[0]
			if len(runner.reminderStates) > 1 {
				runner.reminderStates = runner.reminderStates[1:]
			}
			if name == "launchctl" {
				if state == "activating" {
					return []byte("state = running\n"), nil
				}
				return []byte("state = not running\n"), nil
			}
			return []byte(state + "\n"), nil
		}
		if name == "launchctl" && strings.HasSuffix(call, "/"+launchdLabel) {
			state := "not running"
			if runner.nameSyncActive {
				state = "running"
			}
			return []byte("state = " + state + "\n"), nil
		}
	}
	if call == nameSyncStateProbe {
		if runner.nameSyncActive {
			return []byte("activating\n"), nil
		}
		if runner.nameSyncIdle {
			return []byte("inactive\n"), nil
		}
	}
	if call == reminderStateProbe {
		if runner.reminderActive {
			return []byte("activating\n"), nil
		}
		if runner.nameSyncIdle || runner.nameSyncActive {
			return []byte("inactive\n"), nil
		}
	}
	if call == "systemctl --user show --property=ActiveState --value "+mcpUnitName {
		if runner.mcpState == "" {
			return []byte("active\n"), nil
		}
		return []byte(runner.mcpState + "\n"), nil
	}
	return nil, errors.New("fakeRunner: no output for " + call)
}

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
	t.Parallel()
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          mode,
				Home:          home,
				Stdout:        &output,
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
	t.Parallel()
	if schedulerIsLaunchd {
		t.Skip("the systemd name-sync gate is Linux-only")
	}
	home := t.TempDir()
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("an unprobed gate refused the install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "name-sync gate NOT probed") {
		t.Fatalf("an unprobed name-sync gate was silent:\n%s", output.String())
	}
}

func TestRunningNameSyncRefusesMutatingModesBeforeWriting(t *testing.T) {
	t.Parallel()
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
				MCPConfigPath: testConfigPath(t),
				Mode:          mode, Home: home, Runner: runner,
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
// schedulerServiceRunning can be unit-tested directly against every answer
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
// schedulerServiceRunning. pfm-name-sync.service is Type=oneshot: mid-run its
// state is "activating", and `is-active` exits 3 for that exactly as it does for
// "inactive" — so the gate reads the state by name. Any state in which the unit
// is doing work refuses; inactive/failed is probed-idle; anything the gate
// cannot read (a coded exit, a plain error, an unknown state, a runner that
// cannot read output) is unprobed, never idle.
func TestNameSyncServiceRunningClassifiesProbeAnswers(t *testing.T) {
	t.Parallel()
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
			running, probed := schedulerServiceRunning(context.Background(), testCase.runner, "pfm-name-sync.service")
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"schedulerServiceRunning() = (%v, %v), want (%v, %v)",
					running, probed, testCase.wantRunning, testCase.wantProbed,
				)
			}
		})
	}
}

// TestNameSyncServiceRunningProductionShape runs schedulerServiceRunning
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
				if err := testjail.WriteExecutable(
					filepath.Join(dir, "systemctl"),
					[]byte(testCase.script),
					0o755,
				); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			running, probed := schedulerServiceRunning(
				context.Background(), execCommandRunner{}, "pfm-name-sync.service",
			)
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"schedulerServiceRunning() = (%v, %v), want (%v, %v)",
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
	t.Parallel()
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
			running, probed := launchAgentRunning(context.Background(), runner, launchdLabel)
			if running != testCase.wantRunning || probed != testCase.wantProbed {
				t.Fatalf(
					"launchAgentRunning() = (%v, %v), want (%v, %v)",
					running, probed, testCase.wantRunning, testCase.wantProbed,
				)
			}
		})
	}
}

func TestSchedulerRefusalNamesOnlyARunningJob(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{
			ErrNameSyncRunning,
			"pfm install: the pfm name-sync service is running; wait for it to finish or run `systemctl --user stop pfm-name-sync.service`, then retry",
		},
		{
			fmt.Errorf("gate: %w", ErrLaunchAgentRunning),
			"pfm install: the pfm name-sync launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync` first",
		},
		{
			ErrReminderRunning,
			"pfm install: the pfm reminder service is running; wait for it to finish or run `systemctl --user stop pfm-reminder.service`, then retry",
		},
		{
			fmt.Errorf("gate: %w", ErrReminderAgentRunning),
			"pfm install: the pfm reminder launch agent is running; wait for it to finish or `launchctl bootout gui/$(id -u)/com.professor.pfm.reminder` first",
		},
		{errors.New("other"), ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := SchedulerRefusal("install", tc.err); got != tc.want {
			t.Fatalf("SchedulerRefusal(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A reminder fire is a oneshot like name-sync: while it runs, an install must
// not rewrite its unit or binary. The refusal names the reminder, not name-sync.
func TestRunningReminderRefusesMutatingModesBeforeWriting(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		t.Skip("the systemd reminder gate is Linux-only")
	}
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			_, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          mode, Home: home,
				Runner: &fakeRunner{nameSyncIdle: true, reminderActive: true},
				Sleep:  func(time.Duration) {},
			})
			if !errors.Is(err, ErrReminderRunning) {
				t.Fatalf("Run() error = %v, want %v", err, ErrReminderRunning)
			}
			if got := SchedulerRefusal("install", err); !strings.Contains(got, "the pfm reminder service is running") {
				t.Fatalf("SchedulerRefusal = %q, want the reminder named", got)
			}
			if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
				t.Fatalf("running-reminder refusal wrote files: entries=%v err=%v", entries, readErr)
			}
		})
	}
}

// labelRunner answers a `launchctl print` per label, so the two agents can be
// told apart.
type labelRunner struct {
	states map[string]string
}

func (r labelRunner) Run(context.Context, string, ...string) error { return nil }

func (r labelRunner) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	target := args[len(args)-1]
	for label, state := range r.states {
		if strings.HasSuffix(target, "/"+label) {
			return []byte(state), nil
		}
	}
	return nil, commandExitError{name: "launchctl", code: 113}
}

// launchAgentRunning probes the label it is given: the reminder agent running
// is not read off the name-sync agent's state, and vice versa.
func TestLaunchAgentRunningProbesTheGivenLabel(t *testing.T) {
	t.Parallel()
	runner := labelRunner{states: map[string]string{
		launchdLabel:         "state = not running\n",
		reminderLaunchdLabel: "state = running\n",
	}}
	if running, probed := launchAgentRunning(context.Background(), runner, launchdLabel); running || !probed {
		t.Fatalf("name-sync label = (%v, %v), want (false, true)", running, probed)
	}
	if running, probed := launchAgentRunning(context.Background(), runner, reminderLaunchdLabel); !running || !probed {
		t.Fatalf("reminder label = (%v, %v), want (true, true)", running, probed)
	}
}

func TestAwaitSchedulerGate(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"finishes", "bound", "name-sync"} {
		t.Run(scenario, func(t *testing.T) {
			runner := &fakeRunner{nameSyncIdle: true, reminderStates: []string{"activating"}}
			wantErr, wantSleeps, wantProbes := ErrReminderRunning, make([]time.Duration, 18), 19
			if schedulerIsLaunchd {
				wantErr = ErrReminderAgentRunning
			}
			for i := range wantSleeps {
				wantSleeps[i] = 5 * time.Second
			}
			wantOutput := "  wait    the pfm reminder is delivering; waiting up to 1m30s for it to finish\n"
			switch scenario {
			case "finishes":
				runner.reminderStates = []string{"activating", "activating", "inactive"}
				wantErr, wantSleeps, wantProbes = nil, []time.Duration{5 * time.Second, 5 * time.Second}, 3
			case "name-sync":
				runner.nameSyncActive = true
				wantErr, wantSleeps, wantProbes, wantOutput = ErrNameSyncRunning, nil, 0, ""
				if schedulerIsLaunchd {
					wantErr = ErrLaunchAgentRunning
				}
			}
			var output bytes.Buffer
			var sleeps []time.Duration
			probed, err := awaitSchedulerGate(context.Background(), Options{
				Runner: runner, Stdout: &output,
				Sleep: func(d time.Duration) { sleeps = append(sleeps, d) },
			})
			if !probed || !errors.Is(err, wantErr) || !reflect.DeepEqual(sleeps, wantSleeps) ||
				output.String() != wantOutput {
				t.Fatalf(
					"gate=(%v,%v) sleeps=%v output=%q, want (true,%v) %v %q",
					probed,
					err,
					sleeps,
					output.String(),
					wantErr,
					wantSleeps,
					wantOutput,
				)
			}
			probes := 0
			for _, call := range runner.calls {
				if call == reminderStateProbe || strings.HasSuffix(call, "/"+reminderLaunchdLabel) {
					probes++
				}
			}
			if probes != wantProbes {
				t.Fatalf("reminder probes=%d, want %d: %v", probes, wantProbes, runner.calls)
			}
		})
	}
}

func TestCheckSchedulerReturnsRunningReminder(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{nameSyncIdle: true, reminderStates: []string{"activating", "inactive"}}
	wantErr := ErrReminderRunning
	if schedulerIsLaunchd {
		wantErr = ErrReminderAgentRunning
	}
	unprobed, err := CheckScheduler(context.Background(), runner)
	if unprobed != "" || !errors.Is(err, wantErr) || len(runner.calls) != 2 {
		t.Fatalf("check=(%q,%v) calls=%v, want running reminder after one probe", unprobed, err, runner.calls)
	}
}
