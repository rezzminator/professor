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

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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
	}
	if returnErr == nil && found && (seat.Backend != spawn.BackendHerdr || seat.SessionID != id) {
		found = false
	}
	return
}

// nativeAlive checks foreground process evidence, not the reporter's cached status.
func nativeAlive(ctx context.Context, seat fleetdb.Seat) (bool, error) {
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.get",
		map[string]string{nativePaneIDKey: seat.HerdrPane},
	)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil
		}
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) && refusal.Code == "pane_not_found" {
			return false, nil
		}
		return false, fmt.Errorf("probe Herdr pane %s: %w", seat.HerdrPane, err)
	}
	var pane struct {
		Pane struct {
			ID string `json:"pane_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &pane); err != nil {
		return false, fmt.Errorf("decode Herdr pane probe: %w", err)
	}
	if pane.Pane.ID != seat.HerdrPane {
		return false, fmt.Errorf("herdr pane probe returned unexpected identity: %s", body)
	}
	body, err = herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.process_info",
		map[string]string{nativePaneIDKey: seat.HerdrPane},
	)
	if err != nil {
		return false, fmt.Errorf("read Herdr foreground processes: %w", err)
	}
	if _, err := herdr.ParseProcessInfo(body); err != nil {
		return false, err
	}
	var info struct {
		ProcessInfo struct {
			Pane      string `json:"pane_id"`
			Processes []struct {
				PID   int      `json:"pid"`
				Name  string   `json:"name"`
				Argv0 string   `json:"argv0"`
				Argv  []string `json:"argv"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return false, fmt.Errorf("decode Herdr foreground engine: %w", err)
	}
	if info.ProcessInfo.Pane != seat.HerdrPane || info.ProcessInfo.Processes == nil {
		return false, fmt.Errorf("herdr process info missing pane identity or foreground_processes: %s", body)
	}
	descriptor, err := pfmengine.Lookup(pfmengine.ID(seat.Engine))
	if err != nil {
		return false, fmt.Errorf("native seat engine: %w", err)
	}
	for _, process := range info.ProcessInfo.Processes {
		if process.PID <= 0 || process.Name == "" {
			return false, fmt.Errorf("herdr foreground process missing pid or name")
		}
		for _, value := range []string{process.Name, process.Argv0} {
			if filepath.Base(value) == descriptor.Binary {
				return true, nil
			}
		}
		if len(process.Argv) > 0 && filepath.Base(process.Argv[0]) == descriptor.Binary {
			return true, nil
		}
	}
	return false, nil
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
			return fmt.Errorf("focus Herdr pane %s: %w", seat.HerdrPane, err)
		}
		var ack struct {
			Type string `json:"type"`
			Pane string `json:"pane_id"`
		}
		if err := json.Unmarshal(body, &ack); err != nil {
			return fmt.Errorf("decode Herdr focus: %w", err)
		}
		if ack.Type != "pane_focused" || ack.Pane != seat.HerdrPane {
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

func openNativeRow(ctx context.Context, row compose.Row, stdout, stderr io.Writer, runtime config.Runtime) (bool, int) {
	if row.Backend != spawn.BackendHerdr {
		return false, 0
	}
	seat, found, err := nativeSeat(ctx, runtime, row.ID, row.SeatID)
	if err == nil && !found {
		err = fmt.Errorf("native open target %s no longer matches its durable seat", row.SeatID)
	}
	if err == nil {
		var alive bool
		alive, err = nativeAlive(ctx, seat)
		if err == nil && !alive {
			// D4: a dead seat resumes by the caller's current birth rule.
			choice, choiceErr := BirthBackend(ctx, spawn.BackendAuto, paths.OSEnv{})
			switch {
			case choiceErr != nil:
				err = choiceErr
			case choice.Backend == spawn.BackendHerdr:
				return true, OpenRestoredID(ctx, row.ID, stdout, stderr, &runtime)
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
				return true, OpenRow(
					ctx,
					row,
					runtime.Config.PrimaryAccountFor(pfmengine.ID(seat.Engine), row.Account),
					row.C1H,
					"",
					stdout,
					stderr,
					&runtime,
				)
			}
		}
		if err == nil {
			err = viewNative(ctx, seat, stdout, stderr)
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
	alive, err := nativeAlive(ctx, seat)
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
		next.State = liveState
		born, err := spawn.SpawnHerdr(
			ctx,
			spawn.HerdrRequest{Choice: choice, Seat: next, Run: run, Paths: runtime.Paths},
		)
		if err != nil {
			return result, err
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
	return openDetachedResolvedRow(ctx, row, stderr, runtime)
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
