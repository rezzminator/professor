package kill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	herdrPaneKey = "pane_id"
	herdrBackend = "herdr"
)

// herdrTarget resolves ownership before the index fallback: a new native engine
// can be live before it has flushed any transcript. Environment is not routing.
func (manager *Manager) herdrTarget(ctx context.Context, request Request) (Target, bool, error) {
	if (request.SocketName == "") != (request.PaneID == "") {
		return Target{}, false, fmt.Errorf("resolved live address requires socket and pane")
	}
	var seat fleetdb.Seat
	var found bool
	var err error
	switch {
	case request.SocketName != "" && request.PaneID != "":
		seat, found, err = manager.database.Shared().SeatByPane(ctx, request.SocketName, request.PaneID)
	case request.ID != "" && !request.Self:
		seat, found, err = manager.database.Shared().SeatBySession(ctx, request.ID)
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
	if !found && request.ID != "" && request.SocketName != "" {
		candidate, ok, lookupErr := manager.database.Shared().SeatBySession(ctx, request.ID)
		if lookupErr != nil {
			return Target{}, false, fmt.Errorf("resolve moved native kill owner: %w", lookupErr)
		}
		if ok && candidate.Backend == herdrBackend && candidate.HerdrSocket == request.SocketName {
			values, err := paths.Resolve()
			if err != nil {
				return Target{}, false, fmt.Errorf("resolve moved native kill paths: %w", err)
			}
			proved, owned, err := gather.ResolveHerdrSeat(ctx, values, candidate)
			if err != nil {
				return Target{}, false, fmt.Errorf("prove moved native kill owner: %w", err)
			}
			if !owned {
				return Target{}, false, fmt.Errorf("native kill explicit pane has no live proved owner")
			}
			if proved.HerdrPane != request.PaneID {
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
	if !found || seat.Backend != herdrBackend || seat.State != "live" {
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
	proved, _, err := (herdrKiller{database: manager.database, seat: seat}).prove(ctx)
	if err != nil {
		return Target{}, false, fmt.Errorf("resolve native kill ownership: %w", err)
	}
	seat = proved
	id := seat.SessionID
	if id == "" {
		id = seat.SeatID
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
	tmuxDir string,
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
			return nil, false, fmt.Errorf("resolve moved kill seat: %w", lookupErr)
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
		return herdrKiller{TmuxClient: fallback, engine: engine, database: database, seat: seat}, true, nil
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
		dead, err := gather.HerdrHostDead(gather.NewProcFS(values.ProcRoot), client.seat)
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
	result, err = herdr.SeatRequest(ctx, socket, "pane.process_info", map[string]string{herdrPaneKey: pane})
	if err != nil {
		return false, fmt.Errorf("read Herdr processes in %s: %w", pane, err)
	}
	return client.enginePresent(result, pane)
}

func (client herdrKiller) enginePresent(result json.RawMessage, pane string) (bool, error) {
	var response struct {
		Type string `json:"type"`
		Info *struct {
			Pane      string `json:"pane_id"`
			Workspace string `json:"workspace_id"`
			Processes []struct {
				PID   int    `json:"pid"`
				Name  string `json:"name"`
				Argv0 string `json:"argv0"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("decode Herdr processes in %s: %w", pane, err)
	}
	if response.Type != "pane_process_info" || response.Info == nil ||
		!herdrPaneMatches(pane, response.Info.Pane, response.Info.Workspace) {
		return false, fmt.Errorf("read Herdr processes in %s: malformed process_info result", pane)
	}
	if len(response.Info.Processes) == 0 {
		return false, fmt.Errorf("herdr foreground evidence unavailable in %s; engine exit is unproved", pane)
	}
	for _, process := range response.Info.Processes {
		if process.PID <= 0 || (process.Name == "" && process.Argv0 == "") {
			return false, fmt.Errorf("read Herdr processes in %s: process has no valid pid or identity", pane)
		}
		for _, name := range []string{process.Name, process.Argv0} {
			if pfmengine.MatchCommand(client.engine, []string{name}, client.engine == pfmengine.Claude) {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf(
		"herdr foreground evidence unavailable in %s; live supervisor engine exit is unproved",
		pane,
	)
}

func herdrPaneMatches(target, pane, workspace string) bool {
	return pane != "" && (pane == target || (workspace != "" && workspace+":"+pane == target))
}

func (client herdrKiller) Capture(ctx context.Context, socket, pane string) (string, error) {
	return client.captureFormat(ctx, socket, pane, "text")
}

func (client herdrKiller) captureFormat(ctx context.Context, _, _, format string) (string, error) {
	owner, owned, err := client.prove(ctx)
	if err != nil {
		return "", fmt.Errorf("prove native capture target: %w", err)
	}
	if !owned {
		return "", fmt.Errorf("native capture supervisor is dead")
	}
	socket, pane := owner.HerdrSocket, owner.HerdrPane
	result, err := herdr.SeatRequest(
		ctx,
		socket,
		"pane.read",
		map[string]any{herdrPaneKey: pane, "source": "visible", "lines": 1000, "format": format, "strip_ansi": false},
	)
	if err != nil {
		return "", fmt.Errorf("read Herdr pane %s: %w", pane, err)
	}
	var response struct {
		Type string `json:"type"`
		Read *struct {
			Pane      string  `json:"pane_id"`
			Workspace string  `json:"workspace_id"`
			Text      *string `json:"text"`
			Truncated *bool   `json:"truncated"`
			Format    string  `json:"format"`
		} `json:"read"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("decode Herdr pane %s: %w", pane, err)
	}
	if response.Type != "pane_read" || response.Read == nil || response.Read.Text == nil ||
		response.Read.Truncated == nil ||
		!herdrPaneMatches(
			pane,
			response.Read.Pane,
			response.Read.Workspace,
		) || (format == "ansi" && response.Read.Format != "ansi") {
		return "", fmt.Errorf("read Herdr pane %s: malformed pane_read result", pane)
	}
	return *response.Read.Text, nil
}

func (client herdrKiller) CaptureStyled(ctx context.Context, socket, pane string) (string, error) {
	frame, err := client.captureFormat(ctx, socket, pane, "ansi")
	if err != nil {
		return "", err
	}
	if !pfmcall.ComposerRowShown(frame) || pfmcall.SelectorLine(frame) != "" ||
		pfmengine.ClaudeTrustDialog(frame) || pfmcall.IsFooterBusy(pfmengine.ID(client.seat.Engine), frame) ||
		!pfmcall.ComposerBoxEmpty(frame) {
		return "", fmt.Errorf("herdr pane %s: composer changed while checking styling", pane)
	}
	return frame, nil
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
	if method != "pane.close" {
		evidence, err := herdr.SeatRequest(ctx, socket, "pane.process_info", map[string]string{herdrPaneKey: pane})
		if err != nil {
			return fmt.Errorf("verify foreground before Herdr %s: %w", method, err)
		}
		if _, err := client.enginePresent(evidence, pane); err != nil {
			return fmt.Errorf("verify foreground before Herdr %s: %w", method, err)
		}
	}

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

func (client herdrKiller) SendLine(ctx context.Context, socket, pane, line string) error {
	if err := client.ack(
		ctx,
		socket,
		pane,
		"pane.send_text",
		map[string]string{herdrPaneKey: pane, "text": line},
	); err != nil {
		return err
	}
	return client.SendKey(ctx, socket, pane, "Enter")
}

func (client herdrKiller) SendKey(ctx context.Context, socket, pane, key string) error {
	return client.ack(ctx, socket, pane, "pane.send_keys", map[string]any{herdrPaneKey: pane, "keys": []string{key}})
}

func (client herdrKiller) KillPane(ctx context.Context, socket, pane string) error {
	// A retry prompt has a live supervisor but no foreground engine.
	_, owned, err := client.prove(ctx)
	if err != nil {
		return fmt.Errorf("refuse Herdr close without supervisor proof: %w", err)
	}
	if !owned {
		return fmt.Errorf("native seat %s supervisor is dead; pane not closed, seat retained", client.seat.SeatID)
	}

	if err := client.ack(ctx, socket, pane, "pane.close", map[string]string{herdrPaneKey: pane}); err != nil {
		return err
	}
	// A forced pane close also kills its supervisor, so it cannot retire the
	// durable address itself. Keep the independent kill ledger untouched.
	if err := client.database.Shared().DeleteSeatRun(client.seat.SeatID); err != nil {
		return fmt.Errorf("remove closed Herdr seat recipe %s: %w", client.seat.SeatID, err)
	}
	if err := client.database.Shared().DeleteSeat(ctx, client.seat.SeatID); err != nil {
		return fmt.Errorf("remove closed Herdr seat %s: %w", client.seat.SeatID, err)
	}
	return nil
}
