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
	// One statement reads, from one snapshot, whether meta exists and the
	// schema version. A database another pfm is still creating stays at
	// user_version 0 until its migration stamps it, and until its schema
	// statement has run it may lack meta: it records no primary account yet.
	// Two separate looks (the meta query failing, then a table count) could
	// straddle that creation and read a database being made as a broken one.
	// pfm never drops meta, so once this look finds it the query below can
	// fail only for a real reason; a migrated database without meta is broken
	// and its query failure is returned.
	const schemaQuery = "SELECT (SELECT count(*) FROM sqlite_master WHERE type='table' AND name='meta')," +
		" (SELECT user_version FROM pragma_user_version)"
	schema := obs.SQL(ctx, kind, schemaQuery)
	var metaTables, version int
	err = db.QueryRowContext(ctx, schemaQuery).Scan(&metaTables, &version)
	schema.End(-1, err)
	if err != nil {
		return 0, false, fmt.Errorf("read shared fleet database %s schema: %w", path, err)
	}
	if metaTables == 0 && version == 0 {
		return 0, false, nil
	}
	var value string
	const primaryQuery = "SELECT val FROM meta WHERE key=?"
	// Absence is a successful statement: sql.ErrNoRows means nothing is set
	// yet. Every other scan error is this lookup failing to look and reaches
	// both the activity record and the caller.
	read := obs.SQL(ctx, kind, primaryQuery)
	err = db.QueryRowContext(ctx, primaryQuery, PrimaryAccountKey).Scan(&value)
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
