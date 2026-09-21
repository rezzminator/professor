// Package reload reboots one Claude seat in its existing tmux pane.
package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Usage leads with the flags because a caller who guesses is guessing
// at a POSITION, and a positional slot that only accepts a bare integer is the
// one shape a model reading "reload the cache off" will fill with the words it
// was given. Every meaning now has a flag with a name on it.
//
// --sock is deliberately last and documented as the exception: with no --sock
// the command finds the CALLER'S OWN pane by itself, so asking for it is the
// unusual case, not the normal one.
//
// It is exported (not a cmd/pfm local const) because the installer folds it
// verbatim into the `/reload` slash command's own description — the picker
// shows the human exactly the flags this package's Run understands, never a
// hand-maintained restatement that can drift from them.
const Usage = "usage: pfm chat reload [--account N] [--model M] [--effort E] [--1h on|off] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n" +
	"       with no --sock, the calling chat's own pane is detected automatically;\n" +
	"       --hide (with --new) hides the conversation left behind from the picker"

type Pane struct {
	ID          string
	Dead        bool
	CurrentPath string
	TTY         string
	PID         int
}

type Tmux interface {
	ListPanes(context.Context, string) ([]Pane, error)
	SetRemain(context.Context, string, string, bool) error
	PaneInMode(context.Context, string, string) (bool, error)
	CancelMode(context.Context, string, string) error
	Capture(context.Context, string, string) (string, error)
	SendKey(context.Context, string, string, string) error
	SendLiteral(context.Context, string, string, string) error
	Respawn(context.Context, string, string, string, string) error
	Display(context.Context, string, string, string) error
}

type Process interface {
	PIDs() ([]int, error)
	Cmdline(int) ([]string, error)
	Environ(int) (map[string]string, error)
	Stat(int) (gather.ProcStat, error)
}

type Request struct {
	Engine      pfmengine.ID
	SocketPath  string
	Pane        string
	PanePID     int
	SessionID   string
	Transcript  string
	CWD         string
	Account     int
	AccountIDs  []int
	CodexHome   string
	CodexBinary string
	CodexYolo   bool
	Cache1H     bool
	Then        string
	// Name is the display name the chat wore before a --new reboot; the
	// reborn pane takes it over and the abandoned session is relabelled
	// (followName). "" carries nothing — a chat named from its prompts.
	Name string
	// Model and Effort pin the reborn seat's tier — the same pair
	// HeadlessRequest carries for a fresh launch (internal/action/headless.go).
	// "" means "inherit whatever the CLI/account would have chosen on its
	// own"; neither is validated here, only carried — claudeRun and codexRun
	// validate against the engine's own roster right before rendering it,
	// because only there is the engine known.
	Model  string
	Effort string
	// Home and Machine are the Claude respawn's whole policy: the account's
	// config dir, its autonomy posture and its system-prompt choice all come
	// from them through action.ClaudeSpawn. A reload used to synthesize its
	// own launch line here, and a rebooted chat silently lost the fleet's
	// configured system prompt because this constructor never knew about one.
	Home    string
	Machine pfmconfig.Config
}

type Options struct {
	Home        string
	SIDDir      string
	ClaudeRoots []string
	Delay       time.Duration
	Poll        time.Duration
	ExitTries   int
	IdleTries   int
	ThenTries   int
	// Clock is the time seam every wait crosses; nil defaults to clock.Real.
	Clock clock.Clock
}

type Result struct {
	Account int
	Cache1H bool
	// New reports whether the reborn seat started a brand-new session id
	// (--new was requested, or no transcript existed yet to resume).
	New bool
}

func (o *Options) defaults() {
	if o.Delay < 0 {
		o.Delay = 0
	} else if o.Delay == 0 {
		o.Delay = 1500 * time.Millisecond
	}
	if o.Poll < 0 {
		o.Poll = 0
	} else if o.Poll == 0 {
		o.Poll = time.Second
	}
	if o.ExitTries == 0 {
		o.ExitTries = 20
	}
	if o.IdleTries == 0 {
		o.IdleTries = 120
	}
	if o.ThenTries == 0 {
		o.ThenTries = 900
	}
	if o.Clock == nil {
		o.Clock = clock.Real
	}
}

// LockPath is the pane mutex Run holds for the whole reboot — from before the
// old process is sent /exit until the reborn one is up. The file persists;
// the flock on it is the signal.
func LockPath(sidDir, socketName, pane string) string {
	return filepath.Join(sidDir, "."+socketName+"."+pane+".reloadlock")
}

// InFlight reports whether a reload currently holds the pane mutex, so a
// SessionEnd hook can tell a reload's /exit (the pane is being rebooted —
// leave its terminal alone) from a human's. A missing lock file is a plain
// "no"; a lock that cannot be probed is an error, never a "no".
func InFlight(sidDir, socketName, pane string) (inFlight bool, returnErr error) {
	lock, err := os.OpenFile(LockPath(sidDir, socketName, pane), os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open reload lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close reload lock %s: %w", LockPath(sidDir, socketName, pane), err),
			)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, fmt.Errorf("probe reload lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		return false, fmt.Errorf("release reload lock probe: %w", err)
	}
	return false, nil
}

// Run performs the graceful in-place reboot. The caller has already resolved
// the target identity and account/cache birth values; this package owns every
// tmux mutation and the pane lock.
func Run(
	ctx context.Context,
	request Request,
	options Options,
	tmux Tmux,
	proc Process,
	stderr io.Writer,
) (result Result, err error) {
	// The state door: each phase below is one transition; a failure is attributed to its phase.
	trail := obs.NewTrail(ctx, "reload", "requested")
	defer func() { trail.End(err) }()
	options.defaults()
	if request.SocketPath == "" || request.Pane == "" {
		return Result{}, errors.New("reload requires a socket and pane")
	}
	if !rosterContains(request.AccountIDs, request.Account) {
		return Result{}, fmt.Errorf("account %d is not in the configured roster", request.Account)
	}
	run, err := engineRun(request)
	if err != nil {
		return Result{}, err
	}
	if run == "" {
		if descriptor, lookupErr := pfmengine.Lookup(request.Engine); lookupErr == nil {
			return Result{}, fmt.Errorf("%s does not support in-place reload", descriptor.Short)
		}
		return Result{}, fmt.Errorf("engine %q does not support in-place reload", request.Engine)
	}
	if stderr == nil {
		stderr = io.Discard
	}
	lockPath := LockPath(options.SIDDir, filepath.Base(request.SocketPath), request.Pane)
	if err := os.MkdirAll(options.SIDDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create reload lock directory: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("open reload lock: %w", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: close pane mutex: %v\n", err)
		}
	}()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Result{}, errors.New("another reload of this pane is already in flight")
		}
		return Result{}, fmt.Errorf("lock reload pane: %w", err)
	}
	defer func() {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: unlock pane mutex: %v\n", err)
		}
	}()
	if tmux == nil {
		return Result{}, errors.New("reload requires a tmux client")
	}
	trail.Reach("locked", "pane mutex held")

	if options.Delay > 0 {
		if err := options.Clock.Sleep(ctx, options.Delay); err != nil {
			return Result{}, err
		}
	}
	if err := tmux.SetRemain(ctx, request.SocketPath, request.Pane, true); err != nil {
		return Result{}, fmt.Errorf("set pane remain-on-exit: %w", err)
	}
	clearRemain := true
	defer func() {
		if clearRemain {
			if err := tmux.SetRemain(context.Background(), request.SocketPath, request.Pane, false); err != nil {
				fmt.Fprintf(stderr, "pfm chat reload: clear remain-on-exit after failure: %v\n", err)
			}
		}
	}()
	mode, err := tmux.PaneInMode(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return Result{}, fmt.Errorf("read pane mode: %w", err)
	}
	if mode {
		if err := tmux.CancelMode(ctx, request.SocketPath, request.Pane); err != nil {
			return Result{}, fmt.Errorf("cancel pane mode: %w", err)
		}
	}
	capture, err := waitCallerIdle(ctx, request, options, tmux, stderr)
	if err != nil {
		return Result{}, err
	}
	trail.Reach("idle", "caller turn ended")
	if selectorOpen(capture) {
		cause := errors.New("open selector menu on the pane — refusing to /exit")
		abort := "reload ABORTED — answer the open menu first, then reload again"
		if displayErr := tmux.Display(ctx, request.SocketPath, request.Pane, abort); displayErr != nil {
			return Result{}, errors.Join(cause, fmt.Errorf("display selector refusal: %w", displayErr))
		}
		return Result{}, cause
	}
	if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "C-s"); err != nil {
		return Result{}, fmt.Errorf("stash pane draft: %w", err)
	}
	if err := tmux.SendLiteral(ctx, request.SocketPath, request.Pane, "/exit"); err != nil {
		return Result{}, fmt.Errorf("send /exit: %w", err)
	}
	if err := waitExitRendered(ctx, request, options.Clock, tmux, stderr); err != nil {
		return Result{}, err
	}
	if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
		return Result{}, fmt.Errorf("submit /exit: %w", err)
	}
	trail.Reach("exit-typed", "/exit submitted")

	dead := false
	empties := 0
	dialogSeen := false
	for i := 0; i < options.ExitTries; i++ {
		panes, listErr := tmux.ListPanes(ctx, request.SocketPath)
		switch {
		case listErr != nil:
			return Result{}, fmt.Errorf("check pane exit state: %w", listErr)
		case len(panes) == 0:
			empties++
			dead = empties >= 3
		default:
			empties = 0
			for _, pane := range panes {
				dead = dead || pane.ID == request.Pane && pane.Dead
			}
		}
		if dead {
			break
		}
		capture, captureErr := tmux.Capture(ctx, request.SocketPath, request.Pane)
		switch {
		case captureErr != nil:
			fmt.Fprintf(stderr, "pfm chat reload: confirm /exit submission (try %d): %v\n", i+1, captureErr)
		case exitDialogOpen(capture):
			// Claude Code answers /exit with a confirmation whenever the chat
			// has background work — a scheduled task, a background shell, a
			// sub-agent — with "Exit and stop tasks" preselected. Nothing the
			// composer shows says /exit any more, so this dialog, not the
			// composer, is what the retry has to press Enter on. The reboot
			// IS the answer: everything in-flight dies with the pane anyway.
			if !dialogSeen {
				dialogSeen = true
				fmt.Fprintln(
					stderr,
					"pfm chat reload: confirming the exit dialog — background work stops with the chat",
				)
			}
			if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
				return Result{}, fmt.Errorf("confirm exit dialog: %w", err)
			}
		case composerShowsExit(capture):
			if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
				return Result{}, fmt.Errorf("retry /exit submission: %w", err)
			}
		}
		if err := sleepPoll(ctx, options.Clock, options.Poll); err != nil {
			return Result{}, err
		}
	}
	if !dead {
		return Result{}, exitIncomplete(ctx, request, options, tmux)
	}
	trail.Reach("dead", "pane exited")
	if err := tmux.Respawn(ctx, request.SocketPath, request.Pane, request.CWD, run); err != nil {
		return Result{}, fmt.Errorf("respawn pane: %w", err)
	}
	if err := tmux.SetRemain(ctx, request.SocketPath, request.Pane, false); err != nil {
		return Result{}, fmt.Errorf("clear pane remain-on-exit: %w", err)
	}
	clearRemain = false
	trail.Reach("respawned", "pane respawned")
	if request.Then != "" {
		if request.Transcript != "" {
			if info, statErr := os.Stat(request.Transcript); statErr == nil {
				if options.ThenTries == 900 {
					options.ThenTries = min(900, 90+30*int(info.Size()/1048576))
				}
			}
		}
		if err := deliverThen(ctx, request, options, tmux, proc, stderr); err != nil {
			return Result{}, errors.Join(err, failThen(ctx, request, options.SIDDir, tmux, err.Error()))
		}
		trail.Reach("then-delivered", "--then delivered")
	}
	if request.SessionID == "" {
		followName(ctx, request, options, tmux, proc, stderr)
	}
	return Result{Account: request.Account, Cache1H: request.Cache1H, New: request.SessionID == ""}, nil
}

func waitExitRendered(ctx context.Context, request Request, clk clock.Clock, tmux Tmux, stderr io.Writer) error {
	for attempt := 0; attempt < 40; attempt++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: confirm typed /exit (try %d): %v\n", attempt+1, err)
		} else if composerShowsExit(capture) {
			return nil
		}
		if err := clk.Sleep(ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	cause := errors.New("typed /exit never rendered — refusing blind Enter")
	if err := clearTypedExit(ctx, request, tmux); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// clearTypedExit backspaces the "/exit" this worker itself just typed
// (SendLiteral, over a composer C-s had already stashed empty) back out of
// the composer, so a refusal to reboot never leaves that stray text sitting
// there for a human to notice — or, worse, accidentally submit — then
// confirms the composer actually cleared. Shared by waitExitRendered (never
// confirmed rendering, so pressing Enter would be blind) and exitIncomplete
// (rendered, then the pane never died on it).
func clearTypedExit(ctx context.Context, request Request, tmux Tmux) error {
	for range len("/exit") {
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "BSpace"); err != nil {
			return fmt.Errorf("the typed /exit could NOT be cleared from the composer — clear it by hand: %w", err)
		}
	}
	capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return fmt.Errorf("sent backspaces over the typed /exit but could not confirm the composer is clear: %w", err)
	}
	if composerShowsExit(capture) {
		return errors.New("the typed /exit could NOT be cleared from the composer — clear it by hand")
	}
	return nil
}

// waitCallerIdle holds the /exit until the pane's current turn has ended.
// The worker is spawned from INSIDE the chat's own turn — the Bash call that
// scheduled it — so the first thing it sees is that turn still rendering. A
// /exit typed into a running turn cuts the chat's last words off mid-render
// and kills whatever tool that turn still has in flight; the contract every
// caller was given is "one short line, then end the turn", and this is the
// worker keeping its half of it. Two quiet captures in a row are the idle
// proof; a chat still busy at the bound is left untouched and told so.
func waitCallerIdle(
	ctx context.Context,
	request Request,
	options Options,
	tmux Tmux,
	stderr io.Writer,
) (string, error) {
	stable := 0
	announced := false
	sawComposer := false
	for attempt := 0; attempt < options.IdleTries; attempt++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			return "", fmt.Errorf("capture pane before /exit: %w", err)
		}
		showsComposer := composerDrawn(capture)
		sawComposer = sawComposer || showsComposer
		switch {
		case inject.IsBusy(capture):
			stable = 0
			if !announced {
				announced = true
				fmt.Fprintln(stderr, "pfm chat reload: the chat's turn is still running — holding /exit until it ends")
				announcePane(
					ctx, request, tmux, stderr, "pfm reload: waiting for this turn to end, then rebooting this chat",
				)
			}
		case !showsComposer:
			// A pane with no input box on it is not an idle chat — it is a
			// chat whose TUI has not drawn one yet, and it cannot be typed
			// into at all. The pane is still in cooked mode, so /exit sent now
			// is echoed by the line discipline and then DISCARDED by the
			// raw-mode switch the TUI makes as it starts: the chat never reads
			// a byte, nothing in the composer ever says /exit, and every exit
			// try is spent on a chat nobody asked to quit.
			stable = 0
		default:
			stable++
			if stable >= 2 {
				if announced {
					announcePane(ctx, request, tmux, stderr, "pfm reload: rebooting now")
				}
				return capture, nil
			}
		}
		if err := sleepPoll(ctx, options.Clock, options.Poll); err != nil {
			return "", err
		}
	}
	if !sawComposer {
		return "", fmt.Errorf(
			"the pane never showed a chat input box in %d polls — /exit was not typed, nothing changed",
			options.IdleTries,
		)
	}
	return "", fmt.Errorf("chat still busy after %d polls — /exit was not typed, nothing changed", options.IdleTries)
}

// announcePane puts a Display message on the caller's own pane; a failure
// here is logged, never fatal — the pane is a courtesy, not the contract.
func announcePane(ctx context.Context, request Request, tmux Tmux, stderr io.Writer, message string) {
	if err := tmux.Display(ctx, request.SocketPath, request.Pane, message); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: display %q: %v\n", message, err)
	}
}

// exitIncomplete names the state a refused /exit leaves behind, because
// "chat left running" on its own hid the one that mattered: an exit dialog
// nobody confirmed, or a typed /exit nobody submitted, sits in front of the
// composer and the harness holds every queued prompt — cron heartbeats,
// injected steers — behind it until a human clears it. Whatever this worker
// put on the screen, it takes back off, and the error says whether that was
// seen to succeed.
func exitIncomplete(ctx context.Context, request Request, options Options, tmux Tmux) error {
	cause := fmt.Errorf("/exit did not complete after %d tries; chat left running", options.ExitTries)
	capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
	if err != nil {
		return errors.Join(
			cause,
			fmt.Errorf(
				"could not read what the pane shows now — an exit dialog or the typed /exit may still be there, clear it by hand: %w",
				err,
			),
		)
	}
	switch {
	case exitDialogOpen(capture):
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Escape"); err != nil {
			return errors.Join(
				cause,
				fmt.Errorf(
					"the exit dialog would not confirm and could NOT be dismissed — press Esc in the pane by hand: %w",
					err,
				),
			)
		}
		if capture, err = tmux.Capture(ctx, request.SocketPath, request.Pane); err != nil {
			return errors.Join(
				cause,
				fmt.Errorf("sent Esc to the exit dialog but could not confirm it closed: %w", err),
			)
		} else if exitDialogOpen(capture) {
			return errors.Join(
				cause,
				errors.New(
					"the exit dialog would not confirm and did not close on Esc — press Esc in the pane by hand",
				),
			)
		}
		return errors.Join(cause, errors.New("the exit dialog would not confirm; dismissed it (Esc)"))
	case composerShowsExit(capture):
		if err := clearTypedExit(ctx, request, tmux); err != nil {
			return errors.Join(cause, err)
		}
		return errors.Join(cause, errors.New("cleared the typed /exit from the composer"))
	}
	return errors.Join(cause, errors.New("the pane shows neither the typed /exit nor an exit dialog"))
}

func sleepPoll(ctx context.Context, clk clock.Clock, poll time.Duration) error {
	return clk.Sleep(ctx, poll)
}

func rosterContains(accounts []int, wanted int) bool {
	for _, account := range accounts {
		if account == wanted {
			return true
		}
	}
	return false
}

// claudeBinary is the executable word the reborn Claude pane must show — the
// same account-aware resolution the spawn door uses, so the liveness proof and
// the respawn line can never disagree about which binary was launched.
func (request Request) claudeBinary() string {
	if binary := request.Machine.EffectiveClaude(request.Account).Binary; binary != "" {
		return binary
	}
	return pfmengine.MustLookup(pfmengine.Claude).Binary
}

// claudeRun is the respawn line for a Claude seat. It owns nothing: the strip,
// the autonomy posture and the system prompt all come from the one spawn door,
// so a chat that reboots in place comes back with exactly what a fresh launch
// would have carried.
func claudeRun(request Request) (string, error) {
	arguments := []string(nil)
	if request.SessionID != "" {
		arguments = []string{"--resume", request.SessionID}
	}
	effort, err := action.ClaudeEffort(request.Effort)
	if err != nil {
		return "", fmt.Errorf("resolve claude respawn effort: %w", err)
	}
	run, err := action.ClaudeSpawn{
		Purpose: action.PurposeResume,
		Account: request.Account,
		Cache1H: request.Cache1H,
		Args:    arguments,
		Home:    request.Home,
		Machine: request.Machine,
		Model:   request.Model,
		Effort:  effort,
	}.ShellCommand()
	if err != nil {
		return "", fmt.Errorf("render claude respawn command: %w", err)
	}
	return run, nil
}

func engineRun(request Request) (string, error) {
	switch request.Engine {
	case pfmengine.Claude:
		return claudeRun(request)
	case pfmengine.Codex:
		return codexRun(request)
	default:
		return "", nil
	}
}

func codexRun(request Request) (string, error) {
	effort, err := action.CodexEffort(request.Effort)
	if err != nil {
		return "", fmt.Errorf("resolve codex respawn effort: %w", err)
	}
	parts := []string{
		"env", "-u", "CODEX_THREAD_ID", "-u", "CLAUDE_CODE_SESSION_ID",
		"-u", "CLAUDECODE", "-u", "CLAUDE_CONFIG_DIR",
	}
	if request.CodexHome != "" {
		parts = append(parts, "CODEX_HOME="+action.Quote(request.CodexHome))
	}
	binary := request.CodexBinary
	if binary == "" {
		binary = pfmengine.MustLookup(pfmengine.Codex).Binary
	}
	if binary != pfmengine.MustLookup(pfmengine.Codex).Binary {
		binary = action.Quote(binary)
	}
	parts = append(parts, binary)
	if request.CodexYolo {
		parts = append(parts, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		parts = append(parts, "--sandbox", "workspace-write")
	}
	if request.Model != "" {
		parts = append(parts, "--model", action.Quote(request.Model))
	}
	if effort != "" {
		flag := action.CodexEffortArg(effort)
		parts = append(parts, flag[0], action.Quote(flag[1]))
	}
	if request.SessionID != "" {
		parts = append(parts, "resume", action.Quote(request.SessionID))
	}
	return strings.Join(parts, " "), nil
}

func deliverThen(
	ctx context.Context,
	request Request,
	options Options,
	tmux Tmux,
	proc Process,
	stderr io.Writer,
) error {
	// Idempotent: a direct-call test never routes through Run's defaults().
	options.defaults()
	for i := 0; i < options.ThenTries; i++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload --then: capture input box (try %d): %v\n", i+1, err)
		} else {
			trustPrompt := false
			for _, needle := range []string{"Trust this directory?", "trust this folder", "trust these settings"} {
				if strings.Contains(capture, needle) {
					if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
						return fmt.Errorf("reload --then: accept trust prompt: %w", err)
					}
					trustPrompt = true
				}
			}
			if trustPrompt {
				continue
			}
			if composerDrawn(capture) {
				goto ready
			}
		}
		if err := options.Clock.Sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	return fmt.Errorf("reload --then: input box never appeared")
ready:
	// A respawn returns before the child necessarily reaches Claude. Prove the
	// process only after the input box appears; checking immediately after
	// respawn races normal startup and drops an otherwise deliverable baton.
	panePID, err := currentPanePID(ctx, request.SocketPath, request.Pane, tmux)
	if err != nil {
		return fmt.Errorf("reload --then: refresh reborn pane process: %w", err)
	}
	live, err := engineLive(proc, panePID, request.Engine, request.claudeBinary(), request.CodexBinary)
	if err != nil {
		return fmt.Errorf("reload --then: prove live Claude: %w", err)
	}
	if !live {
		return fmt.Errorf("reload --then: no live %s on the pane", engineLabel(request.Engine))
	}
	// A composer baseline captured BEFORE the send lets us tell a freshly
	// typed paste placeholder from one left over from an earlier turn — a
	// pre-existing placeholder must never count as proof. haveBaseline is
	// tracked explicitly: a failed capture must never silently masquerade
	// as an empty-but-legitimate baseline (an error is never absence).
	baseline, baselineErr := tmux.Capture(ctx, request.SocketPath, request.Pane)
	haveBaseline := baselineErr == nil
	if baselineErr != nil {
		fmt.Fprintf(stderr, "pfm chat reload --then: capture composer baseline before typing prompt: %v\n", baselineErr)
	}
	if err := tmux.SendLiteral(ctx, request.SocketPath, request.Pane, request.Then); err != nil {
		return fmt.Errorf("reload --then: type prompt: %w", err)
	}
	flat := strings.Join(strings.Fields(request.Then), " ")
	needle := flat
	if len(needle) > 40 {
		needle = needle[len(needle)-40:]
	}
	typed := false
	for i := 0; i < 40; i++ {
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload --then: confirm typed text (try %d): %v\n", i+1, err)
			continue
		}
		// A resumed session (--resume / resume <id>, reload.go:339-340,
		// :380-381) renders its prior transcript in scrollback, and any
		// message the human pasted earlier in that conversation re-renders
		// as its own "[Pasted text #N +M lines]" placeholder there. Only the
		// ACTIVE composer line — not the whole pane — can prove THIS send;
		// scanning the full capture would let a stale placeholder from a
		// past turn stand in for text that never landed.
		composer := composerText(capture)
		hasTail := strings.Contains(squashSpace(composer), squashSpace(needle))
		hasPlaceholder := inject.HasPastePlaceholder(composer)
		if !haveBaseline {
			// No baseline means we cannot rule out a placeholder that was
			// already sitting in the composer before we ever typed (e.g. a
			// leftover unsent paste). A refusal here costs nothing —
			// failThen preserves the prompt on disk — so require the tail
			// needle, which is the human's own new prompt text and cannot
			// come from a stale paste.
			if hasTail {
				typed = true
				break
			}
			if err := options.Clock.Sleep(ctx, 200*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		// capture != baseline is a weak, supporting signal only: both TUIs
		// animate every frame (spinners, token counters, clocks), so the
		// pane differs from any baseline within a beat regardless of
		// whether our text landed. The composer-scoped needle/placeholder
		// check above carries the real proof; this only guards against a
		// placeholder that was already sitting in the composer (not
		// scrollback) before we typed.
		changed := capture != baseline
		if changed && (hasTail || hasPlaceholder) {
			typed = true
			break
		}
		if err := options.Clock.Sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	if !typed {
		return errors.New(
			"reload --then: typed text never rendered in the composer — looked for the prompt's tail text and, when a pre-send baseline was captured, a paste placeholder there too, but neither appeared — refusing blind Enter",
		)
	}
	// The submit proof reuses the SAME tail needle the typed proof just saw
	// present. Evidence seen present and then seen absent is a real transition;
	// the head of the prompt, which this used to look for, was never verified
	// present at all — and in a draft long enough to scroll inside the box, the
	// head has scrolled out of view before the first Enter, so its absence
	// would report a submission that never happened. The tail stays visible:
	// the cursor sits at the end of what was just typed.
	for i := 0; i < 12; i++ {
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
			return fmt.Errorf("reload --then: submit prompt: %w", err)
		}
		if err := options.Clock.Sleep(ctx, 150*time.Millisecond); err != nil {
			return err
		}
		if err := tmux.SendKey(ctx, request.SocketPath, request.Pane, "Enter"); err != nil {
			return fmt.Errorf("reload --then: confirm prompt submit: %w", err)
		}
		if err := options.Clock.Sleep(ctx, 400*time.Millisecond); err != nil {
			return err
		}
		capture, err := tmux.Capture(ctx, request.SocketPath, request.Pane)
		if err != nil {
			return fmt.Errorf("reload --then: verify prompt submit: %w", err)
		}
		// Submitted text remains in pane scrollback. Only the active composer
		// decides whether Enter cleared it; scanning the whole capture would
		// report every successful submission as still pending.
		composer := composerText(capture)
		if !strings.Contains(squashSpace(composer), squashSpace(needle)) {
			fmt.Fprintln(stderr, "then: follow-up delivered and submitted")
			return nil
		}
	}
	if err := tmux.Display(
		ctx,
		request.SocketPath,
		request.Pane,
		"reload --then typed but submit unconfirmed — press Enter",
	); err != nil {
		return fmt.Errorf("reload --then: display unconfirmed submit: %w", err)
	}
	// Neither "submitted" nor "never typed" is true here: the prompt was
	// typed and Enter was sent 12 times, but the composer never cleared, so
	// delivery is unproven either way. Returning an error (not nil) routes
	// this through Run's failThen, which writes request.Then to
	// <sidDir>/<socket-basename>.then-failed so the operator can recover it
	// instead of it vanishing silently.
	return fmt.Errorf(
		"reload --then: prompt was typed into %q and Enter was sent 12 times, but the composer never cleared — submission is unproven; the prompt is being saved to %s.then-failed so it is not lost",
		request.Pane,
		filepath.Base(request.SocketPath),
	)
}

func currentPanePID(ctx context.Context, socket, wanted string, tmux Tmux) (int, error) {
	panes, err := tmux.ListPanes(ctx, socket)
	if err != nil {
		return 0, err
	}
	for _, pane := range panes {
		if pane.ID != wanted {
			continue
		}
		if pane.Dead {
			return 0, errors.New("reborn pane is dead")
		}
		if pane.PID <= 0 {
			return 0, errors.New("reborn pane process id is unavailable")
		}
		return pane.PID, nil
	}
	return 0, errors.New("reborn pane disappeared")
}

func engineLabel(id pfmengine.ID) string {
	return pfmengine.MustLookup(id).Short
}

func engineLive(proc Process, panePID int, engine pfmengine.ID, claudeBinary, codexBinary string) (bool, error) {
	if proc == nil {
		return false, errors.New("process reader is unavailable")
	}
	if panePID <= 0 {
		return false, errors.New("pane process id is unavailable")
	}
	pids, err := proc.PIDs()
	if err != nil {
		return false, err
	}
	matcher, err := gather.MatcherFor(engine)
	if err != nil {
		return false, err
	}
	binary := claudeBinary
	if engine == pfmengine.Codex {
		binary = codexBinary
	}
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("read process %d command: %w", pid, err)
		}
		if !matcher.IsCommand(argv, binary) {
			continue
		}
		current := pid
		for depth := 0; depth <= 4; depth++ {
			if current == panePID {
				return true, nil
			}
			stat, statErr := proc.Stat(current)
			if statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					break
				}
				return false, fmt.Errorf("read process %d ancestry: %w", current, statErr)
			}
			if stat.ParentPID <= 1 || stat.ParentPID == current {
				break
			}
			current = stat.ParentPID
		}
	}
	return false, nil
}

func failThen(ctx context.Context, request Request, sidDir string, tmux Tmux, reason string) error {
	var failures []error
	if request.Then != "" && request.SocketPath != "" {
		path := filepath.Join(sidDir, filepath.Base(request.SocketPath)+".then-failed")
		if err := os.WriteFile(path, []byte(request.Then+"\n"), 0o600); err != nil {
			failures = append(failures, fmt.Errorf("write reload --then sentinel %q: %w", path, err))
		}
		if err := tmux.Display(
			ctx,
			request.SocketPath,
			request.Pane,
			"reload --then NOT delivered ("+reason+") — prompt saved",
		); err != nil {
			failures = append(failures, fmt.Errorf("display reload --then failure: %w", err))
		}
	}
	return errors.Join(failures...)
}

func SessionFromCrumb(sidDir, socket, pane string) (string, string, error) {
	for _, name := range []string{socket + "." + pane, socket} {
		id, transcript, found, err := readSessionCrumb(filepath.Join(sidDir, name))
		if err != nil {
			return "", "", err
		}
		if !found {
			continue
		}
		return id, transcript, nil
	}
	return "", "", nil
}

// ErrInvalidPaneCrumb reports a socket/pane pair that cannot name one exact
// pane breadcrumb under gather's strict filename grammar.
var ErrInvalidPaneCrumb = errors.New("invalid pane breadcrumb name")

// SessionFromPaneCrumb reads only the exact pane binding. It deliberately has
// no socket-level fallback: split callers must prove which pane owns an ID.
func SessionFromPaneCrumb(sidDir, socket, pane string) (string, string, error) {
	if !filepath.IsAbs(sidDir) {
		return "", "", fmt.Errorf("reload breadcrumb directory %q is not absolute", sidDir)
	}
	name := socket + "." + pane
	parsedSocket, parsedPane, ok := gather.ParseCrumbName(name)
	if pane == "" || !ok || parsedSocket != socket || parsedPane != pane {
		return "", "", fmt.Errorf("%w: socket %q pane %q", ErrInvalidPaneCrumb, socket, pane)
	}
	id, transcript, _, err := readSessionCrumb(filepath.Join(sidDir, name))
	return id, transcript, err
}

func readSessionCrumb(path string) (id, transcript string, found bool, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("read reload breadcrumb %q: %w", path, err)
	}
	transcript = strings.TrimSpace(string(content))
	if transcript == "" {
		return "", "", false, nil
	}
	id = strings.TrimSuffix(filepath.Base(transcript), filepath.Ext(transcript))
	return id, transcript, true, nil
}

// ParseIntEnv reads name through env and returns fallback when it is unset
// or not a positive integer.
func ParseIntEnv(env paths.Env, name string, fallback int) int {
	if value, err := strconv.Atoi(env.Get(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}
