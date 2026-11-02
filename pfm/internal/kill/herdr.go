package kill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	herdrPaneKey  = "pane_id"
	herdrBackend  = "herdr"
	liveSeatState = "live"
	// defaultHeldShellWait bounds how long a held pane's close waits for
	// herdr-shield to end the holder's shell, polled every heldShellPoll.
	defaultHeldShellWait = 5 * time.Second
	heldShellPoll        = 100 * time.Millisecond
)

// herdrTarget resolves ownership before the index fallback: a new native engine
// can be live before it has flushed any transcript. Environment is not routing.
func (manager *Manager) herdrTarget(ctx context.Context, request Request) (Target, bool, error) {
	nativeSelf := request.Self
	owner := request.ID
	if request.SocketName != "" && request.PaneID == "" && request.ID != "" && filepath.IsAbs(request.SocketName) {
		// A native seat whose pane was never bound carries its Herdr socket and
		// no pane (a tmux socket name is never absolute): its stable identity
		// alone routes, so the half address is dropped, never acted on.
		request.SocketName = ""
	}
	if (request.SocketName == "") != (request.PaneID == "") {
		return Target{}, false, fmt.Errorf("resolved live address requires socket and pane")
	}
	var seat fleetdb.Seat
	var found bool
	var err error
	switch {
	case request.SocketName != "" && request.PaneID != "":
		seat, found, err = manager.database.Shared().SeatByPane(ctx, request.SocketName, request.PaneID)
	case request.ID != "":
		seat, found, err = manager.database.Shared().SeatBySession(ctx, request.ID)
		if err == nil && !found {
			seat, found, err = manager.database.Shared().Seat(ctx, request.ID)
		}
	default:
		return Target{}, false, nil
	}
	if err != nil {
		lookupErr := fmt.Errorf("resolve kill seat: %w", err)
		if request.SocketName == "" || filepath.Base(request.SocketName) != request.SocketName {
			return Target{}, false, lookupErr
		}
		target := Target{SocketPath: filepath.Join(manager.paths.tmuxDir, request.SocketName), PaneID: request.PaneID}
		return Target{}, false, warnTmuxSeatFallback(manager.paths.tmuxDir, target, manager.log, lookupErr)
	}
	if !found && owner != "" && request.SocketName != "" {
		candidate, ok, lookupErr := manager.database.Shared().SeatBySession(ctx, owner)
		if lookupErr != nil {
			return Target{}, false, fmt.Errorf("resolve moved native kill owner: %w", lookupErr)
		}
		if ok && candidate.Backend == herdrBackend && candidate.State == liveSeatState &&
			candidate.HerdrSocket == request.SocketName {
			values, err := paths.Resolve()
			if err != nil {
				return Target{}, false, fmt.Errorf("resolve moved native kill paths: %w", err)
			}
			proved, owned, err := gather.ResolveHerdrSeat(ctx, values, candidate)
			if err != nil {
				return Target{}, false, fmt.Errorf("prove moved native kill owner: %w", err)
			}
			if !owned {
				dead, err := gather.HerdrSeatRetired(ctx, gather.NewProcFS(values.ProcRoot), candidate)
				if err != nil {
					return Target{}, false, fmt.Errorf("prove native ghost host: %w", err)
				}
				if !dead {
					return Target{}, false, fmt.Errorf("native kill explicit pane has no live proved owner")
				}
			}
			if owned && proved.HerdrPane != request.PaneID {
				body, err := herdr.SeatRequest(
					ctx,
					proved.HerdrSocket,
					"pane.get",
					map[string]string{herdrPaneKey: request.PaneID},
				)
				if err != nil {
					return Target{}, false, fmt.Errorf("resolve native kill caller alias: %w", err)
				}
				var alias struct {
					Type string `json:"type"`
					Pane struct {
						ID        string `json:"pane_id"`
						Terminal  string `json:"terminal_id"`
						Workspace string `json:"workspace_id"`
					} `json:"pane"`
				}
				if err := json.Unmarshal(body, &alias); err != nil {
					return Target{}, false, fmt.Errorf("decode native kill caller alias: %w", err)
				}
				if alias.Type != "pane_info" || alias.Pane.ID != proved.HerdrPane ||
					alias.Pane.Terminal != proved.HerdrTerminalID ||
					alias.Pane.Workspace != proved.HerdrWorkspace {
					return Target{}, false, fmt.Errorf("native kill explicit pane is not the proved owner")
				}
			}
			seat, found = proved, true
		}
	}
	if !found || seat.Backend != herdrBackend || seat.State != liveSeatState {
		if nativeSelf {
			return Target{}, false, fmt.Errorf(
				"could not identify this native chat: no live seat for Herdr pane %s on %s",
				request.PaneID,
				request.SocketName,
			)
		}
		return Target{}, false, nil
	}
	if request.ID != "" && request.ID != seat.SessionID && request.ID != seat.SeatID {
		return Target{}, false, fmt.Errorf(
			"kill seat %s belongs to session %q, not %q",
			seat.SeatID,
			seat.SessionID,
			request.ID,
		)
	}
	engine := pfmengine.ID(seat.Engine)
	if _, lookupErr := pfmengine.Lookup(
		engine,
	); lookupErr != nil ||
		(request.Engine != "" && request.Engine != engine) {
		return Target{}, false, fmt.Errorf(
			"kill seat %s engine mismatch: recorded %q, requested %q",
			seat.SeatID,
			seat.Engine,
			request.Engine,
		)
	}
	proved, owned, err := (herdrKiller{database: manager.database, seat: seat}).prove(ctx)
	if err != nil {
		return Target{}, false, fmt.Errorf("resolve native kill ownership: %w", err)
	}
	seat = proved
	id := seat.SessionID
	if id == "" {
		id = seat.SeatID
	}
	if !owned {
		values, err := paths.Resolve()
		if err != nil {
			return Target{}, false, fmt.Errorf("resolve native ghost paths: %w", err)
		}
		dead, err := gather.HerdrSeatRetired(ctx, gather.NewProcFS(values.ProcRoot), seat)
		if err != nil {
			return Target{}, false, fmt.Errorf("prove native ghost host: %w", err)
		}
		if !dead {
			return Target{}, false, fmt.Errorf("native kill has no live proved owner")
		}
		// A positively dead saved supervisor authorizes hiding this identity,
		// never mutating its stale address (which may now name another pane).
		return Target{Engine: engine, ID: id}, true, nil
	}
	return Target{
		Engine:     engine,
		ID:         id,
		SocketName: seat.SeatID,
		SocketPath: seat.HerdrSocket,
		PaneID:     seat.HerdrPane,
	}, true, nil
}

// targetClient freezes this run's routing from the durable positive owner.
// A vanished native record never sends an absolute Herdr socket to tmux.
func targetClient(
	ctx context.Context,
	database *store.Store,
	fallback TmuxClient,
	tmuxDir, sidDir string,
	target Target,
	log io.Writer,
) (TmuxClient, bool, error) {
	seat, found, err := database.Shared().SeatByPane(ctx, target.SocketPath, target.PaneID)
	if err != nil {
		if fallbackErr := warnTmuxSeatFallback(
			tmuxDir,
			target,
			log,
			fmt.Errorf("read kill pane owner: %w", err),
		); fallbackErr != nil {
			return nil, false, fallbackErr
		}
		return fallback, false, nil
	}
	if !found && target.ID != "" {
		candidate, ok, lookupErr := database.Shared().SeatBySession(ctx, target.ID)
		if lookupErr != nil {
			// The same degraded-lookup door as the pane read above: an explicit
			// pfm tmux address goes on, warned; any other address stays refused.
			if fallbackErr := warnTmuxSeatFallback(
				tmuxDir,
				target,
				log,
				fmt.Errorf("resolve moved kill seat: %w", lookupErr),
			); fallbackErr != nil {
				return nil, false, fallbackErr
			}
			return fallback, false, nil
		}
		if ok && candidate.Backend == herdrBackend && candidate.HerdrSocket == target.SocketPath {
			seat, found = candidate, true
		}
	}
	if found {
		if (target.ID != "" && target.ID != seat.SessionID && target.ID != seat.SeatID) ||
			(target.Engine != "" && string(target.Engine) != seat.Engine) {
			return nil, false, fmt.Errorf(
				"kill pane %s ownership changed (seat %s, session %q, engine %q)",
				target.PaneID,
				seat.SeatID,
				seat.SessionID,
				seat.Engine,
			)
		}
		engine := pfmengine.ID(seat.Engine)
		if _, lookupErr := pfmengine.Lookup(engine); lookupErr != nil {
			return nil, false, fmt.Errorf("kill seat %s has unknown engine %q", seat.SeatID, seat.Engine)
		}
		return herdrKiller{
			TmuxClient: fallback,
			engine:     engine,
			database:   database,
			seat:       seat,
			sidDir:     sidDir,
			log:        log,
		}, true, nil
	}
	if filepath.Dir(target.SocketPath) != filepath.Clean(tmuxDir) {
		return nil, false, fmt.Errorf("kill pane %s: no durable owner for socket %q", target.PaneID, target.SocketPath)
	}
	return fallback, false, nil
}

// warnTmuxSeatFallback preserves the optional seat lookup failure visibly,
// without making that lookup a new dependency of an explicit tmux address.
func warnTmuxSeatFallback(tmuxDir string, target Target, log io.Writer, lookupErr error) error {
	if !filepath.IsAbs(target.SocketPath) || filepath.Clean(target.SocketPath) != target.SocketPath ||
		filepath.Dir(target.SocketPath) != filepath.Clean(tmuxDir) ||
		len(target.PaneID) < 2 || target.PaneID[0] != '%' {
		return lookupErr
	}
	if _, err := strconv.ParseUint(target.PaneID[1:], 10, 64); err != nil {
		return lookupErr
	}
	if log == nil {
		return errors.Join(lookupErr, errors.New("kill warning writer is unavailable"))
	}
	if _, err := fmt.Fprintf(
		log,
		"pfm kill: %v; continuing with explicit tmux target %s %s\n",
		lookupErr,
		target.SocketPath,
		target.PaneID,
	); err != nil {
		return errors.Join(lookupErr, fmt.Errorf("write kill seat warning: %w", err))
	}
	return nil
}

// herdrKiller adapts the existing ratchet, not a second exit choreography.
// Tmux-only teammate cleanup remains on the finisher's original client.
type herdrKiller struct {
	TmuxClient
	engine   pfmengine.ID
	database *store.Store
	seat     fleetdb.Seat
	// sidDir holds the seat's reload schedule record, retired with its row.
	sidDir string
	// log receives the warnings of a close that succeeded around a failure.
	log io.Writer
	// heldShellWait bounds the wait for a held pane's shell to end after its
	// close; zero is defaultHeldShellWait.
	heldShellWait time.Duration
}

// prove re-reads by stable seat identity; stale wire addresses never confer ownership.
func (client herdrKiller) prove(ctx context.Context) (fleetdb.Seat, bool, error) {
	owner, found, err := client.database.Shared().Seat(ctx, client.seat.SeatID)
	if err != nil {
		return owner, false, fmt.Errorf("read native kill owner: %w", err)
	}
	if !found {
		values, err := paths.Resolve()
		if err != nil {
			return owner, false, fmt.Errorf("native kill proof paths: %w", err)
		}
		dead, err := gather.HerdrSeatRetired(ctx, gather.NewProcFS(values.ProcRoot), client.seat)
		if err != nil {
			return owner, false, fmt.Errorf("prove retired native kill host: %w", err)
		}
		if dead {
			return client.seat, false, nil
		}
		return owner, false, fmt.Errorf("native kill owner disappeared with live supervisor")
	}
	if owner.Backend != herdrBackend || owner.HerdrSocket != client.seat.HerdrSocket ||
		owner.SessionID != client.seat.SessionID ||
		owner.Engine != client.seat.Engine ||
		client.seat.HerdrTerminalID != "" && owner.HerdrTerminalID != client.seat.HerdrTerminalID ||
		client.seat.SeatHostPID > 0 &&
			(owner.SeatHostPID != client.seat.SeatHostPID || owner.SeatHostStart != client.seat.SeatHostStart || client.seat.SeatHostBirth > 0 && owner.SeatHostBirth != client.seat.SeatHostBirth) {
		return owner, false, fmt.Errorf("native kill durable ownership changed")
	}
	values, err := paths.Resolve()
	if err != nil {
		return owner, false, fmt.Errorf("native kill proof paths: %w", err)
	}
	return gather.ResolveHerdrSeat(ctx, values, owner)
}

func (client herdrKiller) PaneExists(ctx context.Context, _, _ string) (bool, error) {
	owner, owned, err := client.prove(ctx)
	if err != nil {
		return false, fmt.Errorf("prove native kill target: %w", err)
	}
	if !owned {
		// An acknowledged close retires the durable row before confirmation.
		// A dead supervisor with a retained row is not evidence of pane death.
		_, retained, readErr := client.database.Shared().Seat(ctx, client.seat.SeatID)
		if readErr != nil {
			return false, fmt.Errorf("read native kill retirement: %w", readErr)
		}
		if !retained {
			return false, nil
		}
		if owner.SeatHostPID == 0 {
			return false, fmt.Errorf("native seat %s is unbound with pane absent; seat retained", owner.SeatID)
		}
		return false, fmt.Errorf(
			"native seat %s supervisor is dead; pane disappearance unproved, seat retained",
			client.seat.SeatID,
		)
	}
	socket, pane := owner.HerdrSocket, owner.HerdrPane
	result, err := herdr.SeatRequest(ctx, socket, "pane.get", map[string]string{herdrPaneKey: pane})
	if err != nil {
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
			return false, fmt.Errorf("native kill pane disappeared while its supervisor remains live: %w", err)
		}
		return false, fmt.Errorf("get Herdr pane %s: %w", pane, err)
	}
	var response struct {
		Type string `json:"type"`
		Pane *struct {
			PaneID    string `json:"pane_id"`
			Workspace string `json:"workspace_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("decode Herdr pane %s: %w", pane, err)
	}
	if response.Type != "pane_info" || response.Pane == nil {
		return false, fmt.Errorf("get Herdr pane %s: malformed pane result", pane)
	}
	got := response.Pane.PaneID
	if !herdrPaneMatches(pane, got, response.Pane.Workspace) {
		return false, fmt.Errorf("get Herdr pane %s: result belongs to %q", pane, got)
	}
	return client.enginePresent(ctx, socket, pane)
}

// enginePresent reads the pane's foreground through herdr-shield: a pane it holds
// shows its holder's shell, so the engine is found where it runs, never in the
// attach client alone.
func (client herdrKiller) enginePresent(ctx context.Context, socket, pane string) (bool, error) {
	_, processes, err := paneProcesses(ctx, socket, pane)
	if err != nil {
		return false, err
	}
	present, err := herdr.EngineForeground(processes, pane, client.engine)
	if err != nil || present {
		return present, err
	}
	// Engine absence does not prove the pane or its live supervisor retired.
	return false, fmt.Errorf(
		"herdr foreground evidence unavailable in %s; live supervisor engine exit is unproved", pane,
	)
}

// paneProcesses reads pane's processes through herdr-shield, returning the shield
// door that read them.
func paneProcesses(ctx context.Context, socket, pane string) (*herdr.Shield, herdr.PaneProcesses, error) {
	values, err := paths.Resolve()
	if err != nil {
		return nil, herdr.PaneProcesses{}, fmt.Errorf("read Herdr processes in %s: resolve paths: %w", pane, err)
	}
	shield := gather.HerdrShield(gather.NewProcFS(values.ProcRoot))
	processes, err := shield.Read(ctx, socket, pane)
	if err != nil {
		return nil, herdr.PaneProcesses{}, fmt.Errorf("read Herdr processes in %s: %w", pane, err)
	}
	return shield, processes, nil
}

// awaitHeldShell waits, bounded, for herdr-shield to end holder's shell once its
// pane closed: Herdr's close ends only the attach client, so a shell that
// outlives it keeps the seat and names herdr-shield's own cleanup.
func (client herdrKiller) awaitHeldShell(
	ctx context.Context, shield *herdr.Shield, holder herdr.ShieldHolder, pane string,
) error {
	wait := client.heldShellWait
	if wait <= 0 {
		wait = defaultHeldShellWait
	}
	deadline := clock.Real.Now().Add(wait)
	for {
		ended, err := shield.ShellEnded(holder)
		if err != nil {
			return fmt.Errorf("closed Herdr pane %s; seat %s retained: %w", pane, client.seat.SeatID, err)
		}
		if ended {
			return nil
		}
		if !clock.Real.Now().Before(deadline) {
			return fmt.Errorf(
				"closed Herdr pane %s, but herdr-shield holder %s still runs its shell (pid %d): "+
					"end it with `%s kill %s`; seat %s retained",
				pane, holder.ID, holder.Child.PID, herdr.ShieldBinary, holder.ID, client.seat.SeatID,
			)
		}
		if err := waitContext(ctx, heldShellPoll); err != nil {
			return fmt.Errorf("closed Herdr pane %s; seat %s retained: await its herdr-shield shell: %w",
				pane, client.seat.SeatID, err)
		}
	}
}

func herdrPaneMatches(target, pane, workspace string) bool {
	return pane != "" && (pane == target || (workspace != "" && workspace+":"+pane == target))
}

// screenTarget is the seat's address for the screen door (call.Screen),
// re-proved by stable seat identity first: a dead supervisor or a changed
// owner is an error, never a pane to read or type into. Input (keys, text)
// also needs the engine itself in the pane's foreground, so a retry prompt
// or a shell left behind never receives an exit command.
func (client herdrKiller) screenTarget(ctx context.Context, input bool) (pfmcall.Target, error) {
	owner, owned, err := client.prove(ctx)
	if err != nil {
		return pfmcall.Target{}, fmt.Errorf("prove native screen target: %w", err)
	}
	if !owned {
		return pfmcall.Target{}, errors.New("native screen target: saved supervisor is dead")
	}
	if input {
		if _, err := client.enginePresent(ctx, owner.HerdrSocket, owner.HerdrPane); err != nil {
			return pfmcall.Target{}, fmt.Errorf("verify foreground before input: %w", err)
		}
	}
	return pfmcall.Target{
		Backend: herdrBackend, SeatID: owner.SeatID, SocketPath: owner.HerdrSocket, Pane: owner.HerdrPane,
		HerdrWorkspace: owner.HerdrWorkspace, Engine: owner.Engine, ID: owner.SessionID,
	}, nil
}

func (client herdrKiller) ack(ctx context.Context, _, _, method string, params any) error {
	owner, owned, err := client.prove(ctx)
	if err != nil {
		return fmt.Errorf("verify owner before Herdr %s: %w", method, err)
	}
	if !owned {
		return fmt.Errorf("herdr %s: saved supervisor is dead", method)
	}
	socket, pane := owner.HerdrSocket, owner.HerdrPane
	switch values := params.(type) {
	case map[string]string:
		values[herdrPaneKey] = pane
	case map[string]any:
		values[herdrPaneKey] = pane
	default:
		return fmt.Errorf("herdr %s: unsupported mutation params", method)
	}
	result, err := herdr.SeatRequest(ctx, socket, method, params)
	if err != nil {
		return fmt.Errorf("herdr %s pane %s: %w", method, pane, err)
	}
	var response struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return fmt.Errorf("decode Herdr %s pane %s: %w", method, pane, err)
	}
	if response.Type != "ok" {
		return fmt.Errorf("herdr %s pane %s: unexpected result type %q", method, pane, response.Type)
	}
	return nil
}

func (client herdrKiller) KillPane(ctx context.Context, socket, pane string) error {
	// A retry prompt has a live supervisor but no foreground engine.
	owner, owned, err := client.prove(ctx)
	if err != nil {
		return fmt.Errorf("refuse Herdr close without supervisor proof: %w", err)
	}
	if !owned {
		if owner.SeatHostPID == 0 {
			return fmt.Errorf(
				"native seat %s is unbound with pane absent; pane not closed, seat retained",
				owner.SeatID,
			)
		}
		return fmt.Errorf("native seat %s supervisor is dead; pane not closed, seat retained", client.seat.SeatID)
	}

	// The proved owner's address, never the caller's stale one, as ack uses.
	shield, processes, err := paneProcesses(ctx, owner.HerdrSocket, owner.HerdrPane)
	if err != nil {
		return fmt.Errorf("refuse Herdr close: %w", err)
	}
	if err := client.ack(ctx, socket, pane, "pane.close", map[string]string{herdrPaneKey: pane}); err != nil {
		return err
	}
	if processes.Holder != nil {
		if err := client.awaitHeldShell(ctx, shield, *processes.Holder, owner.HerdrPane); err != nil {
			return err
		}
	}
	// A forced pane close also kills its supervisor, so it cannot retire the
	// durable address itself. Keep the independent kill ledger untouched.
	// The pane is gone now: a reload record that will not go is named, never a
	// reason to keep a closed pane's row live, which no retry could retire.
	if err := herdr.RemoveReloadRecord(client.sidDir, client.seat.SeatID); err != nil {
		warning := fmt.Errorf("closed Herdr seat %s: its reload record stays behind: %w", client.seat.SeatID, err)
		obs.Logger(ctx).WarnContext(ctx, "kill: reload record of a closed native seat not removed",
			"seat", client.seat.SeatID, "err", err)
		if client.log == nil {
			return errors.Join(warning, errors.New("kill warning writer is unavailable"))
		}
		if _, writeErr := fmt.Fprintf(client.log, "pfm chat kill: warning: %v\n", warning); writeErr != nil {
			return errors.Join(warning, fmt.Errorf("write kill warning: %w", writeErr))
		}
	}
	if err := client.database.Shared().DeleteSeatRun(client.seat.SeatID); err != nil {
		return fmt.Errorf("remove closed Herdr seat recipe %s: %w", client.seat.SeatID, err)
	}
	if err := client.database.Shared().DeleteSeat(ctx, client.seat.SeatID); err != nil {
		return fmt.Errorf("remove closed Herdr seat %s: %w", client.seat.SeatID, err)
	}
	return nil
}
