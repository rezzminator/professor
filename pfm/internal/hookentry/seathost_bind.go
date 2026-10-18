package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// bindNativeHost validates the caller's canonical producer identity and kernel
// membership before publishing any new binding. Only explicit lost restoration
// after positive old-host death may adopt a new terminal.
func bindNativeHost(
	ctx context.Context,
	db *fleetdb.Store,
	seat fleetdb.Seat,
	values paths.Values,
) (result fleetdb.Seat, returnErr error) {
	key := fleetdb.ConversationIdentity(seat.SessionID, seat.SeatID)
	unlock, err := fleetdb.LockSeat(ctx, values, key)
	if err != nil {
		return seat, fmt.Errorf("lock supervisor bind: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	current, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return seat, fmt.Errorf("read supervisor binding: %w", err)
	}
	if !found || current.EndPending {
		return seat, errors.New("seat absent or pending end")
	}
	seat = current
	proc := gather.NewProcFS(values.ProcRoot)
	cold := false
	if seat.SeatHostPID > 0 {
		dead, err := gather.HerdrHostDead(proc, seat)
		if err != nil {
			return seat, err
		}
		if seat.SeatHostPID != os.Getpid() || dead {
			if seat.State != "lost" || !dead {
				return seat, errors.New("previous supervisor is not explicitly lost and dead")
			}
			cold = true
		}
	}
	socket, pane := (paths.OSEnv{}).Get(herdr.EnvSocket), (paths.OSEnv{}).Get(herdr.EnvPane)
	if socket != seat.HerdrSocket && !cold {
		return seat, errors.New("caller socket differs from durable owner")
	}
	body, err := herdr.SeatRequest(ctx, socket, "pane.get", map[string]string{"pane_id": pane})
	if err != nil {
		return seat, fmt.Errorf("resolve caller alias: %w", err)
	}
	var info struct {
		Type string `json:"type"`
		Pane struct {
			ID        string `json:"pane_id"`
			Workspace string `json:"workspace_id"`
			Terminal  string `json:"terminal_id"`
		} `json:"pane"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return seat, fmt.Errorf("decode caller pane: %w", err)
	}
	if info.Type != "pane_info" || info.Pane.ID == "" || info.Pane.Workspace == "" || info.Pane.Terminal == "" ||
		!strings.HasPrefix(info.Pane.ID, info.Pane.Workspace+":") {
		return seat, errors.New("caller pane has malformed identity")
	}
	if !cold && seat.HerdrTerminalID != "" && seat.HerdrTerminalID != info.Pane.Terminal {
		return seat, errors.New("caller terminal differs from durable owner")
	}
	if !cold && seat.HerdrTerminalID == "" && seat.HerdrPane != pane && seat.HerdrPane != info.Pane.ID {
		return seat, errors.New("unbound caller differs from reserved pane")
	}
	// Validate our exact process role before a tentative binding can be observed.
	argv, err := proc.Cmdline(os.Getpid())
	if err != nil {
		return seat, fmt.Errorf("read supervisor argv: %w", err)
	}
	if len(argv) < 4 {
		return seat, errors.New("supervisor argv unavailable")
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	if len(args) >= 2 && args[0] == "--config" {
		args = args[2:]
	}
	if (name != "pfm" && name != "pfm.dev") || len(args) != 3 || args[0] != internalCommand || args[1] != "seat-host" ||
		args[2] != seat.SeatID {
		return seat, errors.New("supervisor must exec exact seat-host role")
	}
	body, err = herdr.SeatRequest(ctx, socket, "pane.process_info", map[string]string{"pane_id": info.Pane.ID})
	if err != nil {
		return seat, fmt.Errorf("read caller shell: %w", err)
	}
	var process struct {
		Type string `json:"type"`
		Info struct {
			Pane  string `json:"pane_id"`
			Shell int    `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &process); err != nil {
		return seat, fmt.Errorf("decode caller shell: %w", err)
	}
	if process.Type != "pane_process_info" || process.Info.Pane != info.Pane.ID || process.Info.Shell <= 1 {
		return seat, errors.New("caller shell identity unavailable")
	}
	member := false
	seen := map[int]bool{}
	for pid := os.Getpid(); pid > 0 && !seen[pid]; {
		if pid == process.Info.Shell {
			member = true
			break
		}
		seen[pid] = true
		stat, err := proc.Stat(pid)
		if err != nil {
			return seat, fmt.Errorf("read supervisor ancestry: %w", err)
		}
		pid = stat.ParentPID
	}
	if !member {
		return seat, errors.New("supervisor outside caller pane")
	}
	next := seat
	next.HerdrSocket = socket
	next.HerdrPane = info.Pane.ID
	next.HerdrWorkspace = info.Pane.Workspace
	next.HerdrTerminalID = info.Pane.Terminal
	if err := gather.StampHerdrHost(proc, &next, os.Getpid()); err != nil {
		return seat, err
	}
	if err := db.UpdateSeat(ctx, seat, next); err != nil {
		return seat, fmt.Errorf("persist supervisor binding: %w", err)
	}
	proved, owned, err := gather.ProveHerdrSeat(ctx, db, proc, next)
	if err != nil || !owned {
		if err == nil {
			err = errors.New("bound supervisor disappeared")
		}
		rollbackErr := db.UpdateSeat(ctx, next, seat)
		return seat, fmt.Errorf(
			"prove bound supervisor (binding rolled back unless state changed): %w",
			errors.Join(err, rollbackErr),
		)
	}
	return proved, nil
}
