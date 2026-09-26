package inject

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// SenderSessionEnv, SenderLabelEnv, and SenderIDEnv are how a chat states its
// own identity to a process that cannot derive one — the --then waiter, and
// any dispatcher a chat detaches from itself. Both implementations read the
// same three names, so a chat.sh waiter and a pfm waiter sign alike.
const (
	SenderSessionEnv = "CHAT_SENDER_SESSION"
	SenderLabelEnv   = "CHAT_SENDER_LABEL"
	SenderIDEnv      = "CHAT_SENDER_SID"
)

// Engine owns target resolution and the guarded tmux delivery sequence.
type Engine struct {
	resolver      Resolver
	names         NameResolver
	tmux          Tmux
	spawner       ThenSpawner
	options       Options
	whoami        SelfIdentifier
	codexSeat     SelfIdentifier
	binaries      map[pfmengine.ID]string
	accountEmojis []string
	recorder      func(context.Context, fleetdb.CommsEvent) error
	warningWriter io.Writer
	// requestIdentity is present only on an engine scoped to one validated
	// request caller. Raw pane ids use its socket instead of daemon ambient
	// state because pane ids are unique only within one tmux server.
	requestIdentity *resolve.Identity
	// env is Dependencies.Env, defaulting to paths.OSEnv{}.
	env paths.Env
	// senderSelf is this process's own identity, resolved at most once: the
	// session and id cannot change while we run. Its Label is final only
	// when it was handed to us (options.Sender, the stated environment);
	// with senderLive the label is read afresh for every delivery from
	// senderSeat, because a name CAN change while we run — chat_name renames
	// at will, and an MCP server lives for days.
	senderOnce sync.Once
	senderSelf Sender
	senderSeat resolve.Identity
	senderLive bool
}

// senderKey pins one delivery's sender in its context (withSender).
type senderKey struct{}

// withSender pins the sender for one delivery, so every footer, waiter
// environment and ledger row of that delivery names the same chat and the
// roster is asked once, not once per footer.
func withSender(ctx context.Context, sender Sender) context.Context {
	return context.WithValue(ctx, senderKey{}, sender)
}

// New constructs a jailed-path-aware injection engine.
func New(dependencies Dependencies) (*Engine, error) {
	binaries := resolve.Binaries{
		Values: map[pfmengine.ID]string{
			pfmengine.Claude:   dependencies.ClaudeBinary,
			pfmengine.Codex:    dependencies.CodexBinary,
			pfmengine.OpenCode: dependencies.OpenCodeBinary,
		},
		AccountEmojis: dependencies.AccountEmojis,
	}
	if dependencies.Resolver == nil {
		resolver, err := resolve.New(nil, binaries)
		if err != nil {
			return nil, err
		}
		dependencies.Resolver = resolver
	}
	if dependencies.Tmux == nil {
		dependencies.Tmux = TmuxInjector{}
	}
	if dependencies.Spawner == nil {
		dependencies.Spawner = CommandThenSpawner{}
	}
	if dependencies.WarningWriter == nil {
		dependencies.WarningWriter = os.Stderr
	}
	if dependencies.Env == nil {
		dependencies.Env = paths.OSEnv{}
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	options := withDefaults(dependencies.Options)
	applyEnvironment(&options, dependencies.Env)
	if options.BodyRoot == "" {
		options.BodyRoot = filepath.Join(resolved.Home, ".local", "state", "pfm", "inject-bodies")
	}
	// chat.sh:147 locks under ${TMPDIR:-/tmp}/chat-inject-locks. The Go engine
	// shares that namespace so a Go inject and a chat.sh inject into the same
	// pane mutually exclude instead of interleaving keystrokes.
	if options.LockRoot == "" {
		options.LockRoot = filepath.Join(tempRoot(dependencies.Env), "chat-inject-locks")
	}
	if options.ThenLogRoot == "" {
		options.ThenLogRoot = tempRoot(dependencies.Env)
	}
	if dependencies.Identifier == nil {
		identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{})
		if err != nil {
			return nil, err
		}
		dependencies.Identifier = identifier
	}
	return &Engine{
		resolver:      dependencies.Resolver,
		names:         dependencies.Names,
		tmux:          dependencies.Tmux,
		spawner:       dependencies.Spawner,
		options:       options,
		whoami:        dependencies.Identifier,
		codexSeat:     dependencies.CodexSeat,
		binaries:      cloneEngineBinaries(binaries.Values),
		accountEmojis: append([]string(nil), dependencies.AccountEmojis...),
		recorder:      dependencies.Recorder,
		warningWriter: dependencies.WarningWriter,
		env:           dependencies.Env,
	}, nil
}

// WithIdentity returns a request-scoped engine whose caller identity is the
// live seat supplied by the fleet registry. MCP's HTTP server is shared by
// every Codex thread, so the long-lived engine must never cache one caller's
// sender or use it to resolve another caller's "self" target.
//
// The engine is rebuilt field-by-field instead of copying Engine: senderOnce
// is a sync.Once and may already have been used by the shared engine. The
// scoped copy starts with a fresh sender cache and an explicit sender.
func (engine *Engine) WithIdentity(identity resolve.Identity, label string) *Engine {
	requestIdentity := identity
	sender := &Sender{
		Session: identity.Session,
		Label:   strings.TrimSpace(label),
		UUID:    identity.ID,
	}
	options := engine.options
	options.Sender = sender
	return &Engine{
		resolver:        engine.resolver,
		names:           engine.names,
		tmux:            engine.tmux,
		spawner:         engine.spawner,
		options:         options,
		whoami:          fixedIdentifier{identity: identity},
		binaries:        cloneEngineBinaries(engine.binaries),
		accountEmojis:   append([]string(nil), engine.accountEmojis...),
		recorder:        engine.recorder,
		warningWriter:   engine.warningWriter,
		requestIdentity: &requestIdentity,
		env:             engine.env,
	}
}

type fixedIdentifier struct {
	identity resolve.Identity
}

func (fixed fixedIdentifier) Identify(context.Context) (resolve.Identity, error) {
	return fixed.identity, nil
}

func cloneEngineBinaries(values map[pfmengine.ID]string) map[pfmengine.ID]string {
	cloned := make(map[pfmengine.ID]string, len(values))
	for id, binary := range values {
		cloned[id] = binary
	}
	return cloned
}

// tempRoot is chat.sh's ${TMPDIR:-/tmp}, the root both implementations share
// for inject locks and --then chain logs.
func tempRoot(env paths.Env) string {
	if value := env.Get("TMPDIR"); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "/tmp"
}

func applyEnvironment(options *Options, env paths.Env) {
	seconds := []struct {
		name   string
		target *time.Duration
	}{
		{"CHAT_INJECT_POLL", &options.Poll},
		{"CHAT_INJECT_ENTER_SETTLE", &options.EnterSettle},
		{"CHAT_INJECT_PROOF_SETTLE", &options.ProofSettle},
		{"CHAT_INJECT_LOCK_TIMEOUT", &options.LockTimeout},
		{"CHAT_INJECT_LOCK_POLL", &options.LockPoll},
		{"CHAT_INJECT_LOCK_MAXHOLD", &options.LockMaxHold},
		{"CHAT_INJECT_COMMAND_GAP", &options.CommandChunkGap},
		{"CHAT_INJECT_TYPIST_QUIET", &options.TypistQuiet},
		{"CHAT_THEN_MIN", &options.ThenMin},
		{"CHAT_THEN_IDLE_POLL", &options.ThenIdlePoll},
		{"CHAT_THEN_SETTLE", &options.ThenSettle},
	}
	for _, setting := range seconds {
		value := env.Get(setting.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil && parsed >= 0 {
			*setting.target = time.Duration(parsed * float64(time.Second))
		}
	}
	integers := []struct {
		name   string
		target *int
	}{
		{"CHAT_INJECT_BUSY_TRIES", &options.BusyTries},
		{"CHAT_INJECT_INTR_TRIES", &options.InterruptTries},
		{"CHAT_INJECT_STASH_TRIES", &options.StashTries},
		{"CHAT_INJECT_SETTLE_TRIES", &options.SettleTries},
		{"CHAT_INJECT_ENTER_TRIES", &options.EnterTries},
		{"CHAT_INJECT_SCROLLBACK", &options.Scrollback},
		{"CHAT_INJECT_PROOF_LINES", &options.ProofLines},
		{"CHAT_INJECT_COMMAND_CHUNK", &options.CommandChunkRunes},
		{"CHAT_THEN_BUSY_TRIES", &options.ThenBusyTries},
		{"CHAT_THEN_IDLE_TRIES", &options.ThenIdleTries},
		{"CHAT_THEN_IDLE_STABLE", &options.ThenIdleStable},
	}
	for _, setting := range integers {
		value := env.Get(setting.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			*setting.target = parsed
		}
	}
}

func withDefaults(options Options) Options {
	if options.Poll == 0 {
		options.Poll = 200 * time.Millisecond
	}
	if options.EnterSettle == 0 {
		options.EnterSettle = 400 * time.Millisecond
	}
	if options.ProofSettle == 0 {
		options.ProofSettle = 500 * time.Millisecond
	}
	if options.BusyTries == 0 {
		options.BusyTries = 5
	}
	if options.InterruptTries == 0 {
		options.InterruptTries = 8
	}
	if options.StashTries == 0 {
		options.StashTries = 4
	}
	if options.SettleTries == 0 {
		options.SettleTries = 40
	}
	if options.EnterTries == 0 {
		options.EnterTries = 12
	}
	if options.Scrollback == 0 {
		options.Scrollback = 300
	}
	if options.ProofLines == 0 {
		options.ProofLines = 15
	}
	if options.LockTimeout == 0 {
		options.LockTimeout = 30 * time.Second
	}
	if options.LockPoll == 0 {
		options.LockPoll = 100 * time.Millisecond
	}
	if options.LockMaxHold == 0 {
		options.LockMaxHold = 60 * time.Second
	}
	if options.TypistQuiet == 0 {
		options.TypistQuiet = 3 * time.Second
	}
	if options.ClaudeInlineMax == 0 {
		options.ClaudeInlineMax = ClaudeInlineMax
	}
	if options.CodexInlineMax == 0 {
		options.CodexInlineMax = CodexInlineMax
	}
	if options.CommandChunkRunes == 0 {
		options.CommandChunkRunes = CommandChunkRunes
	}
	if options.CommandChunkGap == 0 {
		options.CommandChunkGap = CommandChunkGap
	}
	if options.BodyMaxAge == 0 {
		options.BodyMaxAge = defaultBodyMaxAge
	}
	if options.Clock == nil {
		options.Clock = clock.Real
	}
	// chat.sh:1063-1077 __then cadence.
	if options.ThenMin == 0 {
		options.ThenMin = 1500 * time.Millisecond
	}
	if options.ThenBusyTries == 0 {
		options.ThenBusyTries = 25
	}
	if options.ThenIdlePoll == 0 {
		options.ThenIdlePoll = 400 * time.Millisecond
	}
	if options.ThenIdleTries == 0 {
		options.ThenIdleTries = 1500
	}
	if options.ThenIdleStable == 0 {
		options.ThenIdleStable = 3
	}
	if options.ThenSettle == 0 {
		options.ThenSettle = 400 * time.Millisecond
	}
	return options
}

// Resolve applies chat.sh's live target ladder.
func (engine *Engine) Resolve(ctx context.Context, name string) (Target, int, string, error) {
	return engine.resolve(ctx, name, "")
}

// ResolveEngine applies the same roster-first ladder within one explicit
// engine namespace. MCP's cxwin preview uses this so its roster rung cannot
// widen a Codex-window query into a same-named Claude target.
func (engine *Engine) ResolveEngine(
	ctx context.Context,
	name, requiredEngine string,
) (Target, int, string, error) {
	return engine.resolve(ctx, name, requiredEngine)
}

// ResolveKind applies the shared ladder while restricting the raw fallback to
// one requested namespace. The roster rung still runs first for every kind.
func (engine *Engine) ResolveKind(
	ctx context.Context,
	name string,
	kind resolve.Kind,
	requiredEngine string,
) (Target, int, string, error) {
	return engine.resolveWithOptions(ctx, name, resolve.LadderOptions{
		RequiredEngine: requiredEngine,
		Kinds:          []resolve.Kind{kind},
	})
}

func (engine *Engine) resolve(
	ctx context.Context,
	name, requiredEngine string,
) (Target, int, string, error) {
	return engine.resolveWithOptions(ctx, name, resolve.LadderOptions{RequiredEngine: requiredEngine})
}

func (engine *Engine) resolveWithOptions(
	ctx context.Context,
	name string,
	options resolve.LadderOptions,
) (Target, int, string, error) {
	var roster resolve.RosterResolver
	if engine.names != nil {
		roster = resolve.RosterFunc(func(
			ctx context.Context,
			name, requiredEngine string,
		) (resolve.Seat, int, string, error) {
			target, code, detail, err := engine.names.ResolveName(ctx, name, requiredEngine)
			return seatFromTarget(target), code, detail, err
		})
	}
	seat, code, detail, err := (resolve.Ladder{
		Roster: roster, Raw: engine.resolver, Self: engine.whoami,
		CodexSelf: engine.codexSeat, RequestIdentity: engine.requestIdentity,
		Env: engine.env, Session: engine.tmux,
	}).Resolve(ctx, name, options)
	return targetFromSeat(seat), code, detail, err
}

func seatFromTarget(target Target) resolve.Seat {
	return resolve.Seat{
		SocketPath: target.SocketPath, Pane: target.Pane, Engine: target.Engine,
		Name: target.Name, ID: target.ID, Session: target.Session,
	}
}

func targetFromSeat(seat resolve.Seat) Target {
	return Target{
		SocketPath: seat.SocketPath, Pane: seat.Pane, Engine: seat.Engine,
		Name: seat.Name, ID: seat.ID, Session: seat.Session,
	}
}

// Capture resolves and captures a pane without mutating it.
func (engine *Engine) Capture(
	ctx context.Context,
	name string,
	tailLines int,
) (Target, string, int, string, error) {
	target, code, detail, err := engine.Resolve(ctx, name)
	if err != nil || code != 0 {
		return target, "", code, detail, err
	}
	// chat.sh:1215-1219 captures with -S - : the WHOLE retained scrollback, not
	// just the visible fold, bounded only by tmux's history-limit. The caller's
	// last_n / max_bytes bounds are applied to the result, after the capture.
	capture, err := engine.tmux.Capture(
		ctx,
		target.SocketPath,
		target.Pane,
		false,
		FullScrollback,
	)
	if err != nil {
		if pfmtmux.CouldNotRun(err) {
			// tmux itself never started (missing binary, bad configured
			// socket dir) — a probe that could not run, never a pane that
			// answered dead. Folding this into CodeDead would tell the
			// caller a live chat's pane is gone when the truth is "could
			// not look"; the cause rides in detail since this door's err
			// return stays nil for every prior caller (mcpserv among them).
			return target, "", CodeCaptureFailed, fmt.Sprintf(
				"could not run tmux to capture %q: %v", target.Pane, err,
			), nil
		}
		return target, "", CodeDead, "target pane is dead or unreadable", nil
	}
	if tailLines > 0 {
		capture = lastNonEmptyLines(capture, tailLines)
	}
	return target, capture, 0, "", nil
}

// ScheduleAfterCurrentTurn arms a detached steer chain without typing into
// the current composer. Slash commands queued while a Codex model turn is
// running become ordinary model input rather than TUI commands; the detached
// waiter must therefore wait for idle before it types the primary command.
func (engine *Engine) ScheduleAfterCurrentTurn(
	ctx context.Context,
	request Request,
) (Result, error) {
	ctx = withSender(ctx, engine.sender(ctx))
	if request.Message == "" {
		return refused(CodeUndelivered, "refusing to schedule an empty message"), nil
	}
	if result, ok := engine.checkSteerChain(request); !ok {
		return result, nil
	}
	target, code, detail, err := engine.Resolve(ctx, request.Target)
	if err != nil {
		return Result{}, err
	}
	if code != 0 {
		return refused(code, detail), nil
	}
	if _, captureErr := engine.capture(ctx, target, 0); captureErr != nil {
		return refused(CodeDead, "target pane is dead or unreadable"), nil
	}
	then := request.Then
	steers := make([]string, 0, len(then)+1)
	steers = append(steers, request.Message)
	steers = append(steers, then...)
	logPath := engine.steerLogPath(target)
	// The check and the arming are ONE step, under the pane's own inject lock.
	// refuseIfArmed only READS the armed record; the matching write happens
	// later, inside spawner.Spawn (armRecord), so two schedules whose reads
	// both landed before either write both saw an unarmed pane and both
	// spawned a waiter — armRecord's "leave a live arming alone" branch
	// suppresses the second RECORD, never the second PROCESS, and two waiters
	// then race one pane and one O_TRUNC log. The lock is the same one a live
	// inject holds while it types (lockTarget), and it is released as soon as
	// the record is down and the waiter is running.
	lock, lockRefusal := engine.lockTarget(ctx, target)
	if lockRefusal != "" {
		return refused(CodeUndelivered, lockRefusal), nil
	}
	defer lock.release()
	if result, ok := engine.refuseIfArmed(target, request, logPath); !ok {
		return result, nil
	}
	// Only the ORIGINAL self-compaction needs the caller's turn ridden
	// out. A chained re-arm is typed into a pane this waiter already
	// watched settle, so its next busy is the steer's own turn.
	selfTarget := !request.Chain && isSelfTarget(request.Target)
	if err := engine.spawner.Spawn(ctx, SteerSpawn{
		SocketPath: target.SocketPath,
		Target:     target.Pane,
		Engine:     target.Engine,
		Steers:     steers,
		LogPath:    logPath,
		Append:     request.Chain,
		Sender:     engine.sender(ctx),
		SelfTarget: selfTarget,
	}); err != nil {
		return refused(
			CodeUndelivered,
			fmt.Sprintf("could not schedule command after the current turn: %v", err),
		), nil
	}
	engine.announceArmed(ctx, target, request, selfTarget)
	message := fmt.Sprintf(
		"scheduled COMMAND into %q after the current turn settles — %d post-command steer(s) armed (log: %s)",
		target.Pane,
		len(then),
		logPath,
	)
	if isSelfCompactRequest(request) {
		message += SelfCompactStopNotice
	}
	return Result{
		Status:     "scheduled",
		Code:       0,
		Message:    message,
		SocketPath: target.SocketPath,
		Pane:       target.Pane,
		Steers:     len(then),
		SteerLog:   logPath,
	}, nil
}

// ScheduleSelfCompact composes and schedules a self-compaction the ONE way,
// shared by every caller — the chat_self_compact MCP tool and
// `pfm chat self-compact` alike. Nobody else composes "/compact ".
func (engine *Engine) ScheduleSelfCompact(
	ctx context.Context,
	focus string,
	then []string,
) (result Result, err error) {
	states := trail(ctx, "self-compact")
	defer func() { outcome(states, result, err) }()
	focus = strings.TrimSpace(focus)
	// The full control-character class, not just \r\n\x00: ESC, BEL, and
	// the rest of C0/DEL are the same threat class (an injected control
	// byte typed as a real keypress) this diff's own typist/mash guards
	// exist to police elsewhere, and unicode.IsControl is what makes this
	// check match its own doc comment below (and SelfCompactInput's, in
	// mcpserv/types.go) word for word: non-empty, single line, no control
	// characters — one canonical rule, stated once, enforced here.
	if focus == "" || strings.ContainsFunc(focus, unicode.IsControl) {
		return refused(CodeUndelivered, "focus must be one non-empty line"), nil
	}
	target, code, detail, err := engine.Resolve(ctx, "self")
	if err != nil {
		return Result{}, err
	}
	if code != 0 {
		return refused(code, detail), nil
	}
	// focus is validated above (single line, non-empty, no control characters),
	// which is exactly what makes it safe to concatenate onto the slash
	// command. isHarnessCommand only checks for a leading "/", so the composed
	// string still routes through the paced-literal command transport.
	//
	// Codex is the exception, and it is an ASSUMPTION HELD, not one disproved:
	// an earlier investigation recorded that Codex accepts no inline arguments
	// on /compact. Nothing in this repo re-tests that — TESTPLAN's /compact
	// rows are all Claude jail tests — so the claim stands until a real Codex
	// composer says otherwise, and the focus is composed only where the target
	// is NOT known to be Codex.
	message := "/compact " + focus
	if target.Engine == string(pfmengine.Codex) {
		message = "/compact"
	}
	return engine.ScheduleAfterCurrentTurn(ctx, Request{
		Target:  "self",
		Message: message,
		Then:    then,
	})
}

// isSelfCompactRequest is true for the one shape the stop rule applies to: a
// chat compacting ITSELF. For any other target the waiter is watching somebody
// else's pane, so what this caller does next cannot blur the turn boundary.
func isSelfCompactRequest(request Request) bool {
	return isCompactCommand(request.Message) && isSelfTarget(request.Target)
}

// isSelfTarget reports the one target spelling that names the calling chat's
// own pane.
func isSelfTarget(target string) bool {
	return strings.EqualFold(strings.TrimSpace(target), "self")
}

// SelfCompactStopNotice rides on the SUCCESS result of a self-compaction,
// where it is read in the same breath as the decision it governs. The --then
// waiter recognises the compaction turn by watching this pane yield and then go
// busy again (waitForSettledTurn); a caller that keeps working after queueing
// the compaction merges its own turn into the compaction's and leaves the
// waiter no boundary to find.
const SelfCompactStopNotice = " — STOP NOW: end this turn without running " +
	"another tool. Say only that compaction is queued with its steer to " +
	"follow. Any further work here competes with the compaction the waiter " +
	"is watching for, and the steer will land beside it instead of after it."

// Inject performs the delivery and records a successful direct send without
// making the recipient pay for a ledger failure.
//
// A /compact primary is refused here, before checkSteerChain or any resolve
// step runs: /compact ends a turn at an idle prompt with none fired, so
// typing it live races whatever the target's operator is doing right now
// (the 2026-09-03 self-compact that ate an operator's live draft) — compaction belongs to
// chat_self_compact / `pfm chat self-compact`, both of which wait for the
// target's own turn to end first. The internal inject() this delegates to
// still accepts a /compact primary when Chain is true — that is how
// ScheduleAfterCurrentTurn's detached waiter (DeliverThen) delivers one.
func (engine *Engine) Inject(ctx context.Context, request Request) (result Result, err error) {
	return engine.injectRequest(ctx, request, nil)
}

// InjectTarget performs one delivery against an already-resolved immutable
// seat. Context-scoped MCP mutations use it so a split pane is never resolved
// again through an aggregate session or name.
func (engine *Engine) InjectTarget(
	ctx context.Context,
	target Target,
	request Request,
) (result Result, err error) {
	return engine.injectRequest(ctx, request, &target)
}

func (engine *Engine) injectRequest(
	ctx context.Context,
	request Request,
	resolved *Target,
) (result Result, err error) {
	states := trail(ctx, "inject")
	defer func() { outcome(states, result, err) }()
	ctx = withSender(ctx, engine.sender(ctx))
	if isCompactCommand(request.Message) {
		return refused(
			CodeUndelivered,
			"ABORT: /compact is never injected — compaction is chat_self_compact "+
				"(MCP) or `pfm chat self-compact --then '<steer>' '<focus>'` (CLI); "+
				"both wait for the target's own turn to end and never type over a "+
				"human. Nothing was typed.",
		), nil
	}
	if resolved == nil {
		result, err = engine.inject(ctx, request)
	} else {
		if request.Message == "" {
			return refused(CodeUndelivered, "refusing to inject an empty message"), nil
		}
		if checked, ok := engine.checkSteerChain(request); !ok {
			return checked, nil
		}
		result, err = engine.injectResolved(ctx, request, *resolved, "")
	}
	if err != nil || result.Code != 0 || !result.Typed || request.Origin != "" || engine.recorder == nil {
		return result, err
	}
	sender := engine.sender(ctx)
	event := fleetdb.CommsEvent{
		AtNS:           engine.options.Clock.Now().UnixNano(),
		Kind:           fleetdb.KindInject,
		SenderSession:  sender.Session,
		SenderLabel:    sender.Label,
		SenderUUID:     sender.UUID,
		Target:         request.Target,
		ReceiverSocket: result.SocketPath,
		ReceiverPane:   result.Pane,
		Message:        request.Message,
	}
	if recordErr := engine.recorder(ctx, event); recordErr != nil {
		engine.warnf("pfm: comms ledger: %v\n", recordErr)
	}
	return result, nil
}

// warnf reports a non-fatal failure on the configured writer, stderr when
// none was configured — a nil writer must never turn a warning into a panic.
func (engine *Engine) warnf(format string, args ...any) {
	writer := engine.warningWriter
	if writer == nil {
		writer = os.Stderr
	}
	fmt.Fprintf(writer, format, args...)
}

// inject performs the full guard/type/submit/proof transaction.
func (engine *Engine) inject(ctx context.Context, request Request) (Result, error) {
	if request.Message == "" {
		return refused(CodeUndelivered, "refusing to inject an empty message"), nil
	}
	if result, ok := engine.checkSteerChain(request); !ok {
		return result, nil
	}

	target, code, detail, err := engine.Resolve(ctx, request.Target)
	if err != nil {
		return Result{}, err
	}
	if code != 0 {
		return refused(code, detail), nil
	}
	return engine.injectResolved(ctx, request, target, detail)
}

func (engine *Engine) injectResolved(
	ctx context.Context,
	request Request,
	target Target,
	detail string,
) (Result, error) {
	base := Result{
		Status:         "refused",
		SocketPath:     target.SocketPath,
		Pane:           target.Pane,
		ResolutionNote: detail,
	}
	lock, lockRefusal := engine.lockTarget(ctx, target)
	if lockRefusal != "" {
		base.Code = CodeUndelivered
		base.Message = lockRefusal
		return base, nil
	}
	defer lock.release()

	capture, err := engine.capture(ctx, target, 0)
	if err != nil {
		base.Code = CodeDead
		base.Message = "target pane is dead or unreadable"
		return base, nil
	}
	command, commandErr := engine.tmux.PaneCommand(ctx, target.SocketPath, target.Pane)
	verifiedEngine := ""
	if commandErr == nil {
		verifiedEngine = paneCommandEngine(command, engine.binaries)
		if verifiedEngine != "" {
			target.Engine = verifiedEngine
		}
	}
	// Busy is read only AFTER the pane's own process names the engine: the
	// three TUIs render three different footers (IsBusyFor).
	paneEngine := pfmengine.ID(target.Engine)
	base.Busy = IsBusyFor(paneEngine, capture)
	// Both TUIs own a safe composer queue while a turn is running. A normal
	// inject types there and submits without interrupting the active turn;
	// force-now alone is allowed to send Escape. pane_current_command is NOT a
	// queue gate: tmux reports the foreground descendant, so a legitimate chat
	// reads as node/python/sleep while one of its tools owns the foreground.
	queueing := base.Busy && !request.ForceNow
	if base.Busy && request.ForceNow {
		for attempt := 0; attempt < engine.options.InterruptTries; attempt++ {
			if !IsBusyFor(paneEngine, capture) {
				break
			}
			if err := engine.tmux.SendKey(
				ctx,
				target.SocketPath,
				target.Pane,
				"Escape",
			); err != nil {
				return Result{}, err
			}
			base.Interrupted = true
			engine.sleepContext(ctx, engine.options.Poll)
			capture, err = engine.capture(ctx, target, 0)
			if err != nil {
				base.Code = CodeDead
				base.Message = "target pane died while interrupting"
				return base, nil
			}
		}
	} else if base.Busy && !queueing {
		for attempt := 0; attempt < engine.options.BusyTries; attempt++ {
			engine.sleepContext(ctx, engine.options.Poll)
			capture, err = engine.capture(ctx, target, 0)
			if err != nil {
				base.Code = CodeDead
				base.Message = "target pane died while waiting for idle"
				return base, nil
			}
			if !IsBusyFor(paneEngine, capture) {
				base.Busy = false
				break
			}
		}
		if base.Busy {
			base.Code = CodeBusy
			base.Message = "ABORT: target pane is busy; nothing was typed (retry when idle or use force_now)"
			return base, nil
		}
	}

	// Claude's agents panel can hold the keyboard under an empty composer. One
	// Escape hands focus back without touching the turn; a panel that keeps it is
	// refused by name, never typed into.
	if agentPanelFocused(capture) {
		if err := engine.tmux.SendKey(ctx, target.SocketPath, target.Pane, "Escape"); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.Poll)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			base.Code = CodeDead
			base.Message = "target pane died while leaving the agents panel"
			return base, nil
		}
		if agentPanelFocused(capture) {
			base.Code = CodeUndelivered
			base.Message = fmt.Sprintf(
				"ABORT: %q has its agents panel focused and Escape did not return focus to the composer; nothing was typed",
				target.Pane,
			)
			return base, nil
		}
	}

	// A human at the keyboard is not a safe queue surface, busy or idle: C-s
	// below protects a PARKED draft, never a live keystroke, and the second
	// Enter this engine used to send is exactly what let an operator's next
	// keystroke land as a submitted message once the first Enter had already
	// cleared the composer (the 2026-09-03 self-compact that ate an operator's live draft). ForceNow
	// alone bypasses this — the same override that allows the Escape
	// interrupt above.
	if !request.ForceNow {
		last, typing, activityErr := engine.tmux.ClientActivity(
			ctx,
			target.SocketPath,
			target.Pane,
		)
		if activityErr != nil {
			base.Code = CodeUndelivered
			base.Status = "undelivered"
			base.Message = fmt.Sprintf(
				"ABORT: could not read who is at %q (tmux list-clients: %v); nothing was typed",
				target.Pane,
				activityErr,
			)
			return base, nil
		}
		// client_activity moves on focus events, mouse reports and the
		// terminal's own query replies as well as keystrokes, so an attached
		// VS Code tab reads as "typing" without end. A human mid-sentence
		// leaves a draft in the composer; an empty composer with recent
		// activity is a watched pane, not a typed one.
		if typing && hasDraft(lastComposerLine(capture)) {
			if quiet := engine.options.Clock.Now().Sub(last); quiet < engine.options.TypistQuiet {
				base.Code = CodeBusy
				base.Status = "typing"
				base.Message = fmt.Sprintf(
					"ABORT: a human is typing in %q (last keystroke %s ago) — the pane is busy with its operator; nothing was typed. Retry once the composer has been quiet for %s, or pass force_now to override.",
					target.Pane,
					quiet.Round(time.Second),
					engine.options.TypistQuiet,
				)
				return base, nil
			}
		}
	}

	if inMode, modeErr := engine.tmux.PaneInMode(
		ctx,
		target.SocketPath,
		target.Pane,
	); modeErr == nil && inMode {
		if err := engine.tmux.CancelCopyMode(ctx, target.SocketPath, target.Pane); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.Poll)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			base.Code = CodeDead
			base.Message = "target pane died while leaving copy mode"
			return base, nil
		}
	}
	if strings.Contains(strings.ToLower(capture), "restore the code") ||
		strings.Contains(strings.ToLower(captureLastLines(capture, 12)), "create a plan?") {
		if err := engine.tmux.SendKey(
			ctx,
			target.SocketPath,
			target.Pane,
			"Escape",
		); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.Poll)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			base.Code = CodeDead
			base.Message = "target pane died while dismissing an overlay"
			return base, nil
		}
	}
	// Overlay dismissal can put tmux back into copy mode. Re-check at the last
	// possible moment before any composer key, matching the first cancellation
	// rather than assuming the overlay Escape left a writable pane.
	if inMode, modeErr := engine.tmux.PaneInMode(
		ctx,
		target.SocketPath,
		target.Pane,
	); modeErr == nil && inMode {
		if err := engine.tmux.CancelCopyMode(ctx, target.SocketPath, target.Pane); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.Poll)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			base.Code = CodeDead
			base.Message = "target pane died while leaving copy mode after overlay dismissal"
			return base, nil
		}
	}

	if menu := SelectorLine(capture); menu != "" {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"ABORT: %q has an OPEN selector menu (marker on a numbered option: %s); nothing was typed",
			target.Pane,
			menu,
		)
		return base, nil
	}

	// Idle panes take the ordinary C-s mash guard. A busy pane with an empty
	// composer is already a safe queue surface and receives no control key;
	// only an actual busy draft is stashed before the new turn is queued.
	// The "stashed and restored on submit" semantics this guard leans on are
	// not documented by Claude Code — they are pinned empirically, against a
	// real process, by the PINNED OBSERVATIONS comment atop
	// claude_stash_real_test.go's TestRealClaudeStashSemantics.
	needsStashGuard := !queueing
	if queueing && hasDraft(lastComposerLine(capture)) {
		styled, _ := engine.tmux.Capture(
			ctx,
			target.SocketPath,
			target.Pane,
			true,
			0,
		)
		needsStashGuard = !isDimPlaceholder(lastComposerLine(styled))
	}
	if needsStashGuard {
		if err := engine.tmux.SendKey(
			ctx,
			target.SocketPath,
			target.Pane,
			"C-s",
		); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.Poll)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			base.Code = CodeDead
			base.Message = "target pane died during draft guard"
			return base, nil
		}
		base.DraftStashed = strings.Contains(
			strings.ToLower(captureLastLines(capture, 8)),
			"stashed",
		)
		draftLine := lastComposerLine(capture)
		for attempt := 0; attempt < engine.options.StashTries && hasDraft(draftLine); attempt++ {
			styled, _ := engine.tmux.Capture(
				ctx,
				target.SocketPath,
				target.Pane,
				true,
				0,
			)
			if isDimPlaceholder(lastComposerLine(styled)) {
				break
			}
			if err := engine.tmux.SendKey(
				ctx,
				target.SocketPath,
				target.Pane,
				"C-s",
			); err != nil {
				return Result{}, err
			}
			engine.sleepContext(ctx, engine.options.Poll)
			capture, err = engine.capture(ctx, target, 0)
			if err != nil {
				break
			}
			if strings.Contains(strings.ToLower(captureLastLines(capture, 8)), "stashed") {
				base.DraftStashed = true
			}
			draftLine = lastComposerLine(capture)
		}
		if hasDraft(draftLine) {
			styled, _ := engine.tmux.Capture(
				ctx,
				target.SocketPath,
				target.Pane,
				true,
				0,
			)
			if !isDimPlaceholder(lastComposerLine(styled)) {
				base.Code = CodeUndelivered
				base.Message = fmt.Sprintf(
					"ABORT: %q composer holds a draft that will not stash (mash guard): %.80s; nothing was typed and the draft is preserved (dim placeholder text is exempt)",
					target.Pane,
					strings.TrimSpace(strings.TrimLeft(draftLine, "❯›")),
				)
				return base, nil
			}
		}
	}

	preSubmitCapture := capture
	prepared, prepareErr := engine.prepareLiveMessage(
		ctx,
		target,
		request.Message,
		base.Interrupted,
	)
	if prepareErr != nil {
		base.Code = CodeUndelivered
		if errors.Is(prepareErr, ErrUnsigned) {
			// Nothing was sent, so nothing is unsigned: the flag describes what
			// the RECIPIENT got, and the recipient got nothing.
			base.Message = prepareErr.Error()
			return base, nil
		}
		base.Message = fmt.Sprintf("could not persist long inject body: %v", prepareErr)
		return base, nil
	}
	message := prepared.Message
	base.Unsigned = prepared.Unsigned
	base.AutoFilePath = prepared.AutoFilePath
	commandTransport := isHarnessCommand(request.Message)
	// pasteTransport now covers both shapes that need byte-safe bracketed
	// paste: an explicit --file body (any size, unconditionally — a literal
	// newline in send-keys -l submits the composer early) and an ordinary
	// message over the engine's inline threshold, which prepareMessage used
	// to spill to a file pointer before this ladder ever saw it — see its
	// own comment. prepared.AutoFilePath is therefore always empty here for
	// a live delivery; it is populated below ONLY as a rescue, if this
	// attempt cannot be proven.
	pasteTransport := !commandTransport &&
		(request.FileBacked || utf8.RuneCountInString(message) > engine.inlineThreshold(target.Engine))
	var sendErr error
	switch {
	case pasteTransport:
		sendErr = engine.tmux.SendPaste(
			ctx,
			target.SocketPath,
			target.Pane,
			message,
		)
	case commandTransport:
		base.LiteralChunks, sendErr = engine.sendPacedLiteral(
			ctx,
			target,
			message,
			lock,
		)
	default:
		sendErr = engine.tmux.SendLiteral(
			ctx,
			target.SocketPath,
			target.Pane,
			message,
		)
	}
	if sendErr != nil {
		return Result{}, sendErr
	}
	base.Typed = true
	normalized := normalizeSpace(message)
	needle := tailRunes(normalized, 40)
	for attempt := 0; attempt < engine.options.SettleTries; attempt++ {
		if beatErr := lock.beat(); beatErr != nil {
			return lockLost(base, target.Pane, beatErr), nil
		}
		capture, err = engine.capture(ctx, target, 0)
		if err == nil && (strings.Contains(normalizeSpace(capture), needle) ||
			(pasteTransport && HasPastePlaceholder(capture))) {
			break
		}
		engine.sleepContext(ctx, engine.options.Poll)
	}
	// Render-settle is advisory only. Echo detection flakes on wrapping,
	// bracketed-paste placeholders, and footer glyphs; once literal bytes have
	// been typed we must always reach the bounded Enter loop. Returning here
	// would strand a typed-but-unsent body and make every retry mash another
	// message into the same composer.

	prefix := headRunes(normalizeSpace(message), 24)
	submitted := false
	for attempt := 1; attempt <= engine.options.EnterTries; attempt++ {
		base.SubmitRetries = attempt
		if beatErr := lock.beat(); beatErr != nil {
			return lockLost(base, target.Pane, beatErr), nil
		}
		if err := engine.tmux.SendKey(
			ctx,
			target.SocketPath,
			target.Pane,
			"Enter",
		); err != nil {
			return Result{}, err
		}
		engine.sleepContext(ctx, engine.options.EnterSettle)
		capture, err = engine.capture(ctx, target, 0)
		if err != nil {
			continue
		}
		// Confirmation needs POSITIVE evidence, never a blind re-Enter: a
		// re-Enter is warranted ONLY when this LIVE capture proves the
		// composer STILL holds the message (its prefix, or an unexpanded
		// paste placeholder, visibly still sitting there). An unreadable or
		// blank composer row is NOT evidence anything is still there — a
		// large body can legitimately scroll its own "❯"/"›" marker off the
		// visible viewport while it (or its post-submit echo) keeps
		// rendering, and re-reading a deeper scrollback capture here used
		// to find only the ORIGINAL pre-submit line (still holding the
		// placeholder) sitting further back in history, so a genuinely
		// confirmed submission could never confirm no matter how many
		// Enters were sent (F3 of the merge-gating review — the real
		// jailed 1MB bracketed-paste round trip). Never re-add an
		// unconditional second Enter here: the loop already retries
		// exactly when — and only when — the capture proves the message is
		// still sitting in the composer.
		input := lastComposerLine(capture)
		if input != "" &&
			(HasPastePlaceholder(input) || strings.Contains(normalizeSpace(input), prefix)) {
			continue
		}
		submitted = true
		break
	}
	if !submitted {
		base.Status = "typed_unconfirmed"
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"typed text into %q but could not confirm submission after %d Enter attempts",
			target.Pane,
			engine.options.EnterTries,
		)
		if pasteTransport {
			if rescuePath, note := engine.pasteRescue(target, request, prepared); note != "" {
				base.AutoFilePath = rescuePath
				base.Message += " — " + note
			}
		}
		return base, nil
	}

	// chat.sh:922-951 — the steer chain is armed ONLY here, after the primary
	// submit is confirmed. Our lock is released first so the detached waiter is
	// never blocked by us; it takes its own per-target lock when it delivers.
	if len(request.Then) > 0 {
		lock.release()
		base.SteerLog = engine.steerLogPath(target)
		if err := engine.spawner.Spawn(ctx, SteerSpawn{
			SocketPath: target.SocketPath,
			Target:     target.Pane,
			Engine:     target.Engine,
			Steers:     request.Then,
			LogPath:    base.SteerLog,
			Append:     request.Chain,
			// Resolved HERE, in the chat, because the waiter cannot: it is
			// detached from us and derives nothing.
			Sender: engine.sender(ctx),
		}); err != nil {
			base.Status = "delivered"
			base.Code = CodeUndelivered
			base.Message = fmt.Sprintf(
				"delivered the primary into %q but could NOT arm the %d then steer(s): %v — deliver them yourself once the pane is idle",
				target.Pane,
				len(request.Then),
				err,
			)
			base.Proof = lastNonEmptyLines(capture, engine.options.ProofLines)
			return base, nil
		}
		base.Steers = len(request.Then)
	}

	engine.sleepContext(ctx, engine.options.ProofSettle)
	proof, proofErr := engine.capture(ctx, target, 0)
	if proofErr != nil {
		base.Status = "delivered_unproven"
		base.Code = 0
		base.Message = fmt.Sprintf(
			"delivered-unproven into %q — input cleared, but the post-submit pane capture failed: %v",
			target.Pane,
			proofErr,
		)
		base.Proof = fmt.Sprintf("[pane capture failed: %v]", proofErr)
		if pasteTransport {
			if rescuePath, note := engine.pasteRescue(target, request, prepared); note != "" {
				base.AutoFilePath = rescuePath
				base.Message += " — " + note
			}
		}
		return base, nil
	}
	proofInput := lastComposerLine(proof)
	if proofInput == "" {
		scrollback, _ := engine.capture(ctx, target, engine.options.Scrollback)
		proofInput = lastComposerLine(scrollback)
	}
	blindProof := proofInput == ""
	if HasPastePlaceholder(proofInput) ||
		(proofInput != "" && strings.Contains(normalizeSpace(proofInput), prefix)) {
		base.Status = "typed_unconfirmed"
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"PROOF-CONTRADICTION: %q composer still holds the message",
			target.Pane,
		)
		base.Proof = lastNonEmptyLines(proof, engine.options.ProofLines)
		if pasteTransport {
			if rescuePath, note := engine.pasteRescue(target, request, prepared); note != "" {
				base.AutoFilePath = rescuePath
				base.Message += " — " + note
			}
		}
		return base, nil
	}
	base.Status = "delivered"
	if queueing {
		base.Status = "queued"
	}
	base.Code = 0
	switch {
	case queueing && commandTransport:
		base.Message = fmt.Sprintf(
			"queued COMMAND into %q — busy %s accepted %d paced literal chunk(s) without interruption (Enter confirmed, input cleared)",
			target.Pane,
			injectedEngineName(target.Engine),
			base.LiteralChunks,
		)
	case queueing && request.FileBacked:
		base.Message = fmt.Sprintf(
			"queued FILE-BACKED into %q — busy %s accepted the bracketed-paste block without interruption (Enter confirmed, input cleared)",
			target.Pane,
			injectedEngineName(target.Engine),
		)
	case queueing && pasteTransport:
		base.Message = fmt.Sprintf(
			"queued PASTE into %q — busy %s accepted the bracketed-paste block without interruption (Enter confirmed, input cleared)",
			target.Pane,
			injectedEngineName(target.Engine),
		)
	case queueing:
		base.Message = fmt.Sprintf(
			"queued into %q — busy %s accepted the turn without interruption (Enter confirmed, input cleared)",
			target.Pane,
			injectedEngineName(target.Engine),
		)
	case commandTransport:
		base.Message = fmt.Sprintf(
			"injected LIVE COMMAND into %q — %d paced literal chunk(s) submitted (Enter confirmed, input cleared)",
			target.Pane,
			base.LiteralChunks,
		)
	case request.FileBacked:
		base.Message = fmt.Sprintf(
			"injected LIVE FILE-BACKED into %q — bracketed-paste body submitted (Enter confirmed, input cleared)",
			target.Pane,
		)
	case pasteTransport:
		base.Message = fmt.Sprintf(
			"injected LIVE PASTE into %q — bracketed-paste body submitted (Enter confirmed, input cleared)",
			target.Pane,
		)
	default:
		base.Message = fmt.Sprintf(
			"injected LIVE into %q — typed inline and submitted (Enter confirmed, input cleared)",
			target.Pane,
		)
	}
	if base.Interrupted {
		base.Message += " — FORCE-NOW: interrupted the target's running flow"
	}
	if base.DraftStashed {
		base.Message += " — stashed and restored the target's unsent draft"
	}
	if base.Steers > 0 {
		base.Message += fmt.Sprintf(
			" — %d then steer(s) queued; deliver in order, one settled turn apart (log: %s)",
			base.Steers,
			base.SteerLog,
		)
	}
	for _, warning := range prepared.Warnings {
		base.Message += " — WARNING: " + warning
	}
	base.Proof = lastNonEmptyLines(proof, engine.options.ProofLines)
	if !deliveryProven(paneEngine, preSubmitCapture, proof, message, queueing, pasteTransport) {
		base.Status = "delivered_unproven"
		base.Message = fmt.Sprintf(
			"delivered-unproven into %q — input cleared, but the pane capture below shows neither the message past the input bar nor the expected %s",
			target.Pane,
			proofExpectation(queueing),
		)
		if pasteTransport {
			if rescuePath, note := engine.pasteRescue(target, request, prepared); note != "" {
				base.AutoFilePath = rescuePath
				base.Message += " — " + note
			}
		}
	}
	if blindProof {
		base.Message += " (proof caveat: no composer line was visible in capture or scrollback; submission was confirmed, but the screen proof is blind)"
	}
	return base, nil
}

// pasteRescue is the auto-file fallback for the one case paste transport
// genuinely cannot serve: a bracketed-paste delivery that could not be
// proven. It never redelivers — retyping a different message into a
// composer already left in an unknown state would be worse than the
// original failure — it only preserves request.Message byte-exact to the
// canonical auto-file store, the SAME store and pruning prepareMessage's own
// resume-path spill uses, so the caller (and whoever reads the transcript
// later) has a durable, honestly-reported copy to recover from instead of
// silently losing the text. A persist failure is folded into the returned
// note rather than swallowed, per this repo's own error-handling rule.
func (engine *Engine) pasteRescue(target Target, request Request, prepared PreparedMessage) (path, note string) {
	if prepared.AutoFilePath != "" {
		// Never actually true on the live path today — prepareMessage no
		// longer spills there (see its own comment) — kept as a guard
		// against double-rescuing if a future caller shape changes that.
		return "", ""
	}
	name := filepath.Base(target.SocketPath)
	if target.Pane != "" {
		name += "-" + target.Pane
	}
	stored, warnings, err := engine.persistBody(request.Message, name)
	if err != nil {
		return "", fmt.Sprintf(
			"AUTO-FILE RESCUE FAILED: could not preserve the unproven body to a rescue file: %v",
			err,
		)
	}
	note = fmt.Sprintf("AUTO-FILE RESCUE: the full body was preserved at %s — read it fully", stored)
	for _, warning := range warnings {
		note += " — WARNING: " + warning
	}
	return stored, note
}

func injectedEngineName(value string) string {
	id, err := pfmengine.Parse(value)
	if err != nil {
		return fmt.Sprintf("engine %q", value)
	}
	return pfmengine.MustLookup(id).Short
}

func (engine *Engine) sendPacedLiteral(
	ctx context.Context,
	target Target,
	message string,
	lock *targetLock,
) (int, error) {
	runes := []rune(message)
	chunks := 0
	for start := 0; start < len(runes); start += engine.options.CommandChunkRunes {
		end := min(start+engine.options.CommandChunkRunes, len(runes))
		if err := lock.beat(); err != nil {
			return chunks, fmt.Errorf("refresh inject lock while typing command chunk %d: %w", chunks+1, err)
		}
		if err := engine.tmux.SendLiteral(
			ctx,
			target.SocketPath,
			target.Pane,
			string(runes[start:end]),
		); err != nil {
			return chunks, err
		}
		chunks++
		if end < len(runes) {
			engine.sleepContext(ctx, engine.options.CommandChunkGap)
		}
	}
	return chunks, nil
}

func paneCommandEngine(command string, binaries map[pfmengine.ID]string) string {
	name := filepath.Base(strings.TrimSpace(command))
	for _, id := range pfmengine.All() {
		descriptor := pfmengine.MustLookup(id)
		configured := binaries[id]
		if name == descriptor.Binary || configured != "" && name == filepath.Base(configured) {
			return string(id)
		}
	}
	// Claude's version-managed native executable is named like 2.1.47. This
	// is the same process-name seam the live resolver already recognizes.
	dot := strings.IndexByte(name, '.')
	if dot <= 0 {
		return ""
	}
	for _, character := range name[:dot] {
		if character < '0' || character > '9' {
			return ""
		}
	}
	return string(pfmengine.Claude)
}

// checkSteerChain applies chat.sh:596-625 before anything is resolved or
// typed: a bad chain must die at the caller, not later in a detached waiter's
// log where nobody is watching.
func (engine *Engine) checkSteerChain(request Request) (Result, bool) {
	for _, steer := range request.Then {
		if strings.TrimSpace(steer) == "" {
			return refused(
				CodeUndelivered,
				"ERROR: a then steer must be non-empty",
			), false
		}
		if isCompactCommand(steer) {
			return refused(
				CodeUndelivered,
				"ERROR: a then steer must not itself start with /compact — compact-steering-into-compact recurses and loses the thread",
			), false
		}
	}
	if !isCompactCommand(request.Message) {
		return Result{}, true
	}
	// EVERY /compact inject must carry a steer (operator rule): compaction
	// returns to an idle prompt — no turn fires — so a steerless compact
	// strands the target command-less (chat.sh:591-611).
	if len(request.Then) == 0 {
		return refused(
			6,
			"ABORT: a /compact inject requires exactly one then steer — compaction ends at an idle prompt with no turn fired, stranding the target. Use chat_self_compact{focus, then:'<post-compact steer>'} or `pfm chat self-compact --then '<steer>' '<focus>'` instead. Nothing was typed.",
		), false
	}
	return Result{}, true
}

func (engine *Engine) capture(
	ctx context.Context,
	target Target,
	scrollback int,
) (string, error) {
	return engine.tmux.Capture(
		ctx,
		target.SocketPath,
		target.Pane,
		false,
		scrollback,
	)
}

// signedMessage appends chat.sh's mandatory sender footer (chat.sh:628-668).
// The second return reports the UNSIGNED fallback, so the caller can say the
// message went out unsigned instead of leaving the absence silent.
func (engine *Engine) signedMessage(
	ctx context.Context,
	message string,
	interrupted bool,
) (string, bool) {
	if isHarnessCommand(message) || engine.options.DisableSignature {
		return message, false
	}
	marker := ""
	if interrupted {
		marker = " — ⚠ FORCE-DELIVERED via Esc (your running flow was interrupted; re-check any in-progress action)"
	}
	sender := engine.sender(ctx)
	parts := signatureParts(sender)
	if len(parts) == 0 {
		// chat.sh:648-657 — every derivation source came back empty. Dropping
		// the footer silently makes an unsigned message indistinguishable from
		// a signed one at the recipient, so the absence is STATED instead.
		return message + marker + "  — " + unsignedFooter(sender), true
	}
	return message + marker + "  — " + strings.Join(parts, " · "), false
}

// SignForResume applies the same identity policy as a live send, but uses a
// block footer because transcript injection is JSON text rather than terminal
// keystrokes. Slash commands remain byte-exact.
func (engine *Engine) SignForResume(ctx context.Context, message string) (string, bool) {
	if isHarnessCommand(message) || engine.options.DisableSignature {
		return message, false
	}
	sender := engine.sender(ctx)
	parts := signatureParts(sender)
	if len(parts) == 0 {
		return message + "\n\n— " + unsignedFooter(sender), true
	}
	return message + "\n\n— " + strings.Join(parts, " · "), false
}

// signatureParts builds the footer a recipient reads to know who spoke and
// how to answer. Two rules it got wrong for a long time are load-bearing here.
//
// The reply hint advertises the LABEL whenever there is one, and the sid
// otherwise — never the tmux session. A chat knows names; the fleet's
// resolver turns them into panes (ResolveRosterName: the exact name, or an
// id prefix of eight or more characters — the same eight the sid part shows).
// The session is a socket file name: it used to be the fallback here, and a
// server whose first capture showed no statusline yet stamped it on every
// message for a day, so peers replied to a socket, which landed in the
// comms ledger as the target and minted a cosmos node named after it. A
// sender with neither a label nor an id has nothing a peer could resolve,
// and says so (unsignedFooter) instead of offering its socket.
//
// There is no 🔖 part any more, and its absence is deliberate rather than an
// omission. 🔖 is the STATUSLINE's identity marker, and naming.BookmarkLabelFor
// scrapes a pane's own label by finding the last 🔖 line on screen. Stamping
// it into a delivered message wrote the SENDER's identity into the
// RECIPIENT's pane, where the scraper could read it back as the recipient's
// own label — a chat's identity becoming whatever the last message it
// received claimed its sender was called. The reply hint above already names
// the sender, so nothing is lost by not re-stating it under a marker that
// means something else.
func signatureParts(sender Sender) []string {
	parts := make([]string, 0, 2)
	if sender.UUID != "" {
		parts = append(parts, "sid "+headRunes(sender.UUID, 8))
	}
	if address := replyAddress(sender); address != "" {
		parts = append(
			parts,
			naming.DeliveredFooterMarker+address+" <message>",
		)
	}
	return parts
}

// unquoteTarget strips one layer of surrounding double quotes from a target.
//
// It is the read side of the reply hint's write side: a label containing
// spaces is advertised as chat_inject "Delivery Trust" <message>, because the
// CLI form needs the shell quoting to see one argument. A recipient reaching
// for the MCP tool instead passes the target as a JSON string, where those
// quotes are just two extra characters that would make the label match
// nothing. Accepting both spellings costs one trim; refusing one of them
// would make the hint wrong for whichever caller read it the other way.
func unquoteTarget(name string) string {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) >= 2 &&
		strings.HasPrefix(trimmed, `"`) &&
		strings.HasSuffix(trimmed, `"`) {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	return trimmed
}

// replyAddress is the one string a recipient can pass straight back to
// chat_inject. A label with whitespace is quoted, because a codex chat's
// label is its tmux WINDOW name and those legitimately contain spaces —
// unquoted, "chat_inject Delivery Trust <message>" reads as a target plus a
// stray word. Quoting is safe for the label's other hazard too: ':' is tmux's
// session:window separator, but this is a chat_inject argument and never a
// tmux target, and inject.Resolve hands a label to the label resolver, which
// answers with a %pane id.
//
// Without a label the address is the sid — the same eight characters the
// footer's sid part shows, which the roster resolves as an id prefix. It is
// never the session: see signatureParts.
func replyAddress(sender Sender) string {
	if sender.Label != "" {
		if strings.ContainsFunc(sender.Label, unicode.IsSpace) {
			return `"` + sender.Label + `"`
		}
		return sender.Label
	}
	return headRunes(sender.UUID, 8)
}

// unsignedFooter states what was TRIED, never what happens to be unset. Reading
// $TMUX told a codex chat sitting in a live pane it had "no tmux context" —
// true of the variable, false of the chat, since a scrubbed tool shell never
// carries one and ANCESTRY is what resolves the handle there. The resolved
// sender is the honest witness.
func unsignedFooter(sender Sender) string {
	footer := "UNSIGNED — sender identity underivable"
	if sender.Session == "" {
		footer += "; no tmux session (ambient or via ancestry)"
	} else if sender.Label == "" {
		footer += "; no name for this seat in the fleet roster or on its statusline"
	}
	if sender.UUID == "" {
		footer += "; no session id"
	}
	return footer
}

// sender answers who WE are. The identity — session and id — is resolved at
// most once per process: the caller's override (tests, the MCP surface), then
// the STATED sender, then live derivation from this process. The LABEL of a
// derived sender is read afresh per delivery (pinned by withSender at each
// door), never remembered: one MCP server serves a chat for days, and a
// label cached at its first delivery outlived a blank first screen AND every
// rename after it — LUNA:ORCHESTRATOR signed twenty-three messages as its
// socket because its server's first capture, seconds after a reload, showed
// no statusline yet.
//
// The stated rung exists because derivation is only possible where the chat
// is. A --then waiter is spawned with setsid, which severs the process chain
// ancestry recovery walks; a codex tool shell carries no $TMUX and no session
// id of its own (codex scrubs its command environment), so a codex-origin
// waiter has NOTHING left to derive from and every steer it delivered went out
// UNSIGNED. The origin resolves identity while it still can and hands it down,
// which is why this is also resolved for a `/`-prefixed primary: that message
// is exempt from signing, but its steers are not.
func (engine *Engine) sender(ctx context.Context) Sender {
	if pinned, ok := ctx.Value(senderKey{}).(Sender); ok {
		return pinned
	}
	engine.senderOnce.Do(func() {
		if engine.options.Sender != nil {
			engine.senderSelf = *engine.options.Sender
			return
		}
		if stated, ok := statedSender(engine.env); ok {
			engine.senderSelf = stated
			return
		}
		engine.senderSelf, engine.senderSeat, engine.senderLive = engine.detectSender(ctx)
	})
	sender := engine.senderSelf
	if engine.senderLive {
		sender.Label = engine.senderLabel(ctx, engine.senderSeat)
	}
	return sender
}

// statedSender reads the identity a spawning chat handed this process. It is
// read from our OWN environment only — never from a message or a caller flag,
// so a chat can state who IT is and never who somebody else is.
func statedSender(env paths.Env) (Sender, bool) {
	stated := Sender{
		Session: env.Get(SenderSessionEnv),
		Label:   env.Get(SenderLabelEnv),
		UUID:    env.Get(SenderIDEnv),
	}
	if stated.Session == "" && stated.Label == "" && stated.UUID == "" {
		return Sender{}, false
	}
	return stated, true
}

// detectSender derives this chat's own identity the way chat.sh does — from
// its own process, never from a caller flag. The tmux handle comes from
// $TMUX, or from ANCESTRY RECOVERY when the engine spawned our shell without
// passing tmux context through (the codex path), which is what left every
// codex-origin message unsigned (chat.sh:65-96).
//
// The label is NOT derived here. What comes back is the identity the label
// is read from, and whether it is live at all: a sender with no seat has no
// label to read and signs by its session id alone.
func (engine *Engine) detectSender(ctx context.Context) (Sender, resolve.Identity, bool) {
	sender := Sender{UUID: engine.env.Get("CLAUDE_CODE_SESSION_ID")}
	identity, err := engine.whoami.Identify(ctx)
	if (err != nil || identity.Session == "") &&
		engine.codexSeat != nil &&
		engine.env.Get(resolve.CodexThreadEnv) != "" &&
		engine.env.Get(resolve.ClaudeSessionEnv) == "" {
		identity, err = engine.codexSeat.Identify(ctx)
	}
	if err != nil || identity.Session == "" {
		return sender, resolve.Identity{}, false
	}
	if sender.UUID == "" && identity.Engine == string(pfmengine.Codex) {
		sender.UUID = identity.ID
	}
	if identity.ID == "" {
		identity.ID = sender.UUID
	}
	sender.Session = identity.Session
	return sender, identity, true
}

// senderLabel answers what this chat is CALLED, in the order a peer's reply
// resolves it: the fleet roster first — the exact-name rung of
// ResolveRosterName, asked through the same NameResolver the target went
// through — then this chat's own 🔖 statusline, then chat.sh's codex fallback
// (chat.sh:104-121): a codex chat has no 🔖 statusline, so its label is the
// tmux window name — the human thread name set by pfm — returned BARE, since
// the recipient must be able to reply by exactly the label the operator gave
// the chat. A roster that fails to answer is reported, then read around: a
// screen is a weaker witness than the registry, but it is not none.
func (engine *Engine) senderLabel(
	ctx context.Context,
	identity resolve.Identity,
) string {
	if namer, ok := engine.names.(SenderNamer); ok {
		name, found, err := namer.SenderName(ctx, identity)
		if err != nil {
			engine.warnf("pfm: sender label: %v\n", err)
		} else if found {
			return name
		}
	}
	target := identity.Session
	if identity.Pane != "" {
		target = identity.Pane
	}
	capture, err := engine.tmux.Capture(
		ctx,
		identity.SocketPath,
		target,
		false,
		0,
	)
	if err == nil {
		if label := naming.BookmarkLabelFor(capture, engine.accountEmojis); label != "" {
			return label
		}
	}
	if identity.Engine != string(pfmengine.Codex) {
		return ""
	}
	window, err := engine.tmux.WindowName(ctx, identity.SocketPath, target)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(window)
}

func targetFromParts(socketPath, pane string) Target {
	return targetFromSeat(resolve.SeatFromParts(socketPath, pane, paths.OSEnv{}))
}

func refused(code int, message string) Result {
	return Result{Status: "refused", Code: code, Message: message}
}

// lockLost fills base for a delivery whose heartbeat could no longer prove
// it still holds the target's lock (F9): it stops typing rather than risk
// interleaving keystrokes with whoever stole the lock, and reports the loss
// under its own code instead of the generic CodeUndelivered.
func lockLost(base Result, pane string, err error) Result {
	base.Status = "lock_lost"
	base.Code = CodeLockLost
	base.Message = fmt.Sprintf(
		"stopped delivering into %q: %v",
		pane,
		err,
	)
	return base
}

func normalizeSpace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func headRunes(value string, count int) string {
	runes := []rune(value)
	if len(runes) > count {
		runes = runes[:count]
	}
	return string(runes)
}

func tailRunes(value string, count int) string {
	runes := []rune(value)
	if len(runes) > count {
		runes = runes[len(runes)-count:]
	}
	return string(runes)
}

func captureLastLines(value string, count int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}

// sleepContext waits duration or until ctx is cancelled, through the
// engine's own Clock seam (nil defaults to clock.Real), so a test driving
// clock.NewFake advances every retry loop here without a real sleep.
func (engine *Engine) sleepContext(ctx context.Context, duration time.Duration) {
	if duration <= 0 {
		return
	}
	_ = engine.options.Clock.Sleep(ctx, duration)
}

// captureLabel reads this chat's own 🔖 label through naming, the one package
// that owns that scrape (K3).
func captureLabel(capture string) string {
	return naming.BookmarkLabel(capture)
}
