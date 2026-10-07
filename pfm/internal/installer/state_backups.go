package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// stateBackupMaxAge is how long a backup or archive pfm set aside under its
// state dir is kept: pfm install removes it once it is older.
const stateBackupMaxAge = 30 * 24 * time.Hour

// stateStampLength is the length of the stamp every stamping writer below
// uses, the layout "20060102-150405".
const stateStampLength = len("20060102-150405")

// stateRemoveAll removes one expired state path; a test swaps it to fail.
var stateRemoveAll = os.RemoveAll

// StateBackup is one backup or archive entry pfm set aside under its state
// dir, with the time it was made.
type StateBackup struct {
	// Check is the pfm doctor --stale row the entry is listed under.
	Check string
	Path  string
	// Made is when the backup was made: its writer's stamp where the name
	// carries one, else its modification time (written by a copy).
	Made time.Time
	// Err is a failed look at Path; Made is then zero.
	Err error
	// DoctorAside marks pfm doctor's own move-aside dirs: --stale never lists
	// them, since --purge would move its backup dir into itself.
	DoctorAside bool
}

// stateBackupKind is one place a writer sets backups aside: the entries of dir
// whose names start with prefix, each aged by made, which reports false for a
// name its writer never gives (that entry is not pfm's and stays).
type stateBackupKind struct {
	check, dir, prefix string
	made               func(name string, info fs.FileInfo) (time.Time, bool)
	doctorAside        bool
}

// madeByMTime ages an entry its writer copied (a fresh mtime) or whose writer
// is gone (nothing makes one today).
func madeByMTime(_ string, info fs.FileInfo) (time.Time, bool) { return info.ModTime(), true }

// madeByStamp ages an entry by the stamp its writer put in its name right
// after marker ("" for a name that starts with it), read in the writer's zone.
func madeByStamp(marker string, zone *time.Location) func(string, fs.FileInfo) (time.Time, bool) {
	return func(name string, _ fs.FileInfo) (time.Time, bool) {
		_, rest, found := strings.Cut(name, marker)
		if !found || len(rest) < stateStampLength {
			return time.Time{}, false
		}
		made, err := time.ParseInLocation("20060102-150405", rest[:stateStampLength], zone)
		return made, err == nil
	}
}

// stateBackupKinds is the registry of every writer's backups, each cited to
// the code that writes it. pfm doctor --stale lists the same entries
// (hostcheck/retired_paths.go), so the two never disagree on what is retired.
func stateBackupKinds(home, stateDB, cacheDB string) []stateBackupKind {
	state := filepath.Join(home, ".local", "state", "pfm")
	var kinds []stateBackupKind
	// fleetdb.BackupBeforeMigration copies {db}.bak-before-v{N}[.n].
	for _, db := range []string{stateDB, cacheDB} {
		if db != "" {
			kinds = append(kinds, stateBackupKind{
				check: "migration-backup", dir: filepath.Dir(db), prefix: filepath.Base(db) + ".bak-before-v",
				made: madeByMTime,
			})
		}
	}
	return append(kinds,
		// The retired fleet.db's backups and an older pfm's chat-skill
		// retirement temp dir: their writers are gone.
		stateBackupKind{check: "retired-fleet-db", dir: state, prefix: "fleet.db.bak-", made: madeByMTime},
		stateBackupKind{check: "retired-chat-skills", dir: state, prefix: "retired-chat-skills.", made: madeByMTime},
		// archiveRetiredStoreEntry renames (keeping the old mtime) and
		// retireLegacyCommand copies, both named by availableBackup with the
		// install's local stamp.
		stateBackupKind{
			check: "retired-archive", dir: RetiredStoreArchive(home),
			made: madeByStamp(".pre-professor-", time.Local),
		},
		stateBackupKind{
			check: "retired-archive", dir: filepath.Join(state, "retired-commands"),
			made: madeByStamp(".pre-professor-", time.Local),
		},
		// doctor --stale --purge's createStaleBackupDir (local stamp) and
		// hostcheck's strayAsideDir (UTC stamp).
		stateBackupKind{
			check: "stale-backup", dir: filepath.Join(state, "stale-backup"),
			made: madeByStamp("", time.Local), doctorAside: true,
		},
		stateBackupKind{
			check: "stray-claude-state", dir: filepath.Join(state, "stray-claude-state"),
			made: madeByStamp("", time.UTC), doctorAside: true,
		},
	)
}

// StateBackups lists every backup and archive entry pfm set aside, each with
// the time it was made; a failed look is an entry carrying its error.
func StateBackups(home, stateDB, cacheDB string) []StateBackup {
	var backups []StateBackup
	for _, kind := range stateBackupKinds(home, stateDB, cacheDB) {
		entries, err := os.ReadDir(kind.dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			backups = append(backups, StateBackup{Check: kind.check, Path: kind.dir, Err: err})
			continue
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), kind.prefix) {
				continue
			}
			path := filepath.Join(kind.dir, entry.Name())
			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				backups = append(backups, StateBackup{Check: kind.check, Path: path, Err: err})
				continue
			}
			made, ok := kind.made(entry.Name(), info)
			if !ok {
				continue
			}
			backups = append(backups, StateBackup{
				Check: kind.check, Path: path, Made: made, DoctorAside: kind.doctorAside,
			})
		}
	}
	return backups
}

// expireStateBackups removes each backup made more than stateBackupMaxAge
// ago, naming what it freed; a backup inside its window is never touched.
func (installer *engine) expireStateBackups() {
	_, cacheDB, err := pfmconfig.StatePathsFrom(installer.env(), installer.options.Home)
	if err != nil {
		installer.warnStateExpiry("the cache database's backups", fmt.Errorf("resolve the cache database: %w", err))
	}
	now := installer.now()
	for _, backup := range StateBackups(installer.options.Home, installer.options.StateDB, cacheDB) {
		if backup.Err != nil {
			installer.warnStateExpiry(backup.Path, backup.Err)
			continue
		}
		if now.Sub(backup.Made) <= stateBackupMaxAge {
			continue
		}
		installer.removeStatePath(backup.Path, "expired backup", "made "+backup.Made.Format("2006-01-02")+", ")
	}
}

// removeStatePath removes one state path pfm wrote that has outlived its use
// and names the bytes it freed; a dry run names the bytes it would free. A
// failure is one warn line and an obs event, never a failed pfm install, and
// the path stays for the next run.
func (installer *engine) removeStatePath(path, what, detail string) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		installer.warnStateExpiry(path, err)
		return
	}
	size, err := stateBytes(path, info)
	if err != nil {
		installer.warnStateExpiry(path, err)
		return
	}
	if !installer.apply {
		_ = installer.change(fmt.Sprintf("remove %s %s (%sfrees %d bytes)", what, path, detail, size), nil)
		return
	}
	if err := stateRemoveAll(path); err != nil {
		installer.warnStateExpiry(path, err)
		return
	}
	_ = installer.change(fmt.Sprintf("remove %s %s (%sfreed %d bytes)", what, path, detail, size), nil)
}

// stateBytes sums the regular files under path, never following a link.
func stateBytes(path string, info fs.FileInfo) (int64, error) {
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return info.Size(), nil
		}
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", path, err)
	}
	return total, nil
}

func (installer *engine) warnStateExpiry(path string, err error) {
	installer.say("  warn    %s: %v", path, err)
	ctx := obs.Component(context.Background(), comp)
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelWarn, "installer.state_expiry",
		slog.String("path", path), slog.String(obs.FieldErr, err.Error()))
}
