package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// PrimaryAccount reads the database first and the ~/.claude-primary mirror
// only when the database has no row.
func TestPrimaryAccountPrefersTheDatabaseOverTheMirror(t *testing.T) {
	t.Parallel()
	state, values := openFileTestStore(t)
	ctx := context.Background()

	if account, found, err := ClaudePrimaryAccount(ctx, values); found || err != nil {
		t.Fatalf("PrimaryAccount() with nothing recorded = %d, %v, %v", account, found, err)
	}

	mirror := filepath.Join(values.Home, ".claude-primary")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mirror, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if account, found, err := ClaudePrimaryAccount(ctx, values); !found || account != 1 || err != nil {
		t.Fatalf("PrimaryAccount() from the mirror = %d, %v, %v", account, found, err)
	}

	if err := state.SetMeta(ctx, PrimaryAccountKey, "2", 99); err != nil {
		t.Fatal(err)
	}
	if account, found, err := ClaudePrimaryAccount(ctx, values); !found || account != 2 || err != nil {
		t.Fatalf(
			"PrimaryAccount() = %d, %v, %v; want 2,true,nil — the database outranks a stale mirror",
			account,
			found,
			err,
		)
	}
}

// TestClaudePrimaryAccountSurfacesAQueryFailureNotNotFound is item 4's
// regression: a database that IS present and opens fine but whose query
// fails for a reason other than sql.ErrNoRows (here, a corrupted schema —
// the meta table itself is gone) used to fold into found=false the same way
// "nothing recorded yet" does, so a caller checking only found silently
// treated a broken shared store as an operator who never set a primary
// account, and answered every query with the roster's first configured
// account instead of surfacing the outage.
func TestClaudePrimaryAccountSurfacesAQueryFailureNotNotFound(t *testing.T) {
	t.Parallel()
	state, values := openFileTestStore(t)
	ctx := context.Background()
	if err := state.SetMeta(ctx, PrimaryAccountKey, "2", 99); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, "DROP TABLE meta"); err != nil {
		t.Fatalf("corrupt fixture schema: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	account, found, err := ClaudePrimaryAccount(ctx, values)
	if err == nil {
		t.Fatalf("ClaudePrimaryAccount() over a broken schema returned no error (account=%d, found=%v)", account, found)
	}
	if found {
		t.Fatalf("ClaudePrimaryAccount() reported found=true alongside an error: %v", err)
	}
}
