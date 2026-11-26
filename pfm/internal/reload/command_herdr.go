package reload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	nativeReloadFailed = "failed"
	nativeSeatLive     = "live"
	herdrPaneKey       = "pane_id"
)

type nativeReload struct {
	request Request
	options Options
	target  pfmcall.Target
	door    *pfmcall.Engine
	db      *fleetdb.Store
	owner   fleetdb.Seat
}

// pane is the native seat's seatScreen: call's door, the seat's supervisor
// re-proved before every read and key. Herdr shows a worker's failure through
// its notification, so a refusal is returned, never displayed here.
func (n *nativeReload) pane() seatScreen {
	return seatScreen{
		door: n.door,
		bind: func(ctx context.Context) (pfmcall.Target, error) {
			if err := n.refreshTarget(ctx); err != nil {
				return pfmcall.Target{}, err
			}
			return n.target, nil
		},
	}
}

// RunHerdr reboots a native seat without replacing its Herdr pane. Seat-host
// alone owns process startup, pending acknowledgment, and the failed-start UI.
func RunHerdr(
	ctx context.Context,
	request Request,
	options Options,
	stderr io.Writer,
) (result Result, returnErr error) {
	if request.SocketPath == "" || request.Pane == "" {
		return Result{}, errors.New("native Herdr reload: socket and pane are required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if stderr == nil {
		stderr = io.Discard
	}
	options.defaults()
	values, err := paths.Resolve()
	if err != nil {
		return Result{}, fmt.Errorf("native reload paths: %w", err)
	}
	if options.SIDDir == "" {
		options.SIDDir = values.SIDDir
	}
	door, err := pfmcall.New(
		pfmcall.Dependencies{
			WarningWriter: stderr,
			Options:       pfmcall.Options{DisableSignature: true, Clock: options.Clock},
		},
	)
	if err != nil {
		return Result{}, fmt.Errorf("native reload pane door: %w", err)
	}
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	n := nativeReload{
		request: request,
		options: options,
		door:    door,
		db:      db,
		target: pfmcall.Target{
			Backend:    "herdr",
			SocketPath: request.SocketPath,
			Pane:       request.Pane,
			Engine:     string(request.Engine),
		},
	}
	seat, err := n.seat(ctx)
	if err != nil {
		return Result{}, err
	}
	conversation := fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	unlock, err := fleetdb.LockSeat(ctx, values, conversation)
	if err != nil {
		return Result{}, fmt.Errorf("lock native reload conversation: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	seat, err = n.seat(ctx)
	if err != nil {
		return Result{}, err
	}
	request.Pane = seat.HerdrPane
	n.request.Pane = seat.HerdrPane
	n.target.Pane = seat.HerdrPane
	lock, err := lockNativeReload(ctx, request, options)
	if err != nil {
		return Result{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	old, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		return Result{}, err
	}
	// The recipe resumes the chat's newest segment; publication re-keys the seat to it.
	resumed := seat
	if pfmengine.ID(seat.Engine) == pfmengine.Claude && seat.SessionID != "" && !request.New {
		resumed.SessionID, _, err = ContinuedSession(ctx, seat.SessionID, stderr)
		if err != nil {
			return Result{}, err
		}
	}
	updated, run, err := nativeReloadRecipe(request, resumed, old, options.EngineSyncRunner, stderr)
	if err != nil {
		return Result{}, err
	}
	n.request = updated
	retry := seat.State == nativeReloadFailed
	if seat.ReloadPending && !retry {
		return Result{}, errors.New("native reload already pending; wait for seat-host startup or failure")
	}
	oldPID := 0
	if retry {
		pid, _, err := n.engine(ctx)
		if err != nil {
			return Result{}, err
		}
		if pid != 0 {
			return Result{}, errors.New("failed native reload seat engine is still running; refusing retry input")
		}
		screen, err := n.pane().capture(ctx)
		if err != nil {
			return Result{}, err
		}
		if !strings.Contains(screen, "press Enter to retry, q to close") {
			return Result{}, errors.New("failed native seat is not showing the seat-host retry prompt")
		}
	} else {
		var listed []herdr.Process
		oldPID, listed, err = n.engine(ctx)
		if err != nil {
			return Result{}, err
		}
		if oldPID == 0 {
			return Result{}, fmt.Errorf("native reload: target engine is not running: pane %s lists %s",
				n.target.Pane, foregroundRows(listed))
		}
		if options.Delay > 0 {
			if err := options.Clock.Sleep(ctx, options.Delay); err != nil {
				return Result{}, err
			}
		}
		if err := n.ready(ctx, true, false); err != nil {
			return Result{}, err
		}
	}
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return Result{}, fmt.Errorf("recheck native reload seat: %w", err)
	}
	if !found || current.EndPending {
		return Result{}, errChatEnded
	}
	if current.HerdrSocket != seat.HerdrSocket || current.HerdrTerminalID != seat.HerdrTerminalID ||
		current.SeatHostPID != seat.SeatHostPID || current.SeatHostStart != seat.SeatHostStart || current.SeatHostBirth != seat.SeatHostBirth ||
		current.Engine != seat.Engine ||
		current.State != seat.State ||
		current.ReloadPending != seat.ReloadPending ||
		current.SessionID != seat.SessionID {
		return Result{}, errors.New("native reload seat binding changed before publication")
	}
	current, owned, err := gather.ResolveHerdrSeat(ctx, values, current)
	if err != nil {
		return Result{}, fmt.Errorf("reprove native reload before publication: %w", err)
	}
	if !owned {
		return Result{}, n.supervisorGone(ctx, seat.SeatID)
	}
	n.target.Pane = current.HerdrPane
	n.request.Pane = current.HerdrPane
	seat = current
	if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
		return Result{}, fmt.Errorf("publish native reload recipe: %w", err)
	}
	if n.request.fresh && n.request.SessionID != "" && n.request.SessionID != seat.SessionID {
		inheritSpace(ctx, values, seat.HerdrSocket, seat.HerdrPane, n.request.SessionID, stderr)
	}
	pending := seat
	pending.ReloadPending = true
	pending.SessionID = n.request.SessionID
	if err := db.UpdateSeat(ctx, seat, pending); err != nil {
		return Result{}, errors.Join(
			fmt.Errorf("publish native reload pending: %w", err),
			db.WriteSeatRun(seat.SeatID, old),
		)
	}
	submitted, stored := false, false
	defer func() {
		// After an Enter attempt the engine may already have exited even if its
		// acknowledgement was lost. Never revoke the supervisor's durable work.
		if !submitted && returnErr != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			returnErr = errors.Join(returnErr, n.rollback(cleanup, seat, pending, old, oldPID, retry))
			if stored {
				returnErr = errors.Join(returnErr, dropThen(n.options.SIDDir, seat.SeatID))
			}
		}
		// Past /exit a failed reload keeps the --then message for the reborn
		// chat's first prompt, whichever step failed.
		if submitted && stored && returnErr != nil {
			fmt.Fprintf(stderr, "pfm chat reload: the --then message is kept in %s for the chat's first prompt\n",
				ThenBackstopPath(n.options.SIDDir, seat.SeatID))
		}
	}()
	if n.request.Then != "" && n.request.Engine == pfmengine.Claude {
		if err := storeThen(n.options.SIDDir, seat.SeatID, thenBackstop{
			Socket: request.SocketPath, Pane: request.Pane, Session: n.request.SessionID, Message: n.request.Then,
		}); err != nil {
			return Result{}, err
		}
		stored = true
	}
	if retry {
		submitted, err = pressEnter(ctx, n.pane(), "submit native reload retry")
	} else {
		submitted, err = typeExit(ctx, n.pane(), n.options.Clock, stderr)
	}
	if submitted {
		// Past Enter the seat host starts the published recipe: the chat runs
		// on its account from here, whatever the startup wait reports.
		recordReloadLaunch(ctx, n.request, stderr)
	}
	if err != nil {
		return Result{}, err
	}
	if err := n.wait(ctx, seat.SeatID, oldPID, run); err != nil {
		return Result{}, err
	}
	n.claimSession(ctx, stderr)
	if n.request.Then != "" {
		if err := n.then(ctx); err != nil {
			return Result{}, err
		}
		if stored {
			if err := dropThen(n.options.SIDDir, seat.SeatID); err != nil {
				fmt.Fprintf(stderr, "pfm chat reload: --then was typed, but its stored copy stays: %v\n", err)
			}
		}
	}
	n.labelLeftBehind(request.LeftBehind, stderr)
	return Result{
		Account:    n.request.Account,
		Cache1H:    n.request.Cache1H,
		LeftBehind: request.LeftBehind,
		New:        n.request.fresh || request.New,
	}, nil
}

// nativeReloadLockKey names a native pane's reload mutex, held for the whole
// reboot: lockNativeReload takes it, ClaimThen probes it.
func nativeReloadLockKey(socket, pane string) string {
	return fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(socket+"\x00"+pane)))
}

func lockNativeReload(ctx context.Context, request Request, options Options) (*os.File, error) {
	if err := os.MkdirAll(options.SIDDir, 0o700); err != nil {
		return nil, fmt.Errorf("create native reload mutex directory: %w", err)
	}
	path := LockPath(options.SIDDir, nativeReloadLockKey(request.SocketPath, request.Pane), "seat")
	lock, err := herdr.OpenStateFile(options.SIDDir, filepath.Base(path), os.O_CREATE|os.O_RDWR, "native reload mutex")
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < options.IdleTries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, lock.Close())
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("lock native reload: %w", err), lock.Close())
		}
		if err := options.Clock.Sleep(ctx, options.Poll); err != nil {
			return nil, errors.Join(err, lock.Close())
		}
	}
	return nil, errors.Join(errors.New("another reload of this pane is already in flight"), lock.Close())
}

func nativeReloadRecipe(
	request Request,
	seat fleetdb.Seat,
	old fleetdb.SeatRun,
	engineSync deps.Runner,
	stderr io.Writer,
) (Request, fleetdb.SeatRun, error) {
	for _, word := range append(append([]string{old.Binary, old.CWD}, old.Args...), old.Env...) {
		if strings.ContainsRune(word, '\x00') {
			return request, fleetdb.SeatRun{}, errors.New("native reload current recipe contains NUL")
		}
	}
	if request.Engine != pfmengine.Claude && request.Engine != pfmengine.Codex {
		return request, fleetdb.SeatRun{}, fmt.Errorf(
			"engine %q does not support native in-place reload without a canonical structured recipe",
			request.Engine,
		)
	}
	var err error
	request, err = nativeReloadDefaults(request, old, stderr)
	if err != nil {
		return request, fleetdb.SeatRun{}, err
	}
	if !rosterContains(request.AccountIDs, request.Account) {
		return request, fleetdb.SeatRun{}, fmt.Errorf("account %d is not in the configured roster", request.Account)
	}
	request.SessionID = seat.SessionID
	if request.Name == "" {
		request.Name = seat.Name
	}
	if request.CWD == "" {
		request.CWD = old.CWD
	}
	if request.New || request.SessionID == "" {
		request.SessionID, request.fresh = "", true
		if request.Engine == pfmengine.Claude {
			id, err := claudelaunch.NewSessionID()
			if err != nil {
				return request, fleetdb.SeatRun{}, fmt.Errorf("new native reload session: %w", err)
			}
			request.SessionID = id
		}
	}
	request, err = applyReloadWorkbench(request, engineSync, stderr)
	if err != nil {
		return request, fleetdb.SeatRun{}, err
	}
	run, err := nativeReloadRun(request, seat.SeatID)
	if err != nil {
		return request, run, fmt.Errorf("render native reload recipe: %w", err)
	}
	// A native seat already resolved the canonical PATH name past the managed
	// launcher. Retain that executable, never recursively invoke the launcher.
	if run.Binary == pfmengine.MustLookup(request.Engine).Binary {
		run.Binary = old.Binary
	}
	if !filepath.IsAbs(run.Binary) || !filepath.IsAbs(run.CWD) {
		return request, run, errors.New("native reload recipe binary and cwd must be absolute")
	}
	for _, word := range append(append([]string{run.Binary, run.CWD}, run.Args...), run.Env...) {
		if strings.ContainsRune(word, '\x00') {
			return request, run, errors.New("native reload recipe contains NUL")
		}
	}
	return request, run, nil
}

// nativeReloadDefaults reads the current recipe, not historical LaunchFor data:
// an explicit earlier reload may have changed the account or cache in-place.
func nativeReloadDefaults(request Request, old fleetdb.SeatRun, stderr io.Writer) (Request, error) {
	if !request.AccountGiven {
		account, found := RecipeAccount(request.Machine, request.Engine, old.Env)
		if !found {
			return request, errors.New(
				"native reload current recipe account is missing, ambiguous, or no longer configured; specify --account",
			)
		}
		request.Account = account
	}
	if !request.CacheGiven && request.Engine == pfmengine.Claude {
		key := ""
		for _, knob := range claudelaunch.Knobs {
			if knob.Name == "cache1h" && knob.Wire == claudelaunch.WireEnv {
				key = knob.Target
				break
			}
		}
		if key == "" {
			return request, errors.New("native reload cache registry metadata is unavailable")
		}
		switch recorded := nativeReloadEnv(old.Env, key); recorded {
		case "1h":
			request.Cache1H = true
		case "5m":
			request.Cache1H = false
		default:
			kept := "5m"
			if request.Cache1H {
				kept = "1h"
			}
			fmt.Fprintf(
				stderr,
				"pfm chat reload: the pane's recipe holds no valid cache (%s=%q) — reloading with %s; pass --cache to choose\n",
				key,
				recorded,
				kept,
			)
		}
	}
	return request, nil
}

func nativeReloadEnv(env []string, key string) string {
	for index := len(env) - 1; index >= 0; index-- {
		if value, found := strings.CutPrefix(env[index], key+"="); found {
			return value
		}
	}
	return ""
}

func nativeReloadRun(request Request, name string) (fleetdb.SeatRun, error) {
	if request.Engine == pfmengine.Claude {
		spawn, err := claudeSpawn(request)
		if err != nil {
			return fleetdb.SeatRun{}, err
		}
		return spawn.SeatRun(request.CWD)
	}
	plan, err := action.PlanCodex(action.HeadlessRequest{
		Engine: request.Engine, Name: name, CWD: request.CWD, Home: request.Home,
		PrimaryAccount: request.Account, Config: request.Machine, Model: request.Model, Effort: request.Effort,
		PromptChannel: request.PromptChannel, ResumeSessionID: request.SessionID, Envs: request.Envs,
	})
	if err != nil {
		return fleetdb.SeatRun{}, err
	}
	if plan.SeatRun == nil {
		return fleetdb.SeatRun{}, errors.New("canonical planner returned no native recipe")
	}
	return *plan.SeatRun, nil
}

var errEngineUnprovable = errors.New("native reload engine process lacks a valid pid/argv; startup cannot be proved")

// process is the target engine's pid and argv (0: Herdr listed none) in the
// pane's foreground, as one read lists it, with every row that read listed.
func (n *nativeReload) process(ctx context.Context) (int, []string, []herdr.Process, error) {
	if err := n.refreshTarget(ctx); err != nil {
		return 0, nil, nil, err
	}
	processes, err := herdr.ReadPaneProcesses(ctx, n.target.SocketPath, n.target.Pane)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("native reload process_info: %w", err)
	}
	if processes.ForegroundErr != nil {
		return 0, nil, nil, fmt.Errorf("decode native reload process_info: %w", processes.ForegroundErr)
	}
	if processes.Pane != n.target.Pane {
		return 0, nil, nil, errors.New("native reload malformed process_info or mismatched pane")
	}
	listed := processes.Foreground
	for _, process := range listed {
		if !process.RunsEngine(n.request.Engine) {
			continue
		}
		if process.PID <= 0 || len(process.Argv) == 0 {
			return 0, nil, listed, errEngineUnprovable
		}
		return process.PID, process.Argv, listed, nil
	}
	return 0, nil, listed, nil
}

// foregroundRows names every row a read of the pane's foreground listed.
func foregroundRows(listed []herdr.Process) string {
	if len(listed) == 0 {
		return "no foreground process"
	}
	rows := make([]string, len(listed))
	for index, process := range listed {
		rows[index] = process.String()
	}
	return strings.Join(rows, "; ")
}

// nativeEngineReads bounds the reads of a target engine Herdr lists without a
// valid pid/argv: Herdr 0.9.5 now and then answers a live engine's row with an
// empty argv for one read.
const nativeEngineReads = 3

// engine is the target engine's pid (0: Herdr listed none) with the rows the
// read listed, read again through the clock while its row carries no valid
// pid/argv; a row that never does is an engine that could not be proven,
// never one that is not running.
func (n *nativeReload) engine(ctx context.Context) (int, []herdr.Process, error) {
	for read := 1; ; read++ {
		pid, _, listed, err := n.process(ctx)
		if !errors.Is(err, errEngineUnprovable) {
			return pid, listed, err
		}
		if read == nativeEngineReads {
			return 0, listed, fmt.Errorf(
				"native reload: target engine could not be proven: %d reads of pane %s listed it without a valid "+
					"pid/argv; the last listed %s",
				read,
				n.target.Pane,
				foregroundRows(listed),
			)
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return 0, nil, err
		}
	}
}

func (n *nativeReload) wait(ctx context.Context, id string, oldPID int, run fleetdb.SeatRun) error {
	var unprovable error
	for attempt := 0; attempt < n.options.ExitTries+n.options.ExitGraceTries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := n.refreshTarget(ctx); err != nil {
			return err
		}
		seat, found, err := n.db.Seat(ctx, id)
		if err != nil {
			return fmt.Errorf("read native reload startup: %w", err)
		}
		if !found || seat.HerdrSocket != n.target.SocketPath || seat.HerdrPane != n.target.Pane {
			return errors.New("native reload seat binding changed during startup")
		}
		// A retry starts in failed state; seat-host consumes Enter asynchronously.
		// Do not mistake the preceding attempt's row for this attempt's result.
		if seat.State == nativeReloadFailed &&
			(oldPID != 0 || attempt+1 == n.options.ExitTries+n.options.ExitGraceTries) {
			return fmt.Errorf("native reload new engine failed: %s", seat.LostNote)
		}
		pid, argv, _, err := n.process(ctx)
		// The old engine dying after /exit has no argv for a moment: read again.
		if unprovable = nil; errors.Is(err, errEngineUnprovable) {
			unprovable, err = err, nil
		}
		if err != nil {
			return err
		}
		// The seat host reapplies Remote Control under the seat's current name
		// at every start (RefreshRecipeRemoteControl): the new engine runs the
		// published recipe up to that one knob.
		if !seat.ReloadPending && seat.State == nativeSeatLive && pid > 0 && pid != oldPID &&
			slices.Equal(claudelaunch.RefreshRemoteControl(argv[1:], false, ""),
				claudelaunch.RefreshRemoteControl(run.Args, false, "")) {
			return nil
		}
		if pid == oldPID && oldPID != 0 {
			screen, err := n.pane().capture(ctx)
			if err != nil {
				return err
			}
			if pfmcall.ExitDialogOpen(screen) || composerShowsExit(screen) {
				if err := n.pane().key(ctx, "Enter"); err != nil {
					return fmt.Errorf("confirm native reload exit: %w", err)
				}
			}
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return err
		}
	}
	if unprovable != nil {
		return unprovable
	}
	return errors.New("native reload new engine startup was not confirmed; seat-host retains the pending recipe")
}

func (n *nativeReload) rollback(
	ctx context.Context,
	old, pending fleetdb.Seat,
	run fleetdb.SeatRun,
	oldPID int,
	retry bool,
) error {
	current, found, err := n.db.Seat(ctx, old.SeatID)
	if err != nil {
		return fmt.Errorf("recheck native reload rollback: %w", err)
	}
	if !found || current.HerdrSocket != old.HerdrSocket || current.HerdrPane != old.HerdrPane ||
		current.ReloadPending != pending.ReloadPending ||
		current.State != pending.State {
		return errors.New("native reload binding changed before rollback; pending left to seat-host")
	}
	if !retry {
		pid, _, err := n.engine(ctx)
		if err != nil {
			return fmt.Errorf("native reload cannot safely roll back pending: %w", err)
		}
		if pid != oldPID {
			return errors.New("native reload engine changed before rollback; pending left to seat-host")
		}
		screen, err := n.pane().capture(ctx)
		if err != nil {
			return err
		}
		if composerShowsExit(screen) {
			if err := clearTypedExit(ctx, n.pane()); err != nil {
				return fmt.Errorf("clear unsubmitted native /exit: %w", err)
			}
		}
	}
	if err := n.db.WriteSeatRun(old.SeatID, run); err != nil {
		return fmt.Errorf("restore native reload recipe: %w", err)
	}
	if err := n.db.UpdateSeat(ctx, pending, old); err != nil {
		return fmt.Errorf("restore native reload pending: %w", err)
	}
	return nil
}
