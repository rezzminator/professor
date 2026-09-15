package installer

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// callKind reports whether calls contains any entry whose first token
// (after the command name) is verb — "bootout" or "bootstrap" — so the
// tests below assert on what was actually run, not just on the returned
// error.
func callKind(calls []string, verb string) bool {
	for _, call := range calls {
		fields := strings.Fields(call)
		if len(fields) >= 2 && fields[1] == verb {
			return true
		}
	}
	return false
}

// loadedRunner models a job launchd already has loaded: `print` always
// succeeds. `bootout`/`bootstrap` are recorded but otherwise no-ops, so a
// test built on it can assert on whether they were ever called at all.
type loadedRunner struct {
	calls []string
}

func (r *loadedRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

// TestReloadLaunchAgentLoadedUnchangedRestartsNothing is the headline
// regression: a healthy, already-loaded daemon whose plist did not change
// must survive an ordinary install untouched. Before the fix this reloaded
// (bootout then bootstrap) unconditionally, which is exactly what killed a
// running `pfm mcp serve` on this host. Checked for both launchd labels.
func TestReloadLaunchAgentLoadedUnchangedRestartsNothing(t *testing.T) {
	for _, label := range []string{mcpLaunchdLabel, launchdLabel} {
		t.Run(label, func(t *testing.T) {
			runner := &loadedRunner{}
			installer := engine{
				options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
				apply:   true,
			}
			if err := installer.reloadLaunchAgentWithLabel(
				context.Background(),
				"/fixture/agent.plist",
				label,
				false,
			); err != nil {
				t.Fatalf("reload of a loaded, unchanged agent returned %v, want nil", err)
			}
			if callKind(runner.calls, "bootout") {
				t.Fatalf(
					"calls=%q, want no bootout — a healthy daemon with no new plist must not be stopped",
					runner.calls,
				)
			}
			if callKind(runner.calls, "bootstrap") {
				t.Fatalf(
					"calls=%q, want no bootstrap — a healthy daemon with no new plist must not be restarted",
					runner.calls,
				)
			}
		})
	}
}

// TestReloadLaunchAgentLoadedChangedReloads is the companion boundary: a
// loaded job whose plist DID change must still be stopped and
// re-registered, so the new file actually takes effect.
func TestReloadLaunchAgentLoadedChangedReloads(t *testing.T) {
	runner := &loadedRunner{}
	installer := engine{
		options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
	}
	if err := installer.reloadLaunchAgentWithLabel(
		context.Background(),
		"/fixture/agent.plist",
		mcpLaunchdLabel,
		true,
	); err != nil {
		t.Fatalf("reload of a loaded, changed agent returned %v, want nil", err)
	}
	if !callKind(runner.calls, "bootout") {
		t.Fatalf("calls=%q, want a bootout — a changed plist must stop the stale job", runner.calls)
	}
	if !callKind(runner.calls, "bootstrap") {
		t.Fatalf("calls=%q, want a bootstrap — a changed plist must be reloaded", runner.calls)
	}
}

// notLoadedRunner models a job launchd has never heard of: `print` fails
// until a `bootstrap` call succeeds, at which point the job is "loaded" and
// `print` starts succeeding — the shape the installer's own post-bootstrap
// verification print depends on.
type notLoadedRunner struct {
	calls       []string
	bootstraped bool
}

func (r *notLoadedRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "print":
		if r.bootstraped {
			return nil
		}
		return errors.New("could not find service")
	case "bootstrap":
		r.bootstraped = true
		return nil
	}
	return nil
}

// TestReloadLaunchAgentNotLoadedBootstrapsWithoutBootout covers the other
// half of the early-return boundary: a service that is down must still be
// started even though its plist did not change, and starting it must never
// issue a bootout against a job that was never running.
func TestReloadLaunchAgentNotLoadedBootstrapsWithoutBootout(t *testing.T) {
	runner := &notLoadedRunner{}
	installer := engine{
		options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
	}
	if err := installer.reloadLaunchAgentWithLabel(
		context.Background(),
		"/fixture/agent.plist",
		mcpLaunchdLabel,
		false,
	); err != nil {
		t.Fatalf("reload of a not-loaded agent returned %v, want nil", err)
	}
	if !callKind(runner.calls, "bootstrap") {
		t.Fatalf("calls=%q, want a bootstrap — a down service must be started", runner.calls)
	}
	if callKind(runner.calls, "bootout") {
		t.Fatalf("calls=%q, want no bootout — nothing was running to stop", runner.calls)
	}
}

// flakyBootstrapRunner models a teardown still in flight: the job was
// loaded and booted out, and `bootstrap` fails with the real EIO shape for
// failCount attempts before it succeeds, at which point the job reports
// loaded again.
type flakyBootstrapRunner struct {
	calls          []string
	failCount      int
	bootstrapCalls int
	loaded         bool
}

func (r *flakyBootstrapRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "print":
		if r.loaded {
			return nil
		}
		return errors.New("could not find service")
	case "bootout":
		r.loaded = false
		return nil
	case "bootstrap":
		r.bootstrapCalls++
		if r.bootstrapCalls <= r.failCount {
			return errors.New("bootstrap exit status 5")
		}
		r.loaded = true
		return nil
	}
	return nil
}

// TestReloadLaunchAgentRetriesBootstrapThroughATeardownInFlight is the
// retry regression: `bootout` returns as soon as launchd ACCEPTS the
// request, not once the label is actually gone, so an immediate bootstrap
// can hit EIO against a label still on its way out. The retry must ride
// that out rather than surface the first failure.
func TestReloadLaunchAgentRetriesBootstrapThroughATeardownInFlight(t *testing.T) {
	runner := &flakyBootstrapRunner{failCount: 2, loaded: true}
	installer := engine{
		options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
	}
	if err := installer.reloadLaunchAgentWithLabel(
		context.Background(),
		"/fixture/agent.plist",
		mcpLaunchdLabel,
		true,
	); err != nil {
		t.Fatalf("reload returned %v, want nil — the retry should have ridden out the in-flight teardown", err)
	}
	if runner.bootstrapCalls <= 1 {
		t.Fatalf("bootstrapCalls=%d, want more than one attempt", runner.bootstrapCalls)
	}
}

// alwaysFailsBootstrapRunner models a job that was never loaded and whose
// bootstrap never succeeds — the installer neither stopped nor started
// anything, so its error must not claim a service went DOWN.
type alwaysFailsBootstrapRunner struct {
	calls []string
}

func (r *alwaysFailsBootstrapRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "print":
		return errors.New("could not find service")
	case "bootstrap":
		return errors.New("bootstrap exit status 5")
	}
	return nil
}

// TestReloadLaunchAgentNeverLoadedBootstrapFailureStaysPlain keeps the two
// bootstrap-failure messages distinguishable: a job that was never running
// gets the plain "not loaded" wording, never the DOWN/restart wording
// reserved for a job the installer itself stopped.
func TestReloadLaunchAgentNeverLoadedBootstrapFailureStaysPlain(t *testing.T) {
	runner := &alwaysFailsBootstrapRunner{}
	installer := engine{
		options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
	}
	err := installer.reloadLaunchAgentWithLabel(context.Background(), "/fixture/agent.plist", mcpLaunchdLabel, false)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "service is not loaded") {
		t.Fatalf("error=%q, want the plain \"not loaded\" wording", err.Error())
	}
	if strings.Contains(err.Error(), "DOWN") {
		t.Fatalf("error=%q, must not claim the service is DOWN — nothing was running to stop", err.Error())
	}
	if callKind(runner.calls, "bootout") {
		t.Fatalf("calls=%q, want no bootout — nothing was loaded", runner.calls)
	}
}
