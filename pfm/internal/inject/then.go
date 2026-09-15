package inject

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"hostops/pfm/internal/deps"
)

// DeliverThen is the waiter half of chat.sh's __then subcommand
// (chat.sh:1048-1085). It rides out the primary turn — typically a /compact
// compaction — and delivers the FIRST steer once the pane has been idle long
// enough to hold, passing the remainder along so the chain re-arms itself one
// confirmed delivery at a time: steer N+1 always waits out steer N's whole
// turn. It runs in a DETACHED process because for a self-inject the waiter
// waits on the very turn that spawned it.
func (engine *Engine) DeliverThen(
	ctx context.Context,
	socketPath, target string,
	steers []string,
	selfTarget bool,
) (Result, error) {
	ctx = withSender(ctx, engine.sender(ctx))
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
	observed := engine.waitForSettledTurn(ctx, socketPath, target, selfTarget)
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
				Status: "undelivered",
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
	result, err := engine.inject(ctx, Request{
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

// paneSample is one observation of the target pane. Busy alone cannot answer
// "is the turn I was sent to ride out over yet" — it is true for ANY turn,
// including the caller's own and the one the session starts by itself after a
// compaction. The receipt is the only positive evidence in the pane that a
// compaction actually ran.
type paneSample struct {
	busy    bool
	receipt bool
}

func (engine *Engine) samplePane(
	ctx context.Context,
	socketPath, target string,
) paneSample {
	capture, err := engine.tmux.Capture(ctx, socketPath, target, false, 0)
	if err != nil {
		// An unreadable pane is not busy; the delivery attempt reports the
		// dead pane truthfully instead of spinning here.
		return paneSample{}
	}
	return paneSample{
		busy:    IsBusy(capture),
		receipt: CompactionReceipt(capture),
	}
}

// waitForSettledTurn rides out the turn the PRIMARY started and reports whether
// it ever actually saw that turn.
//
// The old shape (chat.sh:1062-1077) waited for the pane to go busy and then for
// idle to hold steady. That works only if the busy it latches onto belongs to
// the primary — and busy carries no identity. For a self-inject the pane is
// already busy with the caller's own turn when the waiter wakes up, so the
// waiter would ride out the WRONG turn and then race whichever idle came first,
// losing in one of two directions depending on nothing but timing:
//
//   - caller stops promptly -> the waiter sees the idle BEFORE the queued
//     /compact has run and delivers the steer into a session that is about to
//     be compacted away, taking the steer with it.
//   - caller keeps working -> the brief idle right after the compaction is
//     shorter than the stability window, so the waiter sleeps through the one
//     usable moment and delivers on top of work that already resumed.
//
// Both are the same defect. The fix is to stop inferring the turn from a
// coincidence and identify it instead:
//
//  1. the caller's own turn must END first (an idle observation) — until then
//     nothing on screen can belong to the primary;
//  2. a turn must START after that (a busy observation) — that one is the
//     primary's;
//  3. a compaction receipt seen after BOTH is positive proof the primary was a
//     compaction and that it finished, so the first quiet sample after it is
//     the delivery point.
//
// Requiring the receipt to arrive after step 2 is what keeps step 3 from
// becoming a coincidence detector in its own right: a receipt already on screen
// when the waiter wakes up is scrollback from an EARLIER compaction and proves
// nothing about this one.
//
// Step 3 cannot apply to a primary that prints no receipt — a reload steer, a
// plain queued message, and (a NAMED gap) a Codex compaction, whose receipt
// spelling nobody here has confirmed. Those fall back to steps 1-2 plus the
// steady-idle window, which is strictly better than the old behaviour because
// the caller's own turn can no longer be mistaken for the primary's.
//
// The returned bool is false when the bound expired without ever observing a
// turn boundary. It is not an error — refusing to deliver would strand the
// chain, which is worse — but it is a WEAKER guarantee than the caller asked
// for, and DeliverThen says so on the visible result rather than only in a log.
func (engine *Engine) waitForSettledTurn(
	ctx context.Context,
	socketPath, target string,
	selfTarget bool,
) bool {
	sleepContext(ctx, engine.options.ThenMin)

	// Step 1 exists only for a self-inject, where the pane is busy with the
	// CALLER's turn when the waiter wakes up. For any other target nothing else
	// owns that pane, so its first busy already belongs to the primary and
	// insisting on a prior idle would wait out a boundary that never comes.
	callerYielded := !selfTarget
	turnStarted := false
	stable := 0
	sinceYield := 0

	tries := engine.options.ThenBusyTries + engine.options.ThenIdleTries
	for attempt := 0; attempt < tries; attempt++ {
		sample := engine.samplePane(ctx, socketPath, target)

		switch {
		case !callerYielded:
			callerYielded = !sample.busy
		case !turnStarted:
			turnStarted = sample.busy
			sinceYield++
		}

		// Positive proof outranks the busy/idle dance: once this turn's own
		// compaction receipt is on screen and the pane has gone quiet, the
		// turn we were sent to ride out is provably over.
		if turnStarted && sample.receipt && !sample.busy {
			sleepContext(ctx, engine.options.ThenSettle)
			return true
		}

		if turnStarted {
			if sample.busy {
				stable = 0
			} else {
				stable++
			}
			if stable >= engine.options.ThenIdleStable {
				sleepContext(ctx, engine.options.ThenSettle)
				return true
			}
		}

		// The primary's turn never began. Either it started and finished
		// inside ThenMin, or this pane does not report busy at all. Holding
		// out for a boundary that already went by would burn the whole idle
		// budget — minutes — and strand the steer, which is a worse failure
		// than delivering on a weaker guarantee. So fall back to steady idle
		// and return false, which is what puts the warning on the result
		// instead of letting a guess pass for proof.
		if callerYielded && !turnStarted &&
			sinceYield > engine.options.ThenBusyTries {
			if sample.busy {
				stable = 0
			} else {
				stable++
			}
			if stable >= engine.options.ThenIdleStable {
				sleepContext(ctx, engine.options.ThenSettle)
				return false
			}
		}
		sleepContext(ctx, engine.options.ThenIdlePoll)
	}
	sleepContext(ctx, engine.options.ThenSettle)
	return false
}

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
		case !typing || engine.options.Now().Sub(last) >= engine.options.TypistQuiet:
			return true, nil
		}
		sleepContext(ctx, engine.options.ThenIdlePoll)
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
}

// Spawn launches the detached waiter and returns as soon as it is running.
func (spawner CommandThenSpawner) Spawn(
	ctx context.Context,
	request SteerSpawn,
) error {
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
	var command *exec.Cmd
	if usingNohup {
		// The POSIX floor has no `setsid -f`; start it asynchronously and
		// release the process handle so the waiter outlives this caller.
		command = exec.Command(launcher, arguments...)
	} else {
		command = exec.CommandContext(ctx, launcher, arguments...)
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
	command.Env = append(withoutNames(os.Environ(), stated), stated...)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device for then waiter: %w", err)
	}
	defer null.Close()
	command.Stdin = null
	// A fresh chain truncates the log; a HOP appends — truncating on a hop
	// would wipe the chain's earlier hops while they are still being written.
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if request.Append {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	log, err := os.OpenFile(request.LogPath, flags, 0o600)
	if err != nil {
		command.Stdout = null
		command.Stderr = null
	} else {
		defer log.Close()
		command.Stdout = log
		command.Stderr = log
	}
	if usingNohup {
		if err := command.Start(); err != nil {
			return fmt.Errorf("start detached then waiter with nohup: %w", err)
		}
		if err := command.Process.Release(); err != nil {
			return fmt.Errorf("release detached then waiter: %w", err)
		}
		return nil
	}
	if err := command.Run(); err != nil {
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
