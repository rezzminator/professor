package hookentry

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	hostdeps "github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// Claude Code fires no hook when the user answers a permission dialog with Esc
// or No (2.1.301, probed live): the chat would stay blocked in Herdr until the
// idle_prompt notification a minute later. Each blocked event of a Claude chat
// therefore starts a detached watch on the chat's transcript, which reports
// idle once Claude writes the interrupt entry that ends the turn.

const (
	// herdrWatchVerb is the herdr-state verb a blocked event starts detached.
	herdrWatchVerb  = "watch"
	herdrSocketFlag = "--socket"
	herdrWatchUsage = "usage: pfm internal herdr-state watch --socket {tmux socket} --session {tmux session} " +
		"--seq {blocked seq} --transcript {transcript.jsonl} --offset {bytes}"
	// herdrWatchPoll is how often the watch reads the transcript: the idle
	// lands within one poll of the interrupt entry, well inside the 2 s bound.
	herdrWatchPoll = 250 * time.Millisecond
	// herdrWatchLifetime caps how long a forgotten dialog keeps its watch
	// asleep; past it the idle_prompt notification is the net again.
	herdrWatchLifetime = 30 * time.Minute
	// herdrWatchRecord ends the per-session file naming the blocked seq the
	// session's one live watch watches: {session}.watch in the lock directory.
	herdrWatchRecord = ".watch"
	// herdrWatchLineBytes is the longest line the scan holds; no interrupt
	// entry comes near it, so a longer line is passed over unread.
	herdrWatchLineBytes = 64 << 10
	// claudeInterruptText is the whole content of the user entry Claude Code
	// writes when Esc or No at a permission dialog (or Esc during the tool)
	// ends the turn, 1-2 ms after the tool_result rejection.
	claudeInterruptText = "[Request interrupted by user for tool use]"
)

// herdrWatchTiming is the watch's poll interval and lifetime.
type herdrWatchTiming struct{ poll, lifetime time.Duration }

// routeHerdrState sends the watch verb to the transcript watch and every other
// verb to the reporter.
func routeHerdrState(args []string, stdin io.Reader, stderr io.Writer, deps herdrDeps) int {
	if len(args) != 0 && args[0] == herdrAgentsWatch {
		return watchHerdrAgents(args[1:], stderr, deps)
	}
	if len(args) != 0 && args[0] == herdrWatchVerb {
		return watchForHerdr(
			args[1:],
			stderr,
			deps,
			herdrWatchTiming{poll: herdrWatchPoll, lifetime: herdrWatchLifetime},
		)
	}
	return reportToHerdr(args, stdin, stderr, deps)
}

// watch keeps the session's transcript watch in step with the state an event
// just stored, under the session lock: a Claude block starts one (replacing
// any older one), every other stored state ends it. Only a pfm chat server,
// whose session names are unique, keeps a watch. An error leaves the event's
// own report as it is (fail-open).
func (reporter *herdrReporter) watch(session string, next herdrStored, payload herdrPayload) error {
	if reporter.deps.spawn == nil || !gather.IsChatSocketName(filepath.Base(reporter.options.socket)) {
		return nil
	}
	record := filepath.Join(reporter.deps.lockDir, session+herdrWatchRecord)
	if err := os.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stop the transcript watch: remove %s: %w", record, err)
	}
	if next.State != string(herdr.StateBlocked) || reporter.options.engine != pfmengine.Claude {
		return nil
	}
	transcript := payload.TranscriptPath
	if transcript == "" || !filepath.IsAbs(transcript) || filepath.Ext(transcript) != ".jsonl" {
		return fmt.Errorf("no transcript to watch: transcript_path %q is not an absolute .jsonl path "+
			"(an Esc at the dialog stays blocked until the next hook)", transcript)
	}
	info, err := os.Stat(transcript)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", info.Mode().Type())
	}
	if err != nil {
		return fmt.Errorf("no transcript to watch at %s: %w (an Esc at the dialog stays blocked until the next hook)",
			transcript, err)
	}
	seq := strconv.FormatUint(next.Seq, 10)
	if err := atomicfile.Write(record, []byte(seq+"\n"), 0o600); err != nil {
		return fmt.Errorf("start the transcript watch: write %s: %w", record, err)
	}
	argv := []string{
		"internal", "herdr-state", herdrWatchVerb, herdrSocketFlag, reporter.options.socket, "--session", session,
		"--seq", seq, "--transcript", transcript, "--offset", strconv.FormatInt(info.Size(), 10),
	}
	if err := reporter.deps.spawn(argv); err != nil {
		return errors.Join(fmt.Errorf("start the transcript watch: %w", err), os.Remove(record))
	}
	return nil
}

// startHerdrWatch starts this binary detached with argv: its own session, so a
// hook timeout that kills the hook's process group spares it, and no stdio, so
// the engine waiting on the hook's output does not wait on the watch.
func startHerdrWatch(argv []string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find the pfm binary: %w", err)
	}
	command := append([]string{self}, argv...)
	if _, err := (hostdeps.RealRunner{}).Start(
		context.Background(), command, hostdeps.StartOptions{Detach: true},
	); err != nil {
		return fmt.Errorf("start %s: %w", strings.Join(command, " "), err)
	}
	return nil
}

// herdrWatch is one transcript watch: the session it reports for, the blocked
// seq it watches, and the transcript read from offset on.
type herdrWatch struct {
	reporter   *herdrReporter
	record     string
	seq        uint64
	transcript string
	offset     int64
	// skipping is set while the scan passes over a line longer than
	// herdrWatchLineBytes.
	skipping bool
}

// watchForHerdr is the detached watch: it polls the transcript until the
// interrupt entry appears (then reports idle), its record is gone or names a
// newer block, the transcript cannot be read, or its lifetime ends. It always
// exits 0 after its arguments parsed; every error lands in the session's
// reporter error log.
func watchForHerdr(args []string, stderr io.Writer, deps herdrDeps, timing herdrWatchTiming) int {
	reporter := &herdrReporter{deps: deps, stderr: stderr}
	watch, err := parseHerdrWatch(args, reporter)
	if err != nil {
		reporter.warn("pfm internal herdr-state watch: %v\n%s", err, herdrWatchUsage)
		return 1
	}
	deadline := clock.Real.Now().Add(timing.lifetime)
	for {
		current, err := watch.current()
		if err != nil || !current {
			watch.fail(err)
			return 0
		}
		found, err := watch.scan()
		if err != nil {
			watch.fail(err)
			return 0
		}
		if found {
			watch.fail(watch.report())
			return 0
		}
		if !clock.Real.Now().Before(deadline) {
			return 0
		}
		<-clock.Real.After(timing.poll)
	}
}

func parseHerdrWatch(args []string, reporter *herdrReporter) (*herdrWatch, error) {
	flags := flag.NewFlagSet(herdrWatchVerb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "")
	session := flags.String("session", "", "")
	seq := flags.Uint64("seq", 0, "")
	transcript := flags.String("transcript", "", "")
	offset := flags.Int64("offset", -1, "")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case flags.NArg() != 0:
		return nil, fmt.Errorf("unexpected arguments %q", flags.Args())
	case *socket == "" || *seq == 0 || *offset < 0:
		return nil, errors.New("--socket, --seq and --offset are required")
	case *session == "" || *session != filepath.Base(*session) || *session == "." || *session == "..":
		return nil, fmt.Errorf("--session %q is not a plain session name", *session)
	case !filepath.IsAbs(*transcript):
		return nil, fmt.Errorf("--transcript %q is not an absolute path", *transcript)
	case reporter.deps.lockDir == "":
		return nil, errors.New("no pfm state directory for the watch record")
	}
	reporter.session = *session
	reporter.options = herdrOptions{
		engine:  pfmengine.Claude,
		event:   herdr.EventIdle,
		socket:  *socket,
		session: *session,
	}
	return &herdrWatch{
		reporter: reporter, record: filepath.Join(reporter.deps.lockDir, *session+herdrWatchRecord), seq: *seq,
		transcript: *transcript, offset: *offset,
	}, nil
}

// fail logs a watch error to the session's reporter error log.
func (watch *herdrWatch) fail(err error) {
	if err != nil {
		watch.reporter.warn("pfm internal herdr-state watch: %v (the pane stays as reported)", err)
	}
}

// current reports whether the session's record still names this watch's
// block: a later event removed it, or a later block rewrote it.
func (watch *herdrWatch) current() (bool, error) {
	data, err := os.ReadFile(watch.record)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the watch record %s: %w", watch.record, err)
	}
	return strings.TrimSpace(string(data)) == strconv.FormatUint(watch.seq, 10), nil
}

// scan reads the complete lines appended since the last scan and reports
// whether one is the interrupt entry. An unfinished last line is left for the
// next scan; a transcript that is gone, unreadable or shorter than what was
// read is an error.
func (watch *herdrWatch) scan() (bool, error) {
	// Opened without blocking and checked through the open file: a FIFO put in
	// the transcript's place would otherwise hold the open past the lifetime.
	// O_NOCTTY: the watcher leads its own session, so a terminal put there
	// would otherwise become its controlling terminal.
	file, err := os.OpenFile(watch.transcript, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return false, fmt.Errorf("read transcript %s: %w", watch.transcript, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", info.Mode().Type())
	}
	switch {
	case err != nil:
		return false, fmt.Errorf("read transcript %s: %w", watch.transcript, err)
	case info.Size() < watch.offset:
		return false, fmt.Errorf("transcript %s shrank to %d bytes below the %d already read",
			watch.transcript, info.Size(), watch.offset)
	case info.Size() == watch.offset:
		return false, nil
	}
	if _, err := file.Seek(watch.offset, io.SeekStart); err != nil {
		return false, fmt.Errorf("read transcript %s at %d: %w", watch.transcript, watch.offset, err)
	}
	reader := bufio.NewReaderSize(io.LimitReader(file, info.Size()-watch.offset), herdrWatchLineBytes)
	for {
		line, err := reader.ReadSlice('\n')
		switch {
		case err == nil:
			if !watch.skipping && claudeToolInterrupt(line) {
				return true, nil
			}
			watch.skipping = false
		case errors.Is(err, bufio.ErrBufferFull):
			watch.skipping = true
		case errors.Is(err, io.EOF):
			if watch.skipping {
				watch.offset += int64(len(line))
			}
			return false, nil
		default:
			return false, fmt.Errorf("read transcript %s at %d: %w", watch.transcript, watch.offset, err)
		}
		watch.offset += int64(len(line))
	}
}

// claudeToolInterrupt reports whether one transcript line is the main thread's
// user entry whose content is exactly the interrupt text, as a string or as a
// text block. A sub-agent's entry, the next API request's blob re-carrying the
// text, and a torn line are not.
func claudeToolInterrupt(line []byte) bool {
	if !bytes.Contains(line, []byte(claudeInterruptText)) {
		return false
	}
	var entry struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsSidechain {
		return false
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		return text == claudeInterruptText
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "text" && block.Text == claudeInterruptText {
			return true
		}
	}
	return false
}

// report writes idle over the watched block, under the session lock, only
// while the stored state is still that block: at the stored seq right above
// it, so every real event that started after the block outranks the idle.
func (watch *herdrWatch) report() error {
	reporter := watch.reporter
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	// Take Claude's activity lock before the tmux and bridge locks, just as a
	// real hook does. Re-read the tmux state after acquiring both locks.
	initial, err := reporter.readState(ctx, reporter.session)
	if err != nil {
		return err
	}
	activityUnlock, err := reporter.lock(ctx, reporter.activityKey(initial.SessionID))
	if err != nil {
		return err
	}
	defer activityUnlock()
	unlock, err := reporter.lock(ctx, reporter.session)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := reporter.readState(ctx, reporter.session)
	if err != nil {
		return err
	}
	if state.Seq != watch.seq || state.State != string(herdr.StateBlocked) || state.SessionID != initial.SessionID {
		return nil
	}
	reporter.seq = watch.seq + 1
	payload := herdrPayload{SessionID: state.SessionID}
	activity, err := reporter.readActivity(state.SessionID)
	if err != nil {
		return err
	}
	// Held-block hooks can advance only the activity record. Never lower its
	// stale-event fence when the transcript watch clears the older tmux block.
	reporter.seq = max(reporter.seq, activity.Stored.Seq+1)
	wasWaiting := activity.Stored.MainStoppedAt != 0 && len(activity.Stored.Agents) > 0
	action, err := reporter.planActivity(payload, &activity.Stored)
	if err != nil {
		return err
	}
	action.stored.Agents = activity.Stored.Agents
	action.stored.MainStoppedAt = activity.Stored.MainStoppedAt
	action.stored.Seq = reporter.seq
	activity.Stored = action.stored
	activity.TmuxSocket = reporter.options.socket
	activity.TmuxSession = reporter.session
	reporter.activity = &activity
	reporter.activityAction = action
	if reporter.themeOn() {
		reporter.themeState(ctx, payload)
	}
	err = reporter.event(ctx, reporter.session, state, payload)
	return errors.Join(err, reporter.finishActivity(state.SessionID, activity, wasWaiting))
}
