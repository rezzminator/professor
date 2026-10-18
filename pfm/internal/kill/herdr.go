package kill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const herdrPaneKey = "pane_id"

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
	if !found || seat.Backend != "herdr" || seat.State != "live" {
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

func (client herdrKiller) PaneExists(ctx context.Context, socket, pane string) (bool, error) {
	result, err := herdr.SeatRequest(ctx, socket, "pane.get", map[string]string{herdrPaneKey: pane})
	if err != nil {
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
			return false, nil
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
			Processes *[]struct {
				PID   int    `json:"pid"`
				Name  string `json:"name"`
				Argv0 string `json:"argv0"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return false, fmt.Errorf("decode Herdr processes in %s: %w", pane, err)
	}
	if response.Type != "pane_process_info" || response.Info == nil || response.Info.Processes == nil ||
		!herdrPaneMatches(pane, response.Info.Pane, response.Info.Workspace) {
		return false, fmt.Errorf("read Herdr processes in %s: malformed process_info result", pane)
	}
	for _, process := range *response.Info.Processes {
		if process.PID <= 0 || (process.Name == "" && process.Argv0 == "") {
			return false, fmt.Errorf("read Herdr processes in %s: process has no valid pid or identity", pane)
		}
		for _, name := range []string{process.Name, process.Argv0} {
			if pfmengine.MatchCommand(client.engine, []string{name}, client.engine == pfmengine.Claude) {
				return true, nil
			}
		}
	}
	return false, nil
}

func herdrPaneMatches(target, pane, workspace string) bool {
	return pane != "" && (pane == target || (workspace != "" && workspace+":"+pane == target))
}

func (client herdrKiller) Capture(ctx context.Context, socket, pane string) (string, error) {
	result, err := herdr.SeatRequest(
		ctx,
		socket,
		"pane.read",
		map[string]any{herdrPaneKey: pane, "source": "recent", "lines": 1000},
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
		} `json:"read"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("decode Herdr pane %s: %w", pane, err)
	}
	if response.Type != "pane_read" || response.Read == nil || response.Read.Text == nil ||
		response.Read.Truncated == nil ||
		!herdrPaneMatches(pane, response.Read.Pane, response.Read.Workspace) {
		return "", fmt.Errorf("read Herdr pane %s: malformed pane_read result", pane)
	}
	return *response.Read.Text, nil
}

func (client herdrKiller) CaptureStyled(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("herdr snapshots do not expose composer styling")
}

func (client herdrKiller) ack(ctx context.Context, socket, pane, method string, params any) error {
	owner, found, err := client.database.Shared().SeatByPane(ctx, socket, pane)
	if err != nil {
		return fmt.Errorf("verify owner before Herdr %s: %w", method, err)
	}
	if !found || owner.SeatID != client.seat.SeatID || owner.SessionID != client.seat.SessionID ||
		owner.Engine != client.seat.Engine {
		return fmt.Errorf("herdr %s pane %s: durable ownership changed", method, pane)
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
