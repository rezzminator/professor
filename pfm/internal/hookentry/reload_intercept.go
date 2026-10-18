package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// ReloadFront is the in-process reload command front injected by runInternal.
// Its ctx carries the hook's deps.CommitGate, so the reload worker start and
// its record never split at the deadline.
type ReloadFront func(ctx context.Context, args []string, stdout, stderr io.Writer, runtime config.Runtime) int

// ReloadIntercept converts a /reload prompt into the injected reload front.
func ReloadIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
) int {
	// A /reload must never reach the model as a prompt, so a step past
	// promptHookDeadline blocks it (exit 2) with the timeout on stderr.
	return RunPromptHook("pfm internal reload-intercept", stdout, stderr, 2,
		func(ctx context.Context, step func(string), stdout, stderr io.Writer) int {
			return reloadInterceptBody(ctx, step, stdin, stdout, stderr, runtime, reload)
		})
}

func reloadInterceptBody(
	ctx context.Context,
	step func(string),
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
) int {
	step("read hook payload")
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal reload-intercept: decode hook payload: %v\n", err)
		return 0
	}
	if !isReloadPrompt(payload.Prompt) {
		return 0
	}
	trimmed := strings.TrimSpace(payload.Prompt)
	words, err := action.SplitShellWords(strings.TrimPrefix(trimmed, "/reload"))
	if err != nil {
		recordReloadRefusal(trimmed, err.Error())
		fmt.Fprintf(stderr, "reload: %v\n", err)
		return 2
	}
	// Past the deadline the reload may still have been scheduled, so the
	// timeout line says so and names where its worker logs.
	step("schedule reload (it may still be scheduled: its worker logs to reload-*.log in " +
		runtime.Paths.SIDDir + ")")
	var captured bytes.Buffer
	if reload(ctx, words, &captured, &captured, runtime) == 0 {
		reason := fmt.Sprintf(
			"reload scheduled — this chat reboots when the current turn ends (%s)",
			strings.TrimSpace(captured.String()),
		)
		return blockPrompt(stdout, reason)
	}
	recordReloadRefusal(trimmed, strings.TrimSpace(captured.String()))
	fmt.Fprintf(stderr, "reload: %s", captured.String())
	return 2
}

// recordReloadRefusal writes the refusal's reason to the activity log. The
// hook's stderr reaches only the user's screen, so without this record a
// refused /reload left nothing a later reader could diagnose it from.
func recordReloadRefusal(prompt, reason string) {
	ctx := obs.Component(context.Background(), "hooks")
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelWarn, "reload.refused",
		slog.String("prompt", prompt),
		slog.String(obs.FieldErr, reason),
	)
}
