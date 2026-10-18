package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const (
	nativePaneIDKey = "pane_id"
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

func nativeLocation(seat fleetdb.Seat) string {
	return fmt.Sprintf("%s lives in Herdr pane %s (%s)", seat.Name, seat.HerdrPane, seat.HerdrSocket)
}

func viewNative(ctx context.Context, seat fleetdb.Seat, stdout, stderr io.Writer) error {
	if (paths.OSEnv{}).Get(herdr.EnvMarker) == "1" && (paths.OSEnv{}).Get(herdr.EnvSocket) == seat.HerdrSocket {
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
		if ack.Type == "pane_info" {
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
			choice, choiceErr := BirthBackend(ctx, spawn.BackendAuto, paths.OSEnv{})
			switch {
			case choiceErr != nil:
				err = choiceErr
			case choice.Backend == spawn.BackendHerdr:
				result, openErr := detachedNativeLocked(ctx, seat, stderr, runtime, true)
				err = openErr
				if err == nil && result.State == liveState {
					seat, alive, err = nativeAlive(ctx, seat, runtime.Paths)
					if err == nil && !alive {
						err = fmt.Errorf("native seat %s died before view", seat.SeatID)
					}
					if err == nil {
						err = unlock()
						unlock = nil
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
			default:
				if choice.Warning != "" {
					fmt.Fprintln(stderr, choice.Warning)
				}
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
			if err == nil {
				seat, err = offerNativeAccount(ctx, seat, primary, cache1H, stderr, runtime)
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
	focus bool,
) (result action.OpenResult, returnErr error) {
	current, unlock, err := lockNativeSeat(ctx, runtime, seat)
	if err != nil {
		return result, err
	}
	defer func() {
		if unlock != nil {
			returnErr = errors.Join(returnErr, unlock())
		}
	}()
	result, returnErr = detachedNativeLocked(ctx, current, stderr, runtime, focus)
	if returnErr == nil && result.State == liveState && focus {
		var alive bool
		current, alive, returnErr = nativeAlive(ctx, current, runtime.Paths)
		if returnErr != nil {
			return result, returnErr
		}
		if !alive {
			return result, fmt.Errorf("native seat %s died before view", current.SeatID)
		}
		returnErr = unlock()
		unlock = nil
		if returnErr == nil {
			returnErr = viewNative(ctx, current, io.Discard, stderr)
		}
	}
	return result, returnErr
}

func detachedNativeLocked(
	ctx context.Context,
	seat fleetdb.Seat,
	stderr io.Writer,
	runtime config.Runtime,
	focus bool,
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
	choice, err := BirthBackend(ctx, spawn.BackendAuto, paths.OSEnv{})
	if err != nil {
		return result, err
	}
	if choice.Warning != "" {
		fmt.Fprintln(stderr, choice.Warning)
	}
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	run, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		return result, err
	}
	run, err = resumeNativeRun(run, seat)
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
				Focus:  focus,
				Stderr: stderr,
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
