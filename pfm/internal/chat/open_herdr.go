package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	nativePaneIDKey = "pane_id"
	// nativePaneInfo is the type of Herdr's answer naming one pane.
	nativePaneInfo  = "pane_info"
	nativeTargetKey = "target"
)

func nativeSeat(
	ctx context.Context,
	runtime config.Runtime,
	id, seatID string,
) (seat fleetdb.Seat, found bool, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	if seatID != "" {
		seat, found, returnErr = db.Seat(ctx, seatID)
	} else {
		seat, found, returnErr = db.SeatBySession(ctx, id)
		if returnErr == nil && !found && id != "" {
			seat, found, returnErr = db.Seat(ctx, id)
		}
	}
	if returnErr == nil && found &&
		(seat.Backend != spawn.BackendHerdr || (seat.SessionID != id && seat.SeatID != id)) {
		found = false
	}
	return
}

// nativeAlive proves the supervisor, including during startup and retry, and
// returns its persisted canonical address. Unknown is never permission to replace.
func nativeAlive(ctx context.Context, seat fleetdb.Seat, values paths.Values) (fleetdb.Seat, bool, error) {
	return gather.ResolveHerdrSeat(ctx, values, seat)
}

// lockNativeSeat rereads by conversation after waiting: a prior opener may have
// replaced the seat while this caller still held its obsolete SeatID.
func lockNativeSeat(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
) (fleetdb.Seat, func() error, error) {
	unlock, err := fleetdb.LockSeat(ctx, runtime.Paths, fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID))
	if err != nil {
		return seat, nil, fmt.Errorf("lock native conversation %s: %w", seat.SessionID, err)
	}
	current, found, err := nativeSeat(ctx, runtime, fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID), "")
	if err == nil && !found {
		err = fmt.Errorf("native conversation %s ownership changed", seat.SessionID)
	}
	if err == nil && current.EndPending {
		err = fmt.Errorf("native conversation %s end is pending", seat.SessionID)
	}
	if err != nil {
		return seat, nil, errors.Join(err, unlock())
	}
	return current, unlock, nil
}

func viewNative(ctx context.Context, seat fleetdb.Seat, stdout, stderr io.Writer) error {
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: CodexSeats(nil)})
	if err != nil {
		return fmt.Errorf("identify Herdr viewer: %w", err)
	}
	caller, callerErr := identifier.Identify(ctx)
	if callerErr != nil && !errors.Is(callerErr, resolve.ErrNotInChat) &&
		!errors.Is(callerErr, resolve.ErrNotRegistered) {
		return fmt.Errorf("identify Herdr viewer: %w", callerErr)
	}
	if caller.Backend == spawn.BackendHerdr && caller.SocketPath == seat.HerdrSocket {
		body, err := herdr.SeatRequest(
			ctx,
			seat.HerdrSocket,
			"agent.focus",
			map[string]string{nativeTargetKey: seat.HerdrPane},
		)
		if err != nil {
			var refusal *herdr.Refusal
			if errors.As(err, &refusal) && refusal.Code == "agent_not_found" {
				body, err = herdr.SeatRequest(
					ctx,
					seat.HerdrSocket,
					"pane.focus",
					map[string]string{nativePaneIDKey: seat.HerdrPane},
				)
			}
		}
		if err != nil {
			return fmt.Errorf("focus Herdr pane %s: %w", seat.HerdrPane, err)
		}
		var ack struct {
			Type string `json:"type"`
			Pane string `json:"pane_id"`
			Info *struct {
				Pane    string `json:"pane_id"`
				Focused bool   `json:"focused"`
			} `json:"pane"`
			Agent *struct {
				Pane    string `json:"pane_id"`
				Focused bool   `json:"focused"`
			} `json:"agent"`
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			return fmt.Errorf("decode Herdr focus: %w", err)
		}
		valid := ack.Type == "pane_focused" && ack.Pane == seat.HerdrPane
		if ack.Type == nativePaneInfo {
			valid = ack.Info != nil && ack.Info.Pane == seat.HerdrPane && ack.Info.Focused
		}
		if ack.Type == "agent_info" {
			valid = ack.Agent != nil && ack.Agent.Pane == seat.HerdrPane && ack.Agent.Focused
		}
		if !valid {
			return fmt.Errorf("unexpected Herdr focus acknowledgement: %s", body)
		}
		return nil
	}
	process, err := (deps.RealRunner{}).Start(
		ctx,
		[]string{"herdr", "agent", "attach", seat.HerdrPane},
		deps.StartOptions{
			Env:    append(herdr.StripPane(os.Environ()), herdr.EnvSocket+"="+seat.HerdrSocket),
			Stdin:  os.Stdin,
			Stdout: stdout,
			Stderr: stderr,
		},
	)
	if err == nil {
		err = process.Wait()
	}
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("attach Herdr pane %s: %w", seat.HerdrPane, ctx.Err())
		}
		fmt.Fprintf(stderr, "pfm chat open: Herdr attach failed: %v\n%s\n", err, nativeLocation(seat))
		return fmt.Errorf("attach Herdr pane %s: %w", seat.HerdrPane, err)
	}
	return nil
}

// nativePane is the identity pane.get answers for one Herdr pane.
type nativePane struct {
	ID        string `json:"pane_id"`
	Workspace string `json:"workspace_id"`
	Tab       string `json:"tab_id"`
}

func readNativePane(ctx context.Context, socket, pane string) (nativePane, error) {
	body, err := herdr.SeatRequest(ctx, socket, "pane.get", map[string]string{nativePaneIDKey: pane})
	if err != nil {
		return nativePane{}, fmt.Errorf("read Herdr pane %s: %w", pane, err)
	}
	var reply struct {
		Type string     `json:"type"`
		Pane nativePane `json:"pane"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nativePane{}, fmt.Errorf("decode Herdr pane %s: %w", pane, err)
	}
	if reply.Type != nativePaneInfo || reply.Pane.ID != pane || reply.Pane.Workspace == "" || reply.Pane.Tab == "" {
		return nativePane{}, fmt.Errorf("herdr pane.get for %s returned unexpected identity: %s", pane, body)
	}
	return reply.Pane, nil
}

// moveNativeHere brings a live chat beside the caller or swaps it into place.
// Identify must prove a fronted Herdr pane on this server with no seat.
// Cross-workspace moves are refused because they change the running chat's pane id.
// closeHere closes the proven caller pane after the move succeeds.
func moveNativeHere(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
) (here bool, closeHere func() error, returnErr error) {
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: CodexSeats(&runtime)})
	if err != nil {
		return false, nil, fmt.Errorf("identify native move caller: %w", err)
	}
	identity, callerErr := identifier.Identify(ctx)
	if callerErr != nil && !errors.Is(callerErr, resolve.ErrNotInChat) &&
		!errors.Is(callerErr, resolve.ErrNotRegistered) {
		return false, nil, fmt.Errorf("identify native move caller: %w", callerErr)
	}
	if identity.Backend != spawn.BackendHerdr || identity.SocketPath != seat.HerdrSocket {
		return false, nil, nil
	}
	caller := identity.Pane
	if caller == seat.HerdrPane {
		return true, nil, nil
	}
	if !errors.Is(callerErr, resolve.ErrNotRegistered) {
		return false, nil, nil
	}
	socket := seat.HerdrSocket
	callerPane, err := readNativePane(ctx, socket, caller)
	if err != nil {
		return false, nil, err
	}
	chatPane, err := readNativePane(ctx, socket, seat.HerdrPane)
	if err != nil {
		return false, nil, err
	}
	if callerPane.Workspace != chatPane.Workspace {
		return false, nil, fmt.Errorf(
			"chat %s lives in Herdr workspace %s and this pane in workspace %s: a pane moved across workspaces "+
				"takes a new id its chat does not know, so the chat stays where it is",
			seat.Name, chatPane.Workspace, callerPane.Workspace,
		)
	}
	if !identity.Fronted {
		return false, nil, fmt.Errorf(
			"bring chat %s into this pane: this process does not front Herdr pane %s",
			seat.Name,
			caller,
		)
	}
	method, want := methodPaneMove, "pane_move"
	var params any = map[string]any{
		nativePaneIDKey: seat.HerdrPane,
		"destination": map[string]any{
			"type": "tab", "tab_id": callerPane.Tab, "target_pane_id": caller, "split": "right",
		},
		"focus": true,
	}
	if callerPane.Tab == chatPane.Tab {
		method, want = methodPaneSwap, "pane_swap"
		params = map[string]string{"source_pane_id": seat.HerdrPane, "target_pane_id": caller}
	}
	body, err := herdr.SeatRequest(ctx, socket, method, params)
	if err != nil {
		return false, nil, fmt.Errorf("bring chat %s into this pane: %w", seat.Name, err)
	}
	if err := checkMoveReply(method, want, body, seat.HerdrPane, caller, callerPane.Tab); err != nil {
		if errors.Is(err, errMoveApplied) {
			return false, nil, fmt.Errorf("herdr %s moved chat %s, but %w", method, seat.Name, err)
		}
		return false, nil, fmt.Errorf("herdr %s left chat %s where it is: %w", method, seat.Name, err)
	}
	return true, func() error {
		_, err := herdr.SeatRequest(ctx, socket, "pane.close", map[string]string{nativePaneIDKey: caller})
		if err != nil {
			return fmt.Errorf("chat %s is in this tab, but this Herdr pane %s stayed open: %w", seat.Name, caller, err)
		}
		return nil
	}, nil
}

// moveLiveNativeHere is moveNativeHere under the seat's lock, on the seat as
// it stands once the lock is held: the account offer ran unlocked, and a
// reboot it ran may have replaced the seat; a seat that died meanwhile moves
// nowhere and names it.
func moveLiveNativeHere(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
) (here bool, closeHere func() error, returnErr error) {
	current, unlock, err := lockNativeSeat(ctx, runtime, seat)
	if err != nil {
		return false, nil, err
	}
	defer func() {
		if err := unlock(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("unlock native conversation: %w", err))
		}
	}()
	current, alive, err := nativeAlive(ctx, current, runtime.Paths)
	if err != nil {
		return false, nil, err
	}
	if !alive {
		return false, nil, fmt.Errorf("native seat %s died before it moved into this pane", current.SeatID)
	}
	return moveNativeHere(ctx, runtime, current)
}

func openNativeRow(
	ctx context.Context,
	row compose.Row,
	primary int,
	cache1H bool,
	stdout, stderr io.Writer,
	runtime config.Runtime,
) (handled bool, code int) {
	if row.Backend != spawn.BackendHerdr {
		return false, 0
	}
	seat, found, err := nativeSeat(ctx, runtime, row.ID, row.SeatID)
	if err == nil && !found {
		err = fmt.Errorf("native open target %s no longer matches its durable seat", row.SeatID)
	}
	var unlock func() error
	if err == nil {
		seat, unlock, err = lockNativeSeat(ctx, runtime, seat)
		if err == nil {
			defer func() {
				if unlock != nil {
					if closeErr := unlock(); closeErr != nil {
						fmt.Fprintf(stderr, "pfm chat open: unlock native conversation: %v\n", closeErr)
						code = 1
					}
				}
			}()
		}
	}
	if err == nil {
		var alive bool
		seat, alive, err = nativeAlive(ctx, seat, runtime.Paths)
		if err == nil && !alive {
			// D4: a dead seat resumes by the caller's current birth rule.
			choice, choiceErr := BirthBackend(ctx, "open.herdr", spawn.BackendAuto)
			switch {
			case choiceErr != nil:
				err = choiceErr
			case choice.Backend == spawn.BackendHerdr:
				// The chat resumes in the caller's own pane: the seat is rebound
				// to this pane, whose process then execs the seat host.
				// On a terminal restoreNative execs the seat host and never
				// returns, so the unseen flag clears before it.
				markSchedulesSeen(ctx, runtime.Paths, row.ID, stderr)
				err = restoreNative(ctx, runtime, seat, choice, false, stdout, stderr)
				if err != nil {
					fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
					return true, 1
				}
				return true, 0
			default:
				row, err = nativeResumeRow(ctx, seat, runtime, stderr)
				if err != nil {
					fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
					return true, 1
				}
				row.Backend = ""
				row.Kind = compose.ResumeKindFor(row.Kind)
				row.Socket = ""
				row.PaneID = ""
				row.SeatID = ""
				code = OpenRow(
					ctx,
					row,
					runtime.Config.PrimaryAccountFor(pfmengine.ID(seat.Engine), row.Account),
					row.C1H,
					"",
					stdout,
					stderr,
					&runtime,
				)
				if code == 0 {
					if cleanupErr := retireNative(ctx, runtime.Paths, seat); cleanupErr != nil {
						fmt.Fprintf(stderr, "pfm chat open: %v\n", cleanupErr)
						code = 1
					}
				}
				return true, code
			}
		}
		if err == nil {
			// A terminal attach may last the whole interactive session; do not hold the
			// replacement/restore lock while viewing the already proved live seat.
			err = unlock()
			unlock = nil
			// The account offer comes first, wherever the chat opens: a reboot
			// it runs takes the seat's lock itself and may name a new seat.
			if err == nil {
				seat, err = offerNativeAccount(ctx, seat, primary, cache1H, stderr, runtime)
			}
			// Inside a Herdr pane on the chat's server the chat comes to this
			// pane's place; the lock is held again while it moves.
			var here bool
			var closeHere func() error
			if err == nil {
				here, closeHere, err = moveLiveNativeHere(ctx, runtime, seat)
			}
			if err == nil && here {
				markSchedulesSeen(ctx, runtime.Paths, row.ID, stderr)
				if closeHere != nil {
					// Last: closing this pane ends this process.
					if err := closeHere(); err != nil {
						fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
						return true, 1
					}
				}
				return true, 0
			}
			if err == nil {
				err = viewNative(ctx, seat, stdout, stderr)
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return true, 1
	}
	markSchedulesSeen(ctx, runtime.Paths, row.ID, stderr)
	return true, 0
}

func detachedNative(
	ctx context.Context,
	seat fleetdb.Seat,
	stderr io.Writer,
	runtime config.Runtime,
) (result action.OpenResult, returnErr error) {
	current, unlock, err := lockNativeSeat(ctx, runtime, seat)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	return detachedNativeLocked(ctx, current, stderr, runtime)
}

func detachedNativeLocked(
	ctx context.Context,
	seat fleetdb.Seat,
	stderr io.Writer,
	runtime config.Runtime,
) (result action.OpenResult, returnErr error) {
	seat, alive, err := nativeAlive(ctx, seat, runtime.Paths)
	if err != nil {
		return result, err
	}
	if alive {
		return action.OpenResult{
			Name:   seat.Name,
			Socket: seat.HerdrSocket,
			State:  liveState,
			Detail: strings.TrimSpace(nativeLocation(seat)),
		}, nil
	}
	choice, err := BirthBackend(ctx, "open.herdr", spawn.BackendAuto)
	if err != nil {
		return result, err
	}
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	run, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		return result, err
	}
	run, err = resumeNativeRun(run, seat, runtime.Config, stderr)
	if err != nil {
		return result, err
	}
	if choice.Backend == spawn.BackendHerdr {
		if err := herdr.CheckProtocol(ctx, choice.HerdrSocket); err != nil {
			return result, err
		}
		next := seat
		next.SeatID = ""
		next.HerdrTerminalID = ""
		next.SeatHostPID = 0
		next.SeatHostStart = 0
		next.SeatHostBirth = 0
		next.EndPending = false
		next.State = liveState
		born, err := spawn.SpawnHerdr(
			ctx,
			spawn.HerdrRequest{
				Choice: choice,
				Seat:   next,
				Run:    run,
				Paths:  runtime.Paths,
			},
		)
		if err != nil {
			return result, err
		}
		// The old seat's reload schedule files are keyed by its id, which the
		// new seat does not reuse: they go before its row, or nothing removes them.
		if err := herdr.RemoveReloadFiles(runtime.Paths.SIDDir, seat.SeatID); err != nil {
			return result, fmt.Errorf(
				"new native pane %s exists; remove old seat reload files: %w",
				born.Seat.HerdrPane,
				err,
			)
		}
		if err := db.DeleteSeat(ctx, seat.SeatID); err != nil {
			return result, fmt.Errorf("new native pane %s exists; remove old seat: %w", born.Seat.HerdrPane, err)
		}
		if err := db.DeleteSeatRun(seat.SeatID); err != nil {
			return result, fmt.Errorf("new native pane %s exists; remove old recipe: %w", born.Seat.HerdrPane, err)
		}
		return action.OpenResult{
			Name:   born.Seat.Name,
			Socket: born.Seat.HerdrSocket,
			State:  "opened",
			Detail: nativeLocation(born.Seat),
		}, nil
	}
	row, err := nativeResumeRow(ctx, seat, runtime, stderr)
	if err != nil {
		return result, err
	}
	row.Backend = ""
	row.Kind = compose.ResumeKindFor(row.Kind)
	row.Socket = ""
	row.PaneID = ""
	row.SeatID = ""
	result, err = openDetachedResolvedRow(ctx, row, stderr, runtime)
	if err != nil {
		return result, err
	}
	if err := retireNative(ctx, runtime.Paths, seat); err != nil {
		return result, err
	}
	return result, nil
}

func nativeResumeRow(
	ctx context.Context,
	seat fleetdb.Seat,
	runtime config.Runtime,
	stderr io.Writer,
) (row compose.Row, returnErr error) {
	row = nativeRow(seat)
	rows, err := Rows(ctx, stderr, &runtime)
	if err != nil {
		return row, err
	}
	for index := range rows {
		candidate := &rows[index]
		if candidate.ID == seat.SessionID {
			row = *candidate
			break
		}
	}
	if row.CWD == "" {
		db := fleetdb.OpenSharedState(ctx, runtime.Paths)
		defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
		run, err := db.ReadSeatRun(seat.SeatID)
		if err != nil {
			return row, err
		}
		row.CWD = run.CWD
	}
	if row.Account == 0 {
		row.Account = runtime.Config.PrimaryAccountFor(pfmengine.ID(seat.Engine), runtime.Config.ImplicitAccount())
	}
	return row, nil
}

func nativeRow(seat fleetdb.Seat) compose.Row {
	kind := compose.LiveClaude
	switch pfmengine.ID(seat.Engine) {
	case pfmengine.Codex:
		kind = compose.LiveCodex
	case pfmengine.OpenCode:
		kind = compose.LiveOpenCode
	}
	return compose.Row{
		Backend:        spawn.BackendHerdr,
		SeatID:         seat.SeatID,
		ID:             seat.SessionID,
		Name:           seat.Name,
		Kind:           kind,
		Socket:         seat.HerdrSocket,
		PaneID:         seat.HerdrPane,
		HerdrWorkspace: seat.HerdrWorkspace,
	}
}

// retireNative removes only the obsolete reservation after replacement
// succeeded: its reload schedule files first, then its row and recipe.
func retireNative(ctx context.Context, values paths.Values, seat fleetdb.Seat) (returnErr error) {
	if err := herdr.RemoveReloadFiles(values.SIDDir, seat.SeatID); err != nil {
		return fmt.Errorf("retire replaced native seat %s reload files: %w", seat.SeatID, err)
	}
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	if err := db.DeleteSeat(ctx, seat.SeatID); err != nil {
		return fmt.Errorf("retire replaced native seat %s: %w", seat.SeatID, err)
	}
	if err := db.DeleteSeatRun(seat.SeatID); err != nil {
		return fmt.Errorf("retire replaced native recipe %s: %w", seat.SeatID, err)
	}
	return nil
}

// herdrViewRetirer is retireHerdrViews' reach: the chat session's Herdr
// record, the bridge proof and Herdr's pane.close.
type herdrViewRetirer struct {
	// session is the chat's tmux session: a client the proof finds attached
	// to another session of the same server is not this chat's view.
	session string
	stored  func(ctx context.Context) (string, error)
	prove   func(ctx context.Context, socket, pane string) (call.HerdrBridge, error)
	close   func(ctx context.Context, socket, pane string) error
}

// retireOtherHerdrViews runs before a live tmux chat's attach in a Herdr
// pane: the chat ends in this pane alone, its other Herdr views on this
// server retired by retireHerdrViews. Only a caller whose attach runs in its
// own proved Herdr pane retires: a caller inside tmux or a caller pane hosting
// a chat of its own (an agent's pfm call) retires nothing, and an unproved
// pane is named.
func retireOtherHerdrViews(ctx context.Context, row compose.Row, runtime config.Runtime, stderr io.Writer) {
	if row.Socket == "" || row.PaneID == "" || row.Backend == spawn.BackendHerdr {
		return
	}
	identifier, err := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: CodexSeats(&runtime)})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: other Herdr views of %s stay: identify caller: %v\n", row.Name, err)
		return
	}
	caller, callerErr := identifier.Identify(ctx)
	if callerErr != nil && !errors.Is(callerErr, resolve.ErrNotInChat) &&
		!errors.Is(callerErr, resolve.ErrNotRegistered) {
		fmt.Fprintf(stderr, "pfm chat open: other Herdr views of %s stay: %v\n", row.Name, callerErr)
		return
	}
	if caller.Backend != spawn.BackendHerdr || !errors.Is(callerErr, resolve.ErrNotRegistered) {
		return
	}
	socket, pane := caller.SocketPath, caller.Pane
	if !caller.Fronted {
		fmt.Fprintf(
			stderr,
			"pfm chat open: other Herdr views of %s stay: this process does not front pane %s\n",
			row.Name,
			pane,
		)
		return
	}
	tmuxSocket, err := runtime.Paths.SocketUnder(row.Socket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: other Herdr views of %s stay: resolve tmux socket %q: %v\n",
			row.Name, row.Socket, err)
		return
	}
	retireHerdrViews(ctx, tmuxSocket, socket, pane, herdrViewRetirer{
		session: row.SessionName,
		stored: func(ctx context.Context) (string, error) {
			return runTmux(ctx, tmuxSocket, "show-options", "-qv", "-t", row.PaneID, herdr.StoredOption)
		},
		prove: call.ProveHerdrBridge,
		close: func(ctx context.Context, socket, pane string) error {
			_, err := herdr.SeatRequest(ctx, socket, "pane.close", map[string]string{nativePaneIDKey: pane})
			return err
		},
	}, stderr)
}

// retireHerdrViews closes every other Herdr view of a tmux chat about to be
// attached in the caller's Herdr pane: each client the chat session's Herdr
// record lists in another pane of the caller's server is closed only when the
// bridge proof names that very client of this chat's tmux server (its pane
// closing hangs up the client, which detaches; a pane already gone is no
// failure). An unproved view is left alone with a named line, and one failure
// never stops the others.
func retireHerdrViews(
	ctx context.Context,
	tmuxSocket, callerSocket, callerPane string,
	reach herdrViewRetirer,
	stderr io.Writer,
) {
	raw, err := reach.stored(ctx)
	if err != nil {
		fmt.Fprintf(
			stderr, "pfm chat open: other Herdr views of this chat stay: read %s: %v\n", herdr.StoredOption, err,
		)
		return
	}
	if strings.TrimSpace(raw) == "" {
		return
	}
	var stored herdr.Stored
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &stored); err != nil {
		fmt.Fprintf(
			stderr, "pfm chat open: other Herdr views of this chat stay: decode %s: %v\n", herdr.StoredOption, err,
		)
		return
	}
	clients := slices.Sorted(maps.Keys(stored.Clients))
	for _, key := range clients {
		client := stored.Clients[key]
		if client.Socket != callerSocket || client.Pane == "" || client.Pane == callerPane {
			continue
		}
		proof, err := reach.prove(ctx, callerSocket, client.Pane)
		if err == nil && (strconv.Itoa(proof.ClientPID) != key ||
			filepath.Clean(proof.TmuxSocket) != filepath.Clean(tmuxSocket) || proof.Session != reach.session) {
			err = fmt.Errorf("its foreground client %d of %s session %q is not this chat's client %s of %s session %q",
				proof.ClientPID, proof.TmuxSocket, proof.Session, key, tmuxSocket, reach.session)
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat open: Herdr pane %s still shows this chat: %v\n", client.Pane, err)
			continue
		}
		if err := reach.close(ctx, callerSocket, client.Pane); err != nil {
			var refusal *herdr.Refusal
			if errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
				continue
			}
			fmt.Fprintf(stderr, "pfm chat open: Herdr pane %s still shows this chat: close: %v\n", client.Pane, err)
		}
	}
}

// The Herdr methods that bring a live native chat into the caller's tab.
const (
	methodPaneMove = "pane.move"
	methodPaneSwap = "pane.swap"
)

// paneMoveReply is the body of a pane.move or pane.swap answer: the fields
// Herdr's PaneMoveResult and PaneSwapResult carry (src/api/schema/panes.rs
// L521-L530 and L541-L560), Pane only on a move.
type paneMoveReply struct {
	Changed      bool   `json:"changed"`
	Reason       string `json:"reason"`
	SourcePane   string `json:"source_pane_id"`
	TargetPane   string `json:"target_pane_id"`
	PreviousPane string `json:"previous_pane_id"`
	Pane         *struct {
		ID  string `json:"pane_id"`
		Tab string `json:"tab_id"`
	} `json:"pane"`
}

// checkMoveReply checks the answer to a pane.move (want pane_move) or a
// pane.swap (want pane_swap) against what Herdr's handlers return. The wire
// nests the body under the variant's field, `swap` or `move_result`
// (src/api/schema/response.rs L126-L131; docs/next/api/herdr-api.schema.json,
// and a live pane.swap answers {"type":"pane_swap","swap":{"changed":…}}); the
// socket-api prose describes the same fields at the top level, so both shapes
// are read. A swap answers changed:false with a reason, never an error, for a
// pair in different tabs or workspaces (cross_tab, src/app/api/panes.rs
// L860-L891) and echoes the source and target ids it resolved
// (src/app/api/panes.rs L902-L925): a changed answer must name this request's
// own two panes. A move must have put the chat's own pane into the caller's tab.
// errMoveApplied marks a reply that says Herdr changed the layout but not into
// the place asked for: the chat moved, so it never reads as left where it is.
var errMoveApplied = errors.New("its answer does not confirm where it went")

func checkMoveReply(method, want string, body json.RawMessage, chatPane, caller, callerTab string) error {
	var envelope struct {
		Type       string         `json:"type"`
		Swap       *paneMoveReply `json:"swap"`
		MoveResult *paneMoveReply `json:"move_result"`
		paneMoveReply
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode Herdr %s: %w", method, err)
	}
	if envelope.Type != want {
		return fmt.Errorf("answered %q, not %q: %s", envelope.Type, want, body)
	}
	reply := envelope.paneMoveReply
	switch {
	case method == methodPaneSwap && envelope.Swap != nil:
		reply = *envelope.Swap
	case method == methodPaneMove && envelope.MoveResult != nil:
		reply = *envelope.MoveResult
	}
	if !reply.Changed {
		return fmt.Errorf("changed false (reason %q): %s", reply.Reason, body)
	}
	switch method {
	case methodPaneSwap:
		if reply.SourcePane != chatPane || reply.TargetPane != caller {
			return fmt.Errorf("%w: swapped %q with %q, not %s with %s: %s",
				errMoveApplied, reply.SourcePane, reply.TargetPane, chatPane, caller, body)
		}
	case methodPaneMove:
		if reply.Pane == nil || reply.Pane.ID != chatPane || reply.Pane.Tab != callerTab {
			return fmt.Errorf("%w: the chat's pane %s is not in tab %s: %s", errMoveApplied, chatPane, callerTab, body)
		}
	}
	return nil
}
