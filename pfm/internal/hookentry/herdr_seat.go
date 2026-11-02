package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// identity is the hook's own identity snapshot: a hook runs in its chat's
// process tree, so the chat it reports for is this process's subject.
func (deps herdrDeps) identity() resolve.WhoamiEnvironment {
	return resolve.EnvironmentOf(deps.getenv)
}

// nativeSeat routes a proved durable native seat. Unproved native launchers
// must use the explicit restored repair route; tmux-in-Herdr retains its bridge.
func (reporter *herdrReporter) nativeSeat(ctx context.Context, payload herdrPayload) (handled bool, returnErr error) {
	identity := reporter.deps.identity()
	if reporter.deps.seatPaths == nil || identity.TMUX != "" || reporter.deps.getenv(herdr.EnvMarker) != "1" ||
		identity.HerdrSocket == "" ||
		identity.HerdrPane == "" {
		return false, nil
	}
	var db *fleetdb.Store
	if reporter.options.event == herdr.EventStart || reporter.options.event == herdr.EventEnd {
		// Start and end bind the seat through a writable open, which migrates an older file.
		var err error
		if db, err = fleetdb.OpenSharedStateUnlessOlder(ctx, *reporter.deps.seatPaths); err != nil {
			return true, fmt.Errorf("native seat ownership unproved: %w", err)
		}
	} else {
		db = fleetdb.OpenSharedStateReadOnly(ctx, *reporter.deps.seatPaths)
	}
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close native seat state: %w", err))
		}
	}()
	if errors.Is(db.Degraded(), fleetdb.ErrAbsent) {
		return true, fmt.Errorf(
			"native seat ownership unproved: no durable seat state; repair: restore the intended pane with pfm chat open --restored",
		)
	}
	if err := db.Degraded(); err != nil {
		return true, fmt.Errorf(
			"native seat ownership unproved: resolve native seat state: %w; repair: restore the intended pane with pfm chat open --restored",
			err,
		)
	}
	values := *reporter.deps.seatPaths
	if values.ProcRoot == "" {
		values.ProcRoot = reporter.deps.getenv(paths.EnvProcRoot)
	}
	proc := gather.NewProcFS(values.ProcRoot)
	seat, found, err := reporter.nativeReporterSeat(ctx, db, proc)
	if err != nil {
		return true, fmt.Errorf("resolve native seat: %w", err)
	}
	if !found {
		return true, fmt.Errorf(
			"native seat ownership unproved: no matching durable owner; repair: restore the intended pane with pfm chat open --restored",
		)
	}
	unlockSession, unlockBridge, err := reporter.lockNativeReport(ctx, seat)
	if err != nil {
		return true, err
	}
	defer unlockSession()
	unlockBridge = sync.OnceFunc(unlockBridge)
	defer unlockBridge()
	source := seat
	// Read-only hooks must not queue behind an unrelated database writer.
	// Only a proved canonicalization that could not persist may retry writable.
	var owned bool
	seat, owned, err = gather.ProveHerdrSeat(ctx, db, proc, seat)
	if errors.Is(err, gather.ErrHerdrProofPersistence) {
		writable := fleetdb.OpenSharedState(ctx, values)
		seat, owned, err = gather.ProveHerdrSeat(ctx, writable, proc, seat)
		err = errors.Join(err, writable.Close())
	}
	if err != nil {
		return true, fmt.Errorf("prove native reporter seat: %w", err)
	}
	if !owned {
		return true, fmt.Errorf("native reporter seat %s supervisor is dead", seat.SeatID)
	}
	if source.SeatHostPID > 0 &&
		(seat.SeatHostPID != source.SeatHostPID || seat.SeatHostStart != source.SeatHostStart || seat.SeatHostBirth != source.SeatHostBirth) {
		return true, fmt.Errorf("native reporter supervisor changed during proof")
	}

	if seat.Engine != string(reporter.options.engine) {
		return true, fmt.Errorf(
			"native seat %s engine %s does not match reporter %s",
			seat.SeatID,
			seat.Engine,
			reporter.options.engine,
		)
	}
	// A native seat has no tmux client lifecycle, including hooks from old bridges.
	if reporter.options.event.FiredByTmux() {
		return true, nil
	}
	if reporter.options.event == herdr.EventEnd && seat.ReloadPending {
		return true, nil
	}
	action := reporter.plan(payload)
	// Herdr detects a native Claude from the pane's foreground and keys its state
	// by herdr:claude, so pfm reports there; in a pane herdr-shield holds Herdr sees
	// the attach client, so pfm reports under its own source.
	// An event that changes nothing skips the read; a start still needs it.
	detected := false
	if reporter.options.engine == pfmengine.Claude && (action != nil || reporter.options.event == herdr.EventStart) {
		processes, err := reporter.deps.shield.Read(ctx, seat.HerdrSocket, seat.HerdrPane)
		if err != nil {
			// The read only picks the report's source: an unread pane still gets the state, under pfm's own.
			reporter.warn("pfm internal herdr-state %s: native Claude seat %s: read pane %s foreground "+
				"(any report goes under pfm's own source): %v", reporter.options.event, seat.SeatID, seat.HerdrPane, err)
		} else {
			detected = processes.Holder == nil
		}
	}
	if detected && reporter.options.event == herdr.EventStart && action == nil {
		// Compaction/fork still refresh session metadata, never lifecycle state.
		action = &herdrAction{}
	}
	if action == nil {
		return true, nil
	}
	if reporter.options.engine == pfmengine.Claude {
		owner, err := reporter.paneOwner(
			herdrTarget{socket: seat.HerdrSocket, pane: seat.HerdrPane, terminal: seat.HerdrTerminalID},
		)
		if err != nil {
			return true, err
		}
		if owner.Seq > reporter.seq {
			return true, fmt.Errorf("native Claude hook predates current ownership sequence")
		}
	}
	before := seat
	if payload.SessionID != "" {
		seat.SessionID = payload.SessionID
	}
	// A task notification opens a turn through UserPromptSubmit with the notification
	// as its prompt, but the user re-sent nothing.
	promptSubmitted := (payload.HookEventName == claudelaunch.HookEventUserPromptSubmit &&
		!strings.HasPrefix(payload.Prompt, taskNotificationPrompt)) ||
		(reporter.options.engine == pfmengine.OpenCode && payload.HookEventName == "chat.message")
	if reporter.options.event == herdr.EventWorking && promptSubmitted && !payload.IsInterrupt {
		seat.LostNote = ""
	}
	reporter.session = seat.SessionID
	// A bound state-only hook needs no schema work or durable heartbeat write.
	if reporter.options.event == herdr.EventStart || reporter.options.event == herdr.EventEnd ||
		seat.SessionID != before.SessionID || seat.LostNote != before.LostNote {
		writable := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
		defer func() {
			if err := writable.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close native seat binding: %w", err))
			}
		}()
		seat.UpdatedAt = reporter.deps.now().Unix()
		if err := writable.UpdateSeat(ctx, before, seat); err != nil {
			return true, fmt.Errorf("bind native seat: %w", err)
		}
	}
	reporter.nativeSeatReport = true
	action.stored.SessionID = seat.SessionID
	seq := reporter.seq
	if seq == 0 {
		seq = reporter.nextSent()
	}
	if err := reporter.reserveNativeOwnership(seat, seq); err != nil {
		return true, err
	}
	target := herdrTarget{socket: seat.HerdrSocket, pane: seat.HerdrPane, terminal: seat.HerdrTerminalID}
	payload.SessionID = seat.SessionID
	if detected && !action.release {
		err = reporter.reportNativeClaude(ctx, target, payload, action, seq)
	} else {
		err = reporter.send(ctx, target, action, seq)
	}
	if err != nil {
		return true, undelivered(err)
	}
	if reporter.options.event != herdr.EventEnd {
		// The name sync reads the transcript and renames through Herdr: no pane state write
		// needs the host-wide bridge, so every other chat's report stops waiting on it here.
		unlockBridge()
		return true, reporter.syncNativeEngineName(ctx, seat, payload)
	}
	return true, nil
}

// nativeReporterSeat treats inherited pane context as a hint only. The saved
// supervisor must be in this hook's kernel ancestry, even after pane moves.
func (reporter *herdrReporter) nativeReporterSeat(
	ctx context.Context,
	db *fleetdb.Store,
	proc gather.ProcFS,
) (fleetdb.Seat, bool, error) {
	seats, err := db.Seats(ctx)
	if err != nil {
		return fleetdb.Seat{}, true, fmt.Errorf("enumerate native reporter seats: %w", err)
	}
	identity := reporter.deps.identity()
	socket, pane := identity.HerdrSocket, identity.HerdrPane
	candidates, hinted := false, false
	for i := range seats {
		if seats[i].Backend == spawn.BackendHerdr && seats[i].HerdrSocket == socket {
			candidates = true
			hinted = hinted || seats[i].HerdrPane == pane
		}
	}
	if !candidates {
		return fleetdb.Seat{}, false, nil
	}
	chain, err := herdrAncestry(proc, reporter.deps.getpid())
	if err != nil {
		return fleetdb.Seat{}, true, fmt.Errorf("native reporter %w", err)
	}
	seat, found, err := gather.HerdrChatSeat(seats, proc, socket, pane, chain)
	if err != nil {
		return fleetdb.Seat{}, true, fmt.Errorf("native reporter: %w", err)
	}
	if found {
		return seat, true, nil
	}
	if hinted {
		return fleetdb.Seat{}, true, fmt.Errorf("native reporter is outside its saved supervisor ancestry")
	}
	return fleetdb.Seat{}, false, nil
}

// herdrAncestry is pid and its ancestors, nearest first, the chain
// gather.HerdrChatSeat looks for a chat's supervisor in. An ancestor that
// exited ends it; one that cannot be read is an error, and so is a cycle.
func herdrAncestry(proc gather.ProcFS, pid int) ([]int, error) {
	var chain []int
	seen := map[int]bool{}
	for depth := 0; pid > 1 && depth < herdrMaxProcessDepth; depth++ {
		if seen[pid] {
			return nil, fmt.Errorf("ancestry cycle at %d", pid)
		}
		seen[pid] = true
		chain = append(chain, pid)
		stat, err := proc.Stat(pid)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ancestor %d: %w", pid, err)
		}
		pid = stat.ParentPID
	}
	return chain, nil
}

// herdrChatPane is gather.HerdrChatPane for a hook: the pane the chat of pid
// lives in, its seat's when the seat's supervisor is an ancestor of pid, else
// envPane. A reporter with no seat state (a test seam) keeps envPane.
func (reporter *herdrReporter) herdrChatPane(ctx context.Context, socket, envPane string, pid int) (
	pane string,
	returnErr error,
) {
	if reporter.deps.seatPaths == nil {
		return envPane, nil
	}
	values := *reporter.deps.seatPaths
	if values.ProcRoot == "" {
		values.ProcRoot = reporter.deps.getenv(paths.EnvProcRoot)
	}
	proc := gather.NewProcFS(values.ProcRoot)
	chain, err := herdrAncestry(proc, pid)
	if err != nil {
		return "", fmt.Errorf("read the chat's %w", err)
	}
	state := fleetdb.OpenSharedStateReadOnly(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close the chat's seat state: %w", err))
		}
	}()
	pane, _, _, err = gather.HerdrChatPane(ctx, state, proc, socket, envPane, chain)
	return pane, err
}

func (reporter *herdrReporter) resumeArgv(sessionID string) ([]string, error) {
	if !reporter.nativeSeatReport {
		return herdr.ResumeArgv(sessionID)
	}
	// Charge the fixed restored flag to the validator's byte budget. The
	// resulting five words remain below its 64-word limit; the added suffix
	// contains no forbidden characters, so all session validation is retained.
	argv, err := herdr.ResumeArgv(sessionID + "--restored")
	if err != nil {
		return nil, err
	}
	argv[len(argv)-1] = sessionID
	return append(argv[:3:3], "--restored", sessionID), nil
}

// releaseSettled turns Herdr's pane-not-found for a release (the pane closed
// since it was resolved) into success; every other failure remains visible.
func releaseSettled(err error) error {
	if isHerdrNotFound(err) {
		return nil
	}
	return err
}

func isHerdrNotFound(err error) bool {
	var refusal *herdr.Refusal
	return errors.As(err, &refusal) && (refusal.Code == herdrPaneNotFound || refusal.Code == herdrNotFound)
}

// tmuxSeatLifecycle updates only this launcher's durable row; it does not
// change tmux bridge state, hooks, target selection or attach choreography.
func (reporter *herdrReporter) tmuxSeatLifecycle(ctx context.Context, payload herdrPayload) (returnErr error) {
	if payload.SessionID == "" ||
		(reporter.options.event != herdr.EventStart && reporter.options.event != herdr.EventEnd) {
		return nil
	}
	socket := reporter.deps.identity().TmuxSocket()
	db := fleetdb.OpenSharedState(ctx, *reporter.deps.seatPaths)
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close tmux seat state: %w", err))
		}
	}()
	seat, found, err := db.Seat(ctx, filepath.Base(socket))
	if err != nil {
		return fmt.Errorf("read tmux seat: %w", err)
	}
	if !found || seat.Backend != "tmux" {
		return nil
	}
	if reporter.options.event == herdr.EventEnd {
		if payload.Reason == herdrReasonClear || payload.Reason == herdrReasonResume || seat.ReloadPending {
			return nil
		}
		return db.DeleteSeat(ctx, seat.SeatID)
	}
	before := seat
	seat.SessionID = payload.SessionID
	return db.UpdateSeat(ctx, before, seat)
}

// herdrPayload is the part of an engine's hook payload the reporter reads.
type herdrPayload struct {
	TranscriptNull   bool            `json:"-"`
	HookEventName    string          `json:"hook_event_name"`
	SessionID        string          `json:"session_id"`
	AgentID          string          `json:"agent_id"`
	Source           string          `json:"source"`
	Reason           string          `json:"reason"`
	NotificationType string          `json:"notification_type"`
	Message          string          `json:"message"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	IsInterrupt      bool            `json:"is_interrupt"`
	Title            string          `json:"title"`
	TranscriptPath   string          `json:"transcript_path"`
	Prompt           string          `json:"prompt"`
}

// taskNotificationPrompt opens the prompt Claude Code submits when a background
// task reports back; its hook payload carries no other mark.
const taskNotificationPrompt = "<task-notification>"

// Native and legacy reports share SESSION then BRIDGE ordering, including re-proofs:
// the caller releases the bridge before the session.
func (reporter *herdrReporter) lockNativeReport(
	ctx context.Context,
	seat fleetdb.Seat,
) (unlockSession, unlockBridge func(), err error) {
	identity := fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	unlockSession, err = reporter.lock(ctx, identity)
	if err != nil {
		return nil, nil, fmt.Errorf("lock native reporter session: %w", err)
	}
	unlockBridge, err = reporter.lockBridge(ctx)
	if err != nil {
		unlockSession()
		return nil, nil, fmt.Errorf("lock native reporter bridge: %w", err)
	}
	return unlockSession, unlockBridge, nil
}

// This is the existing bridge owner-record protocol, not another ledger. A native
// owner has more identity components than any legacy socket/session/client tuple.
func (reporter *herdrReporter) reserveNativeOwnership(seat fleetdb.Seat, seq uint64) error {
	if seat.HerdrTerminalID == "" || seat.SeatHostPID <= 1 || seat.SeatHostStart == 0 || seq == 0 {
		return fmt.Errorf("reserve native seat %s: incomplete proved identity", seat.SeatID)
	}
	target := herdrTarget{socket: seat.HerdrSocket, pane: seat.HerdrPane, terminal: seat.HerdrTerminalID}
	current, err := reporter.paneOwner(target)
	if err != nil {
		return err
	}
	if current.Seq > seq {
		return fmt.Errorf("reserve native seat %s: older ownership sequence", seat.SeatID)
	}
	client := nativeOwnerClient(seat)
	if current.Client == client {
		return nil
	}
	owner := herdrPaneOwner{Client: client, Seq: seq}

	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("encode native seat %s ownership: %w", seat.SeatID, err)
	}
	path := reporter.paneOwnerPath(target)
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("reserve native seat %s ownership: %w", seat.SeatID, err)
	}
	return nil
}

func nativeOwnerClient(seat fleetdb.Seat) string {
	return fmt.Sprintf(
		"native\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s\x00%s\x00%s",
		seat.SeatID,
		seat.HerdrTerminalID,
		seat.SeatHostPID,
		seat.SeatHostStart,
		seat.SeatHostBirth,
		seat.Engine,
		seat.HerdrPane,
		seat.HerdrWorkspace,
	)
}
