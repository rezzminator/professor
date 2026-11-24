package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// The reader must not be what creates the state store: a missing database is a
// missing database, not an empty one.
func TestPrimaryAccountNeverCreatesTheDatabase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{
		StateDB: filepath.Join(root, "state", "pfm.db"),
		Home:    filepath.Join(root, "home"),
	}
	if account, found, err := ClaudePrimaryAccount(context.Background(), values); found || err != nil {
		t.Fatalf("PrimaryAccount() = %d, %v, %v, want not found, nil", account, found, err)
	}
	if _, err := os.Stat(values.StateDB); !os.IsNotExist(err) {
		t.Fatalf("reading the primary account created %s: %v", values.StateDB, err)
	}
}

func TestSetPrimaryAccountKeepsDatabaseAndMirrorInLockstep(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{
		Home:    root,
		StateDB: filepath.Join(root, ".local", "state", "pfm", "pfm.db"),
	}
	if err := SetClaudePrimaryAccount(context.Background(), values, 2, 123); err != nil {
		t.Fatal(err)
	}
	if account, found, err := ClaudePrimaryAccount(context.Background(), values); !found || account != 2 || err != nil {
		t.Fatalf("PrimaryAccount()=%d,%v,%v, want 2,true,nil", account, found, err)
	}
	content, err := os.ReadFile(filepath.Join(root, ".claude-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "2\n" {
		t.Fatalf("mirror=%q, want 2\\n", content)
	}
}

// A database another pfm is still creating holds no primary account yet:
// every state its creation passes through reads as nothing set, never as an
// error, and the finished database with a primary set returns it.
func TestClaudePrimaryAccountReadsADatabaseBeingCreatedAsNothingSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		create  func(t *testing.T, values paths.Values)
		account int
		found   bool
	}{
		{name: "file made, no table yet", create: databaseAt("")},
		{
			name:   "tables made, meta not yet",
			create: databaseAt("CREATE TABLE hidden(uuid TEXT PRIMARY KEY, hidden_at INTEGER NOT NULL)"),
		},
		{name: "schema made, migrations pending", create: databaseAt(schemaDDL)},
		{
			name: "creation finished, primary set",
			create: func(t *testing.T, values paths.Values) {
				t.Helper()
				state := OpenSharedState(context.Background(), values)
				if err := state.Degraded(); err != nil {
					t.Fatalf("OpenSharedState(%s) degraded: %v", values.StateDB, err)
				}
				if err := state.SetMeta(context.Background(), PrimaryAccountKey, "3", 99); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
			},
			account: 3,
			found:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			values := paths.Values{StateDB: filepath.Join(root, "state", "pfm.db"), Home: filepath.Join(root, "home")}
			tc.create(t, values)
			account, found, err := ClaudePrimaryAccount(context.Background(), values)
			if account != tc.account || found != tc.found || err != nil {
				t.Fatalf("ClaudePrimaryAccount(%s) = %d, %v, %v; want %d, %v, nil",
					values.StateDB, account, found, err, tc.account, tc.found)
			}
		})
	}
}

// databaseAt leaves a state database where a creation that has run ddl stops:
// opened as every pfm opens it, user_version still 0.
func databaseAt(ddl string) func(t *testing.T, values paths.Values) {
	return func(t *testing.T, values paths.Values) {
		t.Helper()
		db, err := sqlitedb.OpenStore(context.Background(), values.StateDB)
		if err != nil {
			t.Fatalf("open %s: %v", values.StateDB, err)
		}
		if ddl != "" {
			if _, err := db.Exec(ddl); err != nil {
				t.Fatalf("create %s: %v", values.StateDB, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
