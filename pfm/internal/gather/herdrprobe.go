package gather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const nativeHerdrBackend = "herdr"

// HerdrSeat is a durable native seat and its visible probe failure, if any.
type HerdrSeat struct {
	CWD         string
	Seat        fleetdb.Seat
	Err         error
	snapshot    json.RawMessage
	directories map[string]string
}

// ProbeHerdrSeats retains every live durable seat, including unreachable ones.
// One pane listing per socket serves all its seats for this invocation.
func probeHerdrSeatRows(ctx context.Context, seats []fleetdb.Seat) []HerdrSeat {
	type snapshot struct {
		cwd  map[string]string
		body json.RawMessage
		err  error
	}
	out := make([]HerdrSeat, 0)
	sockets := make(map[string]snapshot)
	for i := range seats {
		seat := seats[i]
		if seat.Backend != nativeHerdrBackend || seat.State != "live" {
			continue
		}
		current, found := sockets[seat.HerdrSocket]
		if !found {
			current.cwd, current.body, current.err = probeHerdrPanes(ctx, seat.HerdrSocket)
			if current.err != nil {
				label := "HERDR ERROR"
				var transport *net.OpError
				if errors.As(current.err, &transport) {
					label = "HERDR UNREACHABLE"
				}
				current.err = fmt.Errorf("%s %s: %w", label, seat.HerdrSocket, current.err)
			}
			sockets[seat.HerdrSocket] = current
		}
		row := HerdrSeat{Seat: seat, Err: current.err, snapshot: current.body, directories: current.cwd}
		if row.Err == nil {
			if cwd, present := current.cwd[seat.HerdrPane]; present {
				row.CWD = cwd
			} else {
				row.Err = fmt.Errorf(
					"HERDR ERROR %s: pane_not_found: pane %q not present in pane.list",
					seat.HerdrSocket,
					seat.HerdrPane,
				)
			}
		}
		out = append(out, row)
	}
	return out
}

// probeHerdrPanes consumes canonical pane identities and their optional CWDs
// from the same producer snapshot; it never adds per-seat round trips.
func probeHerdrPanes(ctx context.Context, socket string) (map[string]string, json.RawMessage, error) {
	body, err := herdr.SeatRequest(ctx, socket, "pane.list", struct{}{})
	if err != nil {
		return nil, nil, err
	}
	panes, err := herdr.ParsePanes(body)
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		Panes []struct {
			CWD           string `json:"cwd"`
			ForegroundCWD string `json:"foreground_cwd"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, fmt.Errorf("decode pane.list CWDs: %w", err)
	}
	if result.Panes == nil {
		return nil, nil, fmt.Errorf("pane.list: missing or null panes array")
	}
	cwd := make(map[string]string, len(panes))
	for i, pane := range panes {
		if pane.PaneID == "" {
			return nil, nil, fmt.Errorf("pane.list: entry %d is missing pane_id", i)
		}
		if _, duplicate := cwd[pane.PaneID]; duplicate {
			return nil, nil, fmt.Errorf("pane.list: duplicate pane_id %q", pane.PaneID)
		}
		directory := result.Panes[i].ForegroundCWD
		if directory == "" {
			directory = result.Panes[i].CWD
		}
		cwd[pane.PaneID] = directory
	}
	return cwd, body, nil
}

// ErrHerdrProofPersistence marks proved metadata that could not be persisted.
// A caller may reopen writable state and repeat the entire proof.
var ErrHerdrProofPersistence = errors.New("persist native ownership proof")

// ProveHerdrSeat returns the canonical owned pane and true, or false only after
// positive saved-supervisor death. Unknown evidence always returns an error.
// It learns legacy metadata only from the exact seat-host beneath shell_pid.
func ProveHerdrSeat(
	ctx context.Context,
	db *fleetdb.Store,
	proc ProcFS,
	seat fleetdb.Seat,
) (fleetdb.Seat, bool, error) {
	return proveHerdrSeatSnapshot(ctx, db, proc, seat, nil, false)
}

func proveHerdrSeatSnapshot(
	ctx context.Context,
	db *fleetdb.Store,
	proc ProcFS,
	seat fleetdb.Seat,
	snapshot json.RawMessage,
	observe bool,
) (fleetdb.Seat, bool, error) {
	current, found, readErr := db.Seat(ctx, seat.SeatID)
	if readErr != nil {
		return seat, false, fmt.Errorf("read current ownership: %w", readErr)
	}
	if !found || current.Backend != nativeHerdrBackend ||
		current.Engine != seat.Engine {
		return seat, false, fmt.Errorf("seat %s ownership changed or disappeared", seat.SeatID)
	}
	if snapshot != nil && current.HerdrSocket != seat.HerdrSocket {
		return seat, false, fmt.Errorf("seat %s snapshot socket changed during proof", seat.SeatID)
	}
	seat = current
	if seat.Backend != nativeHerdrBackend || seat.SeatID == "" {
		return seat, false, fmt.Errorf("native proof: invalid seat identity")
	}
	if seat.SeatHostPID > 0 && seat.SeatHostStart > 0 {
		dead, err := HerdrHostDead(proc, seat)
		if err != nil {
			return seat, false, err
		}
		if dead {
			return seat, false, nil
		}
	}
	body := snapshot
	if body == nil {
		var err error
		body, err = herdr.SeatRequest(ctx, seat.HerdrSocket, "pane.list", struct{}{})
		if err != nil {
			return seat, false, fmt.Errorf("prove seat %s pane.list: %w", seat.SeatID, err)
		}
	}
	var list struct {
		Type  string            `json:"type"`
		Panes []nativeProofPane `json:"panes"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return seat, false, fmt.Errorf("decode ownership panes: %w", err)
	}
	if list.Type != "pane_list" || list.Panes == nil {
		return seat, false, fmt.Errorf("ownership pane.list: invalid envelope %s", body)
	}
	var candidate *nativeProofPane
	seen := map[string]bool{}
	for i := range list.Panes {
		pane := &list.Panes[i]
		if pane.Pane == "" || pane.Terminal == "" || pane.Workspace == "" ||
			!strings.HasPrefix(pane.Pane, pane.Workspace+":") ||
			seen[pane.Pane] {
			return seat, false, fmt.Errorf("ownership pane.list: malformed or duplicate identity")
		}
		seen[pane.Pane] = true
		matches := pane.Pane == seat.HerdrPane
		if seat.HerdrTerminalID != "" {
			matches = pane.Terminal == seat.HerdrTerminalID
		}
		if matches {
			if candidate != nil {
				return seat, false, fmt.Errorf("ownership pane.list: duplicate terminal identity")
			}
			candidate = pane
		}
	}
	if candidate == nil {
		if seat.SeatHostPID == 0 {
			retired, err := HerdrSeatRetired(ctx, proc, seat)
			if err != nil {
				return seat, false, err
			}
			if retired {
				return seat, false, nil
			}
		}
		return seat, false, fmt.Errorf("seat %s ownership unproved: terminal not present in pane.list", seat.SeatID)
	}
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.process_info",
		map[string]string{"pane_id": candidate.Pane},
	)
	if err != nil {
		return seat, false, fmt.Errorf("prove seat %s shell: %w", seat.SeatID, err)
	}
	var info struct {
		Type string `json:"type"`
		Info struct {
			Pane  string `json:"pane_id"`
			Shell int    `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return seat, false, fmt.Errorf("decode ownership shell: %w", err)
	}
	if info.Type != "pane_process_info" || info.Info.Pane != candidate.Pane || info.Info.Shell <= 1 {
		return seat, false, fmt.Errorf("seat %s ownership shell identity unavailable", seat.SeatID)
	}
	pid := seat.SeatHostPID
	if pid == 0 {
		pids, err := proc.PIDs()
		if err != nil {
			return seat, false, fmt.Errorf("enumerate seat hosts: %w", err)
		}
		for _, possible := range pids {
			member, err := belowHerdrShell(proc, possible, info.Info.Shell)
			if err != nil {
				return seat, false, err
			}
			if !member {
				continue
			}
			argv, err := proc.Cmdline(possible)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return seat, false, fmt.Errorf("read candidate seat host %d: %w", possible, err)
			}
			if HerdrSupervisorArgv(argv, seat.SeatID) {
				if pid != 0 {
					return seat, false, fmt.Errorf("seat %s has multiple supervisors", seat.SeatID)
				}
				pid = possible
			}
		}
	}
	if pid == 0 {
		return seat, false, fmt.Errorf("seat %s supervisor ownership unproved", seat.SeatID)
	}

	argvRole, roleErr := proc.Cmdline(pid)
	if roleErr != nil {
		return seat, false, fmt.Errorf("read supervisor role: %w", roleErr)
	}
	if legacyHerdrLauncher(argvRole) {
		seats, listErr := db.Seats(ctx)
		if listErr != nil {
			return seat, false, fmt.Errorf("map legacy supervisor: %w", listErr)
		}
		for index := range seats {
			other := &seats[index]
			if other.SeatID != seat.SeatID && other.Backend == nativeHerdrBackend &&
				other.HerdrSocket == seat.HerdrSocket &&
				(other.HerdrPane == candidate.Pane || (other.HerdrTerminalID != "" && other.HerdrTerminalID == candidate.Terminal)) {
				if other.State != "live" {
					if other.SeatHostPID == 0 {
						continue
					}
					dead, err := HerdrHostDead(proc, *other)
					if err != nil {
						return seat, false, fmt.Errorf("prove historical seat %s retired: %w", other.SeatID, err)
					}
					if dead {
						continue
					}
				}
				return seat, false, fmt.Errorf("legacy supervisor maps to multiple seats")
			}
		}
	}
	verified := seat
	if err := StampHerdrHost(proc, &verified, pid); err != nil {
		return seat, false, err
	}
	argv, err := proc.Cmdline(pid)
	if err != nil {
		return seat, false, fmt.Errorf("read seat host %d argv: %w", pid, err)
	}
	if !HerdrSupervisorArgv(argv, seat.SeatID) {
		return seat, false, fmt.Errorf("seat %s supervisor argv mismatch", seat.SeatID)
	}
	member, err := belowHerdrShell(proc, pid, info.Info.Shell)
	if err != nil {
		return seat, false, err
	}
	if !member {
		return seat, false, fmt.Errorf("seat %s supervisor is outside pane process tree", seat.SeatID)
	}
	next := seat
	if err := StampHerdrHost(proc, &next, pid); err != nil {
		return seat, false, err
	}
	if next.SeatHostStart != verified.SeatHostStart || next.SeatHostBirth != verified.SeatHostBirth {
		return seat, false, fmt.Errorf("seat %s supervisor birth changed during proof", seat.SeatID)
	}
	if seat.SeatHostPID > 0 && (next.SeatHostStart != seat.SeatHostStart ||
		(seat.SeatHostBirth > 0 && next.SeatHostBirth != seat.SeatHostBirth)) {
		return seat, false, fmt.Errorf("seat %s saved supervisor birth changed during proof", seat.SeatID)
	}
	next.HerdrTerminalID = candidate.Terminal
	next.HerdrPane = candidate.Pane
	next.HerdrWorkspace = candidate.Workspace
	if next != seat && !observe {
		if err := db.UpdateSeat(ctx, seat, next); err != nil {
			return seat, false, fmt.Errorf("persist proved seat %s: %w", seat.SeatID,
				errors.Join(ErrHerdrProofPersistence, err))
		}
	}
	// UpdateSeat composes independent metadata changes. Return the stored row,
	// not the pre-update candidate, and revalidate intent after moves as well.
	latest, found, err := db.Seat(ctx, seat.SeatID)
	if err != nil {
		return seat, false, fmt.Errorf("recheck proved seat %s: %w", seat.SeatID, err)
	}
	expected := next
	if observe {
		expected = seat
	}
	if !found || latest.SeatID != expected.SeatID ||
		latest.Engine != expected.Engine ||
		latest.Backend != expected.Backend ||
		latest.HerdrSocket != expected.HerdrSocket ||
		latest.HerdrPane != expected.HerdrPane ||
		latest.HerdrWorkspace != expected.HerdrWorkspace ||
		latest.HerdrTerminalID != expected.HerdrTerminalID ||
		latest.SeatHostPID != expected.SeatHostPID ||
		latest.SeatHostStart != expected.SeatHostStart ||
		latest.SeatHostBirth != expected.SeatHostBirth ||
		latest.EndPending != expected.EndPending {
		return seat, false, fmt.Errorf("seat %s ownership changed during proof", seat.SeatID)
	}
	if observe {
		latest.HerdrPane = next.HerdrPane
		latest.HerdrWorkspace = next.HerdrWorkspace
		latest.HerdrTerminalID = next.HerdrTerminalID
		latest.SeatHostPID = next.SeatHostPID
		latest.SeatHostStart = next.SeatHostStart
		latest.SeatHostBirth = next.SeatHostBirth
	}
	return latest, true, nil
}

type nativeProofPane struct {
	Pane      string `json:"pane_id"`
	Terminal  string `json:"terminal_id"`
	Workspace string `json:"workspace_id"`
}

// ResolveHerdrSeat proves and persists the current address using the jail-aware
// process reader. Callers must refuse mutation on either an error or !owned.
func ResolveHerdrSeat(
	ctx context.Context,
	values paths.Values,
	seat fleetdb.Seat,
) (result fleetdb.Seat, owned bool, returnErr error) {
	db := fleetdb.OpenSharedState(ctx, values)
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	return ProveHerdrSeat(ctx, db, NewProcFS(values.ProcRoot), seat)
}

// HerdrHostDead positively identifies the disappearance of the saved host, not
// the absence of foreground engine evidence. A reused PID is the old host's death.
func HerdrHostDead(proc ProcFS, seat fleetdb.Seat) (bool, error) {
	if seat.SeatHostPID <= 0 || seat.SeatHostStart == 0 {
		return false, fmt.Errorf("seat %s has no saved supervisor birth", seat.SeatID)
	}
	stat, err := proc.Stat(seat.SeatHostPID)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read seat %s supervisor birth: %w", seat.SeatID, err)
	}
	if stat.StartTime != seat.SeatHostStart {
		return true, nil
	}
	if seat.SeatHostBirth > 0 {
		birther, ok := proc.(ProcBirth)
		if !ok {
			return false, fmt.Errorf("seat %s supervisor wall birth unavailable", seat.SeatID)
		}
		birth, err := birther.Birth(seat.SeatHostPID)
		if err != nil {
			return false, fmt.Errorf("read supervisor wall birth: %w", err)
		}
		if birth != seat.SeatHostBirth {
			return true, nil
		}
	}
	return false, nil
}

// StampHerdrHost records the kernel birth of a supervisor at its trusted bind.
// It does not itself authorize a pane mutation.
func StampHerdrHost(proc ProcFS, seat *fleetdb.Seat, pid int) error {
	stat, err := proc.Stat(pid)
	if err != nil {
		return fmt.Errorf("stamp seat host %d: %w", pid, err)
	}
	if stat.StartTime == 0 {
		return fmt.Errorf("stamp seat host %d: missing kernel birth", pid)
	}
	seat.SeatHostPID = pid
	seat.SeatHostStart = stat.StartTime
	seat.SeatHostBirth = 0
	if birther, ok := proc.(ProcBirth); ok {
		birth, err := birther.Birth(pid)
		if err != nil {
			return fmt.Errorf("stamp seat host wall birth: %w", err)
		}
		seat.SeatHostBirth = birth
	}
	return nil
}

func exactSeatHost(argv []string, id string) bool {
	if len(argv) < 4 {
		return false
	}
	name := filepath.Base(argv[0])
	if name != "pfm" && name != "pfm.dev" {
		return false
	}
	args := argv[1:]
	if len(args) >= 2 && args[0] == "--config" {
		args = args[2:]
	}
	return len(args) == 3 && args[0] == "internal" && args[1] == "seat-host" && args[2] == id
}

func belowHerdrShell(proc ProcFS, pid, shell int) (bool, error) {
	seen := map[int]bool{}
	for pid > 0 && !seen[pid] {
		if pid == shell {
			return true, nil
		}
		seen[pid] = true
		stat, err := proc.Stat(pid)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read supervisor ancestry %d: %w", pid, err)
		}
		pid = stat.ParentPID
	}
	return false, nil
}

// ProbeHerdrSeats returns only proved pane data; unknown and dead seats remain
// visible diagnostics, never recycled occupant metadata. One listing per socket.
// A positively dead seat reads as lost (non-live); unknown evidence stays live
// with its error, so callers count it as a failure.
func ProbeHerdrSeats(ctx context.Context, values paths.Values, seats []fleetdb.Seat, readOnly bool) []HerdrSeat {
	open := fleetdb.OpenSharedState
	if readOnly {
		open = fleetdb.OpenSharedStateReadOnly
	}
	db := open(ctx, values)
	rows := proveHerdrSeats(ctx, db, NewProcFS(values.ProcRoot), probeHerdrSeatRows(ctx, seats), readOnly)
	if err := db.Close(); err != nil {
		for i := range rows {
			rows[i].Err = errors.Join(rows[i].Err, fmt.Errorf("close seat listing state: %w", err))
		}
	}
	return rows
}

func proveHerdrSeats(ctx context.Context, db *fleetdb.Store, proc ProcFS, rows []HerdrSeat, readOnly bool) []HerdrSeat {
	for i := range rows {
		row := &rows[i]
		if row.snapshot == nil {
			continue
		}
		canonical, owned, err := proveHerdrSeatSnapshot(ctx, db, proc, row.Seat, row.snapshot, readOnly)
		row.CWD = ""
		if err != nil {
			row.Err = fmt.Errorf("HERDR ERROR %s: %w", row.Seat.HerdrSocket, err)
			continue
		}
		if !owned {
			// Positive death only: the row stays visible as a lost seat for this
			// listing, never live and never the recycled pane's occupant. The
			// durable row is left to the restore path; a listing never retires it.
			row.Seat.State = "lost"
			row.Err = fmt.Errorf("HERDR DEAD %s: saved supervisor exited", row.Seat.SeatID)
			continue
		}
		row.Seat = canonical
		row.CWD = row.directories[canonical.HerdrPane]
		row.Err = nil
	}
	return rows
}

// Older native launches supervised in-process; their generated seat id is not argv.
func legacyHerdrLauncher(argv []string) bool {
	if len(argv) < 3 {
		return false
	}
	name := filepath.Base(argv[0])
	if name != "pfm" && name != "pfm.dev" {
		return false
	}
	args := argv[1:]
	if len(args) >= 2 && args[0] == "--config" {
		args = args[2:]
	}
	return len(args) >= 2 && args[0] == "internal" && args[1] == "launch"
}

// HerdrSeatRetired also handles pre-upgrade/never-bound rows. Missing process
// identity alone is unknown: require a complete absent listing, explicit alias
// refusal and no matching old supervisor anywhere in the process table.
func HerdrSeatRetired(ctx context.Context, proc ProcFS, seat fleetdb.Seat) (bool, error) {
	if seat.SeatHostPID > 0 {
		return HerdrHostDead(proc, seat)
	}
	fail := func(err error) (bool, error) {
		return false, fmt.Errorf("seat %s has no saved supervisor birth; absence unproved: %w", seat.SeatID, err)
	}
	body, err := herdr.SeatRequest(ctx, seat.HerdrSocket, "pane.list", struct{}{})
	if err != nil {
		return fail(err)
	}
	var list struct {
		Type  string            `json:"type"`
		Panes []nativeProofPane `json:"panes"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fail(err)
	}
	if list.Type != "pane_list" || list.Panes == nil {
		return fail(errors.New("invalid pane list"))
	}
	seen := map[string]bool{}
	for _, pane := range list.Panes {
		if pane.Pane == "" || pane.Terminal == "" || pane.Workspace == "" ||
			!strings.HasPrefix(pane.Pane, pane.Workspace+":") ||
			seen[pane.Pane] {
			return fail(errors.New("malformed pane identity"))
		}
		seen[pane.Pane] = true
		if pane.Pane == seat.HerdrPane || (seat.HerdrTerminalID != "" && pane.Terminal == seat.HerdrTerminalID) {
			return false, nil
		}
	}
	_, err = herdr.SeatRequest(ctx, seat.HerdrSocket, "pane.get", map[string]string{"pane_id": seat.HerdrPane})
	var refusal *herdr.Refusal
	if !errors.As(err, &refusal) || (refusal.Code != "pane_not_found" && refusal.Code != "not_found") {
		return fail(errors.Join(err, errors.New("old pane absence not explicitly acknowledged")))
	}
	return HerdrSupervisorAbsent(proc, seat)
}

// HerdrSupervisorAbsent proves only completed process absence, not pane ownership.
// Explicit restore additionally requires authority over its current caller pane.
func HerdrSupervisorAbsent(proc ProcFS, seat fleetdb.Seat) (bool, error) {
	pids, err := proc.PIDs()
	if err != nil {
		return false, fmt.Errorf("seat %s supervisor absence unproved: %w", seat.SeatID, err)
	}
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			// Protected argv is non-matching only with independent foreign-uid
			// evidence. Same-uid unreadability is still unknown, never absence.
			if identities, ok := proc.(ProcIdentity); ok {
				identity, identityErr := identities.ProcessIdentity(pid)
				if errors.Is(identityErr, fs.ErrNotExist) {
					continue
				}
				if identityErr == nil && identity.EffectiveUID != uint32(os.Geteuid()) {
					continue
				}
				err = errors.Join(err, identityErr)
			}
			return false, fmt.Errorf("seat %s supervisor absence unproved: %w", seat.SeatID, err)
		}
		if exactSeatHost(argv, seat.SeatID) {
			return false, nil
		}
		if !legacyHerdrLauncher(argv) {
			continue
		}
		env, err := proc.Environ(pid)
		if err != nil {
			return false, fmt.Errorf("seat %s supervisor absence unproved: %w", seat.SeatID, err)
		}
		if env[herdr.EnvSocket] == seat.HerdrSocket &&
			(env[herdr.EnvPane] == seat.HerdrPane || env[herdr.EnvPane] == "") {
			return false, nil
		}
	}
	return true, nil
}

// HerdrSupervisorArgv locates candidates only; ProveHerdrSeat supplies the authority.
func HerdrSupervisorArgv(argv []string, id string) bool {
	return exactSeatHost(argv, id) || legacyHerdrLauncher(argv)
}

// HerdrChatSeat is the one rule every door follows to find the Herdr pane a
// process's chat lives in: the durable native seat on socket whose saved
// supervisor is one of chain (the process and its ancestors, nearest first).
// A Herdr pane move gives the pane a new id and rewrites the seat, never the
// HERDR_PANE_ID the chat's processes inherited: the seat's HerdrPane and
// HerdrWorkspace are the chat's current address, envPane only the pane's birth
// id. A legacy seat (no saved supervisor; pfm never moves one) is found by
// envPane and the supervisor argv of an ancestor. An ancestor that exited is no
// supervisor; one whose argv cannot be read is an error, never "no seat".
func HerdrChatSeat(
	seats []fleetdb.Seat,
	proc ProcFS,
	socket, envPane string,
	chain []int,
) (fleetdb.Seat, bool, error) {
	for _, pid := range chain {
		for i := range seats {
			seat := &seats[i]
			if seat.Backend != nativeHerdrBackend || seat.HerdrSocket != socket {
				continue
			}
			if seat.SeatHostPID > 0 && seat.SeatHostPID == pid {
				return *seat, true, nil
			}
			if seat.SeatHostPID != 0 || seat.HerdrPane != envPane {
				continue
			}
			argv, err := proc.Cmdline(pid)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return fleetdb.Seat{}, false, fmt.Errorf("read legacy supervisor candidate %d: %w", pid, err)
			}
			// This only locates a legacy candidate; ProveHerdrSeat validates full argv.
			if HerdrSupervisorArgv(argv, seat.SeatID) {
				return *seat, true, nil
			}
		}
	}
	return fleetdb.Seat{}, false, nil
}

// HerdrChatPane is HerdrChatSeat over the seats of state, which may be nil or
// absent (no pfm state yet: no seat): the pane to address and whether a seat
// named it. With no seat it is envPane. A seat table that cannot be read is an
// error, never envPane.
func HerdrChatPane(
	ctx context.Context,
	state *fleetdb.Store,
	proc ProcFS,
	socket, envPane string,
	chain []int,
) (pane string, seat fleetdb.Seat, found bool, err error) {
	if state == nil || errors.Is(state.Degraded(), fleetdb.ErrAbsent) {
		return envPane, fleetdb.Seat{}, false, nil
	}
	if err := state.Degraded(); err != nil {
		return "", fleetdb.Seat{}, false, fmt.Errorf("read the chat's Herdr seat: %w", err)
	}
	seats, err := state.Seats(ctx)
	if err != nil {
		return "", fleetdb.Seat{}, false, fmt.Errorf("read the chat's Herdr seat: %w", err)
	}
	seat, found, err = HerdrChatSeat(seats, proc, socket, envPane, chain)
	if err != nil {
		return "", fleetdb.Seat{}, false, fmt.Errorf("find the chat's Herdr seat: %w", err)
	}
	if !found || seat.HerdrPane == "" {
		return envPane, seat, found, nil
	}
	return seat.HerdrPane, seat, true, nil
}
