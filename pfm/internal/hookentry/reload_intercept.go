package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	pfmreload "github.com/rezzminator/professor/pfm/internal/reload"
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
	// Blanking the command word keeps the parser's line:column on the prompt
	// the user typed.
	words, err := cmdparse.SplitShellWords(strings.Repeat(" ", len("/reload")) + strings.TrimPrefix(trimmed, "/reload"))
	if err != nil {
		recordReloadRefusal(trimmed, err.Error())
		return blockPrompt(stdout, reloadFailedMessage(trimmed, err.Error()))
	}
	// Past the deadline the reload may still have been scheduled, so the
	// timeout line says so and names where its worker logs.
	step("schedule reload (it may still be scheduled: its worker logs to reload-*.log in " +
		runtime.Paths.SIDDir + ")")
	var captured bytes.Buffer
	if reload(ctx, words, &captured, &captured, runtime) == 0 {
		return blockPrompt(stdout, reloadScheduledMessage(words, strings.TrimSpace(captured.String())))
	}
	recordReloadRefusal(trimmed, strings.TrimSpace(captured.String()))
	return blockPrompt(stdout, reloadFailedMessage(trimmed, captured.String()))
}

// Claude Code shows a hook's block message under its own yellow "blocked by
// hook" line and offers no other style, but it keeps an SGR colour inside the
// reason, re-opening its yellow after every newline: so each line opens its
// own colour and closes it with a full reset.
const (
	reloadStyleScheduled = "\x1b[32m"
	reloadStyleFailed    = "\x1b[31m"
	reloadStyleBold      = "\x1b[1m"
	reloadStyleMuted     = "\x1b[90m"
	reloadStyleReset     = "\x1b[0m"
)

// reloadScheduledMessage says what the user asked the reload for, in the
// settings they typed, and keeps the front's own line with its worker log.
func reloadScheduledMessage(words []string, frontLine string) string {
	asked := "same account, model and effort"
	if settings := reloadAskedSettings(words); len(settings) != 0 {
		asked = strings.Join(settings, " · ")
	}
	lines := []string{
		reloadStyleScheduled + reloadStyleBold + "↻ Reloading — " + asked + reloadStyleReset,
		reloadStyleScheduled + "  This chat restarts when the current turn ends." + reloadStyleReset,
	}
	if frontLine != "" {
		lines = append(lines, reloadStyleMuted+"  "+frontLine+reloadStyleReset)
	}
	return strings.Join(lines, "\n")
}

// reloadAskedSettings names each setting a scheduled /reload carries, in the
// order the usage line lists them; a word the front accepted but this list
// does not know is shown as typed rather than dropped.
func reloadAskedSettings(words []string) []string {
	parsed, err := pfmreload.ParseWorkerArguments(pfmreload.NormalizeArguments(words))
	if err != nil {
		return []string{strings.Join(words, " ")}
	}
	var settings []string
	add := func(value, label string) {
		if value != "" {
			settings = append(settings, label+" "+value)
		}
	}
	add(parsed.Account, "account")
	add(parsed.Model, "model")
	add(parsed.Effort, "effort")
	add(parsed.Cache, "cache")
	if parsed.New {
		settings = append(settings, "new conversation")
	}
	if parsed.Hide {
		settings = append(settings, "old conversation hidden")
	}
	if parsed.Then != "" {
		settings = append(settings, fmt.Sprintf("then %q", parsed.Then))
	}
	add(parsed.Socket, "chat")
	add(parsed.Pane, "pane")
	return settings
}

// reloadFailedMessage says the /reload did not run, the exact text the user
// typed and every line of the reason, so nothing has to be looked up.
func reloadFailedMessage(prompt, reason string) string {
	lines := []string{
		reloadStyleFailed + reloadStyleBold + "✗ Reload failed — this /reload was not scheduled" + reloadStyleReset,
		reloadStyleFailed + "  You typed: " + prompt + reloadStyleReset,
		reloadStyleFailed + "  What went wrong:" + reloadStyleReset,
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "pfm chat reload refused and printed no reason; `pfm log` holds its reload.refused record"
	}
	for _, line := range strings.Split(reason, "\n") {
		lines = append(lines, reloadStyleFailed+"    "+line+reloadStyleReset)
	}
	return strings.Join(lines, "\n")
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
