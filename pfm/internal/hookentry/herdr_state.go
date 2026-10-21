package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	hostdeps "github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/tmux"
)

const (
	herdrStateUsage = "usage: pfm internal herdr-state [--engine claude|codex|opencode] start|working|blocked|idle|end|notification|title|agent-start|agent-stop < hook-payload.json\n" +
		"       pfm internal herdr-state attach|sync|detach --socket {tmux socket} --session {tmux session} [--client {tmux client pid}]\n" +
		"       pfm internal herdr-state name --socket {tmux socket} --session {tmux session}"
	// herdrStoredOption is the chat session's user option holding the last
	// reported state, so the client-attached hook can re-report it to the pane
	// a client just attached from.
	herdrStoredOption = herdr.StoredOption
	herdrDeadline     = herdr.ReportDeadline
	herdrClientFlag   = "--client"
	herdrLockPoll     = 10 * time.Millisecond
	// herdrNameAttempts is the agent names tried for one pane: the slug, then
	// the suffixes -2 … -9.
	herdrNameAttempts = 9
	// herdrNoSession names the error log of a run that failed before it knew
	// its chat session.
	herdrNoSession = "no-session"
	// herdrMaxProcessDepth bounds the walk up from the hook process.
	herdrMaxProcessDepth = 64
	herdrMaxPayload      = 1 << 20
	// SessionEnd reasons on which the session swaps inside the same process.
	herdrReasonClear  = "clear"
	herdrReasonResume = "resume"
	// herdrPaneNotFound is the error code Herdr 0.9.5 answers for a pane it does
	// not know, on pane.release_agent, report_agent, report_metadata and
	// process_info alike ({"code":"pane_not_found","message":"pane wX:p9 not
	// found"}, probed live); herdrNotFound is the socket-api docs' spelling.
	herdrPaneNotFound = "pane_not_found"
	herdrNotFound     = "not_found"
)

// herdrDeps are the reporter's seams: the process environment, one tmux call (against an
// explicit socket when non-empty, else the one $TMUX names), the clock and spawn, which
// starts a detached transcript watch (nil: no watch door).
type herdrDeps struct {
	getenv func(string) string
	tmux   func(ctx context.Context, socket string, args ...string) (string, error)
	now    func() time.Time
	// lookPath finds a program on PATH; lockDir holds the per-session lock
	// files and the Codex pane registry (herdr_codex.go).
	lookPath func(string) (string, error)
	lockDir  string
	spawn    func(argv []string) error
	// logError appends one reporter error to the session's log; tmux discards
	// the stderr of a hook it runs, so this is where the error stays visible.
	logError func(session string, at time.Time, message string) error
	logInfo  func(session string, at time.Time, message string) error
	// procInfo reads one process: its parent pid and command name. getpid is
	// the hook process.
	procInfo func(pid int) (parent int, command string, err error)
	getpid   func() int
	// cmdline and environ read one process's command line and environment:
	// the Codex TUI's own, which name the pane it runs in (herdr_codex.go).
	cmdline func(pid int) ([]string, error)
	environ func(pid int) (map[string]string, error)
	// stateDir is the `pfm theme` state directory: while its sync is on, the
	// reporter marks the chat's own tmux server with the agent state the
	// theme's colours read. Empty means no theme door.
	stateDir string
	// sidDir holds the reload pane mutexes (reload.LockPath): an end event
	// during a reload's /exit leaves the chat server's title alone.
	sidDir    string
	seatPaths *paths.Values
}

func realHerdrDeps() herdrDeps {
	env := paths.OSEnv{}
	procfs := gather.NewProcFS(env.Get(paths.EnvProcRoot))
	deps := herdrDeps{
		getenv: env.Get, tmux: runTmuxForHerdr, now: clock.Real.Now, lookPath: hostdeps.RealRunner{}.LookPath,
		procInfo: realProcInfo(procfs), getpid: os.Getpid, cmdline: procfs.Cmdline, environ: procfs.Environ,
		sidDir: paths.SIDDirFrom(env), spawn: startHerdrWatch,
	}
	home, err := paths.HomeFrom(env)
	// A real reporter always enables durable-seat resolution, even when home
	// lookup fails. Nil is reserved for isolated legacy transport test seams.
	deps.seatPaths = &paths.Values{}
	if err == nil {
		deps.lockDir = paths.HerdrLockDir(home)
		deps.stateDir = themesync.DefaultStateDir(home)
		deps.seatPaths = &paths.Values{
			Home:    home,
			StateDB: paths.EnvOrFrom(env, paths.EnvStateDB, paths.DefaultStateDB(home)),
		}
	}
	deps.logError = func(session string, at time.Time, message string) error {
		if err != nil {
			return fmt.Errorf("no pfm state directory for the error log: %w", err)
		}
		return herdr.AppendError(deps.lockDir, session, at, message)
	}
	deps.logInfo = func(session string, at time.Time, message string) error {
		if err != nil {
			return fmt.Errorf("no pfm state directory for the info log: %w", err)
		}
		return herdr.AppendInfo(deps.lockDir, session, at, message)
	}
	return deps
}

func runTmuxForHerdr(ctx context.Context, socket string, args ...string) (string, error) {
	var stderr strings.Builder
	command := tmux.Exec(ctx, "", socket, args...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(output), nil
}

// herdrClient (a tmux client attached from a Herdr pane: the pane its chat reports
// to while that client stays attached) and herdrStored are the reporter's names
// for the stored state pfm chat ls reads too (herdr.Client, herdr.Stored).
type (
	herdrClient = herdr.Client
	herdrStored = herdr.Stored
)

type herdrOptions struct {
	engine  pfmengine.ID
	event   herdr.Event
	socket  string
	session string
	client  string
}

// herdrAction is what one event does to the pane: report a state, release it
// or nothing.
type herdrAction struct {
	release bool
	stored  herdrStored
}

// HerdrState is the `pfm internal herdr-state` hook entry. It is fail-open: it
// exits 0 whatever Herdr, tmux or the payload do, because Claude Code blocks a
// turn on a hook's exit 2; only a usage error exits 1, a non-blocking hook
// error whose stderr line names the verb.
func HerdrState(args []string, stdin io.Reader, stderr io.Writer) int {
	return routeHerdrState(args, stdin, stderr, realHerdrDeps())
}

func parseHerdrOptions(args []string) (herdrOptions, error) {
	options := herdrOptions{engine: pfmengine.Claude}
	var words []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--engine", herdrSocketFlag, "--session", herdrClientFlag:
			if index+1 >= len(args) {
				if args[index] == herdrClientFlag {
					// tmux expands #{client_pid} to nothing in a client-detached hook (the
					// client is gone by then), which leaves a bare trailing --client.
					continue
				}
				return options, fmt.Errorf("%s needs a value", args[index])
			}
			value := args[index+1]
			index++
			switch args[index-1] {
			case "--engine":
				id, err := pfmengine.Parse(value)
				if err != nil {
					return options, err
				}
				options.engine = id
			case herdrSocketFlag:
				options.socket = value
			case herdrClientFlag:
				options.client = value
			default:
				options.session = value
			}
		default:
			words = append(words, args[index])
		}
	}
	if len(words) != 1 {
		return options, fmt.Errorf("want exactly one event, got %d", len(words))
	}
	for _, event := range herdr.Events() {
		if string(event) == words[0] {
			options.event = event
			return options, nil
		}
	}
	return options, fmt.Errorf("unknown event %q", words[0])
}

type herdrReporter struct {
	activity         *herdrActivity
	activityAction   *herdrAction
	nativeSeatReport bool
	options          herdrOptions
	deps             herdrDeps
	stderr           io.Writer
	// seq orders EVENTS: the start time of this process. sent orders the
	// REQUESTS to Herdr: each is taken under the session lock, so Herdr sees
	// them in the order they were made.
	seq  uint64
	sent uint64
	// session is the chat session once known: the name its error log carries.
	session string
	// tmuxPane is the engine's tmux pane (tmuxOrigin, or a Codex session's
	// registered one), empty outside tmux.
	tmuxPane string
	// panes caches one pane.list per Herdr socket for this run.
	panes map[string]paneListing
	// verifiedClients reuses the foreground proof from adoption in this locked
	// transaction, rather than probing the same client's pane twice.
	verifiedClients map[string]herdrClient
}

type paneListing struct {
	panes []herdr.Pane
	err   error
}

// warn writes one error line to stderr and appends it to the session's error
// log; a log that cannot be written is itself written to stderr.
func (reporter *herdrReporter) warn(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	fmt.Fprintln(reporter.stderr, message)
	if reporter.deps.logError == nil {
		return
	}
	session := reporter.session
	if session == "" {
		session = herdrNoSession
	}
	if err := reporter.deps.logError(session, reporter.deps.now(), message); err != nil {
		fmt.Fprintf(reporter.stderr, "pfm internal herdr-state %s: log error: %v\n", reporter.options.event, err)
	}
}

func (reporter *herdrReporter) nextSent() uint64 {
	next := uint64(reporter.deps.now().UnixNano())
	if next <= reporter.sent {
		next = reporter.sent + 1
	}
	reporter.sent = next
	return next
}

// plan maps one event to its pane action. nil means the event changes nothing.
func (reporter *herdrReporter) plan(payload herdrPayload) *herdrAction {
	if reporter.activityAction != nil {
		return reporter.activityAction
	}
	engine := herdr.Label(reporter.options.engine)
	report := func(state herdr.State, message string) *herdrAction {
		return &herdrAction{
			stored: herdrStored{Engine: engine, SessionID: payload.SessionID, State: string(state), Message: message},
		}
	}
	switch reporter.options.event {
	case herdr.EventStart:
		// Only a new, resumed or cleared session starts idle; a compaction
		// (or a Codex fork) restarts mid-turn and leaves the pane as it is.
		switch payload.Source {
		case "", "startup", herdrReasonResume, herdrReasonClear:
			return report(herdr.StateIdle, "")
		}
		return nil
	case herdr.EventIdle:
		return report(herdr.StateIdle, "")
	case herdr.EventWorking:
		// A tool the user interrupted (Esc) ends the turn, or a batch sibling's
		// failure cancelled it mid-turn: neither says the chat works on, so the
		// state stands and a block stays for the transcript watch to settle.
		if payload.IsInterrupt {
			return nil
		}
		if interceptedSubmit(reporter.options.engine, payload) {
			return nil
		}
		return report(herdr.StateWorking, "")
	case herdr.EventAgentStart, herdr.EventAgentStop:
		return report(herdr.StateWorking, "")
	case herdr.EventBlocked:
		return reporter.toolBlock(payload, report(herdr.StateBlocked, blockedMessage(payload)))
	case herdr.EventNotification:
		switch payload.NotificationType {
		case "permission_prompt", "elicitation_dialog":
			return report(herdr.StateBlocked, blockedMessage(payload))
		case "idle_prompt":
			return report(herdr.StateIdle, "")
		}
		return nil
	case herdr.EventEnd:
		// /clear and resume swap the session inside the same process: the next
		// SessionStart reports the new session, so releasing now would blank
		// the pane between the two.
		if payload.Reason == herdrReasonClear || payload.Reason == herdrReasonResume {
			return nil
		}
		return &herdrAction{release: true, stored: herdrStored{Engine: engine, SessionID: payload.SessionID}}
	}
	return nil
}

// approvalPrefix opens the blocked message of an approval request that names
// its tool.
const approvalPrefix = "waiting for approval: "

func blockedMessage(payload herdrPayload) string {
	switch {
	case payload.Message != "":
		return payload.Message
	case payload.ToolName != "":
		return approvalPrefix + payload.ToolName
	}
	return "waiting for you"
}

// hostHasHerdr is the gate on every tmux write: no Herdr on the host, no
// store. The hook inherits HERDR_BIN_PATH from the shell that created the
// chat server or finds herdr on PATH; either proves a Herdr to report to.
func (reporter *herdrReporter) hostHasHerdr() bool {
	if reporter.deps.getenv(herdr.EnvBin) != "" {
		return true
	}
	if reporter.deps.lookPath == nil {
		return false
	}
	_, err := reporter.deps.lookPath("herdr")
	return err == nil
}

// run resolves the pane, then reports. A Codex event reports to the pane
// registered for its session (runCodex). Inside a pfm chat tmux server the
// pane belongs to the tmux CLIENT that attached from a Herdr pane, never to
// the process environment.
func (reporter *herdrReporter) run(ctx context.Context, payload herdrPayload) (returnErr error) {
	if reporter.deps.seatPaths != nil && reporter.deps.getenv("TMUX") != "" {
		// Durable bookkeeping must not suppress or consume the legacy bridge's deadline.
		defer func() {
			returnErr = errors.Join(returnErr, reporter.tmuxSeatLifecycle(ctx, payload))
		}()
	}
	if handled, err := reporter.nativeSeat(ctx, payload); handled {
		return err
	} else if err != nil {
		// A degraded store cannot identify a durable seat; keep the legacy report
		// visible as well as the diagnostic, just like the tmux bridge.
		defer func() { returnErr = errors.Join(returnErr, err) }()
	}
	if reporter.options.engine == pfmengine.Codex && reporter.firedByEngine() {
		return reporter.runCodex(ctx, payload)
	}
	reporter.tmuxOrigin()
	if reporter.options.socket == "" {
		return reporter.apply(ctx, payload, reporter.processTarget())
	}
	return reporter.runTmux(ctx, payload)
}

// runTmux reports through the chat's tmux server, whose socket
// options.socket names (tmux.Exec always names its socket).
func (reporter *herdrReporter) runTmux(ctx context.Context, payload herdrPayload) (returnErr error) {
	// Only a pfm chat server carries the theme; an engine in the user's own
	// tmux leaves that server unmarked.
	themeOn := reporter.themeOn()
	if !themeOn && !reporter.hostHasHerdr() {
		return nil
	}
	session, panePID, err := reporter.tmuxSession(ctx)
	if err != nil {
		return reporter.tmuxReadFailure(err)
	}
	reporter.session = session
	if reporter.firedByEngine() {
		if ok, err := reporter.paneEngineFired(panePID); err != nil || !ok {
			return err
		}
	}
	if themeOn && !reporter.hostHasHerdr() {
		reporter.themeState(ctx, payload)
	}
	if !reporter.hostHasHerdr() {
		return nil
	}
	if reporter.options.event == herdr.EventTitle {
		return reporter.title(ctx, session, payload)
	}
	unlock, err := reporter.lock(ctx, session)
	if err != nil {
		reporter.themeUnlessHeld(ctx, themeOn, payload, herdrStored{})
		return err
	}
	defer unlock()
	defer func() {
		returnErr = errors.Join(returnErr, reporter.syncTmuxEngineName(ctx, payload))
	}()
	state, err := reporter.readState(ctx, session)
	reporter.themeUnlessHeld(ctx, themeOn, payload, state)
	if err != nil {
		return err
	}
	switch reporter.options.event {
	case herdr.EventAttach:
		return reporter.attach(ctx, session, state)
	case herdr.EventSync:
		return reporter.sync(ctx, session, state)
	case herdr.EventDetach:
		return reporter.detach(ctx, session, state)
	case herdr.EventName:
		return reporter.name(ctx, session, state)
	}
	return reporter.event(ctx, session, state, payload)
}

func (reporter *herdrReporter) processTarget() herdrTarget {
	if reporter.deps.getenv(herdr.EnvMarker) != "1" {
		return herdrTarget{}
	}
	return herdrTarget{pane: reporter.deps.getenv(herdr.EnvPane), socket: reporter.deps.getenv(herdr.EnvSocket)}
}

func (reporter *herdrReporter) apply(ctx context.Context, payload herdrPayload, target herdrTarget) error {
	// An unproved/bare native Claude launch must never fall back to state
	// reporting. Durable native-seat proof is the metadata/release door.
	if reporter.nativeClaude() {
		return nil
	}
	if !target.ok() {
		return nil
	}
	action := reporter.plan(payload)
	if action == nil {
		return nil
	}
	return reporter.send(ctx, target, action, reporter.seq)
}

// tmuxSession returns the chat session and the pid of the pane process. A
// tmux hook names its session; an engine-fired event asks tmux about its pane.
func (reporter *herdrReporter) tmuxSession(ctx context.Context) (session, panePID string, err error) {
	if reporter.options.session != "" && !reporter.firedByEngine() {
		return reporter.options.session, "", nil
	}
	pane := reporter.tmuxPane
	if pane == "" {
		return "", "", fmt.Errorf("inside tmux with no session or pane to ask about")
	}
	output, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "display-message", "-p", "-t", pane, "#{session_name}\t#{pane_pid}",
	)
	if err != nil {
		return "", "", fmt.Errorf("read tmux session of %s: %w", pane, err)
	}
	name, panePID := strings.TrimRight(output, "\n"), ""
	if cut := strings.LastIndex(name, "\t"); cut >= 0 {
		name, panePID = name[:cut], name[cut+1:]
	}
	if reporter.options.session != "" {
		name = reporter.options.session
	}
	if name == "" {
		return "", "", fmt.Errorf("read tmux session of %s: empty session name", pane)
	}
	return name, panePID, nil
}

// firedByEngine reports whether an engine's hook (or plugin) fired the event,
// as against a tmux hook or the reporter's own re-bind: only an engine event
// can come from a nested engine of the same pane.
func (reporter *herdrReporter) firedByEngine() bool {
	return !reporter.options.event.FiredByTmux()
}

// paneEngineFired reports whether the engine that fired this hook is the
// engine running in the chat's pane, so that a `codex exec` or `claude -p`
// the chat's own Bash tool started (it inherits $TMUX and HERDR_*) stays
// silent. The walk goes up from the hook process, skipping shells and pfm
// itself: the first other process is the firing engine. Above it, a command
// wrapper (env, timeout, …) is skipped anywhere, and a node launcher is the
// engine's own while no shell or pfm sits between them (npm's codex runs its
// native binary under one); any other process is a second engine, the one
// whose tool started this one. The engine is the pane's own when no second
// engine sits between it and the pane process. A process table that cannot be
// read, a pane pid that cannot be parsed, or a hook that does not run under the
// pane is an error: never a silent skip, never a report.
func (reporter *herdrReporter) paneEngineFired(panePIDText string) (bool, error) {
	panePID, err := strconv.Atoi(strings.TrimSpace(panePIDText))
	if err != nil || panePID <= 0 {
		return false, fmt.Errorf(
			"read pane pid of the chat pane: %q is not a pid (skipped, cannot tell which engine fired)",
			panePIDText,
		)
	}
	if reporter.deps.procInfo == nil || reporter.deps.getpid == nil {
		return false, errors.New("no process table to tell which engine fired (skipped)")
	}
	engine, shellAbove := 0, false
	pid := reporter.deps.getpid()
	for depth := 0; depth < herdrMaxProcessDepth; depth++ {
		parent, command, err := reporter.deps.procInfo(pid)
		if err != nil {
			return false, fmt.Errorf("walk up from the hook to pane pid %d (skipped): %w", panePID, err)
		}
		plain := isShellOrPfm(command)
		ownEngine := plain || isCommandWrapper(command) || (isNodeLauncher(command) && !shellAbove)
		switch {
		case engine == 0 && !plain:
			engine = pid
		case engine != 0 && !ownEngine:
			// A second engine above the firing one: its tool started this one.
			return false, nil
		case engine != 0 && plain:
			shellAbove = true
		}
		if pid == panePID {
			return engine != 0, nil
		}
		if parent <= 1 || parent == pid {
			return false, fmt.Errorf(
				"walk up from the hook: not under the chat pane's pid %d (skipped, cannot tell which engine fired)",
				panePID,
			)
		}
		pid = parent
	}
	return false, fmt.Errorf(
		"walk up from the hook to pane pid %d: deeper than %d processes (skipped)",
		panePID,
		herdrMaxProcessDepth,
	)
}

// envCommand is env(1), the most common command wrapper.
const envCommand = "env"

// isCommandWrapper names the commands that run another command in their own
// process: a wrapper between an engine and its pane is not an engine.
func isCommandWrapper(command string) bool {
	switch filepath.Base(command) {
	case envCommand, "timeout", "gtimeout", "caffeinate", "script", "nice", "nohup", "stdbuf", "time":
		return true
	}
	return false
}

func isNodeLauncher(command string) bool {
	return filepath.Base(command) == "node"
}

func isShellOrPfm(command string) bool {
	base := filepath.Base(strings.TrimPrefix(command, "-"))
	switch base {
	case "sh", "bash", "zsh", "dash", "fish", "pfm":
		return true
	}
	return strings.HasPrefix(base, "pfm.")
}

// lock serializes every reporter of one chat session: a flock on a file in
// the pfm state directory, retried until the hook's deadline. The state read,
// compare, write and the requests to Herdr all happen inside it.
func (reporter *herdrReporter) lock(ctx context.Context, session string) (func(), error) {
	if reporter.deps.lockDir == "" {
		return nil, fmt.Errorf("lock session %s: no pfm state directory", session)
	}
	if session != filepath.Base(session) || session == "." || session == ".." {
		return nil, fmt.Errorf("lock session %q: not a plain session name", session)
	}
	if err := os.MkdirAll(reporter.deps.lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("lock session %s: create %s: %w", session, reporter.deps.lockDir, err)
	}
	path := filepath.Join(reporter.deps.lockDir, session+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock session %s: open %s: %w", session, path, err)
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, errors.Join(fmt.Errorf("lock session %s: flock %s: %w", session, path, err), file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(
				fmt.Errorf("lock session %s: %s still held at the deadline: %w", session, path, ctx.Err()),
				file.Close(),
			)
		case <-clock.Real.After(herdrLockPoll):
		}
	}
}

func (reporter *herdrReporter) readState(ctx context.Context, session string) (herdrStored, error) {
	var stored herdrStored
	output, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "show-options", "-qv", "-t", session, herdrStoredOption,
	)
	if err != nil {
		return stored, fmt.Errorf("read %s of %s: %w", herdrStoredOption, session, err)
	}
	raw := strings.TrimSpace(output)
	if raw == "" {
		return stored, nil
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return stored, fmt.Errorf("decode %s of %s (%q): %w", herdrStoredOption, session, raw, err)
	}
	return stored, nil
}

func (reporter *herdrReporter) writeState(ctx context.Context, session string, state herdrStored) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode stored state: %w", err)
	}
	if _, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "set-option", "-t", session, herdrStoredOption, string(encoded),
	); err != nil {
		return fmt.Errorf("store state in %s: %w", herdrStoredOption, err)
	}
	return nil
}

func (reporter *herdrReporter) engineLabel(state herdrStored) string {
	if state.Engine != "" {
		return state.Engine
	}
	return herdr.Label(reporter.options.engine)
}

// event stores one state change and reports it to every attached Herdr
// client, resolved to its current pane. A stored seq above this event's means
// a later event already wrote: this one is stale and changes nothing.
func (reporter *herdrReporter) event(
	ctx context.Context,
	session string,
	state herdrStored,
	payload herdrPayload,
) error {
	unlockBridge, err := reporter.lockBridge(ctx)
	if err != nil {
		return err
	}
	defer unlockBridge()
	action := reporter.plan(payload)
	if action == nil || state.Seq > reporter.seq || keepsStored(reporter.options.event, payload, state) {
		return nil
	}
	split, err := reporter.liveClients(ctx, session, &state, !action.release)
	if err != nil {
		return err
	}
	clients, moved, _, resolveErr := reporter.resolveClients(ctx, &state, action.release)
	split.markMoved(clients, moved)
	next := action.stored
	next.Seq = reporter.seq
	next.Name = state.Name
	next.Clients = state.Clients
	if err := reporter.writeState(ctx, session, next); err != nil {
		return err
	}
	errs := []error{split.errs, resolveErr, reporter.blankTitle(ctx, action), reporter.watch(session, next, payload)}
	errs = append(errs, reporter.releaseAll(ctx, reporter.engineLabel(state), split.gone))
	for _, pid := range sortedClientPIDs(clients) {
		client := clients[pid]
		errs = append(errs, reporter.send(
			ctx, clientTarget(pid, client), action, reporter.nextSent(),
		))
	}
	if !action.release {
		// A session start names every pane; any other event names only the
		// panes the chat just moved to, or every pane for a name they lack.
		named := clients
		if reporter.options.event != herdr.EventStart {
			named = movedClients(clients, moved)
		}
		errs = append(errs, reporter.nameSync(ctx, session, next, clientTargets(clients), clientTargets(named)))
	}
	return errors.Join(errs...)
}

// send writes the action to the pane's Herdr socket. A refused resume command
// costs Herdr the whole report, so the state is resent without it.
func (reporter *herdrReporter) send(ctx context.Context, target herdrTarget, action *herdrAction, seq uint64) error {
	if !target.ok() {
		return nil
	}
	allowed, err := reporter.preparePane(target, action.release)
	if err != nil || !allowed {
		return err
	}
	if !action.release {
		if err := reporter.rememberActivityTarget(target, seq); err != nil {
			return err
		}
	}
	label := action.stored.Engine
	if action.release {
		line, err := herdr.Release{Pane: target.pane, Agent: label, Seq: seq}.Line()
		if err != nil {
			return err
		}
		clearTitle, err := herdr.Metadata{Pane: target.pane, ClearTitle: true, Seq: seq + 1}.Line()
		if err != nil {
			return err
		}
		return releaseSettled(herdr.Send(ctx, target.socket, line, clearTitle))
	}
	report := herdr.Report{
		Pane: target.pane, Agent: label, State: herdr.State(action.stored.State),
		Message: action.stored.Message, SessionID: action.stored.SessionID, Seq: seq,
	}
	if report.SessionID != "" {
		argv, err := reporter.resumeArgv(report.SessionID)
		if err != nil {
			reporter.warn(
				"pfm internal herdr-state %s: no resume command for session %q (state still reported): %v",
				reporter.options.event,
				report.SessionID,
				err,
			)
		}
		report.ResumeArgv = argv
	}
	line, err := report.Line()
	if err != nil {
		return err
	}
	err = herdr.Send(ctx, target.socket, line)
	if err != nil && report.ResumeArgv != nil && herdr.RefusedResume(err) {
		reporter.warn(
			"pfm internal herdr-state %s: %v; resending the state without a resume command",
			reporter.options.event,
			err,
		)
		report.ResumeArgv = nil
		if line, err = report.Line(); err != nil {
			return err
		}
		err = herdr.Send(ctx, target.socket, line)
	}
	return err
}

// PayloadHook runs the Herdr reporter entries: herdr-state, which reads an
// engine's payload on stdin, and the shim's shell reporter herdr-shell. One
// dispatch branch in cmd/pfm serves both, which the dispatch line budget
// (arch C3) requires.
func PayloadHook(args []string, stdin io.Reader, stderr io.Writer) int {
	if args[0] == "herdr-shell" {
		return HerdrShell(args[1:], stderr)
	}
	return HerdrState(args[1:], stdin, stderr)
}
