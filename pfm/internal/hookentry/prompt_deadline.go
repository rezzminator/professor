package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// promptHookDeadline bounds every pfm UserPromptSubmit hook. Claude waits for
// a prompt hook up to its own hook timeout, so a hook that blocks (a tmux call
// against a socket that is not tmux, a held lock, a slow read) would hold the
// user's prompt that long.
var promptHookDeadline = 5 * time.Second

// promptHookCommitGrace bounds how long past promptHookDeadline a hook waits
// for a commit span its body started in time (the reload worker's start and
// record, a kill row's finisher launch: milliseconds each), so a span stuck on
// something it cannot cancel holds the prompt at most this much longer.
var promptHookCommitGrace = 2 * time.Second

// PromptHookBody is one UserPromptSubmit hook's work. It names the step in
// hand through step, so a timeout says what it was waiting on, and writes only
// to the stdout and stderr it is given.
type PromptHookBody func(ctx context.Context, step func(string), stdout, stderr io.Writer) int

// RunPromptHook runs body under a promptHookDeadline context and answers by
// the deadline whatever the body blocks on, or at most promptHookCommitGrace
// later while a commit span the body began in time ends. A body that returns in time has
// its output copied out and its exit code returned. Past the deadline only
// "{name}: {step} timed out after {deadline}" reaches stderr, stdout stays
// empty and failCode is returned: 0 lets the prompt through, 2 blocks it.
// The body's late writes land in its buffers, never on the hook's output.
// The body's ctx carries a deps.CommitGate: a commit span the body opened
// before the deadline completes before the hook answers, and none opens
// after it, so the process never exits halfway through one — unless the span
// outlasts promptHookCommitGrace too, when the hook answers anyway and the
// timeout line says the step may be cut short.
func RunPromptHook(name string, stdout, stderr io.Writer, failCode int, body PromptHookBody) int {
	deadline := promptHookDeadline
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	commits := &promptHookCommits{}
	ctx = deps.WithCommitGate(ctx, commits.begin)
	var mu sync.Mutex
	current := "start"
	step := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		current = name
	}
	bodyStdout := &promptHookBuffer{mu: &mu}
	bodyStderr := &promptHookBuffer{mu: &mu}
	result := make(chan int, 1)
	go func() { result <- body(ctx, step, bodyStdout, bodyStderr) }()
	finish := func(code int) int {
		mu.Lock()
		defer mu.Unlock()
		// stderr is the hook's only log, so a failed write to it has nowhere
		// to be reported.
		_, _ = stderr.Write(bodyStderr.buf.Bytes())
		if _, err := stdout.Write(bodyStdout.buf.Bytes()); err != nil {
			fmt.Fprintf(stderr, "%s: write hook output: %v\n", name, err)
			return failCode
		}
		return code
	}
	select {
	case code := <-result:
		return finish(code)
	case <-ctx.Done():
		waited, finished := commits.closeAndWait(promptHookCommitGrace)
		select {
		case code := <-result:
			return finish(code)
		default:
		}
		mu.Lock()
		inHand := current
		mu.Unlock()
		switch {
		case !waited:
			fmt.Fprintf(stderr, "%s: %s timed out after %s\n", name, inHand, deadline)
		case finished:
			fmt.Fprintf(stderr, "%s: %s timed out after %s, then waited up to %s for the step it had started\n",
				name, inHand, deadline, promptHookCommitGrace)
		default:
			fmt.Fprintf(stderr, "%s: %s timed out after %s, and the step it had started was still running %s later; "+
				"it may be cut short\n", name, inHand, deadline, promptHookCommitGrace)
		}
		return failCode
	}
}

// UsageHook is the usage UserPromptSubmit hook: it names the account's usage
// on stdout and fails open, so an evaluation that errors or outlives the
// prompt-hook deadline lets the prompt through. Its log is stderr, while
// stdout stays the prompt text alone.
func UsageHook(stdout, stderr io.Writer, options usagehook.Options) int {
	return usageHookWith(stdout, stderr, options, usagehook.Evaluate)
}

func usageHookWith(
	stdout, stderr io.Writer,
	options usagehook.Options,
	evaluate func(context.Context, usagehook.Options) (string, error),
) int {
	return RunPromptHook("pfm usage-hook", stdout, stderr, 0,
		func(ctx context.Context, step func(string), stdout, stderr io.Writer) int {
			step("evaluate usage")
			options.Log = stderr
			message, err := evaluate(ctx, options)
			if err != nil {
				fmt.Fprintf(stderr, "pfm usage-hook: evaluate (fail-open): %v\n", err)
				return 0
			}
			if _, err := io.WriteString(stdout, message); err != nil {
				fmt.Fprintf(stderr, "pfm usage-hook: write output (fail-open): %v\n", err)
			}
			return 0
		})
}

// promptHookBuffer is a body's stdout or stderr, guarded by the hook's mutex
// so a write after the deadline never races the copy-out.
type promptHookBuffer struct {
	mu  *sync.Mutex
	buf bytes.Buffer
}

func (b *promptHookBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// promptHookCommits is a prompt hook's commit gate: begin opens a span until
// the deadline closes the gate; closeAndWait closes it and waits a bounded
// time for every open span to end.
type promptHookCommits struct {
	mu     sync.Mutex
	closed bool
	active int
	open   sync.WaitGroup
}

func (c *promptHookCommits) begin() (func(), bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false
	}
	c.active++
	c.open.Add(1)
	return sync.OnceFunc(func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		c.open.Done()
	}), true
}

// closeAndWait closes the gate and waits at most grace for the open spans:
// waited reports that one was open, finished that every one ended in time.
func (c *promptHookCommits) closeAndWait(grace time.Duration) (waited, finished bool) {
	c.mu.Lock()
	c.closed = true
	waited = c.active > 0
	c.mu.Unlock()
	if !waited {
		return false, true
	}
	done := make(chan struct{})
	go func() {
		c.open.Wait()
		close(done)
	}()
	wait, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	select {
	case <-done:
		return true, true
	case <-wait.Done():
		return true, false
	}
}
