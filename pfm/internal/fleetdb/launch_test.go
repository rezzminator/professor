package fleetdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
