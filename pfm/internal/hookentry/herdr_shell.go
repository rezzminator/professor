package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// The plain-shell reporter: the shim's Herdr hook hands it a long command a
// shell in a Herdr pane finished, and the next command's start, so a shell or
// server pane shows that run in its title until the next command.
const (
	herdrShellCommand = "pfm internal herdr-shell"
	herdrShellDone    = "done"
	herdrShellRelease = "release"
	// herdrShellSource is the source the shell's state lives under in Herdr,
	// apart from the chat reporter's; herdrShellLog names its error log.
	herdrShellSource = "pfm-shell"
	herdrShellAgent  = "shell"
	herdrShellLog    = "pfm-shell"
)

// herdrShellOptions are one herdr-shell run: the verb, the shell's pid, the
// shell's clock when its hook ran (at, $EPOCHREALTIME; 0 when not given), and
// for done the command, its exit status and its duration in seconds.
type herdrShellOptions struct {
	verb, command      string
	pid, exit, seconds int
	at                 float64
}

// errNoShellPane is a run that found no Herdr pane whose shell, or one of
// whose foreground processes, is the shell.
type errNoShellPane struct{ pid int }

func (missing errNoShellPane) Error() string {
	return fmt.Sprintf("no Herdr pane runs shell %d", missing.pid)
}

// HerdrShell is `pfm internal herdr-shell done --pid P [--at T] --command C
// --exit RC --seconds S | release --pid P [--at T]`. done reports working, then
// idle with "{command} exit {RC} · {S}s", and sets the same text as the pane's
// title (Herdr drops the agent report within a second on a bare shell and keeps
// the title); release releases the agent and clears the title, and a done whose
// T is older than a release's reports nothing. A pane's latest done holds its
// title against any shell: a release started before it leaves the title up and
// an older done reports nothing over it. The pane is the one running
// shell P, found by that identity because a Herdr pane move gives the pane a
// new id. The shim runs it in the background with its output discarded, so
// every failure is a line in the pfm-shell error log `pfm doctor` reads; a
// release that finds no pane running the shell has nothing left to release.
func HerdrShell(args []string, stderr io.Writer) int {
	return reportShellRun(args, stderr, realHerdrDeps())
}

func reportShellRun(args []string, stderr io.Writer, deps herdrDeps) int {
	options, err := parseHerdrShellOptions(args)
	if err != nil {
		logHerdrShell(stderr, deps, err.Error())
		return 2
	}
	if deps.getenv(herdr.EnvMarker) != "1" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), herdr.ReportDeadline)
	defer cancel()
	logRun := func(err error) {
		logHerdrShell(
			stderr, deps,
			fmt.Sprintf("%s %s (shell %d): %v", herdrShellCommand, options.verb, options.pid, err),
		)
	}
	if err := reportShell(ctx, deps, options, logRun); err != nil {
		logRun(err)
	}
	return 0
}

func parseHerdrShellOptions(args []string) (herdrShellOptions, error) {
	var options herdrShellOptions
	if len(args) != 0 {
		options.verb = args[0]
	}
	if options.verb != herdrShellDone && options.verb != herdrShellRelease {
		return options, fmt.Errorf(
			"%s: verb %q, want %s or %s", herdrShellCommand, options.verb, herdrShellDone, herdrShellRelease,
		)
	}
	flags := flag.NewFlagSet(herdrShellCommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.IntVar(&options.pid, "pid", 0, "the shell's pid")
	flags.Float64Var(&options.at, "at", 0, "the shell's clock when its hook ran")
	if options.verb == herdrShellDone {
		flags.StringVar(&options.command, "command", "", "the command line")
		flags.IntVar(&options.exit, "exit", 0, "its exit status")
		flags.IntVar(&options.seconds, "seconds", 0, "its duration")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return options, fmt.Errorf("%s %s: %w", herdrShellCommand, options.verb, err)
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("%s %s: unexpected arguments %q", herdrShellCommand, options.verb, flags.Args())
	}
	set := map[string]bool{}
	flags.Visit(func(given *flag.Flag) { set[given.Name] = true })
	if set["at"] && (!(options.at > 0) || math.IsInf(options.at, 1)) {
		return options, fmt.Errorf("%s %s: --at %v is no time", herdrShellCommand, options.verb, options.at)
	}
	if options.verb == herdrShellRelease && (!set["pid"] || options.pid <= 0) {
		return options, fmt.Errorf("%s release: --pid is required", herdrShellCommand)
	}
	if options.verb == herdrShellDone &&
		(!set["pid"] || options.pid <= 0 || options.command == "" || !set["exit"] || !set["seconds"]) {
		return options, fmt.Errorf(
			"%s done: --pid, --command, --exit and --seconds are all required", herdrShellCommand,
		)
	}
	return options, nil
}

// logHerdrShell writes one error line to stderr and appends it to the
// pfm-shell error log; a log that cannot be written is itself written to
// stderr.
func logHerdrShell(stderr io.Writer, deps herdrDeps, message string) {
	fmt.Fprintln(stderr, message)
	if deps.logError == nil {
		return
	}
	if err := deps.logError(herdrShellLog, deps.now(), message); err != nil {
		fmt.Fprintf(stderr, "%s: log error: %v\n", herdrShellCommand, err)
	}
}

// reportShell runs one verb; warn takes a failure the run reports past.
func reportShell(ctx context.Context, deps herdrDeps, options herdrShellOptions, warn func(error)) error {
	socket := deps.getenv(herdr.EnvSocket)
	if socket == "" {
		return fmt.Errorf("no %s in the shell's environment", herdr.EnvSocket)
	}
	unlock, err := (&herdrReporter{deps: deps}).lock(ctx, fmt.Sprintf("%s-%d", herdrShellSource, options.pid))
	if err != nil {
		return err
	}
	defer unlock()
	superseded, err := shellSuperseded(deps.lockDir, options)
	if err != nil || superseded {
		return err
	}
	var last uint64
	next := func() uint64 {
		seq := uint64(deps.now().UnixNano())
		if seq <= last {
			seq = last + 1
		}
		last = seq
		return seq
	}
	record := filepath.Join(deps.lockDir, fmt.Sprintf("%s-%d.pane", herdrShellSource, options.pid))
	pane, nested, err := shellPane(ctx, socket, options.pid, deps.getenv(herdr.EnvPane), next)
	if options.verb == herdrShellRelease {
		var missing errNoShellPane
		if errors.As(err, &missing) {
			pane, err = recordedShellPane(ctx, socket, record, next)
		}
		if err != nil {
			return err
		}
		if pane == "" {
			return removeShellRecord(record)
		}
		unlockPane, err := lockShellPane(ctx, deps, pane)
		if err != nil {
			return err
		}
		defer unlockPane()
		titled, err := shellTitleLater(deps.lockDir, pane, options.at, warn)
		if err != nil {
			return err
		}
		line, err := herdr.Release{Pane: pane, Source: herdrShellSource, Agent: herdrShellAgent, Seq: next()}.Line()
		if err != nil {
			return err
		}
		lines := [][]byte{line}
		if !titled {
			clearTitle, err := herdr.Metadata{
				Pane:       pane,
				Source:     herdrShellSource,
				ClearTitle: true,
				Seq:        next(),
			}.Line()
			if err != nil {
				return err
			}
			lines = append(lines, clearTitle)
		}
		// A failed release keeps the record, so the next release still finds
		// the pane.
		if err := releaseSettled(herdr.Send(ctx, socket, lines...)); err != nil {
			return err
		}
		return removeShellRecord(record)
	}
	if err != nil {
		return err
	}
	unlockPane, err := lockShellPane(ctx, deps, pane)
	if err != nil {
		return err
	}
	defer unlockPane()
	if later, err := shellTitleLater(deps.lockDir, pane, options.at, warn); err != nil || later {
		return err
	}
	command := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(options.command)
	working, err := herdr.Report{
		Pane: pane, Source: herdrShellSource, Agent: herdrShellAgent, State: herdr.StateWorking, Seq: next(),
	}.Line()
	if err != nil {
		return err
	}
	idle, err := herdr.Report{
		Pane: pane, Source: herdrShellSource, Agent: herdrShellAgent, State: herdr.StateIdle,
		Message: shellDoneMessage(command, options.exit, options.seconds, herdr.MaxMessageRunes), Seq: next(),
	}.Line()
	if err != nil {
		return err
	}
	title, err := herdr.Metadata{
		Pane: pane, Source: herdrShellSource, Seq: next(),
		Title: shellDoneMessage(command, options.exit, options.seconds, herdr.MaxTitleRunes),
	}.Line()
	if err != nil {
		return err
	}
	// Herdr shows a lone idle report on a pane with no agent as nothing, and
	// drops any report within a second once the bare shell holds the
	// foreground again: working then idle mark the run finished, and the
	// title, which Herdr keeps on a bare shell, carries it to the next command.
	// The record comes first and outlives a send error: a send that timed out
	// may have reached Herdr, and an older release must never erase a title
	// that may be showing; a refused one leaves at worst an older title up.
	if err := recordShellTitle(deps.lockDir, pane, options.at); err != nil {
		return err
	}
	if err := herdr.Send(ctx, socket, working, idle, title); err != nil {
		return err
	}
	if !nested {
		return nil
	}
	return recordShellPane(ctx, socket, record, pane, next)
}

// recordShellPane keeps the terminal of the pane a nested shell's done went
// to: the next command takes the foreground from the nested shell, so its
// release finds the shell in no pane and goes to this terminal's pane instead.
func recordShellPane(ctx context.Context, socket, record, pane string, next func() uint64) error {
	panes, err := listShellPanes(ctx, socket, next)
	if err != nil {
		return fmt.Errorf("record the pane of a nested shell: %w", err)
	}
	for _, listed := range panes {
		if listed.PaneID != pane {
			continue
		}
		data, err := json.Marshal(listed)
		if err != nil {
			return fmt.Errorf("record pane %s of a nested shell: %w", pane, err)
		}
		if err := os.WriteFile(record, data, 0o600); err != nil {
			return fmt.Errorf("record pane %s of a nested shell: %w", pane, err)
		}
		return nil
	}
	return fmt.Errorf("record pane %s of a nested shell: pane.list no longer contains the canonical pane", pane)
}

// recordedShellPane is the current pane of the terminal a nested shell's done
// recorded, found by terminal id because a pane move changes the pane id;
// with no record, or a terminal in no pane, there is nothing to release ("").
func recordedShellPane(ctx context.Context, socket, record string, next func() uint64) (string, error) {
	data, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the recorded pane of a nested shell: %w", err)
	}
	var recorded herdr.Pane
	if err := json.Unmarshal(data, &recorded); err != nil || recorded.TerminalID == "" {
		return "", fmt.Errorf("read the recorded pane of a nested shell from %s (%q): %v", record, data, err)
	}
	panes, err := listShellPanes(ctx, socket, next)
	if err != nil {
		return "", err
	}
	for _, pane := range panes {
		if pane.TerminalID == recorded.TerminalID {
			return pane.PaneID, nil
		}
	}
	return "", nil
}

// removeShellRecord drops a nested shell's recorded pane once its release ran.
func removeShellRecord(record string) error {
	if err := os.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the recorded pane of a nested shell: %w", err)
	}
	return nil
}

func listShellPanes(ctx context.Context, socket string, next func() uint64) ([]herdr.Pane, error) {
	line, err := herdr.PaneList{Seq: next()}.Line()
	if err != nil {
		return nil, err
	}
	results, err := herdr.Call(ctx, socket, line)
	if err != nil {
		return nil, err
	}
	return herdr.ParsePanes(results[0])
}

// shellDoneMessage is "{command} exit {rc} · {s}s" within limit runes (the
// report's herdr.MaxMessageRunes, the title's herdr.MaxTitleRunes): a long
// command line is cut, never the exit status and duration that tell a failed
// run from a good one.
func shellDoneMessage(command string, exit, seconds, limit int) string {
	suffix := fmt.Sprintf(" exit %d · %ds", exit, seconds)
	room := limit - utf8.RuneCountInString(suffix)
	if runes := []rune(command); len(runes) > room {
		command = string(runes[:room-1]) + "…"
	}
	return command + suffix
}

// shellSuperseded keeps a done from outliving the next command's release: the
// shell starts the two in order as background runs, and nothing else orders
// them. Under the shell's lock, a release records the shell clock it was
// started at, and a done started before a recorded release is superseded and
// reports nothing. An older shell that had the same pid recorded an earlier
// time, so its record supersedes nothing; a run without --at is never ordered.
func shellSuperseded(lockDir string, options herdrShellOptions) (bool, error) {
	if options.at == 0 {
		return false, nil
	}
	path := filepath.Join(lockDir, fmt.Sprintf("%s-%d.released", herdrShellSource, options.pid))
	if options.verb == herdrShellRelease {
		if err := os.WriteFile(path, []byte(strconv.FormatFloat(options.at, 'f', -1, 64)), 0o600); err != nil {
			return false, fmt.Errorf("record the release of shell %d: %w", options.pid, err)
		}
		return false, nil
	}
	recorded, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the last release of shell %d: %w", options.pid, err)
	}
	released, err := strconv.ParseFloat(strings.TrimSpace(string(recorded)), 64)
	if err != nil {
		return false, fmt.Errorf("read the last release of shell %d from %s (%q): %w", options.pid, path, recorded, err)
	}
	return released > options.at, nil
}

// lockShellPane orders the shells that report to one pane: a nested shell and
// its parent hold different shell locks, and a title carries no seq, so the
// last title request Herdr gets is the one the pane shows. The shell's lock is
// always taken first.
func lockShellPane(ctx context.Context, deps herdrDeps, pane string) (func(), error) {
	return (&herdrReporter{deps: deps}).lock(ctx, fmt.Sprintf("%s-pane-%s", herdrShellSource, pane))
}

// shellTitleLater says a done started after at, from any shell, holds the
// pane's title: a release started earlier leaves it up (a nested shell left
// by Ctrl-D releases from its exit hook while its parent reports the nested
// run done), and an earlier done reports nothing over it. Under the pane's
// lock; a run without --at is never ordered. A record holding no shell clock
// (a write cut short, a value no shell gives) would order every later run
// against nothing for good: it is a warning, dropped, and the run goes on
// unordered.
func shellTitleLater(lockDir, pane string, at float64, warn func(error)) (bool, error) {
	if at == 0 {
		return false, nil
	}
	path := shellTitlePath(lockDir, pane)
	recorded, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the last done title of pane %s: %w", pane, err)
	}
	titled, err := strconv.ParseFloat(strings.TrimSpace(string(recorded)), 64)
	if err == nil && !(titled > 0 && !math.IsInf(titled, 1)) {
		err = errors.New("no shell clock")
	}
	if err != nil {
		warn(errors.Join(
			fmt.Errorf("drop the unreadable last done title of pane %s at %s (%q): %w", pane, path, recorded, err),
			os.Remove(path),
		))
		return false, nil
	}
	return titled > at, nil
}

// recordShellTitle records the shell clock of the done whose title the pane
// shows or may show, under the pane's lock, before the title is sent, in one
// rename, so a write cut short never
// leaves a record holding no clock.
func recordShellTitle(lockDir, pane string, at float64) error {
	if at == 0 {
		return nil
	}
	path := shellTitlePath(lockDir, pane)
	if err := atomicfile.Write(path, []byte(strconv.FormatFloat(at, 'f', -1, 64)), 0o600); err != nil {
		return fmt.Errorf("record the done title of pane %s at %s: %w", pane, path, err)
	}
	return nil
}

func shellTitlePath(lockDir, pane string) string {
	return filepath.Join(lockDir, fmt.Sprintf("%s-pane-%s.title", herdrShellSource, pane))
}

// shellPane is the pane running the shell pid: the pane the shell was born in
// (born) when it still runs it, else the first listed pane that does; nested
// says pid is one of the pane's foreground processes, not its own shell. A
// pane that closed between the listing and its question is skipped.
func shellPane(ctx context.Context, socket string, pid int, born string, next func() uint64) (string, bool, error) {
	if born != "" {
		canonical, nested, err := paneRunsShell(ctx, socket, born, pid, next())
		if err != nil && !isHerdrNotFound(err) {
			return "", false, err
		}
		if canonical != "" {
			return canonical, nested, nil
		}
	}
	panes, err := listShellPanes(ctx, socket, next)
	if err != nil {
		return "", false, err
	}
	for _, pane := range panes {
		if pane.PaneID == born {
			continue
		}
		canonical, nested, err := paneRunsShell(ctx, socket, pane.PaneID, pid, next())
		if err != nil && !isHerdrNotFound(err) {
			return "", false, err
		}
		if canonical != "" {
			return canonical, nested, nil
		}
	}
	return "", false, errNoShellPane{pid: pid}
}

// paneRunsShell returns the producer's canonical pane id when its own shell,
// or one of its foreground processes (nested true), is pid. A moved pane's
// inherited id may be an alias that pane.list never emits.
func paneRunsShell(ctx context.Context, socket, pane string, pid int, seq uint64) (string, bool, error) {
	line, err := herdr.ProcessInfo{Pane: pane, Seq: seq}.Line()
	if err != nil {
		return "", false, err
	}
	results, err := herdr.Call(ctx, socket, line)
	if err != nil {
		return "", false, err
	}
	shell, err := herdr.ParseShellPID(results[0])
	if err != nil {
		return "", false, err
	}
	var identity struct {
		ProcessInfo struct {
			Pane string `json:"pane_id"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(results[0], &identity); err != nil {
		return "", false, fmt.Errorf("read canonical identity of shell pane %s: %w", pane, err)
	}
	if identity.ProcessInfo.Pane == "" {
		return "", false, fmt.Errorf("read canonical identity of shell pane %s: process_info.pane_id is empty", pane)
	}
	if shell == pid {
		return identity.ProcessInfo.Pane, false, nil
	}
	foreground, err := herdr.ParseProcessInfo(results[0])
	if err != nil {
		return "", false, err
	}
	if slices.Contains(foreground, pid) {
		return identity.ProcessInfo.Pane, true, nil
	}
	return "", false, nil
}
