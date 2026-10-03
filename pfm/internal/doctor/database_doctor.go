package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// printDatabaseDoctor prints the database rows from doctor's read-only handles
// (store.OpenWithoutMigrating): schema and integrity, the shared store, the row
// census, the WAL and the busy counters. A database at another schema version
// prints one warning naming it and both versions, and every row that needs it
// says it could not look; a database not yet created says so and is no
// warning, since the first pfm command that needs it creates it. aborted
// reports a row that could not run at all, with doctor's exit code.
func printDatabaseDoctor(
	ctx context.Context,
	stdout io.Writer,
	database *store.Store,
	openErr error,
	values paths.Values,
	tally *doctorTally,
) (code int, aborted bool) {
	if database == nil {
		unread := printUnreadCache(stdout, openErr, values.CacheDB, tally)
		state := fleetdb.OpenSharedStateReadOnly(ctx, values)
		printSharedStoreDoctor(stdout, state.Path(), state.Degraded(), tally)
		if err := state.Close(); err != nil {
			tally.warn()
			fmt.Fprintf(stdout, "doctor: warning close state db=%s error=%v\n", values.StateDB, err)
		}
		if unread != "" {
			fmt.Fprintf(stdout, "doctor: rows %s\n", unread)
			fmt.Fprintf(stdout, "doctor: busy_warnings %s\n", unread)
		} else if text := sharedUnread(state.Degraded()); text != "" {
			fmt.Fprintf(stdout, "doctor: rows %s\n", text)
		}
		return 0, false
	}

	version, err := database.UserVersion(ctx)
	if err != nil {
		return tally.abort(stdout, "doctor: unhealthy user_version: %v\n", err), true
	}
	check, err := database.QuickCheck(ctx)
	if err != nil {
		return tally.abort(stdout, "doctor: unhealthy integrity: %v\n", err), true
	}
	if version != store.SchemaVersion || check != "ok" {
		tally.warn()
	}
	fmt.Fprintf(
		stdout,
		"doctor: database user_version=%d expected=%d quick_check=%s\n",
		version,
		store.SchemaVersion,
		check,
	)

	// Kills live in the fleet's shared database, not this binary's cache.
	shared := database.SharedDegraded()
	printSharedStoreDoctor(stdout, database.SharedPath(), shared, tally)
	if text := sharedUnread(shared); text != "" {
		fmt.Fprintf(stdout, "doctor: rows %s\n", text)
	} else if code, aborted := printRowCensus(ctx, stdout, database, tally); aborted {
		return code, true
	}

	walBytes := int64(0)
	if info, err := os.Stat(database.Path() + "-wal"); err == nil {
		walBytes = info.Size()
	} else if !os.IsNotExist(err) {
		tally.warn()
		fmt.Fprintf(stdout, "doctor: warning WAL stat: %v\n", err)
	}
	fmt.Fprintf(stdout, "doctor: wal_bytes=%d\n", walBytes)

	killWarnings, err := metaCounter(ctx, database, "busy_kill_warnings")
	if err != nil {
		return tally.abort(stdout, "doctor: unhealthy busy counter: %v\n", err), true
	}
	unkillWarnings, err := metaCounter(ctx, database, "busy_unkill_warnings")
	if err != nil {
		return tally.abort(stdout, "doctor: unhealthy busy counter: %v\n", err), true
	}
	if killWarnings != 0 || unkillWarnings != 0 {
		tally.warn()
	}
	fmt.Fprintf(
		stdout,
		"doctor: busy_warnings kill=%d unkill=%d\n",
		killWarnings,
		unkillWarnings,
	)
	return 0, false
}

// printRowCensus prints the table sizes and the orphaned kills it counts.
func printRowCensus(ctx context.Context, stdout io.Writer, database *store.Store, tally *doctorTally) (int, bool) {
	counts, err := database.Counts(ctx)
	if err != nil {
		return tally.abort(stdout, "doctor: unhealthy row counts: %v\n", err), true
	}
	fmt.Fprintf(
		stdout,
		"doctor: rows transcripts=%d rollouts=%d cx_names=%d killed=%d orphaned_killed=%d\n",
		counts.Transcripts,
		counts.Rollouts,
		counts.CxNames,
		counts.Killed,
		counts.OrphanedKills,
	)
	// The census row above counts orphans; it never calls one a defect. A
	// warning nobody can read is the same as no warning at all — worse, it
	// inflates `doctor: warnings=N` past every line the reader can point at —
	// so the counted state names itself here.
	if counts.OrphanedKills != 0 {
		tally.warn()
		fmt.Fprintf(
			stdout,
			"doctor: warning orphaned_killed=%d kills whose chat resolves to no transcript, rollout, or OpenCode session\n",
			counts.OrphanedKills,
		)
		fmt.Fprintln(
			stdout,
			"doctor: remediation: list them with `pfm archive --prune-orphans`, then delete them with "+
				"`pfm archive --prune-orphans --yes` (a deleted kill does not come back)",
		)
	}
	return 0, false
}

// printUnreadCache prints the row for a cache database doctor did not read
// and returns what each row that needs it prints instead, or "" for a cache
// not yet created, which has nothing to report. A legacy cache waiting for
// its move is the legacy-cache-db host check's BLOCK, which carries the fix;
// any other open error is a failure doctor names and reads past.
func printUnreadCache(stdout io.Writer, openErr error, path string, tally *doctorTally) string {
	var mismatch *fleetdb.SchemaMismatchError
	switch {
	case errors.As(openErr, &mismatch):
		printSchemaWarning(stdout, mismatch, tally)
		return mismatch.Error()
	case errors.Is(openErr, fleetdb.ErrAbsent):
		fmt.Fprintf(stdout, "doctor: database %s absent — the first pfm command that needs it creates it\n", path)
		return ""
	case errors.Is(openErr, paths.ErrLegacyPending):
		tally.warn()
		fmt.Fprintf(
			stdout,
			"doctor: database %s could not look: a legacy cache database waits for its move — "+
				"apply the host-check legacy-cache-db fix\n",
			path,
		)
		return "could not look: cache database " + path + " not created yet"
	default:
		tally.fail()
		fmt.Fprintf(stdout, "doctor: unhealthy database: %v\n", openErr)
		return "could not look: " + openErr.Error()
	}
}

// sharedUnread is what the row census prints for a shared store doctor did not
// read — kills live there, so the census cannot count without it — or "" for
// one read, or not yet created, which holds no kills.
func sharedUnread(degraded error) string {
	var mismatch *fleetdb.SchemaMismatchError
	switch {
	case degraded == nil, errors.Is(degraded, fleetdb.ErrAbsent):
		return ""
	case errors.As(degraded, &mismatch):
		return mismatch.Error()
	default:
		return "could not look: " + degraded.Error()
	}
}

// printSharedStoreDoctor prints the shared store row from its read-only open.
func printSharedStoreDoctor(stdout io.Writer, path string, degraded error, tally *doctorTally) {
	var mismatch *fleetdb.SchemaMismatchError
	switch {
	case degraded == nil:
		fmt.Fprintf(stdout, "doctor: shared store=%s state=ok\n", path)
	case errors.Is(degraded, fleetdb.ErrAbsent):
		fmt.Fprintf(
			stdout,
			"doctor: shared store=%s state=absent — the first pfm command that records state creates it\n",
			path,
		)
	case errors.As(degraded, &mismatch):
		printSchemaWarning(stdout, mismatch, tally)
	case errors.Is(degraded, paths.ErrLegacyPending):
		tally.warn()
		fmt.Fprintf(
			stdout,
			"doctor: shared store=%s could not look: a legacy state database waits for its move — "+
				"apply the host-check legacy-state-db fix\n",
			path,
		)
	default:
		tally.fail()
		fmt.Fprintf(stdout, "doctor: unhealthy database: shared store=%s state=%s\n", path, degraded.Error())
	}
}

// printSchemaWarning is the one warning for a database doctor did not read
// because this build would migrate it, or cannot read what a newer pfm wrote.
func printSchemaWarning(stdout io.Writer, mismatch *fleetdb.SchemaMismatchError, tally *doctorTally) {
	tally.warn()
	if mismatch.Newer() {
		fmt.Fprintf(
			stdout,
			"doctor: warning %s database %s schema v%d is newer than this build's v%d — a newer pfm wrote it; "+
				"this pfm cannot use it until that newer pfm is installed again (pfm install --yes from its build)\n",
			mismatch.Name, mismatch.Path, mismatch.Found, mismatch.Expected,
		)
		return
	}
	fmt.Fprintf(
		stdout,
		"doctor: warning %s database %s schema v%d, this build expects v%d — doctor left it as found; "+
			"once this build is installed (pfm install --yes), the first pfm command that opens it "+
			"migrates it and keeps a backup\n",
		mismatch.Name, mismatch.Path, mismatch.Found, mismatch.Expected,
	)
}

// printCodexPaneDoctor audits the Codex pane bindings when the cache was
// read; the bindings live in it, so an unread cache says why it has none.
func printCodexPaneDoctor(
	ctx context.Context, stdout io.Writer, database *store.Store, openErr error, runtime config.Runtime,
) int {
	if database != nil {
		return PrintCodexPaneBinding(ctx, stdout, database, runtime)
	}
	var mismatch *fleetdb.SchemaMismatchError
	switch {
	case errors.As(openErr, &mismatch):
		fmt.Fprintf(stdout, "doctor: codex_pane_bindings %v\n", mismatch)
	case errors.Is(openErr, fleetdb.ErrAbsent):
		fmt.Fprintln(stdout, "doctor: codex_pane_bindings total=0 (cache database absent)")
	default:
		fmt.Fprintf(stdout, "doctor: codex_pane_bindings could not look: %v\n", openErr)
	}
	return 0
}
