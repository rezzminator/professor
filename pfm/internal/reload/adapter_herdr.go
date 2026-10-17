package reload

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// SeatForReload resolves only an exact durable native address, never a socket name.
func SeatForReload(ctx context.Context, values paths.Values, socket, pane string) (*fleetdb.Seat, error) {
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, fmt.Errorf("read reload backend: %w", err)
	}
	for i := range seats {
		seat := &seats[i]
		if seat.Backend == spawn.BackendHerdr && seat.HerdrSocket == socket && seat.HerdrPane == pane {
			return seat, nil
		}
	}
	return nil, nil
}

// NativeTarget's handled result distinguishes native refusal from a tmux target.
// An unreadable store is returned, not rendered as an empty native roster.
func NativeTarget(
	ctx context.Context,
	values paths.Values,
	socket, pane string,
	env paths.Env,
) (*fleetdb.Seat, bool, error) {
	if env == nil {
		env = paths.OSEnv{}
	}
	ambient := socket == "" && env.Get("TMUX") == "" && env.Get("HERDR_ENV") == "1"
	if ambient {
		socket = env.Get("HERDR_SOCKET_PATH")
		if pane == "" {
			pane = env.Get("HERDR_PANE_ID")
		}
		if socket == "" || pane == "" {
			return nil, true, errors.New("native socket and pane are required")
		}
	}
	if socket == "" {
		return nil, false, nil
	}
	if !filepath.IsAbs(socket) {
		socket = filepath.Join(values.TmuxDir, socket)
	}
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, ambient, fmt.Errorf("read native seats: %w", err)
	}
	var chosen *fleetdb.Seat
	handled := ambient
	for i := range seats {
		seat := &seats[i]
		if seat.Backend != spawn.BackendHerdr || seat.HerdrSocket != socket {
			continue
		}
		handled = true
		if pane != "" && pane != seat.HerdrPane {
			continue
		}
		if chosen != nil {
			return nil, true, errors.New("native socket has multiple panes — specify --pane")
		}
		chosen = seat
	}
	if chosen == nil && handled {
		return nil, true, fmt.Errorf("no native seat for %s %s", socket, pane)
	}
	return chosen, handled, nil
}

// NotifyWorkerFailure uses the native backend surface, including config failures.
// On a backend lookup failure the caller must not fall through to tmux.
func NotifyWorkerFailure(ctx context.Context, values paths.Values, socket, pane, message string) (bool, error) {
	seat, err := SeatForReload(ctx, values, socket, pane)
	if err != nil {
		return true, err
	}
	if seat == nil {
		return false, nil
	}
	_, err = herdr.SeatRequest(
		ctx,
		socket,
		"notification.show",
		map[string]string{"title": "pfm chat reload failed", "body": message},
	)
	if err != nil {
		return true, fmt.Errorf("native reload failure notification: %w", err)
	}
	return true, nil
}

// EngineForTarget takes native identity from its durable seat, never its socket name.
func EngineForTarget(ctx context.Context, values paths.Values, socket, pane string) (pfmengine.ID, error) {
	seat, err := SeatForReload(ctx, values, socket, pane)
	if err != nil {
		return "", err
	}
	if seat != nil {
		return pfmengine.ID(seat.Engine), nil
	}
	return EngineOf(socket), nil
}

// WorkerArguments carries the validated CLI values to a backend-specific request.
type WorkerArguments struct {
	Account, Cache, Socket, Pane, Then, Model, Effort string
	New, Hide                                         bool
}

// ParseWorkerArguments decodes values after the CLI's product validation.
func ParseWorkerArguments(args []string) (WorkerArguments, error) {
	var parsed WorkerArguments
	values := map[string]*string{
		"--account": &parsed.Account,
		"--cache":   &parsed.Cache,
		"--sock":    &parsed.Socket,
		"--pane":    &parsed.Pane,
		"--then":    &parsed.Then,
		"--model":   &parsed.Model,
		"--effort":  &parsed.Effort,
	}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--new":
			parsed.New = true
		case "--hide":
			parsed.Hide = true
		default:
			if dest, ok := values[args[index]]; ok {
				if index+1 >= len(args) {
					return parsed, fmt.Errorf("%s needs a value", args[index])
				}
				index++
				*dest = args[index]
			} else {
				parsed.Account = args[index]
			}
		}
	}
	return parsed, nil
}

// NativeRequest constructs a typed native request without tmux identity inference.
func NativeRequest(runtime pfmconfig.Runtime, seat fleetdb.Seat, args WorkerArguments) (Request, error) {
	engine := pfmengine.ID(seat.Engine)
	account := runtime.Config.PrimaryAccountFor(engine, runtime.Config.ImplicitAccount())
	if args.Account != "" {
		var err error
		account, err = strconv.Atoi(args.Account)
		if err != nil {
			return Request{}, fmt.Errorf("native account %q: %w", args.Account, err)
		}
	}
	selected, err := ValidateAccount(runtime.Config, engine, account)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Engine:       engine,
		SocketPath:   seat.HerdrSocket,
		Pane:         seat.HerdrPane,
		SessionID:    seat.SessionID,
		LeftBehind:   seat.SessionID,
		New:          args.New,
		AccountGiven: args.Account != "",
		CacheGiven:   args.Cache != "",
		Cache1H:      args.Cache == "1h",
		Account:      account,
		AccountIDs:   selected.IDs,
		CodexHome:    selected.CodexHome,
		CodexBinary:  selected.CodexBinary,
		CodexYolo:    selected.CodexYolo,
		Model:        args.Model,
		Effort:       args.Effort,
		Then:         args.Then,
		Home:         runtime.Paths.Home,
		Machine:      runtime.Config,
	}, nil
}

// OptionsForWorker retains the CLI's duration parser and common backend defaults.
func OptionsForWorker(values paths.Values, env paths.Env, duration func(string, int, paths.Env) time.Duration) Options {
	return Options{
		Home:        values.Home,
		SIDDir:      values.SIDDir,
		ClaudeRoots: values.Roots[pfmengine.Claude],
		Delay:       duration("PFM_RELOAD_DELAY_MS", 1500, env),
		Poll:        duration("PFM_RELOAD_POLL_MS", 1000, env),
		ExitTries: ParseIntEnv(
			paths.OSEnv{},
			"PFM_RELOAD_EXIT_TRIES",
			20,
		),
		IdleTries: ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_IDLE_TRIES", 120),
		ThenTries: ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_THEN_TRIES", 900),
	}
}
