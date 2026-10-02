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
type ReloadFront func(args []string, stdout, stderr io.Writer, runtime config.Runtime) int

// ReloadIntercept converts a /reload prompt into the injected reload front.
func ReloadIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
) int {
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal reload-intercept: decode hook payload: %v\n", err)
		return 0
	}
	trimmed := strings.TrimSpace(payload.Prompt)
	if trimmed != "/reload" && !strings.HasPrefix(trimmed, "/reload ") {
		return 0
	}
	words, err := action.SplitShellWords(strings.TrimPrefix(trimmed, "/reload"))
	if err != nil {
		recordReloadRefusal(trimmed, err.Error())
		fmt.Fprintf(stderr, "reload: %v\n", err)
		return 2
	}
	var captured bytes.Buffer
	if reload(words, &captured, &captured, runtime) == 0 {
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
