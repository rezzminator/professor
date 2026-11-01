package servicemanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// reply is one scripted runner answer.
type reply struct {
	result deps.RunResult
	err    error
}

// codedError is a runner error that reports the command ran and exited code.
type codedError struct{ code int }

func (err codedError) Error() string { return "exit status " + string(rune('0'+err.code)) }

func (err codedError) ExitCode() int { return err.code }

// scriptedRunner answers each launchctl verb from its own queue, repeating the
// queue's last reply once drained, and records every argv it ran.
type scriptedRunner struct {
	replies map[string][]reply
	calls   []string
}

func (runner *scriptedRunner) Run(_ context.Context, argv []string, _ deps.RunOptions) (deps.RunResult, error) {
	runner.calls = append(runner.calls, strings.Join(argv, " "))
	queue := runner.replies[argv[1]]
	if len(queue) == 0 {
		return deps.RunResult{}, errors.New("unscripted " + strings.Join(argv, " "))
	}
	next := queue[0]
	if len(queue) > 1 {
		runner.replies[argv[1]] = queue[1:]
	}
	return next.result, next.err
}

func exited(code int, stderr string) reply {
	return reply{result: deps.RunResult{ExitCode: code, Stderr: []byte(stderr)}}
}

var (
	loadedReply  = reply{result: deps.RunResult{Stdout: []byte("\tstate = not running\n")}}
	unknownReply = exited(NotFoundExit, "Could not find service \"x\" in domain for user gui: 501")
	unrunnable   = reply{err: errors.New("exec: \"launchctl\": executable file not found in $PATH")}
)

func counting(pauses *int) func(context.Context, time.Duration) error {
	return func(_ context.Context, d time.Duration) error {
		if d != BootstrapRetryInterval {
			return errors.New("paused " + d.String())
		}
		*pauses++
		return nil
	}
}

func TestTargets(t *testing.T) {
	if got := ServiceTarget(501, "com.example.job"); got != "gui/501/com.example.job" {
		t.Fatalf("ServiceTarget = %q, want gui/501/com.example.job", got)
	}
	if got := ShellTarget("com.example.job"); got != "gui/$(id -u)/com.example.job" {
		t.Fatalf("ShellTarget = %q, want gui/$(id -u)/com.example.job", got)
	}
}

func TestAnswerReadings(t *testing.T) {
	for _, tc := range []struct {
		name             string
		reply            reply
		ran, ok, unknown bool
		state, err       string
	}{
		{name: "loaded", reply: loadedReply, ran: true, ok: true, state: "not running"},
		{
			name: "not loaded", reply: unknownReply, ran: true, unknown: true,
			err: "launchctl exited 113: Could not find service \"x\" in domain for user gui: 501",
		},
		{
			name: "not loaded by stderr alone", reply: exited(3, "Could not find service \"x\""), ran: true,
			unknown: true, err: "launchctl exited 3: Could not find service \"x\"",
		},
		{name: "domain missing", reply: exited(112, "no domain"), ran: true, err: "launchctl exited 112: no domain"},
		{name: "coded runner error", reply: reply{err: codedError{code: 5}}, ran: true, err: "exit status 5"},
		{name: "killed by a signal", reply: exited(-1, ""), err: "launchctl exited -1: "},
		{
			name: "could not run", reply: unrunnable,
			err: "exec: \"launchctl\": executable file not found in $PATH",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &scriptedRunner{replies: map[string][]reply{"print": {tc.reply}}}
			answer := Launchd{Runner: runner, UID: 501}.Print(context.Background(), "x")
			if got := strings.Join(runner.calls, "|"); got != "launchctl print gui/501/x" {
				t.Fatalf("ran %q, want launchctl print gui/501/x", got)
			}
			gotErr := ""
			if err := answer.Err(); err != nil {
				gotErr = err.Error()
			}
			if answer.Ran() != tc.ran || answer.OK() != tc.ok || answer.Unknown() != tc.unknown ||
				answer.State() != tc.state || gotErr != tc.err {
				t.Fatalf("ran=%v ok=%v unknown=%v state=%q err=%q, want %v %v %v %q %q",
					answer.Ran(), answer.OK(), answer.Unknown(), answer.State(), gotErr,
					tc.ran, tc.ok, tc.unknown, tc.state, tc.err)
			}
		})
	}
}

func TestBootout(t *testing.T) {
	stillLoaded := make([]reply, BootstrapAttempts)
	for index := range stillLoaded {
		stillLoaded[index] = loadedReply
	}
	for _, tc := range []struct {
		name    string
		bootout reply
		print   []reply
		calls   int
		pauses  int
		want    string
	}{
		{name: "unloaded", bootout: reply{}, calls: 1},
		{name: "not loaded", bootout: exited(5, "Boot-out failed"), print: []reply{unknownReply}, calls: 2},
		{
			name: "unloaded after a teardown in flight", bootout: exited(5, "Boot-out failed"),
			print: []reply{loadedReply, loadedReply, unknownReply}, calls: 4, pauses: 2,
		},
		{
			name: "still loaded", bootout: exited(5, "Boot-out failed"), print: stillLoaded,
			calls: 1 + BootstrapAttempts, pauses: BootstrapAttempts - 1,
			want: "launchctl bootout gui/501/x failed and the job is still loaded after 20 checks: " +
				"launchctl exited 5: Boot-out failed",
		},
		{
			name: "could not run", bootout: unrunnable, calls: 1,
			want: "launchctl bootout gui/501/x could not run: exec: \"launchctl\": executable file not found in $PATH",
		},
		{
			name: "print neither finds nor rules out", bootout: exited(5, "Boot-out failed"),
			print: []reply{exited(1, "Bad request.")}, calls: 2,
			want: "launchctl bootout gui/501/x failed (launchctl exited 5: Boot-out failed) and launchctl print " +
				"could not confirm it unloaded: launchctl exited 1: Bad request.",
		},
		{
			name: "print could not confirm", bootout: reply{err: codedError{code: 5}}, print: []reply{unrunnable},
			calls: 2,
			want: "launchctl bootout gui/501/x failed (exit status 5) and launchctl print could not confirm " +
				"it unloaded: exec: \"launchctl\": executable file not found in $PATH",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &scriptedRunner{replies: map[string][]reply{"bootout": {tc.bootout}, "print": tc.print}}
			pauses := 0
			err := Launchd{Runner: runner, UID: 501, Pause: counting(&pauses)}.Bootout(context.Background(), "x")
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want || len(runner.calls) != tc.calls || pauses != tc.pauses {
				t.Fatalf("err=%q calls=%d pauses=%d, want %q %d %d (ran %q)",
					got, len(runner.calls), pauses, tc.want, tc.calls, tc.pauses, runner.calls)
			}
		})
	}
}

func TestBootstrap(t *testing.T) {
	ioError := exited(5, "Bootstrap failed: 5: Input/output error")
	pauseFailed := errors.New("wait: context canceled")
	for _, tc := range []struct {
		name      string
		bootstrap []reply
		attempts  int
		loaded    []bool
		pauseErr  error
		done      bool
		calls     int
		lastCode  int
		want      string
	}{
		{name: "first attempt", bootstrap: []reply{{}}, attempts: BootstrapAttempts, done: true, calls: 1},
		{
			name: "after a teardown in flight", bootstrap: []reply{ioError, ioError, {}},
			attempts: BootstrapAttempts, done: true, calls: 3,
		},
		{
			name: "attempts exhausted", bootstrap: []reply{ioError}, attempts: BootstrapAttempts,
			calls: BootstrapAttempts, lastCode: 5,
		},
		{
			name: "could not run", bootstrap: []reply{unrunnable}, attempts: BootstrapAttempts, calls: 1,
			want: "exec: \"launchctl\": executable file not found in $PATH",
		},
		{
			name: "loaded despite a failed exit", bootstrap: []reply{ioError}, attempts: 3,
			loaded: []bool{false, true}, done: true, calls: 2, lastCode: 5,
		},
		{
			name: "never loaded", bootstrap: []reply{{}}, attempts: 3, loaded: []bool{false, false, false}, calls: 3,
		},
		{
			name: "loaded after a bootstrap killed by a signal", bootstrap: []reply{exited(-1, "")}, attempts: 3,
			loaded: []bool{true}, done: true, calls: 1, lastCode: -1,
		},
		{
			name: "pause failed", bootstrap: []reply{ioError}, attempts: 3, pauseErr: pauseFailed, calls: 1,
			lastCode: 5, want: "wait: context canceled",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &scriptedRunner{replies: map[string][]reply{"bootstrap": tc.bootstrap}}
			var loaded func(context.Context) (bool, error)
			if tc.loaded != nil {
				answers := tc.loaded
				loaded = func(context.Context) (bool, error) {
					up := answers[0]
					answers = answers[1:]
					return up, nil
				}
			}
			pause := func(context.Context, time.Duration) error { return tc.pauseErr }
			last, done, err := Launchd{Runner: runner, UID: 501, Pause: pause}.Bootstrap(
				context.Background(), "/agents/x.plist", tc.attempts, loaded)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if done != tc.done || got != tc.want || len(runner.calls) != tc.calls ||
				last.Result.ExitCode != tc.lastCode {
				t.Fatalf("done=%v err=%q calls=%d last exit=%d, want %v %q %d %d",
					done, got, len(runner.calls), last.Result.ExitCode, tc.done, tc.want, tc.calls, tc.lastCode)
			}
			if runner.calls[0] != "launchctl bootstrap gui/501 /agents/x.plist" {
				t.Fatalf("ran %q, want launchctl bootstrap gui/501 /agents/x.plist", runner.calls[0])
			}
		})
	}
}
