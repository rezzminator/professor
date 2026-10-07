package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRecordLaunchFirst(t *testing.T) {
	t.Parallel()
	_, values := openTestStore(t)
	ctx := context.Background()
	first := Launch{SessionID: "S", Engine: pfmengine.ID("cc"), Account: 2, Cache1H: true}
	if err := RecordLaunch(ctx, values, first, 100); err != nil {
		t.Fatal(err)
	}
	launches, err := OpenLaunches(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	got, err := launches.LaunchFor(ctx, "S")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Launch{SessionID: "S", Engine: pfmengine.ID("cc"), Account: 2, Cache1H: true, LaunchedAt: 100, UpdatedAt: 100}) {
		t.Fatalf("first launch=%+v", got)
	}
	if err := launches.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordLaunchAgain(t *testing.T) {
	t.Parallel()
	_, values := openTestStore(t)
	ctx := context.Background()
	first := Launch{SessionID: "S", Engine: pfmengine.ID("cc"), Account: 2, Cache1H: true}
	if err := RecordLaunch(ctx, values, first, 100); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Account, second.Cache1H = 3, false
	if err := RecordLaunch(ctx, values, second, 200); err != nil {
		t.Fatal(err)
	}
	launches, err := OpenLaunches(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := launches.Close(); err != nil {
			t.Error(err)
		}
	}()
	got, err := launches.LaunchFor(ctx, "S")
	if err != nil {
		t.Fatal(err)
	}
	if got != (Launch{SessionID: "S", Engine: pfmengine.ID("cc"), Account: 3, Cache1H: false, LaunchedAt: 100, UpdatedAt: 200}) {
		t.Fatalf("updated launch=%+v", got)
	}
}

func TestLaunchForHit(t *testing.T) {
	t.Parallel()
	_, values := openTestStore(t)
	ctx := context.Background()
	if err := RecordLaunch(
		ctx,
		values,
		Launch{SessionID: "hit", Engine: pfmengine.ID("cc"), Account: 2},
		100,
	); err != nil {
		t.Fatal(err)
	}
	l, err := OpenLaunches(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	got, err := l.LaunchFor(ctx, "hit")
	if err != nil || got.SessionID != "hit" || got.Account != 2 {
		t.Fatalf("LaunchFor(hit)=%+v,%v", got, err)
	}
}

func TestRecordLaunchCreatesState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "state", "pfm.db")}
	ctx := context.Background()
	if err := RecordLaunch(
		ctx,
		values,
		Launch{SessionID: "created", Engine: pfmengine.ID("cc"), Account: 2},
		10,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(values.StateDB); err != nil {
		t.Fatal(err)
	}
}

func TestRecordLaunchWriteErrorIncludesSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{}
	ctx := context.Background()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	values.StateDB = filepath.Join(blocker, "pfm.db")
	if err := RecordLaunch(
		ctx,
		values,
		Launch{SessionID: "failure"},
		11,
	); err == nil ||
		!strings.Contains(err.Error(), "failure") {
		t.Fatalf("RecordLaunch error=%v, want session ID", err)
	}
}

func TestLaunchForMiss(t *testing.T) {
	t.Parallel()
	_, values := openTestStore(t)
	l, err := OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := l.LaunchFor(context.Background(), "unknown"); !errors.Is(err, ErrNoLaunch) {
		t.Fatalf("miss=%v", err)
	}
}

func TestLaunchForAbsentFileDoesNotCreate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "state", "pfm.db")}
	l, err := OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := l.LaunchFor(context.Background(), "S"); !errors.Is(err, ErrNoLaunch) {
		t.Fatalf("miss=%v", err)
	}
	if _, err := os.Stat(values.StateDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reader created file: %v", err)
	}
}

func TestLaunchForV1DoesNotMigrate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
	db, err := sql.Open("sqlite", values.StateDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := l.LaunchFor(context.Background(), "S"); !errors.Is(err, ErrNoLaunch) {
		t.Fatalf("v1 miss=%v", err)
	}
	db, err = sql.Open("sqlite", values.StateDB)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("reader changed version=%d,%v", version, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), values)
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(values.StateDB + ".bak-before-v2"); err != nil {
		t.Fatalf("reader migrated v1: %v", err)
	}
}

func TestLaunchForBrokenFileReturnsError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{StateDB: filepath.Join(root, "pfm.db")}
	if err := os.WriteFile(values.StateDB, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := l.LaunchFor(context.Background(), "S"); err == nil || errors.Is(err, ErrNoLaunch) {
		t.Fatalf("broken query=%v", err)
	}
}

// legacyStateHome plants {home}/.cc/fleet.db and returns values whose StateDB
// sits in a directory that does not exist yet.
func legacyStateHome(t *testing.T) paths.Values {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	legacy := paths.LegacyStateDB(home)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	return paths.Values{Home: home, StateDB: paths.DefaultStateDB(home)}
}

func assertLegacyPending(t *testing.T, err error, values paths.Values) {
	t.Helper()
	if !errors.Is(err, paths.ErrLegacyPending) {
		t.Fatalf("error = %v, want paths.ErrLegacyPending", err)
	}
	for _, want := range []string{values.StateDB, paths.LegacyStateDB(values.Home), "run pfm doctor for the fix"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q lacks %q", err, want)
		}
	}
	if _, statErr := os.Lstat(filepath.Dir(values.StateDB)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state directory %s was created (stat err %v)", filepath.Dir(values.StateDB), statErr)
	}
}

func TestOpenSharedStateRefusesCreateWhileLegacyWaits(t *testing.T) {
	t.Parallel()
	values := legacyStateHome(t)
	ctx := context.Background()
	state := OpenSharedState(ctx, values)
	assertLegacyPending(t, state.Degraded(), values)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	err := RecordLaunch(ctx, values, Launch{SessionID: "S", Engine: pfmengine.ID("cc"), Account: 1}, 1)
	assertLegacyPending(t, err, values)
	assertLegacyPending(t, SetClaudePrimaryAccount(ctx, values, 2, 1), values)
}

func TestOpenSharedStateOpensExistingTargetBesideLegacy(t *testing.T) {
	t.Parallel()
	values := legacyStateHome(t)
	ctx := context.Background()
	if err := RecordLaunch(ctx, paths.Values{StateDB: values.StateDB}, Launch{
		SessionID: "S", Engine: pfmengine.ID("cc"), Account: 1,
	}, 1); err != nil {
		t.Fatal(err)
	}
	if err := RecordLaunch(ctx, values, Launch{SessionID: "T", Engine: pfmengine.ID("cc"), Account: 1}, 2); err != nil {
		t.Fatalf("existing target beside legacy: %v", err)
	}
}

func TestOpenSharedStateCreatesInFreshHome(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	values := paths.Values{Home: home, StateDB: paths.DefaultStateDB(home)}
	if err := SetClaudePrimaryAccount(context.Background(), values, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(values.StateDB); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSharedStateLegacyUnreadableIsAnError(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// {home}/.cc as a regular file: the legacy stat fails with ENOTDIR.
	if err := os.WriteFile(filepath.Join(home, ".cc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	values := paths.Values{Home: home, StateDB: paths.DefaultStateDB(home)}
	err := OpenSharedState(context.Background(), values).Degraded()
	if err == nil || !strings.Contains(err.Error(), paths.LegacyStateDB(home)) {
		t.Fatalf("Degraded() = %v, want an error naming %s", err, paths.LegacyStateDB(home))
	}
	if _, statErr := os.Lstat(values.StateDB); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state database created despite an unreadable legacy path (stat err %v)", statErr)
	}
}
