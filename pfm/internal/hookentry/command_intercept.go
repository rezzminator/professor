package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	pfmreload "github.com/rezzminator/professor/pfm/internal/reload"
)

// The typed commands command-intercept runs instead of the model.
const (
	reloadCommand = "/pfm:reload"
	spaceCommand  = "/pfm:space"
)

// ReloadFront is the in-process reload command front injected by runInternal.
// Its ctx carries the hook's deps.CommitGate, so the reload worker start and
// its record never split at the deadline.
type ReloadFront func(ctx context.Context, args []string, stdout, stderr io.Writer, runtime config.Runtime) int

// SpaceFront is the `pfm chat space` door (chat.Space) injected by
// runInternal: it records the calling chat's space and returns its one line.
type SpaceFront func(ctx context.Context, runtime *config.Runtime, name string) (string, error)

// CommandIntercept runs a typed /pfm:reload or /pfm:space through its front
// and blocks the prompt with the front's answer, so neither starts a turn.
func CommandIntercept(
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
	space SpaceFront,
) int {
	// A typed command must never reach the model as a prompt, so a step past
	// promptHookDeadline blocks it (exit 2) with the timeout on stderr.
	return RunPromptHook("pfm internal command-intercept", stdout, stderr, 2,
		func(ctx context.Context, step func(string), stdout, stderr io.Writer) int {
			return commandInterceptBody(ctx, step, stdin, stdout, stderr, runtime, reload, space)
		})
}

func commandInterceptBody(
	ctx context.Context,
	step func(string),
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
	space SpaceFront,
) int {
	step("read hook payload")
	var payload struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		fmt.Fprintf(stderr, "pfm internal command-intercept: decode hook payload: %v\n", err)
		return 0
	}
	trimmed := strings.TrimSpace(payload.Prompt)
	switch {
	case isCommandPrompt(trimmed, reloadCommand):
		return reloadInterceptBody(ctx, step, trimmed, stdout, runtime, reload)
	case isCommandPrompt(trimmed, spaceCommand):
		return spaceInterceptBody(ctx, step, trimmed, stdout, runtime, space)
	}
	return 0
}

// commandWords splits what follows the command word; blanking the command
// word keeps the parser's line:column on the prompt the user typed.
func commandWords(trimmed, command string) ([]string, error) {
	return cmdparse.SplitShellWords(strings.Repeat(" ", len(command)) + strings.TrimPrefix(trimmed, command))
}

func reloadInterceptBody(
	ctx context.Context,
	step func(string),
	trimmed string,
	stdout io.Writer,
	runtime config.Runtime,
	reload ReloadFront,
) int {
	words, err := commandWords(trimmed, reloadCommand)
	if err != nil {
		recordCommandRefusal("reload.refused", trimmed, err.Error())
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
	recordCommandRefusal("reload.refused", trimmed, strings.TrimSpace(captured.String()))
	return blockPrompt(stdout, reloadFailedMessage(trimmed, captured.String()))
}

// spaceInterceptBody takes the words as `pfm chat space` does: none clears
// the space, one names it, anything else is its usage line — a word opening
// with "-" too, which the verb's flag parser refuses rather than records.
func spaceInterceptBody(
	ctx context.Context,
	step func(string),
	trimmed string,
	stdout io.Writer,
	runtime config.Runtime,
	space SpaceFront,
) int {
	words, err := commandWords(trimmed, spaceCommand)
	if err == nil && (len(words) > 1 ||
		len(words) == 1 && (strings.TrimSpace(words[0]) == "" || strings.HasPrefix(words[0], "-"))) {
		err = fmt.Errorf("%s", pfmchat.SpaceUsage)
	}
	if err != nil {
		recordCommandRefusal("space.refused", trimmed, err.Error())
		return blockPrompt(stdout, spaceFailedMessage(trimmed, err.Error()))
	}
	name := strings.Join(words, "") // no word clears the space, one names it
	// Past the deadline the space may still have been recorded.
	step("set the chat's space (it may still be set: check `pfm chat ls`)")
	line, err := space(ctx, &runtime, name)
	if err != nil {
		recordCommandRefusal("space.refused", trimmed, err.Error())
		return blockPrompt(stdout, spaceFailedMessage(trimmed, err.Error()))
	}
	return blockPrompt(stdout, commandStyleScheduled+commandStyleBold+line+commandStyleReset)
}

// Claude Code shows a hook's block message under its own yellow "blocked by
// hook" line and offers no other style, but it keeps an SGR colour inside the
// reason, re-opening its yellow after every newline: so each line opens its
// own colour and closes it with a full reset.
const (
	commandStyleScheduled = "\x1b[32m"
	commandStyleFailed    = "\x1b[31m"
	commandStyleBold      = "\x1b[1m"
	commandStyleMuted     = "\x1b[90m"
	commandStyleReset     = "\x1b[0m"
)

// reloadScheduledMessage says what the user asked the reload for, in the
// settings they typed, and keeps the front's own line with its worker log.
func reloadScheduledMessage(words []string, frontLine string) string {
	asked := "same account, model and effort"
	if settings := reloadAskedSettings(words); len(settings) != 0 {
		asked = strings.Join(settings, " · ")
	}
	lines := []string{
		commandStyleScheduled + commandStyleBold + "↻ Reloading — " + asked + commandStyleReset,
		commandStyleScheduled + "  This chat restarts when the current turn ends." + commandStyleReset,
	}
	if frontLine != "" {
		lines = append(lines, commandStyleMuted+"  "+frontLine+commandStyleReset)
	}
	return strings.Join(lines, "\n")
}

// reloadAskedSettings names each setting a scheduled /pfm:reload carries, in
// the order the usage line lists them; a word the front accepted but this list
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

// reloadFailedMessage says the /pfm:reload did not run.
func reloadFailedMessage(prompt, reason string) string {
	return commandFailedMessage("✗ Reload failed — this "+reloadCommand+" was not scheduled", prompt, reason,
		"pfm chat reload refused and printed no reason; `pfm log` holds its reload.refused record")
}

// spaceFailedMessage says the /pfm:space moved nothing.
func spaceFailedMessage(prompt, reason string) string {
	return commandFailedMessage("✗ Space failed — this "+spaceCommand+" did not move the chat", prompt, reason,
		"pfm chat space refused and printed no reason; `pfm log` holds its space.refused record")
}

// commandFailedMessage says a typed command did not run: the exact text the
// user typed and every line of the reason, so nothing has to be looked up.
func commandFailedMessage(header, prompt, reason, silent string) string {
	lines := []string{
		commandStyleFailed + commandStyleBold + header + commandStyleReset,
		commandStyleFailed + "  You typed: " + prompt + commandStyleReset,
		commandStyleFailed + "  What went wrong:" + commandStyleReset,
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = silent
	}
	for _, line := range strings.Split(reason, "\n") {
		lines = append(lines, commandStyleFailed+"    "+line+commandStyleReset)
	}
	return strings.Join(lines, "\n")
}

// recordCommandRefusal logs a refused typed command under event: the hook's
// output reaches only the user's screen, so this record is what a reader finds.
func recordCommandRefusal(event, prompt, reason string) {
	ctx := obs.Component(context.Background(), "hooks")
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelWarn, event,
		slog.String("prompt", prompt),
		slog.String(obs.FieldErr, reason),
	)
}
