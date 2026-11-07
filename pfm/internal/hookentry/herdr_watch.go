package hookentry

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	pfmtranscript "github.com/rezzminator/professor/pfm/internal/transcript"
)

// Claude Code fires no hook when its user interrupts a turn (2.1.301 and
// 2.1.317, probed live): Esc while the model streams or a tool runs, and Esc or
// No at a permission dialog, end the turn with no Stop and no idle_prompt
// after it; the interrupt entry the transcript gains is its only trace. While
// a Claude main turn is open (stored working or blocked), a detached watch
// therefore reads the chat's transcript and settles the turn once that entry
// lands, for a pfm chat tmux server and a native Herdr seat alike.

const (
	// herdrWatchVerb is the herdr-state verb an open main turn starts detached.
	herdrWatchVerb  = "watch"
	herdrSocketFlag = "--socket"
	herdrWatchUsage = "usage: pfm internal herdr-state watch [--socket {tmux socket} --session {tmux session}] " +
		"--pid {engine pid} --command {engine command} --seq {seq} --since {main seq} " +
		"--transcript {transcript.jsonl} --offset {bytes} {session id}"
	// herdrWatchPoll is how often the watch reads the transcript: the idle
	// lands within one poll of the interrupt entry, well inside the 2 s bound.
	herdrWatchPoll = 250 * time.Millisecond
	// herdrWatchRecord ends the file beside a session's activity record naming
	// the seq of the event that started its one live watch, then, once that
	// watch runs, its own pid and command, so a watch killed mid-turn is told
	// from a live one and replaced (watchLive). No lifetime caps
	// the watch: a turn runs hours without a hook (a long tool, a foreground
	// sub-agent), so the record (removed by every state that closes the main
	// turn), the chat's engine process and a readable transcript bound it.
	herdrWatchRecord = ".watch"
	// herdrWatchLineBytes is the longest line the scan holds; no interrupt
	// entry comes near it, so a longer line is passed over unread.
	herdrWatchLineBytes = 64 << 10
	// herdrWatchRewind is how far before the starting hook's offset the watch
	// reads: an async hook that ran late started its watch past an interrupt
	// Claude wrote between the hook's spawn and its run, a few lines at most.
	// The since fence keeps every earlier turn's interrupt out of the rewind.
	herdrWatchRewind = 4 << 20
)

// routeHerdrState sends the watch verb to the transcript watch and every other
// verb to the reporter.
func routeHerdrState(args []string, stdin io.Reader, stderr io.Writer, deps herdrDeps) int {
	if len(args) != 0 && args[0] == herdrAgentsWatch {
		return watchHerdrAgents(args[1:], stderr, deps)
	}
	if len(args) != 0 && args[0] == herdrSettle {
		return settleHerdrState(args[1:], stderr, deps)
	}
	if len(args) != 0 && args[0] == herdrWatchVerb {
		return watchForHerdr(args[1:], stderr, deps, herdrWatchPoll)
	}
	return reportToHerdr(args, stdin, stderr, deps)
}

// watch starts the session's transcript watch for the state a transport just
// stored for the pane's own engine (event for a tmux chat, nativeSeat for a
// native seat), under the activity lock: an open main turn keeps one live
// watch, started only while none is recorded, so a turn's every tool event
// starts no other. A sub-agent's events never start one, and a pfm chat server
// is the only tmux one that keeps one. endWatch ends it. An error leaves the
// event's own report as it is (fail-open).
func (reporter *herdrReporter) watch(payload herdrPayload, stored herdrStored) error {
	event, tmux := reporter.options.event, reporter.options.socket != ""
	switch {
	// Only a recorded event holds the activity lock the record is kept under.
	case reporter.activity == nil || reporter.deps.spawn == nil || !mainTurnOpen(stored):
		return nil
	case payload.AgentID != "" || event == herdr.EventAgentStart || event == herdr.EventAgentStop:
		return nil
	case tmux && !gather.IsChatSocketName(filepath.Base(reporter.options.socket)):
		// The user's own tmux may rename the session the watch reports through.
		return nil
	}
	record := reporter.watchRecord(payload.SessionID)
	if data, err := os.ReadFile(record); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return fmt.Errorf("read the transcript watch record %s: %w", record, err)
		}
		if live, err := reporter.watchLive(data); err != nil || live {
			return err // the live watch covers this state too
		}
		// A watch killed mid-turn left its record: this state starts the next.
	}
	transcript := payload.TranscriptPath
	if transcript == "" || !filepath.IsAbs(transcript) || filepath.Ext(transcript) != ".jsonl" {
		return fmt.Errorf("no transcript to watch: transcript_path %q is not an absolute .jsonl path "+
			"(an interrupt stays %s until the next hook)", transcript, stored.State)
	}
	info, err := os.Stat(transcript)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", info.Mode().Type())
	}
	if err != nil {
		return fmt.Errorf("no transcript to watch at %s: %w (an interrupt stays %s until the next hook)",
			transcript, err, stored.State)
	}
	engine, err := reporter.hookEngine()
	if err != nil {
		return fmt.Errorf("start the transcript watch: no engine process to bound it: %w", err)
	}
	seq := strconv.FormatUint(stored.Seq, 10)
	if err := atomicfile.Write(record, []byte(seq+"\n"), 0o600); err != nil {
		return fmt.Errorf("start the transcript watch: write %s: %w", record, err)
	}
	argv := []string{internalCommand, herdrStateCommand, herdrWatchVerb}
	if tmux {
		argv = append(argv, herdrSocketFlag, reporter.options.socket, herdrSessionFlag, reporter.session)
	}
	argv = append(argv,
		"--pid", strconv.Itoa(engine.PID),
		"--command", engine.Command,
		"--seq", seq,
		"--since", strconv.FormatUint(cmp.Or(stored.MainSeq, stored.Seq), 10),
		"--transcript", transcript,
		"--offset", strconv.FormatInt(info.Size(), 10),
		payload.SessionID,
	)
	if err := reporter.deps.spawn(argv); err != nil {
		return errors.Join(fmt.Errorf("start the transcript watch: %w", err), os.Remove(record))
	}
	return nil
}

// watchLive reports whether the watch a record names still runs: a record its
// watch has not yet claimed (just spawned) is taken as live.
func (reporter *herdrReporter) watchLive(record []byte) (bool, error) {
	fields := strings.SplitN(strings.TrimSpace(string(record)), " ", 3)
	if len(fields) < 3 {
		return true, nil
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil {
		return false, fmt.Errorf("read the transcript watch record: pid %q: %w", fields[1], err)
	}
	return reporter.processRuns(pid, fields[2])
}

// endWatch ends the session's transcript watch once the activity record stores
// a state that closes the main turn (idle, a Stop held by running sub-agents,
// a release): its record goes, and the watch exits at its next poll. A
// sub-agent's events never end it.
func (reporter *herdrReporter) endWatch(payload herdrPayload, stored herdrStored) error {
	event := reporter.options.event
	if mainTurnOpen(stored) || payload.AgentID != "" || event == herdr.EventAgentStart ||
		event == herdr.EventAgentStop {
		return nil
	}
	record := reporter.watchRecord(payload.SessionID)
	if err := os.Remove(record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stop the transcript watch: remove %s: %w", record, err)
	}
	return nil
}

// mainTurnOpen reports whether a stored state is a main turn still running:
// working or blocked, with no Stop (or idle prompt) since its start.
func mainTurnOpen(stored herdrStored) bool {
	return (stored.State == string(herdr.StateWorking) || stored.State == string(herdr.StateBlocked)) &&
		stored.MainStoppedAt == 0
}

// watchRecord is the session's transcript watch record, beside its activity
// record and under the same lock.
func (reporter *herdrReporter) watchRecord(session string) string {
	return filepath.Join(reporter.deps.lockDir, reporter.activityKey(session)+herdrWatchRecord)
}

// hookEngine is the engine process that fired this hook: the first process up
// from it that is neither a shell nor pfm.
func (reporter *herdrReporter) hookEngine() (herdrHookProcess, error) {
	ancestry, err := reporter.hookAncestry()
	if err != nil {
		return herdrHookProcess{}, err
	}
	if index := engineIndex(ancestry); index >= 0 {
		return ancestry[index], nil
	}
	return herdrHookProcess{}, fmt.Errorf("no process above hook pid %d but shells and pfm", ancestry[0].PID)
}

// engineIndex is where a hook's ancestry reaches its engine: the first
// process that is neither a shell nor pfm, -1 when there is none.
func engineIndex(ancestry []herdrHookProcess) int {
	return slices.IndexFunc(ancestry, func(process herdrHookProcess) bool { return !isShellOrPfm(process.Command) })
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

// herdrWatch is one transcript watch: the session it reports for, the seq
// that started it, the engine whose life bounds it, and the transcript read
// from offset on.
type herdrWatch struct {
	reporter  *herdrReporter
	sessionID string
	record    string
	seq       uint64
	// since is the starting event's own seq: an interrupt Claude wrote before
	// it closed an earlier turn.
	since      uint64
	pid        int
	command    string
	transcript string
	offset     int64
	// fresh is where the starting hook found the transcript's end: past it an
	// interrupt entry naming no time is this turn's too.
	fresh int64
	// skipping is set while the scan passes over a line longer than
	// herdrWatchLineBytes, or the rest of the line a rewind landed in.
	skipping bool
}

// watchForHerdr is the detached watch: it polls the transcript and reports at
// each interrupt entry, until its record is gone or names a newer watch, its
// engine process is gone (a chat that died without its SessionEnd), or the
// transcript cannot be read; the last two remove its record, so the session's
// next working event starts a new one. It always exits 0 after its arguments
// parsed; every error lands in the session's reporter error log.
func watchForHerdr(args []string, stderr io.Writer, deps herdrDeps, poll time.Duration) int {
	reporter := &herdrReporter{deps: deps, stderr: stderr}
	watch, err := parseHerdrWatch(args, reporter)
	if err != nil {
		reporter.warn("pfm internal herdr-state watch: %v\n%s", err, herdrWatchUsage)
		return 1
	}
	if err := watch.rewind(); err != nil {
		watch.fail(errors.Join(err, watch.retire()))
		return 0
	}
	watch.fail(watch.claim())
	var pending *time.Time
	unreadable := ""
	for {
		current, err := watch.current()
		if err != nil || !current {
			watch.fail(err)
			return 0
		}
		runs, err := reporter.processRuns(watch.pid, watch.command)
		switch {
		case err != nil:
			// The engine may well run: the watch goes on, logging each new cause once.
			if err.Error() != unreadable {
				watch.fail(fmt.Errorf("the engine's process could not be read (watching on): %w", err))
				unreadable = err.Error()
			}
		case !runs:
			watch.fail(watch.retire())
			return 0
		default:
			unreadable = ""
		}
		if pending == nil {
			at, found, err := watch.scan()
			if err != nil {
				watch.fail(errors.Join(err, watch.retire()))
				return 0
			}
			if found {
				pending = &at
			}
		}
		if pending != nil {
			// The record says what follows: an idle the turn took removed it, a
			// newer turn that outranked the idle keeps it, and this watch with it.
			// An idle it could not deliver yet (a lock held past the deadline) is
			// tried again at the next poll.
			err := watch.report(*pending)
			watch.fail(err)
			if !isUndelivered(err) {
				pending = nil
				continue
			}
		}
		<-clock.Real.After(poll)
	}
}

func parseHerdrWatch(args []string, reporter *herdrReporter) (*herdrWatch, error) {
	flags := flag.NewFlagSet(herdrWatchVerb, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "")
	session := flags.String("session", "", "")
	pid := flags.Int("pid", 0, "")
	command := flags.String("command", "", "")
	seq := flags.Uint64("seq", 0, "")
	since := flags.Uint64("since", 0, "")
	transcript := flags.String("transcript", "", "")
	offset := flags.Int64("offset", -1, "")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	sessionID := flags.Arg(0)
	switch {
	case flags.NArg() != 1 || sessionID == "":
		return nil, fmt.Errorf("want one session id after the flags, got %q", flags.Args())
	case *pid <= 1 || *command == "" || *seq == 0 || *since == 0 || *offset < 0:
		return nil, errors.New("--pid, --command, --seq, --since and --offset are required")
	case (*socket == "") != (*session == ""):
		return nil, errors.New("--socket and --session come together")
	case *session != "" && (*session != filepath.Base(*session) || *session == "." || *session == ".."):
		return nil, fmt.Errorf("--session %q is not a plain session name", *session)
	case !filepath.IsAbs(*transcript):
		return nil, fmt.Errorf("--transcript %q is not an absolute path", *transcript)
	case reporter.deps.lockDir == "":
		return nil, errors.New("no pfm state directory for the watch record")
	}
	reporter.session = cmp.Or(*session, sessionID)
	reporter.options = herdrOptions{
		engine:  pfmengine.Claude,
		event:   herdr.EventIdle,
		socket:  *socket,
		session: *session,
	}
	return &herdrWatch{
		reporter: reporter, sessionID: sessionID, record: reporter.watchRecord(sessionID), seq: *seq, since: *since,
		pid: *pid, command: *command, transcript: *transcript, offset: *offset, fresh: *offset,
	}, nil
}

// fail logs a watch error to the session's reporter error log.
func (watch *herdrWatch) fail(err error) {
	if err != nil {
		watch.reporter.warn("pfm internal herdr-state watch: %v (the pane stays as reported)", err)
	}
}

// current reports whether the session's record still names this watch: a
// state that closed the main turn removed it, or a newer watch rewrote it.
func (watch *herdrWatch) current() (bool, error) {
	data, err := os.ReadFile(watch.record)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the watch record %s: %w", watch.record, err)
	}
	seq, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	return seq == strconv.FormatUint(watch.seq, 10), nil
}

// claim adds this watch's pid and command to its record, under the activity
// lock, so a later hook tells it from a watch that was killed (watchLive).
func (watch *herdrWatch) claim() error {
	reporter := watch.reporter
	if reporter.deps.procInfo == nil || reporter.deps.getpid == nil {
		return nil
	}
	pid := reporter.deps.getpid()
	_, command, err := reporter.deps.procInfo(pid)
	if err != nil {
		return fmt.Errorf("claim the transcript watch record: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := reporter.lock(ctx, reporter.activityKey(watch.sessionID))
	if err != nil {
		return fmt.Errorf("claim the transcript watch record: %w", err)
	}
	defer unlock()
	if current, err := watch.current(); err != nil || !current {
		return err
	}
	claimed := fmt.Sprintf("%d %d %s\n", watch.seq, pid, command)
	if err := atomicfile.Write(watch.record, []byte(claimed), 0o600); err != nil {
		return fmt.Errorf("claim the transcript watch record: %w", err)
	}
	return nil
}

// rewind backs the first scan up to herdrWatchRewind bytes before the starting
// hook's offset, skipping the rest of the line it lands in.
func (watch *herdrWatch) rewind() error {
	start := max(0, watch.offset-herdrWatchRewind)
	if start == 0 {
		watch.offset = 0
		return nil
	}
	// Opened as scan opens it: no FIFO or terminal in the transcript's place holds the watch.
	file, err := os.OpenFile(watch.transcript, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("read transcript %s: %w", watch.transcript, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", info.Mode().Type())
	}
	var before [1]byte
	if err == nil {
		_, err = file.ReadAt(before[:], start-1)
	}
	if err != nil {
		return fmt.Errorf("read transcript %s at %d: %w", watch.transcript, start-1, err)
	}
	watch.offset, watch.skipping = start, before[0] != '\n'
	return nil
}

// counts reports whether an interrupt entry starting at offset ends this
// watch's turn: written at or after the starting event's spawn, or, naming no
// time, found past where the starting hook saw the transcript end.
func (watch *herdrWatch) counts(at time.Time, offset int64) bool {
	if at.IsZero() {
		return offset >= watch.fresh
	}
	return at.UnixNano() > 0 && uint64(at.UnixNano()) >= watch.since
}

// retire removes the session's record while it still names this watch, under
// the activity lock the hooks keep it under, so the session's next working
// event starts a new watch.
func (watch *herdrWatch) retire() error {
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := watch.reporter.lock(ctx, watch.reporter.activityKey(watch.sessionID))
	if err != nil {
		return err
	}
	defer unlock()
	if current, err := watch.current(); err != nil || !current {
		return err
	}
	if err := os.Remove(watch.record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("retire the transcript watch: remove %s: %w", watch.record, err)
	}
	return nil
}

// scan reads the complete lines appended since the last scan up to the first
// interrupt entry, and reports when Claude wrote it. An unfinished last line
// is left for the next scan; a transcript that is gone, unreadable or shorter
// than what was read is an error.
func (watch *herdrWatch) scan() (time.Time, bool, error) {
	// Opened without blocking and checked through the open file: a FIFO put in
	// the transcript's place would otherwise hold the open, and the watch, forever.
	// O_NOCTTY: the watcher leads its own session, so a terminal put there
	// would otherwise become its controlling terminal.
	file, err := os.OpenFile(watch.transcript, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read transcript %s: %w", watch.transcript, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", info.Mode().Type())
	}
	switch {
	case err != nil:
		return time.Time{}, false, fmt.Errorf("read transcript %s: %w", watch.transcript, err)
	case info.Size() < watch.offset:
		return time.Time{}, false, fmt.Errorf("transcript %s shrank to %d bytes below the %d already read",
			watch.transcript, info.Size(), watch.offset)
	case info.Size() == watch.offset:
		return time.Time{}, false, nil
	}
	if _, err := file.Seek(watch.offset, io.SeekStart); err != nil {
		return time.Time{}, false, fmt.Errorf("read transcript %s at %d: %w", watch.transcript, watch.offset, err)
	}
	reader := bufio.NewReaderSize(io.LimitReader(file, info.Size()-watch.offset), herdrWatchLineBytes)
	for {
		line, err := reader.ReadSlice('\n')
		switch {
		case err == nil:
			if at, found := claudeInterrupt(line); found && !watch.skipping && watch.counts(at, watch.offset) {
				watch.offset += int64(len(line))
				return at, true, nil
			}
			watch.skipping = false
		case errors.Is(err, bufio.ErrBufferFull):
			watch.skipping = true
		case errors.Is(err, io.EOF):
			if watch.skipping {
				watch.offset += int64(len(line))
			}
			return time.Time{}, false, nil
		default:
			return time.Time{}, false, fmt.Errorf("read transcript %s at %d: %w", watch.transcript, watch.offset, err)
		}
		watch.offset += int64(len(line))
	}
}

// claudeInterrupt reports whether one transcript line is the main thread's
// user entry whose content (a string, or one of its text blocks) is Claude's
// interrupt marker, and when Claude wrote it (zero when the entry names no
// readable time). A sub-agent's entry, an entry of another type re-carrying
// the text (the next API request's blob), a prompt that only mentions it and
// a torn line are not.
func claudeInterrupt(line []byte) (time.Time, bool) {
	if !bytes.Contains(line, []byte(pfmtranscript.ClaudeInterruptMarker)) {
		return time.Time{}, false
	}
	var entry struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsSidechain {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		at = time.Time{}
	}
	var text string
	if json.Unmarshal(entry.Message.Content, &text) == nil {
		return at, pfmtranscript.ClaudeInterrupted(text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return time.Time{}, false
	}
	for _, block := range blocks {
		if block.Type == "text" && pfmtranscript.ClaudeInterrupted(block.Text) {
			return at, true
		}
	}
	return time.Time{}, false
}

// report settles the interrupted turn at the interrupt's own time (the entry's
// timestamp; the watch's clock when the entry names none, or one still to
// come), through the door a Stop hook's idle takes on the chat's transport:
// working "N agents running" while sub-agents remain, else idle. Ordered by
// that time like any event, it loses to every hook that started after the
// interrupt and outranks every one before it.
func (watch *herdrWatch) report(at time.Time) error {
	reporter := watch.reporter
	reporter.seq = uint64(reporter.deps.now().UnixNano())
	if !at.IsZero() && at.UnixNano() > 0 && uint64(at.UnixNano()) < reporter.seq {
		reporter.seq = uint64(at.UnixNano())
	}
	payload := herdrPayload{SessionID: watch.sessionID}
	if reporter.options.socket == "" {
		// A native seat's hook proves its seat by its own ancestry, which a
		// detached process lacks: the settle process records the idle and
		// re-sends it to the panes the record last reached, as it does for a
		// Stop hook that met the activity lock held.
		return reporter.spawnSettle(herdrSettleEvent, reporter.plan(payload).stored, payload,
			string(herdr.EventIdle))
	}
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	unlock, err := reporter.lock(ctx, reporter.activityKey(watch.sessionID))
	if err != nil {
		return err
	}
	defer unlock()
	if current, err := watch.current(); err != nil || !current {
		return err
	}
	return reporter.recordActivity(ctx, payload, watch.deliverTmux)
}

// deliverTmux delivers the watch's report through the chat's tmux server, as
// runTmux does a hook's: under the tmux session lock, taken after the activity
// lock, while that session still holds this conversation. A store or send it
// could not finish goes to the settle process.
func (watch *herdrWatch) deliverTmux(ctx context.Context, payload herdrPayload) (returnErr error) {
	reporter := watch.reporter
	defer func() { returnErr = errors.Join(returnErr, reporter.settleTmuxLater(payload, returnErr)) }()
	unlock, err := reporter.lock(ctx, reporter.session)
	if err != nil {
		return err
	}
	defer unlock()
	state, err := reporter.readState(ctx, reporter.session)
	if err != nil {
		return undelivered(err)
	}
	if state.SessionID != payload.SessionID {
		return nil
	}
	reporter.themeUnlessHeld(ctx, reporter.themeOn(), payload, state)
	return reporter.event(ctx, reporter.session, state, payload)
}
