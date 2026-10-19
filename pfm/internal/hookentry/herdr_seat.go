package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// nativeSeat routes a proved durable native seat. Unproved native launchers
// must use the explicit restored repair route; tmux-in-Herdr retains its bridge.
func (reporter *herdrReporter) nativeSeat(ctx context.Context, payload herdrPayload) (handled bool, returnErr error) {
	env := reporter.deps.getenv
	if reporter.deps.seatPaths == nil || env("TMUX") != "" || env(herdr.EnvMarker) != "1" ||
		env(herdr.EnvSocket) == "" ||
		env(herdr.EnvPane) == "" {
		return false, nil
	}
	db := fleetdb.OpenSharedStateReadOnly(ctx, *reporter.deps.seatPaths)
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
		values.ProcRoot = env(paths.EnvProcRoot)
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
	unlock, err := reporter.lockNativeReport(ctx, seat)
	if err != nil {
		return true, err
	}
	defer unlock()
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
	if action == nil {
		return true, nil
	}
	before := seat
	if payload.SessionID != "" {
		seat.SessionID = payload.SessionID
	}
	promptSubmitted := payload.HookEventName == "UserPromptSubmit" ||
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
	if err := reporter.send(ctx, herdrTarget{socket: seat.HerdrSocket, pane: seat.HerdrPane}, action, seq); err != nil {
		return true, err
	}
	if reporter.options.event != herdr.EventEnd {
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
	socket, pane := reporter.deps.getenv(herdr.EnvSocket), reporter.deps.getenv(herdr.EnvPane)
	var candidates []*fleetdb.Seat
	hinted := false
	for i := range seats {
		seat := &seats[i]
		if seat.Backend == spawn.BackendHerdr && seat.HerdrSocket == socket {
			candidates = append(candidates, seat)
			hinted = hinted || seat.HerdrPane == pane
		}
	}
	if len(candidates) == 0 {
		return fleetdb.Seat{}, false, nil
	}
	seen := map[int]bool{}
	for pid, depth := reporter.deps.getpid(), 0; pid > 1 && depth < herdrMaxProcessDepth; depth++ {
		if seen[pid] {
			return fleetdb.Seat{}, true, fmt.Errorf("native reporter ancestry cycle at %d", pid)
		}
		seen[pid] = true
		for _, seat := range candidates {
			if seat.SeatHostPID == pid {
				return *seat, true, nil
			}
			if seat.SeatHostPID == 0 && seat.HerdrPane == pane {
				argv, err := proc.Cmdline(pid)
				if err != nil {
					return fleetdb.Seat{}, true, fmt.Errorf("read legacy reporter ancestor %d: %w", pid, err)
				}
				// This only locates a legacy candidate; shared proof validates full argv.
				if gather.HerdrSupervisorArgv(argv, seat.SeatID) {
					return *seat, true, nil
				}
			}
		}
		stat, err := proc.Stat(pid)
		if err != nil {
			return fleetdb.Seat{}, true, fmt.Errorf("read native reporter ancestor %d: %w", pid, err)
		}
		pid = stat.ParentPID
	}
	if hinted {
		return fleetdb.Seat{}, true, fmt.Errorf("native reporter is outside its saved supervisor ancestry")
	}
	return fleetdb.Seat{}, false, nil
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
	socket, _, _ := strings.Cut(reporter.deps.getenv("TMUX"), ",")
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

// Native and legacy reports share SESSION then BRIDGE ordering, including re-proofs.
func (reporter *herdrReporter) lockNativeReport(ctx context.Context, seat fleetdb.Seat) (func(), error) {
	identity := fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	unlockSession, err := reporter.lock(ctx, identity)
	if err != nil {
		return nil, fmt.Errorf("lock native reporter session: %w", err)
	}
	deps := reporter.deps
	deps.lockDir = filepath.Join(deps.lockDir, "pfm-herdr-bridge")
	unlockBridge, err := (&herdrReporter{deps: deps}).lock(ctx, "transaction")
	if err != nil {
		unlockSession()
		return nil, fmt.Errorf("lock native reporter bridge: %w", err)
	}
	return func() { unlockBridge(); unlockSession() }, nil
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
