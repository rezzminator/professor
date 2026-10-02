package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// errInstallInterrupted is ApplyLayout's and Run's answer once a signal asked
// the install to stop: the step under way finished, and no later one ran.
var errInstallInterrupted = errors.New("interrupted — stopped before the next step")

// layoutApplyWork reports whether ApplyLayout will act on any finding — a host
// still migrating, by the install gate's own test: the advisory managed-cleanup
// row is never work, nor is a row that needs the clone on a host with none
// (layoutNeedsClone), which the apply only reports. Only then does the apply
// stop the pfm services: a migrated host's routine install touches no unit, a
// missing managed drop-in or a missing clone included.
func layoutApplyWork(env LayoutEnv, findings []LayoutFinding) bool {
	for _, finding := range findings {
		if finding.Err != nil || finding.Row == layoutRowManagedCleanup ||
			env.Clone == "" && layoutNeedsClone(finding) {
			continue
		}
		if finding.Verdict != VerdictOK && finding.Verdict != VerdictRefuse || finding.serviceHeld {
			return true
		}
	}
	return false
}

// layoutNeedsClone reports whether applyLayoutRow answers finding with
// errLayoutNoSource when the host has no clone: the zshrc row, and a config
// row that is not a move.
func layoutNeedsClone(finding LayoutFinding) bool {
	switch finding.Row {
	case layoutRowZshrc:
		return true
	case layoutRowConfig, layoutRowHarvesterConfig:
		return finding.Verdict != VerdictMove
	}
	return false
}

// watchSignals arms, before ApplyLayout stops its first unit, the watch that
// holds until every unit it stopped is back (RestartSchedulerUnits): a SIGINT,
// SIGTERM or SIGHUP is an interrupt request, never a kill and never a restart
// beside a running writer. SIGPIPE is watched too: a stdout or stderr whose
// reader died (`pfm update`'s capture, a `| tee` killed by the same Ctrl-C)
// fails its writes with EPIPE instead of killing pfm, and asks the same
// interrupt. ApplyLayout and Run stop at their next safe point, the stopped
// units restart, and `pfm install` exits 128 + the signal's number. A signal
// the process inherited as ignored (nohup) stays ignored.
func (journal *Journal) watchSignals() {
	journal.servicesMu.Lock()
	defer journal.servicesMu.Unlock()
	if journal.signals != nil {
		return
	}
	var watched []os.Signal
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE} {
		if !signal.Ignored(sig) {
			watched = append(watched, sig)
		}
	}
	if len(watched) == 0 {
		return
	}
	signals, done, exited := make(chan os.Signal, 1), make(chan struct{}), make(chan struct{})
	journal.signals, journal.signalsDone, journal.signalsExited = signals, done, exited
	signal.Notify(signals, watched...)
	go func() {
		defer close(exited)
		for {
			select {
			case sig := <-signals:
				journal.interrupt(sig, false)
			case <-done:
				return
			}
		}
	}()
}

// interrupt records the first signal and says what happens next on the
// journal's stderr; a later one is swallowed with the same promise. drained is
// a signal stopWatching found buffered after the held units' restart ran.
func (journal *Journal) interrupt(sig os.Signal, drained bool) {
	number, ok := sig.(syscall.Signal)
	if !ok {
		number = syscall.SIGTERM
	}
	stderr := journal.stderrWriter()
	switch {
	case !journal.interrupted.CompareAndSwap(0, int32(number)):
		fmt.Fprintf(
			stderr,
			"pfm install: %v — finishing the current step; the pfm services restart before pfm install exits\n",
			sig,
		)
	case drained:
		fmt.Fprintf(
			stderr,
			"pfm install: interrupt received (%v) — no pfm service is held stopped any longer; pfm install stops before its next step\n",
			sig,
		)
	default:
		fmt.Fprintf(
			stderr,
			"pfm install: interrupt received (%v) — finishing the current step, then restarting the pfm services\n",
			sig,
		)
	}
}

// stderrWriter is where the signal watch speaks: the stderr `pfm install`
// passed (NewInstallJournal), else os.Stderr.
func (journal *Journal) stderrWriter() io.Writer {
	if journal.stderr != nil {
		return journal.stderr
	}
	return os.Stderr
}

// Interrupted is errInstallInterrupted once a signal arrived, else nil; a nil
// journal was never interrupted. `pfm install` asks it before each write it
// makes after ApplyLayout.
func (journal *Journal) Interrupted() error {
	if journal == nil {
		return nil
	}
	if number := journal.interrupted.Load(); number != 0 {
		return fmt.Errorf("%w (%v)", errInstallInterrupted, syscall.Signal(number))
	}
	return nil
}

// holdServices records the units ApplyLayout stopped: the name-sync scheduler
// units wait for RestartSchedulerUnits, after installer.Run; the rest for
// restartHeldServices, after the last database row. A stop that held nothing
// ends the signal watch. settled is a stop that returned no error: every
// launchd label it booted out read not loaded afterwards.
func (journal *Journal) holdServices(stopped []string, settled bool) {
	journal.servicesMu.Lock()
	defer journal.servicesMu.Unlock()
	journal.stopSettled = settled
	for _, unit := range stopped {
		if schedulerUnit(unit) {
			journal.deferredScheduler = append(journal.deferredScheduler, unit)
		} else {
			journal.heldServices = append(journal.heldServices, unit)
		}
	}
	if len(journal.heldServices)+len(journal.deferredScheduler) == 0 {
		journal.stopWatching()
	}
}

// restartHeldServices restarts the units ApplyLayout restarts itself (the MCP
// service), right after the last database row or at any earlier return; with
// no scheduler unit left for RestartSchedulerUnits, the signal watch ends.
func (journal *Journal) restartHeldServices(ctx context.Context) error {
	journal.servicesMu.Lock()
	defer journal.servicesMu.Unlock()
	units := journal.heldServices
	journal.heldServices = nil
	err := restartLayoutServices(ctx, journal.env, units)
	if len(journal.deferredScheduler) == 0 {
		journal.stopWatching()
	}
	return err
}

// RestartSchedulerUnits starts the name-sync scheduler units ApplyLayout
// stopped and deferred, once installer.Run returned — on every outcome, so a
// refusal never leaves them down, and Run's gate never sees a job the install's
// own restart fired. A systemd unit already back (Run's enable --now) is left
// as it is; every launchd label is bootstrapped again (restartSchedulerLabels).
// The signal watch ends only once they are back.
// It returns 128 + the signal's number for an interrupted install, else code,
// or 1 for a failed start when code was 0.
func (journal *Journal) RestartSchedulerUnits(code int, stderr io.Writer) int {
	journal.servicesMu.Lock()
	defer journal.servicesMu.Unlock()
	units := journal.deferredScheduler
	journal.deferredScheduler = nil
	err := restartSchedulerLabels(journal.ctx, journal.env, units, schedulerIsLaunchd, journal.stopSettled)
	journal.stopWatching()
	if err != nil {
		fmt.Fprintf(stderr, "pfm install: restart name-sync scheduler: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	if number := journal.interrupted.Load(); number != 0 {
		return 128 + int(number)
	}
	return code
}

// stopWatching ends the signal watch once its goroutine returned, and keeps a
// signal still buffered; the caller holds servicesMu.
func (journal *Journal) stopWatching() {
	if journal.signals == nil {
		return
	}
	signal.Stop(journal.signals)
	close(journal.signalsDone)
	<-journal.signalsExited
	select {
	case sig := <-journal.signals:
		journal.interrupt(sig, true)
	default:
	}
	journal.signals, journal.signalsDone, journal.signalsExited = nil, nil, nil
}

// restartSchedulerLabels starts the deferred scheduler units again. On launchd
// every label this run booted out is bootstrapped, whatever a `launchctl print`
// read — one right after the bootout reads loaded while the teardown is still in
// flight — and a failed bootstrap of a label that reads loaded counts as already
// back (Run's own bootstrap) only when the stop settled; a label left failing
// names its manual bootstrap. On systemd only the units down are started
// (servicesDown).
func restartSchedulerLabels(ctx context.Context, env LayoutEnv, units []string, launchd, settled bool) error {
	if launchd {
		return restartLaunchdLabels(ctx, env, env.commandRunner(), units, settled)
	}
	down, err := servicesDown(ctx, env, units)
	return errors.Join(err, restartLayoutServices(ctx, env, down))
}

// servicesDown returns the systemd units to start again now: a unit is down
// unless its ActiveState reads running, and one whose probe cannot answer is
// started anyway (start is idempotent), its error still reported.
func servicesDown(ctx context.Context, env LayoutEnv, units []string) ([]string, error) {
	runner := env.commandRunner()
	var down []string
	var failures []error
	for _, unit := range units {
		state, err := fleetUnitState(ctx, runner, unit)
		if err != nil {
			failures = append(failures, err)
			down = append(down, unit)
			continue
		}
		if running, known := unitStateRunning(state); !known {
			failures = append(failures, fmt.Errorf("fleet unit %s reports unknown ActiveState %q", unit, state))
			down = append(down, unit)
		} else if !running {
			down = append(down, unit)
		}
	}
	return down, errors.Join(failures...)
}
