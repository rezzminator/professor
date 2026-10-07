package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

type Launch struct {
	SessionID  string
	Engine     pfmengine.ID
	Account    int
	Cache1H    bool
	LaunchedAt int64
	UpdatedAt  int64
}

var ErrNoLaunch = errors.New("no launch record")

type Launches struct{ db *sql.DB }

// RecordLaunch is the only launch writer. The first record creates and migrates
// shared state; later records preserve the first launch timestamp.
func RecordLaunch(ctx context.Context, values paths.Values, launch Launch, at int64) (returnErr error) {
	state := OpenSharedState(ctx, values)
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close launch database: %w", err))
		}
	}()
	if err := state.Degraded(); err != nil {
		return fmt.Errorf("record launch %s: %w", launch.SessionID, err)
	}
	cache1h := 0
	if launch.Cache1H {
		cache1h = 1
	}
	_, err := state.exec(ctx, `
INSERT INTO launch(session_id,engine,account,cache1h,launched_at,updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET
  engine=excluded.engine,
  account=excluded.account,
  cache1h=excluded.cache1h,
  updated_at=excluded.updated_at`,
		launch.SessionID, string(launch.Engine), launch.Account, cache1h, at, at)
	if err != nil {
		return fmt.Errorf("record launch %s: %w", launch.SessionID, err)
	}
	return nil
}

// OpenLaunches opens the existing state file read-only, without migrating it.
func OpenLaunches(_ context.Context, values paths.Values) (*Launches, error) {
	if _, err := os.Stat(values.StateDB); errors.Is(err, os.ErrNotExist) {
		return &Launches{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect launch database %s: %w", values.StateDB, err)
	}
	db, err := sqlitedb.OpenReadOnly(values.StateDB, sqlitedb.StoreBusyTimeout)
	if err != nil {
		return nil, fmt.Errorf("open launch database %s: %w", values.StateDB, err)
	}
	return &Launches{db: db}, nil
}

// LaunchFor is the only launch reader. Missing rows and pre-v2 files share the
// same absence result; corrupt files and other query failures remain errors.
func (l *Launches) LaunchFor(ctx context.Context, sessionID string) (Launch, error) {
	if l.db == nil {
		return Launch{}, ErrNoLaunch
	}
	var launch Launch
	var engine string
	var cache1h int
	err := l.db.QueryRowContext(ctx, `
SELECT session_id,engine,account,cache1h,launched_at,updated_at
FROM launch WHERE session_id=?`, sessionID).Scan(
		&launch.SessionID, &engine, &launch.Account, &cache1h,
		&launch.LaunchedAt, &launch.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Launch{}, ErrNoLaunch
	}
	if err != nil && strings.Contains(err.Error(), "no such table: launch") {
		return Launch{}, ErrNoLaunch
	}
	if err != nil {
		return Launch{}, fmt.Errorf("read launch %s: %w", sessionID, err)
	}
	launch.Engine = pfmengine.ID(engine)
	launch.Cache1H = cache1h == 1
	return launch, nil
}

func (l *Launches) Close() error {
	if l.db == nil {
		return nil
	}
	return l.db.Close()
}
