package fleetdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// Seat is the durable address of one interactive engine.
type Seat struct {
	SeatID         string
	SessionID      string
	Engine         string
	Backend        string
	TmuxSocket     string
	TmuxPane       string
	HerdrSocket    string
	HerdrPane      string
	HerdrWorkspace string
	Name           string
	State          string
	ReloadPending  bool
	LostNote       string
	UpdatedAt      int64
}

const seatColumns = `seat_id,session_id,engine,backend,tmux_socket,tmux_pane,herdr_socket,herdr_pane,herdr_workspace,name,state,reload_pending,lost_note,updated_at`

// PutSeat reserves or updates a seat. A failed database is never an absent seat.
func (s *Store) PutSeat(ctx context.Context, seat Seat) error {
	if seat.SeatID == "" {
		return fmt.Errorf("write seat: empty seat id")
	}
	if s.db == nil {
		return fmt.Errorf("write seat %q: %w", seat.SeatID, s.degraded)
	}
	args := append(seatValues(seat), seat.Backend, seat.State, seat.HerdrSocket, seat.HerdrPane,
		seat.HerdrSocket, seat.HerdrPane, seat.SeatID)
	result, err := s.exec(ctx, `INSERT INTO seat (`+seatColumns+`) SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?
 WHERE ?!='herdr' OR ?!='live' OR ?='' OR ?='' OR NOT EXISTS (
 SELECT 1 FROM seat WHERE backend='herdr' AND state='live' AND herdr_socket=? AND herdr_pane=? AND seat_id<>?)
 ON CONFLICT(seat_id) DO UPDATE SET session_id=excluded.session_id,engine=excluded.engine,backend=excluded.backend,
 tmux_socket=excluded.tmux_socket,tmux_pane=excluded.tmux_pane,herdr_socket=excluded.herdr_socket,
 herdr_pane=excluded.herdr_pane,herdr_workspace=excluded.herdr_workspace,name=excluded.name,state=excluded.state,
 reload_pending=excluded.reload_pending,lost_note=excluded.lost_note,updated_at=excluded.updated_at`,
		args...)
	if err != nil {
		return fmt.Errorf("write seat %q: %w", seat.SeatID, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count written seat %q: %w", seat.SeatID, err)
	}
	if count != 1 {
		return fmt.Errorf("write seat %q: Herdr pane is already owned", seat.SeatID)
	}
	return nil
}

// UpdateSeat changes only fields owned by this mutation and never recreates a deleted seat.
func (s *Store) UpdateSeat(ctx context.Context, before, after Seat) error {
	if before.SeatID != after.SeatID || after.SeatID == "" {
		return fmt.Errorf("update seat: immutable seat id changed")
	}
	if s.db == nil {
		return fmt.Errorf("update seat %q: %w", after.SeatID, s.degraded)
	}
	old, next := seatValues(before), seatValues(after)
	columns := strings.Split(seatColumns, ",")
	var assignments []string
	var args []any
	for i := 1; i < len(columns); i++ {
		if old[i] != next[i] {
			assignments = append(assignments, columns[i]+"=?")
			args = append(args, next[i])
		}
	}
	if len(assignments) == 0 {
		return nil
	}
	args = append(args, after.SeatID)
	// Unchanged fields must come from the stored row: before may predate a bind.
	candidate := func(column string) string {
		for i, name := range columns {
			if name == column && old[i] != next[i] {
				args = append(args, next[i])
				return "?"
			}
		}
		return "current." + column
	}
	guard := candidate("backend") + "!='herdr' OR " + candidate("state") + "!='live' OR " +
		candidate("herdr_socket") + "='' OR " + candidate("herdr_pane") + "='' OR NOT EXISTS (" +
		"SELECT 1 FROM seat AS owner WHERE owner.backend='herdr' AND owner.state='live' AND owner.herdr_socket=" +
		candidate(
			"herdr_socket",
		) + " AND owner.herdr_pane=" + candidate("herdr_pane") + " AND owner.seat_id<>current.seat_id)"
	result, err := s.exec(
		ctx,
		"UPDATE seat AS current SET "+strings.Join(assignments, ",")+" WHERE seat_id=? AND ("+guard+")",
		args...)
	if err != nil {
		return fmt.Errorf("update seat %q: %w", after.SeatID, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated seat %q: %w", after.SeatID, err)
	}
	if count != 1 {
		return fmt.Errorf("update seat %q: seat no longer exists or Herdr pane is already owned", after.SeatID)
	}
	return nil
}

func seatValues(seat Seat) []any {
	return []any{
		seat.SeatID, seat.SessionID, seat.Engine, seat.Backend, seat.TmuxSocket, seat.TmuxPane,
		seat.HerdrSocket, seat.HerdrPane, seat.HerdrWorkspace, seat.Name, seat.State, seat.ReloadPending,
		seat.LostNote, seat.UpdatedAt,
	}
}

// Seat finds a seat by its immutable id.
func (s *Store) Seat(ctx context.Context, id string) (Seat, bool, error) {
	return s.findSeat(ctx, "seat_id=?", id)
}

// SeatBySession finds the most recently bound seat of a conversation.
func (s *Store) SeatBySession(ctx context.Context, id string) (Seat, bool, error) {
	if id == "" {
		return Seat{}, false, nil
	}
	return s.findSeat(ctx, "session_id=?", id)
}

// SeatByPane verifies the socket and pane together, never the pane id alone.
func (s *Store) SeatByPane(ctx context.Context, socket, pane string) (seat Seat, found bool, returnErr error) {
	if socket == "" || pane == "" {
		return Seat{}, false, nil
	}
	if s.db == nil {
		return Seat{}, false, fmt.Errorf("read pane owner: %w", s.degraded)
	}
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+seatColumns+" FROM seat WHERE backend='herdr' AND state='live' AND herdr_socket=? AND herdr_pane=? LIMIT 2",
		socket,
		pane,
	)
	if err != nil {
		return Seat{}, false, fmt.Errorf("read pane owner: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close pane owners: %w", err))
		}
	}()
	for rows.Next() {
		if found {
			return Seat{}, false, fmt.Errorf("herdr pane %s at %s has multiple live seats", pane, socket)
		}
		seat, err = scanSeat(rows)
		if err != nil {
			return Seat{}, false, fmt.Errorf("read pane owner: %w", err)
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return Seat{}, false, fmt.Errorf("enumerate pane owners: %w", err)
	}
	return seat, found, nil
}

func (s *Store) findSeat(ctx context.Context, where string, args ...any) (Seat, bool, error) {
	if s.db == nil {
		return Seat{}, false, fmt.Errorf("read seat: %w", s.degraded)
	}
	seat, err := scanSeat(
		s.db.QueryRowContext(
			ctx,
			"SELECT "+seatColumns+" FROM seat WHERE "+where+" ORDER BY updated_at DESC,seat_id LIMIT 1",
			args...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Seat{}, false, nil
	}
	if err != nil {
		return Seat{}, false, fmt.Errorf("read seat: %w", err)
	}
	return seat, true, nil
}

// Seats enumerates the persisted seats in stable order.
func (s *Store) Seats(ctx context.Context) (seats []Seat, returnErr error) {
	if s.db == nil {
		return nil, fmt.Errorf("list seats: %w", s.degraded)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+seatColumns+" FROM seat ORDER BY seat_id")
	if err != nil {
		return nil, fmt.Errorf("list seats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close seats: %w", err))
		}
	}()
	for rows.Next() {
		seat, err := scanSeat(rows)
		if err != nil {
			return nil, fmt.Errorf("read seat row: %w", err)
		}
		seats = append(seats, seat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("enumerate seats: %w", err)
	}
	return seats, nil
}

// DeleteSeat removes a seat's address; its private run file is removed separately.
func (s *Store) DeleteSeat(ctx context.Context, id string) error {
	if s.db == nil {
		return fmt.Errorf("delete seat %q: %w", id, s.degraded)
	}
	if _, err := s.exec(ctx, "DELETE FROM seat WHERE seat_id=?", id); err != nil {
		return fmt.Errorf("delete seat %q: %w", id, err)
	}
	return nil
}

func scanSeat(row interface{ Scan(...any) error }) (Seat, error) {
	var seat Seat
	err := row.Scan(
		&seat.SeatID,
		&seat.SessionID,
		&seat.Engine,
		&seat.Backend,
		&seat.TmuxSocket,
		&seat.TmuxPane,
		&seat.HerdrSocket,
		&seat.HerdrPane,
		&seat.HerdrWorkspace,
		&seat.Name,
		&seat.State,
		&seat.ReloadPending,
		&seat.LostNote,
		&seat.UpdatedAt,
	)
	return seat, err
}

// SeatRun is the private launch recipe read by seat-host, not inherited shell state.
type SeatRun struct {
	CostLedger bool     `json:"cost_ledger"`
	Binary     string   `json:"binary"`
	Args       []string `json:"args"`
	Env        []string `json:"env"`
	CWD        string   `json:"cwd"`
}

var seatIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Store) seatRunPath(id string) (string, error) {
	if !seatIDPattern.MatchString(id) {
		return "", fmt.Errorf("seat run: invalid seat id %q", id)
	}
	return filepath.Join(filepath.Dir(s.path), "seats", id+".json"), nil
}

// WriteSeatRun atomically stores a launch recipe with private permissions.
func (s *Store) WriteSeatRun(id string, run SeatRun) error {
	path, err := s.seatRunPath(id)
	if err != nil {
		return err
	}
	if run.Binary == "" {
		return fmt.Errorf("write seat run %q: empty binary", id)
	}
	body, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode seat run %q: %w", id, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create seat run directory: %w", err)
	}
	if err := atomicfile.Write(path, body, 0o600); err != nil {
		return fmt.Errorf("write seat run %q: %w", id, err)
	}
	return nil
}

// ReadSeatRun returns a launch recipe; missing and malformed recipes are errors.
func (s *Store) ReadSeatRun(id string) (SeatRun, error) {
	path, err := s.seatRunPath(id)
	if err != nil {
		return SeatRun{}, err
	}
	body, err := readSeatRecipe(path)
	if err != nil {
		return SeatRun{}, fmt.Errorf("read seat run %q: %w", id, err)
	}
	var run SeatRun
	if err := json.Unmarshal(body, &run); err != nil {
		return run, fmt.Errorf("decode seat run %q: %w", id, err)
	}
	if run.Binary == "" {
		return run, fmt.Errorf("seat run %q: empty binary", id)
	}
	return run, nil
}

// DeleteSeatRun removes an ended seat's private launch recipe.
func (s *Store) DeleteSeatRun(id string) error {
	path, err := s.seatRunPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete seat run %q: %w", id, err)
	}
	return nil
}

func readSeatRecipe(path string) (body []byte, returnErr error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open regular seat recipe: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat seat recipe: %w", err)
	}
	const maxRecipeBytes = 1 << 20
	if !info.Mode().IsRegular() || info.Size() > maxRecipeBytes {
		return nil, fmt.Errorf("seat recipe is not a regular file within %d bytes", maxRecipeBytes)
	}
	body, err = io.ReadAll(io.LimitReader(file, maxRecipeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded seat recipe: %w", err)
	}
	if len(body) > maxRecipeBytes {
		return nil, fmt.Errorf("seat recipe exceeds %d bytes", maxRecipeBytes)
	}
	return body, nil
}

// ReadSeats enumerates durable native addresses without initializing or migrating state.
func ReadSeats(ctx context.Context, values paths.Values) (seats []Seat, returnErr error) {
	if _, err := os.Stat(values.StateDB); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat durable seat database: %w", err)
	}
	db, err := sqlitedb.OpenReadOnly(values.StateDB, sqlitedb.StoreBusyTimeout)
	if err != nil {
		return nil, fmt.Errorf("open read-only durable seats: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close durable seat reader: %w", err))
		}
	}()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, fmt.Errorf("read durable seat schema: %w", err)
	}
	if version > SchemaVersion {
		return nil, fmt.Errorf("durable seat schema %d is newer than binary %d", version, SchemaVersion)
	}
	if version < 6 {
		return nil, nil
	}
	return (&Store{db: db, path: values.StateDB}).Seats(ctx)
}
