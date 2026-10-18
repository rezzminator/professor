package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	restoredKnobFork = "fork"
	restoredKnobName = "name"
)

type (
	restoredOpenKey struct{}
	restoredGate    struct{}
)

func (restoredGate) Confirm(ctx context.Context, _ action.GateRequest) (bool, error) {
	return false, ctx.Err()
}

// OpenRestoredID resumes a restored pane without an interactive decision.
func OpenRestoredID(ctx context.Context, id string, stdout, stderr io.Writer, runtime *config.Runtime) int {
	if id == "" {
		fmt.Fprintln(stderr, "pfm chat open: restored session ID is required")
		return 2
	}
	effective, err := config.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	ctx = context.WithValue(ctx, restoredOpenKey{}, true)
	seat, found, err := nativeSeat(ctx, effective, id, "")
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: restored seat: %v\n", err)
		return 1
	}
	choice, err := BirthBackend(ctx, spawn.BackendAuto, paths.OSEnv{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: restored backend: %v\n", err)
		return 1
	}
	if choice.Warning != "" {
		fmt.Fprintln(stderr, choice.Warning)
	}
	if choice.Backend != spawn.BackendHerdr {
		return OpenID(ctx, id, stdout, stderr, &effective)
	}
	if !found {
		seat, err = prepareRestoredSeat(ctx, id, effective, choice, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat open: restored recovery: %v\n", err)
			return 1
		}
	}
	if err := restoreNative(ctx, effective, seat, choice, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "pfm chat open: restored recovery: %v\n", err)
		return 1
	}
	return 0
}

func restoreNative(
	ctx context.Context,
	runtime config.Runtime,
	seat fleetdb.Seat,
	choice spawn.BackendChoice,
	stdout, stderr io.Writer,
) (returnErr error) {
	if err := herdr.CheckProtocol(ctx, choice.HerdrSocket); err != nil {
		return err
	}
	if seat.HerdrSocket != choice.HerdrSocket || seat.HerdrPane != choice.HerdrPane {
		alive, err := nativeAlive(ctx, seat)
		if err != nil && !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("probe old restored owner: %w", err)
		}
		if alive {
			return fmt.Errorf(
				"native seat %s still owns a live engine in %s; refusing restored rebind",
				seat.SeatID,
				seat.HerdrPane,
			)
		}
	}
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	run, err := db.ReadSeatRun(seat.SeatID)
	if err != nil {
		return fmt.Errorf("read restored recipe: %w", err)
	}
	run, err = resumeNativeRun(run, seat)
	if err != nil {
		return err
	}
	run.Env = append(
		herdr.StripReporter(run.Env),
		herdr.EnvMarker+"=1",
		herdr.EnvSocket+"="+choice.HerdrSocket,
		herdr.EnvPane+"="+choice.HerdrPane,
		"TMUX=",
	)
	// Preserve existing loss evidence if the producer could not be read.
	note, noteErr := restoredLossNote(ctx, seat.SessionID, pfmengine.ID(seat.Engine), runtime, stderr)
	if noteErr != nil {
		fmt.Fprintf(stderr, "pfm chat open: restart loss unknown: %v\n", noteErr)
	}
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return fmt.Errorf("read restored owner: %w", err)
	}
	if !found || current.SessionID != seat.SessionID || current.HerdrSocket != seat.HerdrSocket ||
		current.HerdrPane != seat.HerdrPane {
		return errors.New("restored seat owner changed before rebind")
	}
	before := current
	current.HerdrSocket = choice.HerdrSocket
	current.HerdrPane = choice.HerdrPane
	current.HerdrWorkspace = choice.HerdrWorkspace
	// SeatHost marks live only after the engine actually starts.
	current.State = "lost"
	if noteErr != nil {
		if note == "" {
			note = current.LostNote
		}
		if !strings.Contains(note, lostUnknown) {
			note = strings.Trim(note+"; "+lostUnknown, "; ")
		}
	}
	current.LostNote = note
	if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
		return fmt.Errorf("write restored recipe: %w", err)
	}
	if err := db.UpdateSeat(ctx, before, current); err != nil {
		return fmt.Errorf("rebind restored seat: %w", err)
	}
	if current.Name != "" {
		target := headless.Chat{
			Backend: spawn.BackendHerdr,
			SeatID:  current.SeatID,
			Socket:  current.HerdrSocket,
			Pane:    current.HerdrPane,
		}
		if err := renameHerdr(ctx, target, current.Name, runtime.Paths); err != nil {
			var refusal *herdr.Refusal
			if !errors.As(err, &refusal) || refusal.Code != "agent_not_found" ||
				!strings.HasPrefix(refusal.Request, "agent.rename ") {
				return fmt.Errorf("restore name: %w", err)
			}
			// The pane label is acknowledged; seat-host registers the agent after launch.
			fmt.Fprintf(stderr, "pfm chat open: restored pane named; agent not registered yet: %v\n", err)
		}
	}
	if current.LostNote != "" {
		if err := notifyNativeLoss(ctx, current, current.LostNote); err != nil {
			fmt.Fprintf(stderr, "pfm chat open: restart loss notification: %v\n", err)
		}
	}
	return action.Dispatch(stdout, "exec pfm internal seat-host "+current.SeatID)
}

func restoredLossNote(
	ctx context.Context,
	id string,
	engine pfmengine.ID,
	runtime config.Runtime,
	stderr io.Writer,
) (note string, returnErr error) {
	if engine == pfmengine.OpenCode {
		return lossOpenCode(ctx, runtime.Paths.FirstRoot(engine), id)
	}
	index, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		return "", fmt.Errorf("open transcript index: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, index.Close()) }()
	if engine == pfmengine.Claude {
		record, found, err := index.Transcript(ctx, id)
		if err != nil {
			return "", err
		}
		if !found {
			return "", errors.New("transcript is not indexed")
		}
		return lossNote(ctx, record.Path, engine)
	}
	target, err := Target(ctx, id, &runtime)
	if err != nil {
		return "", err
	}
	return lossNote(ctx, target.Path, engine)
}

func notifyNativeLoss(ctx context.Context, seat fleetdb.Seat, note string) error {
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"notification.show",
		map[string]string{"title": seat.Name, "body": note},
	)
	if err != nil {
		return err
	}
	var ack struct {
		Type   string `json:"type"`
		Shown  *bool  `json:"shown"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		return fmt.Errorf("decode loss notification: %w", err)
	}
	if ack.Type != "notification_show" || ack.Shown == nil {
		return fmt.Errorf("unexpected loss notification acknowledgement: %s", body)
	}
	if !*ack.Shown {
		return fmt.Errorf("loss note saved; Herdr notification not shown (%s)", ack.Reason)
	}
	return nil
}

// resumeNativeRun retains the structured launch's preferences. Only birth
// identity and its initial positional prompt are consumed, never resent.
func resumeNativeRun(run fleetdb.SeatRun, seat fleetdb.Seat) (fleetdb.SeatRun, error) {
	engineID := pfmengine.ID(seat.Engine)
	if _, err := pfmengine.Lookup(engineID); err != nil {
		return run, err
	}
	if engineID == pfmengine.Claude {
		if _, err := claudelaunch.Parse(append([]string{run.Binary}, run.Args...)); err != nil {
			return run, fmt.Errorf("parse restored recipe: %w", err)
		}
	}
	remove := make(map[string]bool)
	resume := ""
	arity := map[string]bool{claudelaunch.SettingsFlag: true}
	for _, knob := range claudelaunch.Knobs {
		if knob.Wire == claudelaunch.WireFlag {
			for _, flag := range strings.Split(knob.Target, "|") {
				if strings.HasPrefix(flag, "-") {
					arity[flag] = knob.Name != "permissionMode" && knob.Name != restoredKnobFork
				}
			}
		}
		switch knob.Name {
		case "sessionID", "resume", restoredKnobName, restoredKnobFork:
			remove[knob.Target] = knob.Name != restoredKnobFork
			if knob.Name == "resume" {
				resume = knob.Target
			}
		}
	}
	if resume == "" {
		return run, errors.New("launch registry has no resume flag")
	}
	if engineID != pfmengine.Claude {
		remove = map[string]bool{"--prompt": true, "--session": true, "--continue": false}
	}
	// Raw engines have no flag registry; match the canonical recipe producers.
	switch engineID {
	case pfmengine.Codex:
		arity = map[string]bool{
			pfmengine.CodexNoDaemon:                      false,
			"--dangerously-bypass-approvals-and-sandbox": false,
			"--full-auto":                                false,
			"--sandbox":                                  true,
			"--model":                                    true,
			"-c":                                         true,
		}
	case pfmengine.OpenCode:
		arity = map[string]bool{"--model": true}
	}
	args := make([]string, 0, len(run.Args)+2)
	for i := 0; i < len(run.Args); i++ {
		word := run.Args[i]
		key, _, inline := strings.Cut(word, "=")
		if takesValue, ok := remove[key]; ok {
			if takesValue && !inline {
				if i+1 >= len(run.Args) || strings.HasPrefix(run.Args[i+1], "-") {
					return run, fmt.Errorf("restored recipe %s requires a value", key)
				}
				i++
			}
			continue
		}
		if word == "--" {
			break
		}
		if !strings.HasPrefix(word, "-") {
			continue
		}
		args = append(args, word)
		if !inline && i+1 < len(run.Args) && !strings.HasPrefix(run.Args[i+1], "-") {
			takesValue, known := arity[key]
			if !known {
				return run, errors.New("restored recipe has an ambiguous unregistered flag value")
			}
			if takesValue {
				i++
				args = append(args, run.Args[i])
			}
		}
	}
	switch engineID {
	case pfmengine.Claude:
		args = append(args, resume, seat.SessionID)
	case pfmengine.Codex:
		args = append(args, "resume", seat.SessionID)
	case pfmengine.OpenCode:
		args = append(args, "--session", seat.SessionID, run.CWD)
	}
	run.Args = args
	run.Env = herdr.StripReporter(run.Env)
	return run, nil
}

func prepareRestoredSeat(
	ctx context.Context,
	id string,
	runtime config.Runtime,
	choice spawn.BackendChoice,
	stderr io.Writer,
) (fleetdb.Seat, error) {
	if err := herdr.CheckProtocol(ctx, choice.HerdrSocket); err != nil {
		return fleetdb.Seat{}, err
	}
	rows, err := Rows(ctx, stderr, &runtime)
	if err != nil {
		return fleetdb.Seat{}, err
	}
	var row compose.Row
	found := false
	for index := range rows {
		candidate := &rows[index]
		if candidate.ID == id {
			row = *candidate
			found = true
			break
		}
	}
	if !found {
		return fleetdb.Seat{}, fmt.Errorf("chat %q is not indexed", id)
	}
	if info, err := os.Stat(row.CWD); err != nil || !info.IsDir() {
		row.CWD = runtime.Paths.Home
	}
	// A lost display name must not prevent restoring the indexed identity.
	if row.Name == "" {
		row.Name = id
	}
	engineID := compose.EngineForKind(row.Kind)
	account := row.Account
	if account == 0 {
		account = runtime.Config.PrimaryAccountFor(engineID, 1)
	}
	var recipe *fleetdb.SeatRun
	switch engineID {
	case pfmengine.Claude:
		spawnPlan := action.ClaudeSpawn{
			Purpose: action.PurposeResume,
			Home:    runtime.Paths.Home,
			Account: account,
			Cache1H: &row.C1H,
			Resume:  id,
			Machine: runtime.Config,
		}
		run, err := spawnPlan.SeatRun(row.CWD)
		if err != nil {
			return fleetdb.Seat{}, err
		}
		recipe = &run
	case pfmengine.Codex:
		plan, err := action.PlanCodex(
			action.HeadlessRequest{
				Engine:          engineID,
				ResumeSessionID: id,
				Name:            row.Name,
				CWD:             row.CWD,
				Home:            runtime.Paths.Home,
				PrimaryAccount:  account,
				Config:          runtime.Config,
			},
		)
		if err != nil {
			return fleetdb.Seat{}, err
		}
		recipe = plan.SeatRun
	case pfmengine.OpenCode:
		row.Kind = compose.ResumeOpenCode
		plan, err := action.Synthesize(
			action.Request{
				Row:            row,
				Home:           runtime.Paths.Home,
				PrimaryAccount: account,
				Config:         runtime.Config,
				FreshSocket:    "restored",
			},
		)
		if err != nil {
			return fleetdb.Seat{}, err
		}
		recipe = plan.SeatRun
	}
	if recipe == nil {
		return fleetdb.Seat{}, errors.New("restored engine has no structured resume recipe")
	}
	prepared, err := spawn.PrepareHerdr(
		ctx,
		spawn.HerdrRequest{
			Choice: choice,
			Paths:  runtime.Paths,
			Run:    *recipe,
			Seat:   fleetdb.Seat{SessionID: id, Engine: string(engineID), Name: row.Name},
		},
	)
	return prepared.Seat, err
}
