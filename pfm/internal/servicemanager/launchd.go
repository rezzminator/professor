// Package servicemanager is pfm's one door to the per-user service managers:
// launchd's gui/<uid> domain and its targets, `launchctl print`, `bootout`
// confirmed by print and `bootstrap` retried over a teardown in flight, and
// `systemctl --user`. The installer, restart, themesync and doctor all ask
// through it; it imports none of them.
package servicemanager

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	// NotFoundExit is `launchctl print`'s exit for a label launchd does not
	// know ("Could not find service … in domain").
	NotFoundExit = 113
	// BootstrapAttempts and BootstrapRetryInterval bound the retry that rides
	// out a teardown still in flight — ~2s total, bounded so a genuinely
	// unloadable job reports instead of hanging its caller.
	BootstrapAttempts      = 20
	BootstrapRetryInterval = 100 * time.Millisecond
)

// Runner is the exec seam every service-manager command crosses; deps.Runner
// satisfies it. A returned error whose deps.ExitCode is positive is a command
// that ran and answered with that exit; any other error is one that could not
// run.
type Runner interface {
	Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error)
}

// UID is the calling user's uid: the one place a launchd domain's uid comes
// from.
func UID() int { return os.Getuid() }

// Domain is uid's GUI domain, where every pfm launch agent is bootstrapped.
func Domain(uid int) string { return "gui/" + strconv.Itoa(uid) }

// ServiceTarget is the service target launchctl addresses label by in uid's
// domain.
func ServiceTarget(uid int, label string) string { return Domain(uid) + "/" + label }

// ShellTarget is ServiceTarget for a command a human pastes into a shell: the uid is
// left to `id -u` there.
func ShellTarget(label string) string { return "gui/$(id -u)/" + label }

// Answer is one service-manager command's outcome.
type Answer struct {
	Argv   []string
	Result deps.RunResult
	// RunErr is the runner's own error, verbatim: nil for a command that ran
	// and answered by its exit status alone.
	RunErr error
}

// answerOf runs argv once through runner. A coded runner error's exit code is
// copied into Result.ExitCode, so every reading looks at one field.
func answerOf(ctx context.Context, runner Runner, argv []string) Answer {
	names, nameErr := CurrentUnitNames()
	if nameErr != nil {
		return Answer{Argv: argv, RunErr: nameErr}
	}
	qualified := make([]string, len(argv))
	for index, word := range argv {
		qualified[index] = names.Name(word)
	}
	argv = qualified
	result, err := runner.Run(ctx, argv, deps.RunOptions{})
	if code := deps.ExitCode(err); code > 0 && result.ExitCode == 0 {
		result.ExitCode = code
	}
	return Answer{Argv: argv, Result: result, RunErr: err}
}

// Ran reports whether the command ran and answered with an exit status,
// whatever it was: false for a tool missing from PATH, permission denied, a
// signal (a negative exit) or a runner that failed outright.
func (answer Answer) Ran() bool {
	if answer.RunErr == nil {
		return answer.Result.ExitCode >= 0
	}
	return deps.ExitCode(answer.RunErr) > 0
}

// Command is the command line the answer is for, as a person reads it.
func (answer Answer) Command() string { return strings.Join(answer.Argv, " ") }

// OK reports a command that ran and exited 0.
func (answer Answer) OK() bool {
	return answer.RunErr == nil && answer.Result.ExitCode == 0
}

// Err is nil for an OK answer, the runner's error verbatim when it returned
// one, and an *ExitError for a non-zero exit the runner reported without one.
func (answer Answer) Err() error {
	if answer.RunErr != nil {
		return answer.RunErr
	}
	if answer.Result.ExitCode != 0 {
		return &ExitError{Argv: answer.Argv, Result: answer.Result}
	}
	return nil
}

// Unknown reports a `launchctl print` that answered that launchd does not know
// the label: exit NotFoundExit, or stderr naming "Could not find service".
// Callers ask Ran first; a command that could not run carries neither.
func (answer Answer) Unknown() bool {
	return !answer.OK() && (answer.Result.ExitCode == NotFoundExit ||
		strings.Contains(string(answer.Result.Stderr), "Could not find service"))
}

// State is the word on a `launchctl print` answer's `state = …` line, compared
// whole ("state = not running" contains "running"); "" when it has none.
func (answer Answer) State() string {
	for _, line := range strings.Split(string(answer.Result.Stdout), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "state = "); ok {
			return value
		}
	}
	return ""
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Argv   []string
	Result deps.RunResult
}

func (err *ExitError) Error() string {
	return fmt.Sprintf("%s exited %d: %s",
		err.Argv[0], err.Result.ExitCode, strings.TrimSpace(string(err.Result.Stderr)))
}

// ExitCode is the command's exit status.
func (err *ExitError) ExitCode() int { return err.Result.ExitCode }

// Launchd asks launchd about the agents in UID's GUI domain.
type Launchd struct {
	Runner Runner
	UID    int
	// Pause waits between the attempts of a retry; nil waits on clock.Real.
	Pause func(context.Context, time.Duration) error
}

// NewLaunchd is the calling user's domain over runner, pausing through pause
// (nil for clock.Real).
func NewLaunchd(runner Runner, pause func(context.Context, time.Duration) error) Launchd {
	return Launchd{Runner: runner, UID: UID(), Pause: pause}
}

func (launchd Launchd) pause(ctx context.Context, d time.Duration) error {
	if launchd.Pause != nil {
		return launchd.Pause(ctx, d)
	}
	return clock.Real.Sleep(ctx, d)
}

// Target is label's service target in this domain.
func (launchd Launchd) Target(label string) string { return ServiceTarget(launchd.UID, label) }

// Run runs `launchctl args…` once.
func (launchd Launchd) Run(ctx context.Context, args ...string) Answer {
	return answerOf(ctx, launchd.Runner, append([]string{LaunchctlBinary}, args...))
}

// Print runs `launchctl print` on label's target: OK is loaded, Unknown is
// not loaded, anything else could not tell.
func (launchd Launchd) Print(ctx context.Context, label string) Answer {
	return launchd.Run(ctx, "print", launchd.Target(label))
}

// Bootout unloads label and reports whether launchd still holds it. launchctl
// exits non-zero for a label it does not hold, which is the state an unload
// wants, so a coded bootout failure is checked with `launchctl print`: a label
// print no longer finds is unloaded and no error. bootout only requests a
// teardown, so a label print still finds is asked again over BootstrapAttempts
// checks; one still loaded after them, or a launchctl that never answered, is
// an error naming each answer.
func (launchd Launchd) Bootout(ctx context.Context, label string) error {
	target := launchd.Target(label)
	bootout := launchd.Run(ctx, "bootout", target)
	if bootout.OK() {
		return nil
	}
	bootoutErr := bootout.Err()
	if !bootout.Ran() {
		return fmt.Errorf("launchctl bootout %s could not run: %w", target, bootoutErr)
	}
	for attempt := 0; attempt < BootstrapAttempts; attempt++ {
		if attempt > 0 {
			if err := launchd.pause(ctx, BootstrapRetryInterval); err != nil {
				return fmt.Errorf("launchctl bootout %s failed (%w) and the wait for its teardown failed: %w",
					target, bootoutErr, err)
			}
		}
		probe := launchd.Print(ctx, label)
		if probe.OK() {
			continue
		}
		if probe.Unknown() {
			return nil
		}
		// A print that ran but neither found nor ruled out the label (a domain
		// or permission failure) proves nothing about the job: never unloaded.
		return fmt.Errorf(
			"launchctl bootout %s failed (%w) and launchctl print could not confirm it unloaded: %w",
			target,
			bootoutErr,
			probe.Err(),
		)
	}
	return fmt.Errorf(
		"launchctl bootout %s failed and the job is still loaded after %d checks: %w",
		target,
		BootstrapAttempts,
		bootoutErr,
	)
}

// Bootstrap loads the plist at path into this domain, retrying while launchd
// finishes a teardown that a bootout only requested: an immediate bootstrap
// can fail with EIO against a label still on its way out. It makes up to
// attempts tries, pausing BootstrapRetryInterval between them, and is done
// after the first that exits 0 — or, when loaded is given, the first after
// which loaded answers true, whatever the bootstrap's exit. last is the final
// attempt. A runner error that is no exit status (launchctl missing, permission
// denied) ends it with that error, as does a loaded or pause failure; attempts
// exhausted is done false, err nil.
func (launchd Launchd) Bootstrap(
	ctx context.Context,
	path string,
	attempts int,
	loaded func(context.Context) (bool, error),
) (last Answer, done bool, err error) {
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := launchd.pause(ctx, BootstrapRetryInterval); err != nil {
				return last, false, err
			}
		}
		last = launchd.Run(ctx, "bootstrap", Domain(launchd.UID), path)
		if last.RunErr != nil && !last.Ran() {
			return last, false, last.RunErr
		}
		if loaded == nil {
			if last.OK() {
				return last, true, nil
			}
			continue
		}
		up, err := loaded(ctx)
		if err != nil {
			return last, false, err
		}
		if up {
			return last, true, nil
		}
	}
	return last, false, nil
}
