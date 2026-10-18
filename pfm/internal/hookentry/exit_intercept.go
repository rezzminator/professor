package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// KillFront is the in-process kill command front injected by runInternal.
// Its ctx carries the hook's deps.CommitGate, so the kill row and its
// finisher never split at the deadline.
type KillFront func(ctx context.Context, args []string, stdout, stderr io.Writer, runtimes ...config.Runtime) int

// ExitIntercept converts an exact e or /e prompt into the injected kill front.
func ExitIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	kill KillFront,
) int {
	// An exit word must never reach the model as a prompt, so a step past
	// promptHookDeadline blocks it (exit 2) with the timeout on stderr.
	return RunPromptHook("pfm internal exit-intercept", stdout, stderr, 2,
		func(ctx context.Context, step func(string), stdout, stderr io.Writer) int {
			return exitInterceptBody(ctx, step, stdin, stdout, stderr, runtime, kill)
		})
}

func exitInterceptBody(
	ctx context.Context,
	step func(string),
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	kill KillFront,
) int {
	step("read hook payload")
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal exit-intercept: decode hook payload: %v\n", err)
		return 0
	}
	if !isExitPrompt(payload.Prompt) {
		return 0
	}
	// Past the deadline the end may still be under way, so the timeout line
	// says so rather than reading as nothing done.
	step("end chat (it may still be ending: check `pfm chat ls` before typing it again)")
	var captured bytes.Buffer
	if kill(ctx, []string{"--self", "--exit"}, &captured, &captured, runtime) == 0 {
		return blockPromptQuietly(stdout)
	}
	fmt.Fprintf(stderr, "exit: %s", captured.String())
	return 2
}
