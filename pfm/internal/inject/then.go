package inject

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
)

// DeliverThen is the waiter half of chat.sh's __then subcommand
// (chat.sh:1048-1085). It rides out the primary turn — typically a /compact
// compaction — and delivers the FIRST steer once the pane has been idle long
// enough to hold, passing the remainder along so the chain re-arms itself one
// confirmed delivery at a time: steer N+1 always waits out steer N's whole
// turn. It runs in a DETACHED process because for a self-inject the waiter
// waits on the very turn that spawned it.
//
// The armed record beside the steer log (armed.go) names this waiter for as
// long as the chain runs: claimed on entry, handed to the next hop when this
// one delivered and armed it, removed on the chain's last delivery or on any
// refusal — so ScheduleAfterCurrentTurn can refuse a second arming by name.
func (engine *Engine) DeliverThen(ctx context.Context, wait ThenWait) (result Result, err error) {
	ctx = withSender(ctx, engine.sender(ctx))
	socketPath, target, steers := wait.SocketPath, wait.Target, wait.Steers
	if target == "" || len(steers) == 0 || steers[0] == "" {
		return refused(
			1,
			"then waiter needs a socket, a target, and at least one steer",
		), nil
	}
	if socketPath != "" {
		// The socket rides the environment so a pane-id target resolves on the
		// server that actually holds it (chat.sh:1082).
		if err := os.Setenv("CHAT_INJECT_SOCKET", socketPath); err != nil {
			return Result{}, err
		}
	}
	armed := armedPathFor(engine.steerLogPath(Target{SocketPath: socketPath, Pane: target}))
	if claimErr := engine.claimArmed(armed, steers[0]); claimErr != nil {
		// The record could not be READ. Claiming it anyway would overwrite an
		// arming whose identity is unknown — see claimArmed. Nothing is typed
		// and nothing is released: the record stays exactly as it was found,
		// for whoever wrote it.
		return Result{
			Status: statusUndelivered,
			Code:   CodeUndelivered,
			Message: fmt.Sprintf(
				"then steer NOT delivered: could not read the armed steer record %s: %v — refusing to claim it over an arming this waiter cannot identify; steer kept in the log: %q",
				armed,
				claimErr,
				steers[0],
			),
		}, nil
	}
	defer func() {
		engine.releaseArmed(armed, err == nil && result.Code == 0 && result.Steers > 0)
	}()
	observed, baselineErr := engine.waitForSettledTurn(ctx, socketPath, target, wait.SelfTarget)
	if baselineErr != nil {
		// Not one capture of the pane succeeded, so the waiter never had a
		// reference frame to judge a compaction receipt against. Delivering
		// here would be delivering blind — and reporting the strong guarantee
		// over a pane that was never read once is the exact shape this waiter
		// exists to refuse.
		return Result{
			Status: statusUndelivered,
			Code:   CodeUndelivered,
			Message: fmt.Sprintf(
				"then steer NOT delivered: could not read pane %q even once for a baseline (last tmux error: %v) — the waiter never had a reference frame for this turn; steer kept in the log: %q",
				target,
				baselineErr,
				steers[0],
			),
		}, nil
	}
	if !observed && wait.Engine == string(pfmengine.Codex) {
		// The steady-idle fallback below is a GUESS, and on a Codex pane it
		// is the wrong one: the Claude busy regex does not know the Codex
		// footer and the receipt regex does not know its compaction line
		// (guards.go), so "never went busy" is what a Codex compaction in
		// progress looks like, and the two 2026-09-18 sightings were steers
		// typed into exactly that. A steer lost with a named cause beats one
		// typed into a compacting pane; the spelling lands with Tier B E2.09.
		return Result{
			Status: statusUndelivered,
			Code:   CodeUndelivered,
			Message: fmt.Sprintf(
				"then steer NOT delivered: no turn boundary observed on a Codex pane — the Codex busy/compaction footer is not yet pinned (Tier B beat E2.09 captures it); steer kept in the log: %q",
				steers[0],
			),
		}, nil
	}
	if quiet, readErr := engine.waitForQuietTypist(ctx, socketPath, target); !quiet {
		// Never deliver over a typing human, and never force: the waiter has
		// no operator standing by to authorize force_now, and the whole point
		// of this guard is that a live keystroke is not a safe queue surface
		// (the 2026-09-03 self-compact that ate an operator's live draft).
		// readErr distinguishes "tmux could not be read for the whole wait
		// window" from "a human kept typing" — the two exhaust the same loop
		// identically, but only one of them is evidence of a typist, and an
		// error rendered as an affirmative typing claim is the exact
		// anti-pattern this guard exists to police.
		if readErr != nil {
			return Result{
				Status: statusUndelivered,
				Code:   CodeUndelivered,
				Message: fmt.Sprintf(
					"then steer NOT delivered: could not read who is at %q for %s (last tmux error: %v); chain aborted with %d steer(s) undelivered",
					target,
					time.Duration(engine.options.ThenIdleTries)*engine.options.ThenIdlePoll,
					readErr,
					len(steers),
				),
			}, nil
		}
		return Result{
			Status: "typing",
			Code:   CodeBusy,
			Message: fmt.Sprintf(
				"then steer NOT delivered: a human kept typing in %q for %s; chain aborted with %d steer(s) undelivered",
				target,
				time.Duration(engine.options.ThenIdleTries)*engine.options.ThenIdlePoll,
				len(steers),
			),
		}, nil
	}
	result, err = engine.inject(ctx, Request{
		Target:  target,
		Message: steers[0],
		Then:    steers[1:],
		Chain:   true,
	})
	if err == nil && !observed {
		// Delivered, but without ever seeing the primary's turn begin and
		// end. Stranding the chain would be worse, so the steer still goes —
		// and the caller is told on the result itself, because a weaker
		// guarantee that looks identical to a strong one is the failure this
		// whole waiter exists to avoid.
		result.Message += " (WARNING: no turn boundary was observed before " +
			"delivery — the pane never went busy after the caller yielded, so " +
			"this steer may have landed beside the primary rather than after it)"
	}
	return result, err
}

// statusUndelivered is the Result.Status of a waiter that gave up without
// typing: the steer is in the log, nothing reached the pane.
const statusUndelivered = "undelivered"

// waitForQuietTypist holds the waiter back from delivering a steer over a
// human mid-keystroke — the same guard engine.inject applies to a live
// delivery (the 2026-09-03 self-compact that ate an operator's live draft). DeliverThen types through
// engine.inject too, but only AFTER waitForSettledTurn has already decided
// the primary's turn is over; this runs once more here so the waiter never
// spends waitForSettledTurn's decision polling for a turn boundary while
// missing a typist that started AFTER the turn settled.
//
// A tmux error is logged — the waiter's own stderr IS its log
// (CommandThenSpawner.Spawn redirects both to LogPath) — and counts as one
// failed poll, never as "quiet": an error is a failure to look, not evidence
// nobody is there.
//
// The return is a tri-state, not a bool: quiet=true means the pane was
// actually observed idle. quiet=false, readErr=nil means the budget
// exhausted while an actual typist was observed — a real "someone kept
// typing." quiet=false, readErr!=nil means EVERY poll across the whole wait
// window failed to read tmux at all — a dead socket, a vanished pane,
// anything that keeps erroring — which the caller must report as "could not
// read," never alias with "a human kept typing": the two exhaust the loop
// identically, but only one of them is evidence anyone is there.
func (engine *Engine) waitForQuietTypist(
	ctx context.Context,
	socketPath, target string,
) (quiet bool, readErr error) {
	errCount := 0
	var lastErr error
	for attempt := 0; attempt < engine.options.ThenIdleTries; attempt++ {
		last, typing, err := engine.tmux.ClientActivity(ctx, socketPath, target)
		switch {
		case err != nil:
			errCount++
			lastErr = err
			engine.warnf(
				"pfm: then waiter: could not read who is at %q (tmux list-clients: %v)\n",
				target,
				err,
			)
		case !typing || engine.options.Clock.Now().Sub(last) >= engine.options.TypistQuiet:
			return true, nil
		}
		engine.sleepContext(ctx, engine.options.ThenIdlePoll)
	}
	if engine.options.ThenIdleTries > 0 && errCount == engine.options.ThenIdleTries {
		return false, lastErr
	}
	return false, nil
}

// CommandThenSpawner starts this binary's own waiter under a detached process,
// mirroring chat.sh's `$(cc_detach) bash "$0" __then …` (chat.sh:946-948).
type CommandThenSpawner struct {
	Executable string
	ConfigPath string
	Setsid     string
	Nohup      string
	// Runner is the deps.Runner seam Spawn launches the waiter through; nil
	// defaults to deps.RealRunner{}.
	Runner deps.Runner
	// Clock stamps the armed record (armed.go); nil defaults to clock.Real.
	Clock clock.Clock
}

// Spawn launches the detached waiter and returns as soon as it is running.
func (spawner CommandThenSpawner) Spawn(
	ctx context.Context,
	request SteerSpawn,
) (returnErr error) {
	executable := spawner.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve pfm executable: %w", err)
		}
	}
	arguments := []string{executable}
	if spawner.ConfigPath != "" {
		arguments = append(arguments, "--config", spawner.ConfigPath)
	}
	arguments = append(arguments,
		"internal",
		"then",
		"--socket",
		request.SocketPath,
		"--target",
		request.Target,
	)
	if request.SelfTarget {
		arguments = append(arguments, "--self")
	}
	if request.Engine != "" {
		arguments = append(arguments, "--engine", request.Engine)
	}
	for _, steer := range request.Steers {
		arguments = append(arguments, "--steer", steer)
	}
	launcher, prefixArgs, forked, err := deps.DetachLauncher(spawner.Setsid, spawner.Nohup)
	if err != nil {
		return fmt.Errorf("detach then waiter: %w", err)
	}
	usingNohup := !forked
	arguments = append(prefixArgs, arguments...)
	if err := ctx.Err(); err != nil {
		return err
	}
	stated := []string{
		"CHAT_INJECT_SOCKET=" + request.SocketPath,
		"CHAT_THEN_CHAIN=1",
	}
	stated = append(stated, senderEnvironment(request.Sender)...)
	// Any inherited definition of these names is dropped first: os.Getenv
	// answers with the FIRST match, so appending over an inherited value would
	// leave the inherited one winning, and a chain hop would sign as whoever
	// spawned the hop before it.
	env := append(withoutNames(os.Environ(), stated), stated...)
	// Stdin is left unset: a deps.Runner.Start child reads from the null
	// device by default (os/exec's own contract for a nil Stdin) exactly as
	// the explicit /dev/null wiring this replaced did.
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device for then waiter: %w", err)
	}
	defer func() {
		if err := null.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close null device for then waiter: %w", err))
		}
	}()
	// A fresh chain truncates the log; a HOP appends — truncating on a hop
	// would wipe the chain's earlier hops while they are still being written.
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if request.Append {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	var stdout, stderr io.Writer = null, null
	log, err := os.OpenFile(request.LogPath, flags, 0o600)
	if err == nil {
		defer func() {
			if err := log.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close then waiter log %s: %w", request.LogPath, err))
			}
		}()
		stdout, stderr = log, log
	}
	runner := spawner.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	opts := deps.StartOptions{
		Env:    env,
		Stdout: stdout,
		Stderr: stderr,
		// The nohup floor detaches by releasing the process handle right
		// after Start (deps.StartOptions' Detach shape); the setsid launcher
		// already forks and returns on its own (setsid -f), so Spawn waits
		// on it exactly as it waited on command.Run() before this seam.
		Detach: usingNohup,
	}
	// The armed record goes down BEFORE the waiter starts, so a schedule
	// racing this one already sees the pane armed; a chain hop (Append) is
	// the same arming and leaves the record to the hop that owns it.
	if !request.Append && request.LogPath != "" && len(request.Steers) != 0 {
		clk := spawner.Clock
		if clk == nil {
			clk = clock.Real
		}
		if err := armRecord(request, clk.Now()); err != nil {
			return err
		}
	}
	process, err := runner.Start(ctx, append([]string{launcher}, arguments...), opts)
	if err != nil {
		if !request.Append && request.LogPath != "" {
			if removeErr := os.Remove(armedPathFor(request.LogPath)); removeErr != nil {
				err = errors.Join(err, fmt.Errorf("remove armed steer record after a failed start: %w", removeErr))
			}
		}
		if usingNohup {
			return fmt.Errorf("start detached then waiter with nohup: %w", err)
		}
		return fmt.Errorf("start detached then waiter with setsid: %w", err)
	}
	if usingNohup {
		if err := process.Release(); err != nil {
			return fmt.Errorf("release detached then waiter: %w", err)
		}
		return nil
	}
	if err := process.Wait(); err != nil {
		return fmt.Errorf("start detached then waiter with setsid: %w", err)
	}
	return nil
}

// senderEnvironment states the spawning chat's identity to the waiter. An
// empty field is left unset rather than exported empty, so a chat that could
// not derive its own identity hands down nothing and the waiter's message
// still says UNSIGNED out loud instead of signing as a nameless sender.
func senderEnvironment(sender Sender) []string {
	stated := make([]string, 0, 3)
	for _, pair := range [][2]string{
		{SenderSessionEnv, sender.Session},
		{SenderLabelEnv, sender.Label},
		{SenderIDEnv, sender.UUID},
	} {
		if pair[1] != "" {
			stated = append(stated, pair[0]+"="+pair[1])
		}
	}
	return stated
}

// withoutNames drops every definition of the names the given NAME=value pairs
// set, so the caller's own definitions are the only ones in the child.
func withoutNames(environment, pairs []string) []string {
	names := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if name, _, ok := strings.Cut(pair, "="); ok {
			names[name] = true
		}
	}
	// The full set is dropped, not just the ones being set: a waiter that
	// inherits CHAT_SENDER_LABEL from an earlier hop while this hop states
	// only a session would sign with two different chats' fields.
	for _, name := range []string{
		SenderSessionEnv,
		SenderLabelEnv,
		SenderIDEnv,
	} {
		names[name] = true
	}
	kept := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !names[name] {
			kept = append(kept, entry)
		}
	}
	return kept
}
