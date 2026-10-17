package reload

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const nativeReloadFailed = "failed"

type nativeReload struct {
	request Request
	options Options
	target  pfmcall.Target
	client  pfmcall.Engine
	db      *fleetdb.Store
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
	lock, err := lockNativeReload(ctx, request, options)
	if err != nil {
		return Result{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	n := nativeReload{
		request: request,
		options: options,
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
	old, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		return Result{}, err
	}
	updated, run, err := nativeReloadRecipe(request, seat, old)
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
		pid, _, err := n.process(ctx)
		if err != nil {
			return Result{}, err
		}
		if pid != 0 {
			return Result{}, errors.New("failed native reload seat engine is still running; refusing retry input")
		}
		screen, err := n.capture(ctx)
		if err != nil {
			return Result{}, err
		}
		if !strings.Contains(screen, "press Enter to retry, q to close") {
			return Result{}, errors.New("failed native seat is not showing the seat-host retry prompt")
		}
	} else {
		oldPID, _, err = n.process(ctx)
		if err != nil {
			return Result{}, err
		}
		if oldPID == 0 {
			return Result{}, errors.New("native reload: target engine is not running")
		}
		if options.Delay > 0 {
			if err := options.Clock.Sleep(ctx, options.Delay); err != nil {
				return Result{}, err
			}
		}
		if err := n.ready(ctx, true); err != nil {
			return Result{}, err
		}
	}
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return Result{}, fmt.Errorf("recheck native reload seat: %w", err)
	}
	if !found || current.HerdrSocket != seat.HerdrSocket || current.HerdrPane != seat.HerdrPane ||
		current.Engine != seat.Engine ||
		current.State != seat.State ||
		current.ReloadPending != seat.ReloadPending ||
		current.SessionID != seat.SessionID {
		return Result{}, errors.New("native reload seat binding changed before publication")
	}
	seat = current
	if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
		return Result{}, fmt.Errorf("publish native reload recipe: %w", err)
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
	submitted := false
	defer func() {
		// After an Enter attempt the engine may already have exited even if its
		// acknowledgement was lost. Never revoke the supervisor's durable work.
		if !submitted && returnErr != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			returnErr = errors.Join(returnErr, n.rollback(cleanup, seat, pending, old, oldPID, retry))
		}
	}()
	if !retry {
		if err := n.client.SendLiteral(ctx, n.target, "/exit"); err != nil {
			return Result{}, err
		}
		if err := n.renderExit(ctx); err != nil {
			return Result{}, err
		}
	}
	submitted = true
	if err := n.client.SendKey(ctx, n.target, "Enter"); err != nil {
		return Result{}, fmt.Errorf("submit native reload exit/retry: %w", err)
	}
	if err := n.wait(ctx, seat.SeatID, oldPID, run); err != nil {
		return Result{}, err
	}
	if n.request.Then != "" {
		if err := n.then(ctx, stderr); err != nil {
			return Result{}, err
		}
	}
	return Result{
		Account:    n.request.Account,
		Cache1H:    n.request.Cache1H,
		LeftBehind: request.LeftBehind,
		New:        n.request.fresh || request.New,
	}, nil
}

func lockNativeReload(ctx context.Context, request Request, options Options) (*os.File, error) {
	if err := os.MkdirAll(options.SIDDir, 0o700); err != nil {
		return nil, fmt.Errorf("create native reload mutex directory: %w", err)
	}
	directory, err := os.OpenFile(options.SIDDir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open native reload mutex directory: %w", err)
	}
	if err := nativeReloadLockOwner(directory, true); err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	key := fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(request.SocketPath+"\x00"+request.Pane)))
	path := LockPath(options.SIDDir, key, "seat")
	fd, openErr := unix.Openat(
		int(directory.Fd()),
		filepath.Base(path),
		unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK,
		0o600,
	)
	closeErr := directory.Close()
	if openErr != nil {
		return nil, errors.Join(fmt.Errorf("open native reload mutex: %w", openErr), closeErr)
	}
	lock := os.NewFile(uintptr(fd), path)
	if closeErr != nil {
		return nil, errors.Join(fmt.Errorf("close native reload mutex directory: %w", closeErr), lock.Close())
	}
	if err := nativeReloadLockOwner(lock, false); err != nil {
		return nil, errors.Join(err, lock.Close())
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

func nativeReloadLockOwner(file *os.File, directory bool) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat native reload mutex: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("native reload mutex ownership is not the effective user")
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return errors.New("native reload mutex directory is not a protected directory")
		}
	} else if !info.Mode().IsRegular() {
		return errors.New("native reload mutex is not a regular file")
	}
	return nil
}

func (n *nativeReload) seat(ctx context.Context) (fleetdb.Seat, error) {
	seat, found, err := n.db.SeatByPane(ctx, n.request.SocketPath, n.request.Pane)
	if err != nil {
		return seat, fmt.Errorf("resolve native reload seat: %w", err)
	}
	if !found && n.request.SessionID != "" {
		seat, found, err = n.db.SeatBySession(ctx, n.request.SessionID)
	}
	if err != nil {
		return seat, fmt.Errorf("resolve native reload session: %w", err)
	}
	if !found || seat.Backend != "herdr" || seat.HerdrSocket != n.request.SocketPath ||
		seat.HerdrPane != n.request.Pane {
		return seat, errors.New("native reload seat does not match the explicit socket and pane")
	}
	if seat.Engine != string(n.request.Engine) {
		return seat, fmt.Errorf("native reload seat engine %q differs from requested %q", seat.Engine, n.request.Engine)
	}
	if seat.State != "live" && seat.State != nativeReloadFailed {
		return seat, fmt.Errorf("native reload seat is %s", seat.State)
	}
	return seat, nil
}

func nativeReloadRecipe(request Request, seat fleetdb.Seat, old fleetdb.SeatRun) (Request, fleetdb.SeatRun, error) {
	if !filepath.IsAbs(old.Binary) || !filepath.IsAbs(old.CWD) {
		return request, fleetdb.SeatRun{}, errors.New("native reload current recipe binary and cwd must be absolute")
	}
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
	request, err = nativeReloadDefaults(request, old)
	if err != nil {
		return request, fleetdb.SeatRun{}, err
	}
	if !rosterContains(request.AccountIDs, request.Account) {
		return request, fleetdb.SeatRun{}, fmt.Errorf("account %d is not in the configured roster", request.Account)
	}
	request.SessionID = seat.SessionID
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
	request, err = applyReloadWorkbench(request)
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
func nativeReloadDefaults(request Request, old fleetdb.SeatRun) (Request, error) {
	if !request.AccountGiven {
		directory := claudelaunch.ConfigDirFromEnv(old.Env)
		accounts := make(map[int]string)
		if request.Engine == pfmengine.Codex {
			directory = nativeReloadEnv(old.Env, "CODEX_HOME")
			for _, account := range request.Machine.CodexAccounts {
				accounts[account.ID] = account.Home
			}
		} else {
			for _, account := range request.Machine.Accounts {
				accounts[account.ID] = account.ConfigDir
			}
		}
		matches := 0
		if filepath.IsAbs(directory) {
			for id, candidate := range accounts {
				if filepath.Clean(candidate) == filepath.Clean(directory) {
					request.Account = id
					matches++
				}
			}
		}
		if matches != 1 {
			return request, errors.New(
				"native reload current recipe account is missing, ambiguous, or no longer configured; specify --account",
			)
		}
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
		switch nativeReloadEnv(old.Env, key) {
		case "1h":
			request.Cache1H = true
		case "5m":
			request.Cache1H = false
		default:
			return request, errors.New("native reload current recipe cache is missing or invalid; specify --cache")
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
		PromptChannel: request.PromptChannel, ResumeSessionID: request.SessionID,
	})
	if err != nil {
		return fleetdb.SeatRun{}, err
	}
	if plan.SeatRun == nil {
		return fleetdb.SeatRun{}, errors.New("canonical planner returned no native recipe")
	}
	return *plan.SeatRun, nil
}

func (n *nativeReload) capture(ctx context.Context) (string, error) {
	body, err := herdr.SeatRequest(
		ctx,
		n.target.SocketPath,
		"pane.read",
		map[string]any{"pane_id": n.target.Pane, "source": "recent", "lines": 1000, "strip_ansi": false},
	)
	if err != nil {
		return "", fmt.Errorf("native reload read composer: %w", err)
	}
	var result struct {
		Type string `json:"type"`
		Read *struct {
			Pane      string  `json:"pane_id"`
			Workspace string  `json:"workspace_id"`
			Text      *string `json:"text"`
			Truncated *bool   `json:"truncated"`
		} `json:"read"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode native reload composer: %w", err)
	}
	if result.Type != "pane_read" || result.Read == nil || result.Read.Text == nil || result.Read.Truncated == nil {
		return "", errors.New("native reload malformed pane_read result")
	}
	if result.Read.Pane != n.target.Pane && result.Read.Workspace+":"+result.Read.Pane != n.target.Pane {
		return "", fmt.Errorf("native reload snapshot belongs to pane %q", result.Read.Pane)
	}
	return *result.Read.Text, nil
}

func (n *nativeReload) ready(ctx context.Context, stash bool) error {
	stable := 0
	for attempt := 0; attempt < n.options.IdleTries; attempt++ {
		screen, err := n.capture(ctx)
		if err != nil {
			return err
		}
		if selectorOpen(screen) || pfmengine.ClaudeTrustDialog(screen) {
			return errors.New("native reload: open selector/menu; answer it before reloading")
		}
		if n.request.Engine == pfmengine.Claude {
			body, err := herdr.SeatRequest(
				ctx,
				n.target.SocketPath,
				"agent.get",
				map[string]string{"target": n.target.Pane},
			)
			if err != nil {
				return fmt.Errorf("native reload read agent: %w", err)
			}
			var state struct {
				Type  string `json:"type"`
				Agent *struct {
					Status string `json:"agent_status"`
				} `json:"agent"`
			}
			if err := json.Unmarshal(body, &state); err != nil {
				return fmt.Errorf("decode native reload agent: %w", err)
			}
			if state.Type != "agent_info" || state.Agent == nil {
				return errors.New("native reload malformed agent_info")
			}
			switch state.Agent.Status {
			case "blocked", "unknown":
				return fmt.Errorf("native reload target is %s", state.Agent.Status)
			case "working":
				stable = 0
				if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
					return err
				}
				continue
			case "idle", "done":
			default:
				return fmt.Errorf("native reload unknown agent state %q", state.Agent.Status)
			}
		}
		if !composerDrawn(screen) || pfmcall.IsFooterBusy(n.request.Engine, screen) {
			stable = 0
		} else {
			empty, _, err := pfmcall.ComposerEmpty(screen, func() (string, error) { return screen, nil })
			if err != nil {
				return err
			}
			if !empty {
				if !stash {
					return errors.New("native reload composer holds a draft")
				}
				if err := n.client.SendKey(ctx, n.target, "C-s"); err != nil {
					return fmt.Errorf("stash native reload draft: %w", err)
				}
				stash = false
				stable = 0
			} else {
				stable++
				if stable >= 2 {
					return nil
				}
			}
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return err
		}
	}
	return errors.New("native reload: chat still busy or input box not ready; /exit was not typed")
}

func (n *nativeReload) renderExit(ctx context.Context) error {
	for attempt := 0; attempt < n.options.ExitTries; attempt++ {
		screen, err := n.capture(ctx)
		if err != nil {
			return err
		}
		if composerShowsExit(screen) {
			return nil
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return err
		}
	}
	return errors.New("native reload: typed /exit never rendered; refusing blind Enter")
}

type nativeReloadProcess struct {
	PID   int      `json:"pid"`
	Name  string   `json:"name"`
	Argv0 *string  `json:"argv0"`
	Argv  []string `json:"argv"`
}

func (n *nativeReload) process(ctx context.Context) (int, []string, error) {
	body, err := herdr.SeatRequest(
		ctx,
		n.target.SocketPath,
		"pane.process_info",
		map[string]string{"pane_id": n.target.Pane},
	)
	if err != nil {
		return 0, nil, fmt.Errorf("native reload process_info: %w", err)
	}
	var result struct {
		Type string `json:"type"`
		Info *struct {
			Pane      string                `json:"pane_id"`
			Processes []nativeReloadProcess `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, nil, fmt.Errorf("decode native reload process_info: %w", err)
	}
	if result.Type != "pane_process_info" || result.Info == nil || result.Info.Pane != n.target.Pane ||
		result.Info.Processes == nil {
		return 0, nil, errors.New("native reload malformed process_info or mismatched pane")
	}
	binary := pfmengine.MustLookup(n.request.Engine).Binary
	for _, process := range result.Info.Processes {
		name := process.Name
		if process.Argv0 != nil {
			name = filepath.Base(*process.Argv0)
		} else if len(process.Argv) > 0 {
			name = filepath.Base(process.Argv[0])
		}
		if name != binary {
			continue
		}
		if process.PID <= 0 || len(process.Argv) == 0 {
			return 0, nil, errors.New("native reload engine process lacks a valid pid/argv; startup cannot be proved")
		}
		return process.PID, process.Argv, nil
	}
	return 0, nil, nil
}

func (n *nativeReload) wait(ctx context.Context, id string, oldPID int, run fleetdb.SeatRun) error {
	for attempt := 0; attempt < n.options.ExitTries+n.options.ExitGraceTries; attempt++ {
		if err := ctx.Err(); err != nil {
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
		pid, argv, err := n.process(ctx)
		if err != nil {
			return err
		}
		if !seat.ReloadPending && seat.State == "live" && pid > 0 && pid != oldPID && slices.Equal(argv[1:], run.Args) {
			return nil
		}
		if pid == oldPID && oldPID != 0 {
			screen, err := n.capture(ctx)
			if err != nil {
				return err
			}
			if pfmcall.ExitDialogOpen(screen) || composerShowsExit(screen) {
				if err := n.client.SendKey(ctx, n.target, "Enter"); err != nil {
					return fmt.Errorf("confirm native reload exit: %w", err)
				}
			}
		}
		if err := n.options.Clock.Sleep(ctx, n.options.Poll); err != nil {
			return err
		}
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
		pid, _, err := n.process(ctx)
		if err != nil {
			return fmt.Errorf("native reload cannot safely roll back pending: %w", err)
		}
		if pid != oldPID {
			return errors.New("native reload engine changed before rollback; pending left to seat-host")
		}
		screen, err := n.capture(ctx)
		if err != nil {
			return err
		}
		if composerShowsExit(screen) {
			for range len("/exit") {
				if err := n.client.SendKey(ctx, n.target, "BSpace"); err != nil {
					return fmt.Errorf("clear unsubmitted native /exit: %w", err)
				}
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

func (n *nativeReload) then(ctx context.Context, stderr io.Writer) error {
	if err := n.ready(ctx, false); err != nil {
		return fmt.Errorf("native reload --then: %w", err)
	}
	caller, err := pfmcall.New(
		pfmcall.Dependencies{
			WarningWriter: stderr,
			Options:       pfmcall.Options{DisableSignature: true, Clock: n.options.Clock},
		},
	)
	if err != nil {
		return fmt.Errorf("native reload --then client: %w", err)
	}
	result, err := caller.CallTarget(ctx, n.target, pfmcall.Request{Message: n.request.Then})
	if err != nil {
		return fmt.Errorf("native reload --then: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("native reload --then unconfirmed: %s", result.Message)
	}
	return nil
}
