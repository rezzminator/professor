package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// ClaudePrimaryAccount reports the database value first, then the
// ~/.claude-primary mirror when the database has none, and not-found when
// neither parses. The
// roster membership is the CALLER's to apply from the effective machine
// configuration; this package deliberately knows only that ids are positive.
//
// It opens the database read-only and never creates it: reading the primary
// account must not be the act that brings a state store into existence.
//
// The three-value return is value, found, failed-to-look: a database that
// cannot be read (a corrupt file, a permission error, a real query failure —
// distinct from sql.ErrNoRows, which is a legitimate "nothing set yet") is
// reported as an error, never folded into found=false. A caller that only
// checked found used to read "the lookup itself failed" as "no primary
// account", which then quietly answered every query with the roster's first
// configured account instead of surfacing the outage.
func ClaudePrimaryAccount(ctx context.Context, values paths.Values) (int, bool, error) {
	account, found, err := primaryFromDatabase(ctx, values.StateDB)
	if err != nil {
		return 0, false, err
	}
	if found {
		return account, true, nil
	}
	content, err := os.ReadFile(filepath.Join(values.Home, ".claude-primary"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read primary account mirror: %w", err)
	}
	account, err = strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0, false, fmt.Errorf("parse primary account mirror: %w", err)
	}
	return account, true, nil
}

// SetClaudePrimaryAccount updates the authoritative shared row and its statusline
// mirror. The database is written first; a mirror failure is returned loudly
// so a caller never reports a primary switch that only half landed.
func SetClaudePrimaryAccount(
	ctx context.Context,
	values paths.Values,
	account int,
	updatedAt int64,
) (returnErr error) {
	if account < 1 {
		return fmt.Errorf("primary account must be positive, got %d", account)
	}
	state := OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close shared state: %w", err))
		}
	}()
	// An unopenable database degrades to the mirror below.
	if err := state.SetMeta(
		ctx,
		PrimaryAccountKey,
		strconv.Itoa(account),
		updatedAt,
	); err != nil {
		return err
	}

	path := filepath.Join(values.Home, ".claude-primary")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create primary mirror directory: %w", err)
	}
	if err := atomicfile.Write(path, []byte(strconv.Itoa(account)+"\n"), 0o600); err != nil {
		return fmt.Errorf("install primary mirror: %w", err)
	}
	return nil
}

func primaryFromDatabase(ctx context.Context, path string) (int, bool, error) {
	// A stat failure of ANY kind (missing, or a path component that is not a
	// directory) keeps its original degrade-to-mirror answer: the mirror file
	// exists precisely so a whole-store outage never blocks reading who the
	// primary account is (TestPrimaryAccountGoesThroughTheStateStore). The
	// distinction item 4 exists for is narrower and starts once the database
	// is confirmed present: an open or query failure from THERE is this
	// lookup failing to look, never folded into "nothing recorded".
	if _, err := os.Stat(path); err != nil {
		return 0, false, nil
	}
	// Read-only, matching the doc above: this reader must never need write
	// access to a database it does not own, on a path that runs on nearly
	// every ls/statusline.
	db, err := sqlitedb.OpenReadOnly(path, sqlitedb.StoreBusyTimeout)
	if err != nil {
		return 0, false, fmt.Errorf("open shared fleet database %s: %w", path, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			obs.Logger(ctx).Warn("fleet.primary.close", "path", path, obs.FieldErr, err.Error())
		}
	}()
	var value string
	const primaryQuery = "SELECT val FROM meta WHERE key=?"
	// Absence is a successful statement: sql.ErrNoRows means nothing is set
	// yet. Every other scan error is this lookup failing to look and reaches
	// both the activity record and the caller.
	read := obs.SQL(ctx, kind, primaryQuery)
	err = db.QueryRowContext(ctx, primaryQuery, PrimaryAccountKey).Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// A database another process has created and not yet given its schema
		// holds no table at all, so it records no primary account yet.
		const tablesQuery = "SELECT count(*) FROM sqlite_master WHERE type='table'"
		tables := obs.SQL(ctx, kind, tablesQuery)
		var count int
		countErr := db.QueryRowContext(ctx, tablesQuery).Scan(&count)
		tables.End(-1, countErr)
		if countErr == nil && count == 0 {
			err = sql.ErrNoRows
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		read.End(-1, nil)
	} else {
		read.End(-1, err)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("query primary account: %w", err)
	}
	account, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false, fmt.Errorf("parse primary account value %q: %w", value, err)
	}
	return account, true, nil
}
