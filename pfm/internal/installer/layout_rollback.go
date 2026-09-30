package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RollbackLayout replays journal id backwards. Every check reads before any
// write: an already rolled-back journal, a database holder and a live chat on
// a session-store account refuse whatever force says; a destination changed
// since the install refuses unless force. A successful rollback keeps the
// journal and marks it {dir}/rolled-back.
func RollbackLayout(ctx context.Context, env LayoutEnv, id string, force bool, stdout io.Writer) (err error) {
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	if !layoutJournalID.MatchString(id) {
		return fmt.Errorf("unknown layout journal %q in %s", id, root)
	}
	dir := filepath.Join(root, id)
	raw, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unknown layout journal %q in %s", id, root)
	}
	if err != nil {
		return err
	}
	var records []layoutJournalRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return fmt.Errorf("decode layout journal %s: %w", dir, err)
	}
	marker := filepath.Join(dir, layoutRolledBackMarker)
	if stamp, err := os.ReadFile(marker); err == nil {
		return fmt.Errorf("rollback %s refused: already rolled back at %s", id, strings.TrimSpace(string(stamp)))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read rollback marker %s: %w", marker, err)
	}
	scope, err := readLayoutJournalScope(dir, id)
	if err != nil {
		return err
	}
	env = scope.apply(env)
	if err := layoutRollbackLiveChats(env, id, records); err != nil {
		return err
	}
	if !force {
		drifted, err := layoutRollbackDrift(env, dir, records)
		if err != nil {
			return err
		}
		if len(drifted) > 0 {
			return fmt.Errorf("rollback %s refused: drift at %s — rerun with --force to overwrite them",
				id, strings.Join(drifted, ", "))
		}
	}
	// A database replay needs the fleet units down; a config replay may
	// restore a legacy config the running units' binary refuses, so it stops
	// them too (a config moves at the state-db row, journaled even when that
	// row then refuses).
	stopFleet := false
	for _, record := range records {
		if record.Result != layoutRecordRestored &&
			(record.Row == layoutRowStateDB || record.Row == layoutRowCacheDB || record.Row == layoutRowConfig) {
			stopFleet = true
			break
		}
	}
	var stopped []string
	unitsReload := false
	var legacy string
	var legacyErr error
	defer func() {
		err = layoutRollbackRestart(ctx, env, rollbackRestart{
			id: id, stopped: stopped, legacy: legacy, legacyErr: legacyErr, daemonReload: unitsReload,
		}, err, stdout)
	}()
	if stopFleet {
		var stopErr error
		stopped, stopErr = stopLayoutServices(ctx, env)
		if stopErr != nil {
			return fmt.Errorf("rollback %s: stop fleet units: %w", id, stopErr)
		}
		for _, record := range records {
			if record.Result == layoutRecordRestored ||
				(record.Row != layoutRowStateDB && record.Row != layoutRowCacheDB) {
				continue
			}
			for _, path := range []string{record.Destination, env.StateDB, env.CacheDB} {
				pids, scanErr := dbHolderPIDs(
					env.ProcRoot,
					strings.TrimSuffix(strings.TrimSuffix(path, layoutDBWAL), layoutDBSHM),
				)
				if scanErr != nil {
					return scanErr
				}
				if len(pids) > 0 {
					return fmt.Errorf("rollback %s refused: database held by pid %s", id, strings.Join(pids, ","))
				}
			}
		}
	}
	var failures []error
	unitsRestored := false
	createdStores := layoutCreatedSessionStores(env, records)
	keptStores := map[string]bool{}
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if record.Result == layoutRecordRestored {
			continue
		}
		if !layoutRecordSafe(env, dir, record) {
			failures = append(failures, fmt.Errorf("record %d has unsafe path", index))
			continue
		}
		if destination := filepath.Clean(record.Destination); createdStores[destination] {
			// Judged once, before any of its records is replayed: a later
			// record's restore would otherwise empty the store the earlier
			// one is judged by.
			holdsData, judged := keptStores[destination]
			if !judged {
				var inspectErr error
				holdsData, inspectErr = layoutStoreHoldsData(record.Destination)
				if inspectErr != nil {
					// Unjudged is kept: no later record of it replays either.
					keptStores[destination] = true
					failures = append(
						failures,
						fmt.Errorf("record %d inspect session store %s: %w", index, record.Destination, inspectErr),
					)
					continue
				}
				keptStores[destination] = holdsData
				if holdsData {
					fmt.Fprintf(
						stdout,
						"  keep    session store %s holds data written since the install; left in place\n",
						record.Destination,
					)
				}
			}
			if holdsData {
				continue
			}
		}
		if err := restoreLayoutRecord(ctx, record); err != nil {
			failures = append(failures, fmt.Errorf("record %d restore %s: %w", index, record.Destination, err))
			continue
		}
		fmt.Fprintf(stdout, "  rollback layout %s %s\n", record.Row, record.Destination)
		for _, home := range layoutHomes(env) {
			units := filepath.Join(home, ".config", "systemd", "user")
			unitsRestored = unitsRestored || pathWithin(record.Destination, units)
		}
	}
	// The replay may have put back a legacy config this pfm refuses: then no
	// unit restarts on it, and the next step names the order.
	legacy, legacyErr = pfmconfig.LegacyConfigWaiting(env.LegacyConfigDir, env.ConfigPath)
	unitsReload = unitsRestored
	if unitsRestored && (legacy == "" || len(failures) != 0) {
		fmt.Fprintln(stdout, systemdDaemonReloadNote)
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	stamp := clock.Real.Now().UTC().Format(time.RFC3339) + "\n"
	if err := atomicfile.Write(marker, []byte(stamp), 0o600); err != nil {
		return fmt.Errorf("mark journal %s rolled back: %w", dir, err)
	}
	return nil
}

// rollbackRestart is what the deferred restart of RollbackLayout knows.
type rollbackRestart struct {
	id           string
	stopped      []string
	legacy       string
	legacyErr    error
	daemonReload bool
}

// layoutRollbackRestart ends a rollback: with no legacy config waiting it
// restarts and verifies the units the rollback stopped. With a legacy config
// waiting, this pfm would refuse it, so nothing restarts: a replay that
// succeeded prints the ordered next step, one that failed says the units stay
// stopped.
func layoutRollbackRestart(
	ctx context.Context,
	env LayoutEnv,
	state rollbackRestart,
	err error,
	stdout io.Writer,
) error {
	if state.legacyErr != nil {
		return errors.Join(err, fmt.Errorf("rollback %s: fleet units left stopped: %w", state.id, state.legacyErr))
	}
	if state.legacy == "" {
		return errors.Join(err, restartLayoutServices(ctx, env, state.stopped))
	}
	units := "none"
	if len(state.stopped) != 0 {
		units = strings.Join(state.stopped, " ")
	}
	if err != nil {
		return errors.Join(err, fmt.Errorf(
			"fleet units left stopped: %s — this pfm refuses the restored legacy config", units))
	}
	fmt.Fprintf(stdout, "  next    this pfm refuses the restored legacy config %s; fleet units left stopped: %s\n",
		state.legacy, units)
	// daemon-reload comes first: make rollback's mcp-restart restarts
	// pfm-mcp.service, which must run the restored unit file, not the one
	// systemd still holds in memory.
	var steps []string
	if state.daemonReload {
		steps = append(steps, "systemctl --user daemon-reload")
	}
	if env.invocation == nil || env.invocation.Get(paths.EnvUpdateInstall) != "1" {
		clone := env.Clone
		if clone == "" {
			clone = "<clone>"
		}
		steps = append(steps, fmt.Sprintf("make -C %s rollback", filepath.Join(clone, "pfm")))
	}
	if schedulerIsLaunchd {
		for _, label := range state.stopped {
			steps = append(steps,
				fmt.Sprintf("launchctl bootstrap gui/%d %s", os.Getuid(), launchAgentPlist(env, label)))
		}
	} else if len(state.stopped) != 0 {
		steps = append(steps, "systemctl --user start "+units)
	}
	for index, step := range steps {
		fmt.Fprintf(stdout, "  next    %d. %s\n", index+1, step)
	}
	return nil
}
