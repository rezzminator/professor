package reminder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	reminderCommand      = "pfm chat reminder"
	reminderTimeLayout   = "2006-01-02 15:04"
	reminderPromptRunes  = 60
	reminderShortSession = 8
	reminderSetUsage     = "usage: pfm chat reminder set --every <interval> --prompt <text> [chat]"
	reminderLsUsage      = "usage: pfm chat reminder ls [--json]"
	reminderRmUsage      = "usage: pfm chat reminder rm <id>"
)

func printReminderUsage(w io.Writer) {
	fmt.Fprintln(w, reminderSetUsage)
	fmt.Fprintln(w, "       pfm chat reminder ls [--json]")
	fmt.Fprintln(w, "       pfm chat reminder rm <id>")
	fmt.Fprintln(w, "interval: weekly, <N>d, or a Go duration like 90m, minimum 1m")
}

// RunReminderCommand dispatches `pfm chat reminder set|ls|rm`: recurring alarms stored
// in the shared state database and fired by `pfm internal reminder-fire`.
// notFound renders the caller's "no such chat" refusal and returns its exit code.
func RunReminderCommand(
	args []string,
	stdout, stderr io.Writer,
	runtime *pfmconfig.Runtime,
	notFound func(name string, stderr io.Writer) int,
) int {
	if len(args) == 0 {
		printReminderUsage(stderr)
		return 2
	}
	switch args[0] {
	case "set":
		return runReminderSet(args[1:], stdout, stderr, runtime, notFound)
	case "ls":
		return runReminderLs(args[1:], stdout, stderr, runtime)
	case "rm":
		return runReminderRm(args[1:], stdout, stderr, runtime)
	default:
		fmt.Fprintf(stderr, "%s: unknown command %q\n", reminderCommand, args[0])
		printReminderUsage(stderr)
		return 2
	}
}

// openReminderStore opens the shared state database; a degraded store is an
// error here, never an empty list.
func openReminderStore(
	ctx context.Context,
	command string,
	stderr io.Writer,
	runtime *pfmconfig.Runtime,
) (*fleetdb.Store, bool) {
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "%s: read runtime: %v\n", command, err)
		return nil, false
	}
	state := fleetdb.OpenSharedState(ctx, effective.Paths)
	if err := state.Degraded(); err != nil {
		fmt.Fprintf(stderr, "%s: open shared state: %v\n", command, err)
		if closeErr := state.Close(); closeErr != nil {
			fmt.Fprintf(stderr, "%s: close shared state: %v\n", command, closeErr)
		}
		return nil, false
	}
	return state, true
}

func closeReminderStore(state *fleetdb.Store, command string, stderr io.Writer, code int) int {
	if err := state.Close(); err != nil {
		fmt.Fprintf(stderr, "%s: close shared state: %v\n", command, err)
		if code == 0 {
			return 1
		}
	}
	return code
}

// callerIdentity is the chat running this command: runWhoami's resolution,
// else the Codex seat lookup. ok is false when the caller cannot be told. A
// test replaces it to stand inside a chat.
var callerIdentity = func(ctx context.Context, runtime *pfmconfig.Runtime) (resolve.Identity, bool) {
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
	identity := resolve.Identity{}
	if err == nil {
		identity, err = identifier.Identify(ctx)
	}
	if err != nil {
		seat, found := pfmchat.SeatIdentity(ctx, runtime)
		if !found {
			return resolve.Identity{}, false
		}
		identity = seat
	}
	return identity, identity.ID != ""
}

// reminderCaller is the chat running this command, keyed by reminderChatKey,
// then the label its row carries. ok is false when the caller cannot be told;
// keyErr says why a known caller has no fireable key, and identity.ID then
// stays the raw id the caller reported.
func reminderCaller(
	ctx context.Context,
	runtime *pfmconfig.Runtime,
) (identity resolve.Identity, label string, ok bool, keyErr error) {
	identity, ok = callerIdentity(ctx, runtime)
	if !ok {
		return identity, "", false, nil
	}
	key, keyErr := reminderChatKey(ctx, identity.Engine, identity.ID)
	if keyErr != nil {
		return identity, "", true, keyErr
	}
	identity.ID = key
	chat, found, resolveErr := pfmchat.Resolve(ctx, identity.ID, io.Discard, runtime)
	if resolveErr == nil && found {
		label = chat.Name
	}
	return identity, label, true, nil
}

// reminderChatKey is the id a reminder for this chat is stored under: the id
// a fire matches against the fleet's rows. A Codex row is keyed on its
// lineage root, while CODEX_THREAD_ID, and a self lookup built on it, may name
// any thread of the lineage — inherited, resumed or reset — so a Codex id maps
// to its root through the rollout index. An id the index does not hold is
// refused: a reminder keyed on it could never fire.
func reminderChatKey(ctx context.Context, engine, id string) (string, error) {
	if engine != string(pfmengine.Codex) {
		return id, nil
	}
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		return "", fmt.Errorf("open the chat index to map Codex thread %s: %w", id, err)
	}
	lineage, found, err := database.CodexLineage(ctx, id)
	if closeErr := database.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close the chat index: %w", closeErr))
	}
	if err != nil {
		return "", fmt.Errorf("map Codex thread %s to its chat: %w", id, err)
	}
	if !found || lineage.RootID == "" {
		return "", fmt.Errorf(
			"the chat index holds no Codex conversation with thread %s yet; retry once pfm has indexed it, or name the chat",
			id,
		)
	}
	return lineage.RootID, nil
}

func runReminderSet(
	args []string,
	stdout, stderr io.Writer,
	runtime *pfmconfig.Runtime,
	notFound func(string, io.Writer) int,
) int {
	flags := cli.NewFlagSet("reminder set", reminderSetUsage, stderr)
	every := flags.String("every", "", "recurrence: weekly, <N>d, or a Go duration like 90m, minimum 1m")
	prompt := flags.String("prompt", "", "the text typed into the chat on every fire")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		flags.Usage()
		return 2
	}
	interval, err := ParseInterval(*every)
	if err != nil {
		fmt.Fprintf(stderr, "%s set: --every: %v\n", reminderCommand, err)
		return 2
	}
	if strings.TrimSpace(*prompt) == "" {
		fmt.Fprintf(stderr, "%s set: --prompt must not be empty\n", reminderCommand)
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	caller, callerLabel, callerKnown, callerKeyErr := reminderCaller(ctx, runtime)
	entry := fleetdb.Reminder{Prompt: *prompt, Interval: interval, Created: clock.Real.Now()}
	if callerKnown {
		entry.SetByID, entry.SetByLabel = caller.ID, callerLabel
	}
	if len(positional) == 0 {
		if !callerKnown {
			fmt.Fprintf(stderr, "%s set: cannot tell which chat this is; name the chat\n", reminderCommand)
			return 1
		}
		if callerKeyErr != nil {
			fmt.Fprintf(stderr, "%s set: %v\n", reminderCommand, callerKeyErr)
			return 1
		}
		entry.SessionID, entry.Engine, entry.Label = caller.ID, caller.Engine, callerLabel
	} else {
		chat, found, err := pfmchat.Resolve(ctx, positional[0], io.Discard, runtime)
		if err != nil {
			fmt.Fprintf(stderr, "%s set: resolve chat %q: %v\n", reminderCommand, positional[0], err)
			return 1
		}
		if !found {
			return notFound(positional[0], stderr)
		}
		if chat.ID == "" {
			fmt.Fprintf(stderr, "%s set: chat %q has no session id yet\n", reminderCommand, positional[0])
			return 1
		}
		key, err := reminderChatKey(ctx, string(chat.Engine), chat.ID)
		if err != nil {
			fmt.Fprintf(stderr, "%s set: chat %q: %v\n", reminderCommand, positional[0], err)
			return 1
		}
		entry.SessionID, entry.Engine, entry.Label = key, string(chat.Engine), chat.Name
	}
	state, opened := openReminderStore(ctx, reminderCommand+" set", stderr, runtime)
	if !opened {
		return 1
	}
	id, err := state.CreateReminder(ctx, entry)
	if err != nil {
		fmt.Fprintf(stderr, "%s set: %v\n", reminderCommand, err)
		return closeReminderStore(state, reminderCommand+" set", stderr, 1)
	}
	fmt.Fprintln(stdout, id)
	return closeReminderStore(state, reminderCommand+" set", stderr, 0)
}

func runReminderLs(args []string, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	flags := cli.NewFlagSet("reminder ls", reminderLsUsage, stderr)
	asJSON := flags.Bool("json", false, "print the reminders as a JSON array")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	state, opened := openReminderStore(ctx, reminderCommand+" ls", stderr, runtime)
	if !opened {
		return 1
	}
	reminders, err := state.Reminders(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s ls: %v\n", reminderCommand, err)
		return closeReminderStore(state, reminderCommand+" ls", stderr, 1)
	}
	if reminders == nil {
		reminders = []fleetdb.Reminder{}
	}
	if *asJSON {
		encoded, err := json.Marshal(reminders)
		if err != nil {
			fmt.Fprintf(stderr, "%s ls: encode JSON: %v\n", reminderCommand, err)
			return closeReminderStore(state, reminderCommand+" ls", stderr, 1)
		}
		fmt.Fprintf(stdout, "%s\n", encoded)
	} else if err := writeReminderTable(stdout, reminders); err != nil {
		fmt.Fprintf(stderr, "%s ls: %v\n", reminderCommand, err)
		return closeReminderStore(state, reminderCommand+" ls", stderr, 1)
	}
	return closeReminderStore(state, reminderCommand+" ls", stderr, 0)
}

func reminderTime(moment time.Time) string {
	if moment.IsZero() {
		return "-"
	}
	return moment.Local().Format(reminderTimeLayout)
}

func reminderChatColumn(r *fleetdb.Reminder) string {
	if r.Label != "" {
		return r.Label
	}
	if len(r.SessionID) > reminderShortSession {
		return r.SessionID[:reminderShortSession]
	}
	return r.SessionID
}

func reminderPromptColumn(prompt string) string {
	flat := strings.Join(strings.Fields(prompt), " ")
	runes := []rune(flat)
	if len(runes) <= reminderPromptRunes {
		return flat
	}
	return string(runes[:reminderPromptRunes-1]) + "…"
}

// writeReminderTable prints the aligned table; a reminder whose last fire
// failed carries one indented line saying so beneath its row.
func writeReminderTable(out io.Writer, reminders []fleetdb.Reminder) error {
	if len(reminders) == 0 {
		_, err := fmt.Fprintln(out, "no reminders")
		return err
	}
	var table bytes.Buffer
	writer := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tCHAT\tEVERY\tNEXT\tLAST\tUNSEEN\tPROMPT")
	for index := range reminders {
		r := &reminders[index]
		unseen := "-"
		if r.Unseen {
			unseen = "yes"
		}
		fmt.Fprintf(
			writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, reminderChatColumn(r), FormatInterval(r.Interval),
			reminderTime(r.NextFire), reminderTime(r.LastFired), unseen, reminderPromptColumn(r.Prompt),
		)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("render reminder table: %w", err)
	}
	lines := strings.Split(strings.TrimRight(table.String(), "\n"), "\n")
	var rendered strings.Builder
	rendered.WriteString(lines[0] + "\n")
	for index := range reminders {
		r := &reminders[index]
		rendered.WriteString(lines[index+1] + "\n")
		if r.LastError != "" {
			fmt.Fprintf(&rendered, "  last fire failed %s: %s\n", reminderTime(r.LastErrorAt), r.LastError)
		}
	}
	if _, err := io.WriteString(out, rendered.String()); err != nil {
		return fmt.Errorf("write reminder table: %w", err)
	}
	return nil
}

func runReminderRm(args []string, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, reminderRmUsage)
		return 2
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintf(stderr, "%s rm: %q is not a reminder id\n", reminderCommand, args[0])
		fmt.Fprintln(stderr, reminderRmUsage)
		return 2
	}
	ctx := context.Background()
	state, opened := openReminderStore(ctx, reminderCommand+" rm", stderr, runtime)
	if !opened {
		return 1
	}
	removed, err := state.RemoveReminder(ctx, id)
	if err != nil {
		fmt.Fprintf(stderr, "%s rm: %v\n", reminderCommand, err)
		return closeReminderStore(state, reminderCommand+" rm", stderr, 1)
	}
	if !removed {
		fmt.Fprintf(stderr, "pfm chat reminder rm: no reminder %d\n", id)
		return closeReminderStore(state, reminderCommand+" rm", stderr, 1)
	}
	fmt.Fprintf(stdout, "removed reminder %d\n", id)
	return closeReminderStore(state, reminderCommand+" rm", stderr, 0)
}
