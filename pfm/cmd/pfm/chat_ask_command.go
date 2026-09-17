package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/inject"
)

// The defaults of a two-way turn. The timeout is generous because the thing
// on the other end is a model doing real work, and a wait that gives up while
// it is still typing turns a working conversation into a flaky one.
const (
	askTimeoutSeconds = 600
	askSettleSeconds  = 3
	// busyRetry is how long to leave a working chat alone before offering the
	// message again.
	busyRetry = 5 * time.Second
)

// runHeadlessAsk is the two-way verb: say something to a running chat and come
// back with what it said. It is `inject` plus the wait every caller of inject
// was writing by hand — a poll loop over `last` that cannot tell a new answer
// from the previous one, which is the bug this verb exists to delete.
func runHeadlessAsk(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		"chat ask",
		"usage: pfm chat ask [--timeout SECS] [--settle SECS] [--now] "+
			"[--json] [--progress] <name> <message>",
		stderr,
	)
	timeout := flags.Int("timeout", askTimeoutSeconds, "seconds to wait for the answer (0 waits forever)")
	settle := flags.Int("settle", askSettleSeconds, "seconds of quiet before an answer is finished")
	force := flags.Bool("now", false, "interrupt a working chat instead of waiting for it")
	asJSON := flags.Bool(jsonFormat, false, "emit one JSON object")
	progress := flags.Bool("progress", false, "print the chat's turns to stderr while waiting")
	// Only the flags BEFORE the name are parsed, exactly as `inject` does it:
	// a message may legitimately start with a dash, and an order silently
	// eaten as a flag is an order never delivered.
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() < 2 || *timeout < 0 || *settle < 0 {
		flags.Usage()
		return 2
	}
	name := flags.Arg(0)
	message := strings.Join(flags.Args()[1:], " ")

	ctx := context.Background()
	chat, code := headlessTarget(ctx, name, stdout, stderr, *asJSON, runtimes...)
	if code != 0 {
		return code
	}
	if !chat.Live {
		fmt.Fprintf(stderr, "pfm chat ask: %q is not running\n", chat.Name)
		return codeDeadChat
	}
	// The frontier is taken BEFORE the message is delivered: an answer is only
	// an answer if it was written after the question.
	frontier, err := headless.Frontier(chat)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat ask: %v\n", err)
		return 1
	}
	engine, err := pfmchat.NewInjectEngine(false, firstRuntime(runtimes))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat ask: %v\n", err)
		return 1
	}
	// Inject queues directly in a working Claude or Codex composer. The retry
	// remains for an engine that cannot expose a safe queue; --timeout bounds
	// that fallback, and --now interrupts instead of queueing.
	start := time.Now()
	budget := time.Duration(*timeout) * time.Second
	result, err := engine.Inject(ctx, inject.Request{
		Target:   chat.Socket,
		Message:  message,
		ForceNow: *force,
	})
	for err == nil && result.Code == inject.CodeBusy &&
		(budget == 0 || time.Since(start)+busyRetry < budget) {
		time.Sleep(busyRetry)
		result, err = engine.Inject(ctx, inject.Request{
			Target:   chat.Socket,
			Message:  message,
			ForceNow: *force,
		})
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat ask: %v\n", err)
		return 1
	}
	if result.Code != 0 {
		fmt.Fprintf(stderr, "pfm chat ask: %s\n", result.Message)
		return codeUndelivered
	}
	// The timeout covers the whole exchange, so waiting for a turn to end
	// spends the same budget the answer does. A zero budget stays zero: it
	// means wait for as long as it takes.
	remaining := time.Duration(0)
	if budget > 0 {
		remaining = budget - time.Since(start)
	}
	if budget > 0 && remaining <= 0 {
		fmt.Fprintf(
			stderr,
			"pfm chat ask: %s took the whole %ds to become free — "+
				"the message was delivered, the answer is not in yet\n",
			chat.Name,
			*timeout,
		)
		return codeAwaitTimeout
	}
	var progressOut io.Writer
	if *progress {
		progressOut = stderr
	}
	return awaitAnswer(
		ctx,
		askAction,
		chat.Name,
		chatHandle(chat.Socket, chat.Name),
		headless.AwaitOptions{
			Offset:   frontier,
			Timeout:  remaining,
			Settle:   time.Duration(*settle) * time.Second,
			Progress: progressOut,
		},
		*asJSON,
		stdout,
		stderr,
		runtimes...,
	)
}

// awaitAnswer waits for one answer and renders it, so a conversation opened at
// launch (`run --await`) and one continued later (`ask`) print the same thing
// and answer with the same exit codes.
//
// The answer goes to stdout ALONE — `$(pfm chat ask …)` is the reply
// and nothing else — while every diagnostic goes to stderr.
func awaitAnswer(
	ctx context.Context,
	verb, name, handle string,
	options headless.AwaitOptions,
	asJSON bool,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) int {
	turn, err := headless.Await(
		ctx,
		chatResolver(handle, runtimes...),
		options,
	)
	return reportTurn(turn, err, verb, name, options.Timeout, asJSON, stdout, stderr)
}

// reportTurn renders one finished wait and answers with its exit code. It is
// the whole exit-code contract of the two-way verbs in one place.
func reportTurn(
	turn headless.Turn,
	err error,
	verb, name string,
	timeout time.Duration,
	asJSON bool,
	stdout, stderr io.Writer,
) int {
	if turn.Name == "" {
		turn.Name = name
	}
	if asJSON {
		if encodeErr := writeJSON(stdout, turn); encodeErr != nil {
			fmt.Fprintf(stderr, "pfm chat %s: encode JSON: %v\n", verb, encodeErr)
			return 1
		}
	} else if turn.Answer != "" {
		fmt.Fprintln(stdout, turn.Answer)
	}
	if turn.Superseded {
		fmt.Fprintf(
			stderr,
			"pfm chat %s: another message reached %s while this one was "+
				"waiting — the answer above is the newest one, and may be theirs\n",
			verb,
			name,
		)
	}
	switch {
	case err == nil && turn.Superseded:
		return codeAwaitSuperseded
	case err == nil:
		return 0
	case errors.Is(err, headless.ErrAwaitTimeout):
		fmt.Fprintf(
			stderr,
			"pfm chat %s: %s is still working after %ds — the message "+
				"was delivered, the answer is not in yet\n",
			verb,
			name,
			int(timeout.Seconds()),
		)
		return codeAwaitTimeout
	case errors.Is(err, headless.ErrChatGone):
		if turn.Answer != "" {
			// It answered and then the seat went away. The answer is real and
			// is printed; the caller is told there will be no more.
			fmt.Fprintf(stderr, "pfm chat %s: %s answered and is now gone\n", verb, name)
			return 0
		}
		fmt.Fprintf(stderr, "pfm chat %s: %s is gone — it never answered\n", verb, name)
		return codeDeadChat
	default:
		fmt.Fprintf(stderr, "pfm chat %s: %v\n", verb, err)
		return 1
	}
}

// chatHandle is what a wait re-resolves the chat by. The socket wins: it is
// the one name minted by this process, it cannot be ambiguous, and it does not
// depend on the naming pipeline having caught up with a rename that happened a
// second ago.
func chatHandle(socket, name string) string {
	if socket != "" {
		return socket
	}
	return name
}
