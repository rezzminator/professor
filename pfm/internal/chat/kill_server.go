package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// ErrEndRefused is KillServer's refusal: the server was left running, because
// the chat's pane could not be proved its sole pane or tmux never said it died.
var ErrEndRefused = errors.New("end refused")

// serverDeathAttempts probes, serverDeathEvery apart, bound the wait for a
// sole-pane kill-server to leave a dead server.
const (
	serverDeathAttempts = 20
	serverDeathEvery    = 25 * time.Millisecond
)

// KillServer ends one chat's tmux server, only while paneID is the server's
// sole pane, then removes its socket, crumbs and branch-seat marker. A caller
// holding no pane id passes "": the server's one pane stands in for it, and a
// server holding several is refused. A picker row or a scan is a view: a pane
// can join the server after it, so kill.TmuxKiller.KillServerIfSolePane decides
// inside tmux, and only tmux's word that the server is dead clears its handles.
func KillServer(ctx context.Context, resolved paths.Values, socket, paneID string) (err error) {
	if filepath.IsAbs(socket) {
		return errors.New("KillServer requires a tmux socket name; use EndResolved for a native Herdr seat")
	}
	end := obs.Transition(ctx, "chat", "live", "ended", "KillServer")
	defer func() { end(err) }()
	socketPath := filepath.Join(resolved.TmuxDir, socket)
	if paneID == "" {
		state, panes, probeErr := gather.ProbeServer(ctx, "", socketPath)
		if state == gather.ServerDead {
			return clearServerHandles(ctx, resolved, socket)
		}
		if state != gather.ServerAlive || len(panes) != 1 {
			return fmt.Errorf(
				"server %s is %s with %d panes: no sole pane proves the server is one chat's own: %w",
				socket,
				state,
				len(panes),
				errors.Join(probeErr, ErrEndRefused),
			)
		}
		paneID = panes[0].PaneID
	}
	soleErr := kill.TmuxKiller{}.KillServerIfSolePane(ctx, socketPath, paneID)
	// kill-server returns before the server has exited: a probe in between
	// can still answer alive. Only a dead answer within the bound ends the
	// chat; an unresponsive server is not waited on twice.
	tmux := action.TmuxExecutor{TmuxDir: resolved.TmuxDir}
	var state gather.ServerState
	var probeErr error
	for attempt := range serverDeathAttempts {
		state, probeErr = tmux.ServerState(ctx, socket)
		if state == gather.ServerDead || state == gather.ServerUnresponsive || attempt == serverDeathAttempts-1 {
			break
		}
		if err := clock.Real.Sleep(ctx, serverDeathEvery); err != nil {
			probeErr = errors.Join(probeErr, err)
			break
		}
	}
	if state != gather.ServerDead {
		return fmt.Errorf(
			"server %s is still %s after kill-server, which tmux runs only while pane %s is the server's sole pane; "+
				"a server holding another pane is never ended: %w",
			socket,
			state,
			paneID,
			errors.Join(soleErr, probeErr, ErrEndRefused),
		)
	}
	return clearServerHandles(ctx, resolved, socket)
}

// clearServerHandles removes a dead server's socket, crumbs and branch-seat
// marker.
func clearServerHandles(ctx context.Context, resolved paths.Values, socket string) error {
	_ = os.Remove(filepath.Join(resolved.TmuxDir, socket))
	entries, _ := os.ReadDir(resolved.SIDDir)
	for _, entry := range entries {
		if name, _, ok := gather.ParseCrumbName(entry.Name()); ok && name == socket {
			_ = os.Remove(filepath.Join(resolved.SIDDir, entry.Name()))
		}
	}
	shared := fleetdb.OpenSharedState(ctx, resolved)
	clearErr := shared.ClearBranchSeat(ctx, socket)
	closeErr := shared.Close()
	if clearErr != nil || closeErr != nil {
		return fmt.Errorf("clear branch marker after ending %s: %w", socket, errors.Join(clearErr, closeErr))
	}
	return nil
}

// endsNatively is the one end decision every end door asks: a Herdr seat ends
// by the proved pane close, every other chat by ending its tmux server.
func endsNatively(chat headless.Chat) bool {
	return chat.Backend == spawn.BackendHerdr
}

// EndHow names how the end doors end chat and what they verify, for a plan
// printed before the end (pfm restart).
func EndHow(chat headless.Chat) string {
	if endsNatively(chat) {
		return "Herdr pane.close of the proved seat; verified: the seat is retired"
	}
	return "tmux kill-server on " + chat.Socket + "; verified: tmux says the server is dead"
}

// EndNow ends one live chat in this process and confirms it is gone: a Herdr
// seat through the same proved pane close the native end worker runs, a tmux
// chat through endTmux, then its role prompt removed. Its caller already runs
// outside every chat (pfm restart's detached worker), so nothing here waits for
// a turn to end. A host restart uses EndForRestart to retain the native resume
// recipe.
func EndNow(ctx context.Context, chat headless.Chat, values paths.Values) error {
	if endsNatively(chat) {
		return endHerdr(ctx, chat, values)
	}
	if err := endTmux(ctx, chat, values); err != nil {
		return err
	}
	if err := agentrole.RemoveSeatPrompt(values.SIDDir, chat.Socket, chat.Pane); err != nil {
		return fmt.Errorf("chat %s ended, but its role prompt stays: %w", chat.ID, err)
	}
	return nil
}

// endTmux is the one tmux end: KillServer on the chat's pane, which returns nil
// only once tmux says the server is dead. An unresponsive server is not proof
// of an end.
func endTmux(ctx context.Context, chat headless.Chat, values paths.Values) error {
	if chat.Socket == "" {
		return fmt.Errorf("chat %s has neither a Herdr seat nor a tmux socket to end", chat.ID)
	}
	if err := KillServer(ctx, values, chat.Socket, chat.Pane); err != nil {
		return fmt.Errorf("end tmux server %s: %w", chat.Socket, err)
	}
	return nil
}

// EndForRestart closes a chat without discarding a native conversation's launch
// identity and account. The same ownership lock covers closing and retirement:
// a concurrent schedule may resume only after the old supervisor is proved gone.
func EndForRestart(ctx context.Context, chat headless.Chat, values paths.Values) error {
	if endsNatively(chat) {
		return endHerdrSteps(ctx, chat, values, io.Discard, true)
	}
	return EndNow(ctx, chat, values)
}

// EndTarget handles durable native worker/retry paths before asking the caller
// to resolve its normal target. The CLI retains target-error exit-code mapping.
func EndTarget(
	ctx context.Context,
	target string,
	worker bool,
	stdout, stderr io.Writer,
	runtime *pfmconfig.Runtime,
	resolve func(context.Context) (headless.Chat, int),
) int {
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat end: runtime: %v\n", err)
		return 1
	}
	if worker {
		// stderr is the worker's end-{seat}.log, so its steps land there,
		// the failure line included: it is written once, by the steps.
		if err := endNativeWorkerSteps(ctx, target, effective.Paths, stderr); err != nil {
			return 1
		}
		return 0
	}
	pending, found, err := PendingNativeEnd(ctx, target, effective.Paths)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
		return 1
	}
	if found {
		return EndResolved(ctx, pending, stdout, stderr, &effective)
	}
	chat, code := resolve(ctx)
	if code != 0 {
		return code
	}
	return EndResolved(ctx, chat, stdout, stderr, &effective)
}

// EndResolved ends a resolved live chat without re-resolving its identity.
func EndResolved(ctx context.Context, chat headless.Chat, stdout, stderr io.Writer, runtime *pfmconfig.Runtime) int {
	effective, err := pfmconfig.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
		return 1
	}
	if endsNatively(chat) {
		if err := ScheduleHerdrEnd(ctx, chat, effective); err != nil {
			fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "end scheduled %s\n", chat.ID)
		return 0
	}
	if err := endTmux(ctx, chat, effective.Paths); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
		return 1
	}
	// The seat is dead, so its prompt file is litter. Cleanup is best-effort:
	// the end itself succeeded and a prompt-file failure must remain visible
	// without changing that result.
	if err := agentrole.RemoveSeatPrompt(effective.Paths.SIDDir, chat.Socket, chat.Pane); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: WARNING: could not remove role prompt: %v\n", err)
	}
	fmt.Fprintf(stdout, "ended %s\n", chat.ID)
	return 0
}

// ScheduleHerdrEnd durably records intent before handing retirement to a process
// outside the pane session. A failed launch intentionally leaves a retryable row.
func ScheduleHerdrEnd(ctx context.Context, chat headless.Chat, runtime pfmconfig.Runtime) (returnErr error) {
	identity := fleetdb.ConversationIdentity(chat.ID, chat.SeatID)
	unlock, err := fleetdb.LockSeat(ctx, runtime.Paths, identity)
	if err != nil {
		return fmt.Errorf("lock native end: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	seat, found, err := db.Seat(ctx, chat.SeatID)
	if err != nil {
		return fmt.Errorf("read native end intent: %w", err)
	}
	if !found || seat.Backend != spawn.BackendHerdr || seat.SessionID != chat.ID {
		return fmt.Errorf("native end target %s no longer matches its durable seat", chat.SeatID)
	}
	if filepath.Base(seat.SeatID) != seat.SeatID || strings.ContainsRune(seat.SeatID, '\\') {
		return fmt.Errorf("invalid native end seat id %q", seat.SeatID)
	}
	seat, err = proveNativeEnd(ctx, db, seat, runtime.Paths)
	if err != nil {
		return err
	}

	image, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve native end worker image: %w", err)
	}
	log, err := openEndWorkerLog(runtime.Paths.SIDDir, paths.SIDEndLogPrefix+seat.SeatID+".log")
	if err != nil {
		return fmt.Errorf("open end worker log: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, log.Close()) }()
	if !seat.EndPending {
		before := seat
		seat.EndPending = true
		if err := db.UpdateSeat(ctx, before, seat); err != nil {
			return fmt.Errorf("persist native end intent: %w", err)
		}
	}
	argv := []string{image}
	if runtime.Config.Path != "" {
		argv = append(argv, "--config", runtime.Config.Path)
	}
	argv = append(argv, "chat", "end", "--native-end-worker", seat.SeatID)
	process, err := obs.Runner(deps.RealRunner{}).
		Start(context.Background(), argv, deps.StartOptions{Detach: true, Env: herdr.StripReporter(os.Environ()), Stdout: log, Stderr: log})
	if err != nil {
		return fmt.Errorf("start native end worker (intent retained): %w", err)
	}
	if err := process.Release(); err != nil {
		return fmt.Errorf("release native end worker: %w", err)
	}
	return nil
}

// EndNativeWorker consumes only an exact durable seat id, never ambient pane identity.
func EndNativeWorker(ctx context.Context, id string, values paths.Values) error {
	return endNativeWorkerSteps(ctx, id, values, io.Discard)
}

// endNativeWorkerSteps is EndNativeWorker that writes each step it takes to steps.
func endNativeWorkerSteps(ctx context.Context, id string, values paths.Values, steps io.Writer) error {
	db := fleetdb.OpenSharedState(ctx, values)
	seat, found, err := db.Seat(ctx, id)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		err = fmt.Errorf("read end worker seat: %w", errors.Join(err, closeErr))
		endStep(steps, "failed: %v", err)
		return err
	}
	if !found {
		endStep(steps, "seat %s not found: nothing to end", id)
		return nil
	}
	if !seat.EndPending {
		err := fmt.Errorf("seat %s has no pending end intent", id)
		endStep(steps, "failed: %v", err)
		return err
	}
	return endHerdrSteps(
		ctx,
		headless.Chat{Backend: seat.Backend, SeatID: id, ID: seat.SessionID},
		values,
		steps,
		false,
	)
}

// endHerdr closes only a proved canonical owner, and retires only after close.
func endHerdr(ctx context.Context, chat headless.Chat, values paths.Values) error {
	return endHerdrSteps(ctx, chat, values, io.Discard, false)
}

// endStep writes one timestamped line of the end worker's progress; the worker
// points steps at its end-{seat}.log, so the log is never empty after a run.
func endStep(steps io.Writer, format string, args ...any) {
	fmt.Fprintf(
		steps,
		"%s pfm chat end worker: %s\n",
		clock.Real.Now().UTC().Format(time.RFC3339),
		fmt.Sprintf(format, args...),
	)
}

// endHerdrSteps is endHerdr that writes each step it takes to steps, and the
// error it returns.
func endHerdrSteps(ctx context.Context, chat headless.Chat, values paths.Values, steps io.Writer,
	preserve bool,
) (returnErr error) {
	defer func() {
		if returnErr != nil {
			endStep(steps, "failed: %v", returnErr)
		}
	}()
	identity := fleetdb.ConversationIdentity(chat.ID, chat.SeatID)
	unlock, err := fleetdb.LockSeat(ctx, values, identity)
	if err != nil {
		return fmt.Errorf("lock end worker: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	state := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, state.Close()) }()
	seat, found, err := state.Seat(ctx, chat.SeatID)
	if err != nil {
		return fmt.Errorf("read end seat %s: %w", chat.SeatID, err)
	}
	if !found {
		endStep(steps, "seat %s not found: nothing to end", chat.SeatID)
		return nil
	}
	endStep(steps, "seat read: %s pane %s", seat.SeatID, seat.HerdrPane)
	if seat.Backend != spawn.BackendHerdr || seat.SessionID != chat.ID {
		return fmt.Errorf("native end target %s no longer matches its durable seat", chat.SeatID)
	}
	if preserve {
		if _, err := state.ReadSeatRun(seat.SeatID); err != nil {
			return fmt.Errorf("preserve restart recipe before closing %s: %w", seat.SeatID, err)
		}
	}
	pending := seat.EndPending
	seat, owned, proofErr := gather.ProveHerdrSeat(ctx, state, gather.NewProcFS(values.ProcRoot), seat)
	if proofErr != nil || !owned {
		if !pending {
			return fmt.Errorf(
				"prove end owner %s: %w",
				seat.SeatID,
				errors.Join(proofErr, errors.New("supervisor ownership unproved")),
			)
		}
		endStep(steps, "owner unproved (pending end): confirming the terminal is gone")
		absent, err := endedTerminalAbsent(ctx, seat, values)
		if err != nil || !absent {
			return fmt.Errorf(
				"reconcile pending end %s: %w",
				seat.SeatID,
				errors.Join(proofErr, err, errors.New("terminal disappearance unconfirmed")),
			)
		}
		endStep(steps, "terminal absent confirmed")
	} else {
		endStep(steps, "owner proved")
		if !seat.EndPending {
			before := seat
			seat.EndPending = true
			if err := state.UpdateSeat(ctx, before, seat); err != nil {
				return fmt.Errorf("record end intent: %w", err)
			}
		}
		endStep(steps, "pane.close sending: %s", seat.HerdrPane)
		body, err := herdr.SeatRequest(
			ctx,
			seat.HerdrSocket,
			"pane.close",
			map[string]string{nativePaneIDKey: seat.HerdrPane},
		)
		if err == nil {
			// An ack pfm cannot read is a failed close: whether the pane went is
			// then confirmed from Herdr's terminal list, as after a refused close.
			var ack struct {
				Type string `json:"type"`
				Pane string `json:"pane_id"`
			}
			if decodeErr := json.Unmarshal(body, &ack); decodeErr != nil {
				err = fmt.Errorf("decode Herdr pane close: %w", decodeErr)
			} else if ack.Type != "ok" && (ack.Type != "pane_closed" || ack.Pane != seat.HerdrPane) {
				err = fmt.Errorf("unexpected Herdr pane close acknowledgement: %s", body)
			}
		}
		if err != nil {
			endStep(steps, "pane.close failed: %v", err)
			absent, confirmErr := endedTerminalAbsent(ctx, seat, values)
			if confirmErr != nil || !absent {
				return fmt.Errorf(
					"close Herdr pane %s (intent retained): %w",
					seat.HerdrPane,
					errors.Join(err, confirmErr),
				)
			}
			endStep(steps, "terminal absent confirmed after the close failure")
		} else {
			endStep(steps, "pane.close acknowledged")
		}
	}
	if preserve {
		// pane.close may acknowledge before the child exits. Retain end intent
		// on timeout so no opener can overlap the retiring supervisor.
		deadline := clock.Real.Now().Add(5 * time.Second)
		for {
			gone, err := gather.HerdrSeatRetired(ctx, gather.NewProcFS(values.ProcRoot), seat)
			if err != nil {
				return fmt.Errorf("verify restart seat %s retired: %w", seat.SeatID, err)
			}
			if gone {
				break
			}
			if !clock.Real.Now().Before(deadline) {
				return fmt.Errorf(
					"restart seat %s supervisor still running after pane.close; end intent retained",
					seat.SeatID,
				)
			}
			if err := clock.Real.Sleep(ctx, 50*time.Millisecond); err != nil {
				return fmt.Errorf("wait for restart seat %s retirement: %w", seat.SeatID, err)
			}
		}
	}
	// Before the seat row goes, so a failure here leaves the seat retryable.
	if err := herdr.RemoveReloadFiles(values.SIDDir, seat.SeatID); err != nil {
		return fmt.Errorf("remove ended seat reload files: %w", err)
	}
	if preserve {
		before := seat
		seat.State, seat.EndPending, seat.ReloadPending = "lost", false, false
		seat.LostNote = "retired by pfm restart; resume recipe retained"
		if err := state.UpdateSeat(ctx, before, seat); err != nil {
			return fmt.Errorf("retain restart seat %s for resume: %w", seat.SeatID, err)
		}
		endStep(steps, "seat retired for resume: %s", seat.SeatID)
		return nil
	}
	if err := state.DeleteSeatRun(seat.SeatID); err != nil {
		return fmt.Errorf("remove ended seat recipe: %w", err)
	}
	if err := state.DeleteSeat(ctx, seat.SeatID); err != nil {
		return fmt.Errorf("remove ended seat: %w", err)
	}
	endStep(steps, "seat retired: %s", seat.SeatID)
	return nil
}

func endedTerminalAbsent(ctx context.Context, seat fleetdb.Seat, values paths.Values) (bool, error) {
	if seat.HerdrTerminalID == "" {
		if seat.SeatHostPID == 0 {
			return gather.HerdrSeatRetired(ctx, gather.NewProcFS(values.ProcRoot), seat)
		}
		return false, errors.New("pending seat has no terminal identity")
	}
	listed, err := gather.HerdrTerminalListed(ctx, seat.HerdrSocket, seat.HerdrTerminalID)
	if err != nil {
		return false, fmt.Errorf("confirm ended terminal: %w", err)
	}
	return !listed, nil
}

// PendingNativeEnd finds durable intent without a live roster, so a retry still
// works after Herdr already removed the pane. Ambiguous names are refused.
func PendingNativeEnd(ctx context.Context, target string, values paths.Values) (headless.Chat, bool, error) {
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return headless.Chat{}, false, fmt.Errorf("read pending native ends: %w", err)
	}
	var match *fleetdb.Seat
	for i := range seats {
		seat := &seats[i]
		if seat.Backend != spawn.BackendHerdr || !seat.EndPending {
			continue
		}
		if target != seat.SeatID && target != seat.SessionID &&
			(seat.Name == "" || !strings.EqualFold(target, seat.Name)) {
			continue
		}
		if match != nil {
			return headless.Chat{}, false, fmt.Errorf(
				"pending end target %q is ambiguous; use the conversation id",
				target,
			)
		}
		match = seat
	}
	if match == nil {
		return headless.Chat{}, false, nil
	}
	db := fleetdb.OpenSharedState(ctx, values)
	canonical, proofErr := proveNativeEnd(ctx, db, *match, values)
	if err := errors.Join(proofErr, db.Close()); err != nil {
		return headless.Chat{}, false, err
	}
	match = &canonical
	return headless.Chat{
		Backend: match.Backend,
		SeatID:  match.SeatID,
		ID:      match.SessionID,
		Name:    match.Name,
		Socket:  match.HerdrSocket,
		Pane:    match.HerdrPane,
	}, true, nil
}

// Pin every path component before opening the detached worker's append sink.
func openEndWorkerLog(path, name string) (_ *os.File, returnErr error) {
	directory, err := endWorkerLogDirectory(path)
	if err != nil {
		return nil, fmt.Errorf("open end worker log directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, directory.Close()) }()
	info, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat end worker log directory: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("end worker log directory is not a protected user directory")
	}
	fd, err := unix.Openat(
		int(directory.Fd()),
		name,
		unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open end worker log leaf: %w", err)
	}
	log := os.NewFile(uintptr(fd), name)
	info, err = log.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("stat end worker log: %w", err), log.Close())
	}
	owner, ok = info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o077 != 0 {
		return nil, errors.Join(errors.New("end worker log is not a private single-link regular file"), log.Close())
	}
	return log, nil
}

func endWorkerLogDirectory(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve end worker log directory: %w", err)
	}
	directory, err := os.Open(string(filepath.Separator))
	if err != nil {
		return nil, fmt.Errorf("open end worker log root: %w", err)
	}
	parts := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	links := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		if part == "" {
			continue
		}
		flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		fd, openErr := unix.Openat(int(directory.Fd()), part, flags, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if err := unix.Mkdirat(int(directory.Fd()), part, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
				return nil, errors.Join(
					fmt.Errorf("create end worker log directory %q: %w", part, err),
					directory.Close(),
				)
			}
			fd, openErr = unix.Openat(int(directory.Fd()), part, flags, 0)
		}
		if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
			target, linkErr := endWorkerLogLink(directory, part)
			links++
			if linkErr != nil || links > 40 {
				return nil, errors.Join(
					fmt.Errorf("refuse end worker log alias %q: %w", part, errors.Join(linkErr, openErr)),
					directory.Close(),
				)
			}
			if filepath.IsAbs(target) {
				if err := directory.Close(); err != nil {
					return nil, fmt.Errorf("close end worker alias ancestor: %w", err)
				}
				directory, err = os.Open(string(filepath.Separator))
				if err != nil {
					return nil, fmt.Errorf("open end worker alias root: %w", err)
				}
			}
			parts = append(strings.Split(target, string(filepath.Separator)), parts...)
			continue
		}
		if openErr != nil {
			return nil, errors.Join(
				fmt.Errorf("open end worker log directory %q: %w", part, openErr),
				directory.Close(),
			)
		}
		next := os.NewFile(uintptr(fd), part)
		if err := directory.Close(); err != nil {
			return nil, errors.Join(fmt.Errorf("close end worker log ancestor: %w", err), next.Close())
		}
		directory = next
	}
	return directory, nil
}

// A trusted alias is expanded through the same descriptor walk, never followed by the kernel.
func endWorkerLogLink(directory *os.File, name string) (string, error) {
	var parent, link unix.Stat_t
	if err := unix.Fstat(int(directory.Fd()), &parent); err != nil {
		return "", fmt.Errorf("stat log alias parent: %w", err)
	}
	trusted := func(uid uint32) bool { return uid == 0 || uid == uint32(os.Geteuid()) }
	if !trusted(parent.Uid) || parent.Mode&0o022 != 0 {
		return "", errors.New("log alias parent is not protected")
	}
	if err := unix.Fstatat(int(directory.Fd()), name, &link, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return "", fmt.Errorf("stat log alias: %w", err)
	}
	if link.Mode&unix.S_IFMT != unix.S_IFLNK || !trusted(link.Uid) {
		return "", errors.New("log alias is not trusted")
	}
	buffer := make([]byte, 4096)
	n, err := unix.Readlinkat(int(directory.Fd()), name, buffer)
	if err != nil {
		return "", fmt.Errorf("read log alias: %w", err)
	}
	if n == 0 || n == len(buffer) {
		return "", errors.New("log alias target is empty or too long")
	}
	return string(buffer[:n]), nil
}

// A stored end intent is not authority: older binaries can leave it on a new binding.
func proveNativeEnd(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	values paths.Values,
) (fleetdb.Seat, error) {
	proved, owned, err := gather.ProveHerdrSeat(ctx, db, gather.NewProcFS(values.ProcRoot), seat)
	if err != nil {
		return seat, fmt.Errorf("prove native end: %w", err)
	}
	if !owned {
		absent, confirmErr := endedTerminalAbsent(ctx, proved, values)
		if confirmErr != nil || !absent {
			return seat, fmt.Errorf(
				"dead end supervisor terminal absence unconfirmed: %w",
				errors.Join(confirmErr, errors.New("owner retained")),
			)
		}
	}
	return proved, nil
}
