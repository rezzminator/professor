package hostcheck

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The retired-path registry: every host path an earlier pfm wrote that the
// current pfm no longer uses, each kind cited to the code that wrote it. A
// stale path is a WARN row; a retired path whose successor is absent still
// holds the operator's data, so it is a BLOCK naming its move; a failed look
// is the UNREADABLE BLOCK. pfm doctor --stale prints the rows and --purge
// moves every WARN path into one backup dir.

const (
	checkLegacyConfigDir = "legacy-config-dir"
	checkInstallBackup   = "install-backup"
	checkDeadAccountDir  = "dead-account-dir"
	// checkEngineSyncOldConfig is the old machine config engine-sync now
	// refuses to start beside (installer/engine_sync_config.go retires the
	// files pfm owns).
	checkEngineSyncOldConfig = "engine-sync-old-config"
	// checkRetiredGeneratedStore is a home store engine-sync once rendered
	// agents into (installer.RetiredGeneratedStores, the one copy of the paths).
	checkRetiredGeneratedStore = "retired-generated-store"
	engineSyncConfigFile       = "config.json"
	// installBackupMarker is the name availableBackup gives every copy pfm
	// install sets aside (installer/files.go).
	installBackupMarker = ".pre-professor-"
	staleFix            = "pfm doctor --stale --purge moves it into one backup dir"
)

// RetiredPathDetectors is the registry; one entry per kind.
func RetiredPathDetectors() []Detector {
	return []Detector{
		{checkLegacyConfigDir, staleLegacyConfigDir},
		{checkLegacyStateDB, func(env Env) ([]Row, error) {
			return staleLegacyDB(env, checkLegacyStateDB, paths.LegacyStateDB(env.Home), env.StateDB)
		}},
		{checkLegacyCacheDB, func(env Env) ([]Row, error) {
			return staleLegacyDB(env, checkLegacyCacheDB, paths.LegacyCacheDB(env.Home), env.CacheDB)
		}},
		{checkStagedPrompts, func(env Env) ([]Row, error) {
			return stalePresent(
				checkStagedPrompts,
				"retired staged prompt dir",
				filepath.Join(env.ManagedRoot, "harness-prompts"),
			), nil
		}},
		{checkSharedDB, func(env Env) ([]Row, error) {
			return stalePresent(
				checkSharedDB,
				"retired shared.db",
				filepath.Join(env.Home, ".local", "state", "pfm", "shared.db"),
			), nil
		}},
		{checkInstallBackup, staleInstallBackups},
		{"state-backup", staleStateBackups},
		{"retired-migration-journal", func(env Env) ([]Row, error) {
			return stalePresent(
				"retired-migration-journal",
				"the deleted journaled migration's journal",
				installer.RetiredMigrationJournal(env.Home),
			), nil
		}},
		{"dead-registry-link", staleDeadRegistryLinks},
		{checkDeadAccountDir, staleDeadAccountDirs},
		{checkEngineSyncOldConfig, staleEngineSyncOldConfig},
		{checkRetiredGeneratedStore, func(env Env) ([]Row, error) {
			return stalePresent(
				checkRetiredGeneratedStore,
				"home store engine-sync no longer renders agents into",
				installer.RetiredGeneratedStores(env.Home)...,
			), nil
		}},
	}
}

// RetiredPaths walks the registry.
func RetiredPaths(env Env) []Row { return runDetectors(env, RetiredPathDetectors()) }

func staleRow(check, path, problem string) Row {
	return Row{Warn, check, path, problem, staleFix}
}

func heldRow(check, path, successor, fix string) Row {
	return Row{Block, check, path, "retired, but awaits its move to " + successor, fix}
}

func stalePresent(check, problem string, candidates ...string) []Row {
	var rows []Row
	for _, path := range candidates {
		if inspectPath(&rows, check, path) != nil {
			rows = append(rows, staleRow(check, path, problem))
		}
	}
	return rows
}

// staleLegacyConfigDir: pfm config init wrote pfm.config.json and config.json
// here until the config moved into the clone.
func staleLegacyConfigDir(env Env) ([]Row, error) {
	var rows []Row
	dir := env.LegacyConfigDir
	if inspectPath(&rows, checkLegacyConfigDir, dir) == nil ||
		paths.PhysicalPath(dir) == paths.PhysicalPath(filepath.Dir(env.ConfigPath)) {
		return rows, nil
	}
	waiting, err := config.LegacyConfigWaiting(dir, env.ConfigPath)
	if err != nil {
		return append(rows, unreadable(checkLegacyConfigDir, dir, err)), nil
	}
	if waiting != "" {
		return append(rows, heldRow(checkLegacyConfigDir, dir, env.ConfigPath, "mv "+waiting+" "+env.ConfigPath)), nil
	}
	return append(rows, staleRow(checkLegacyConfigDir, dir, "pre-clone pfm config dir")), nil
}

func staleLegacyDB(_ Env, check, legacy, target string) ([]Row, error) {
	var rows []Row
	if paths.PhysicalPath(legacy) == paths.PhysicalPath(target) {
		return rows, nil
	}
	var present []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if inspectPath(&rows, check, legacy+suffix) != nil {
			present = append(present, legacy+suffix)
		}
	}
	if len(present) == 0 || len(rows) != 0 {
		return rows, nil
	}
	if inspectPath(&rows, check, target) == nil {
		if len(rows) != 0 {
			return rows, nil
		}
		return append(rows, heldRow(check, legacy, target, "close every chat, stop pfm's services, then: mv "+
			legacy+" "+target)), nil
	}
	for _, path := range present {
		same, err := samePhysicalFile(target, path)
		if err != nil {
			rows = append(rows, unreadable(check, path, err))
			continue
		}
		if !same {
			rows = append(rows, staleRow(check, path, "pre-layout database beside "+target))
		}
	}
	return rows, nil
}

// staleEngineSyncOldConfig: engine-sync read {config}/engine-sync/config.json
// and targets.json until it moved its machine config into
// ~/.claude/engine-sync.json, and now exits 2 beside either. pfm install
// removes the files it owns; one still here is the operator's, a BLOCK while
// the global file is absent (its keys await the move) and a WARN once the
// file exists.
func staleEngineSyncOldConfig(env Env) ([]Row, error) {
	var rows []Row
	successor := enginesync.GlobalConfigPath(env.Home)
	successorAbsent := inspectPath(&rows, checkEngineSyncOldConfig, successor) == nil
	values := map[string]string{}
	for _, entry := range env.Environ {
		if key, value, found := strings.Cut(entry, "="); found && key == "XDG_CONFIG_HOME" {
			values[key] = value
		}
	}
	for _, dir := range enginesync.OldConfigDirs(&paths.MapEnv{Values: values}, env.Home) {
		for _, name := range []string{engineSyncConfigFile, "targets.json"} {
			path := filepath.Join(dir, name)
			if inspectPath(&rows, checkEngineSyncOldConfig, path) == nil {
				continue
			}
			if successorAbsent {
				rows = append(rows, heldRow(checkEngineSyncOldConfig, path, successor,
					"run pfm install --yes, which writes "+successor+" and retires the old file when it is pfm's; "+
						"else move its keys into "+successor+" (engine-sync exits 2 while "+path+" exists and "+
						successor+" is absent)"))
				continue
			}
			rows = append(rows, staleRow(checkEngineSyncOldConfig, path,
				"old engine-sync machine config, no longer read while "+successor+" exists"))
		}
	}
	return rows, nil
}

// staleInstallBackups: every copy pfm install set aside beside a file it
// replaced — Claude dirs, ~/.zshrc, the launchd plists, the systemd units.
func staleInstallBackups(env Env) ([]Row, error) {
	var rows []Row
	dirs := append(claudeDirs(env),
		env.Home,
		filepath.Join(env.Home, "Library", "LaunchAgents"),
		filepath.Join(env.Home, ".config", "systemd", "user"),
	)
	for _, dir := range uniquePaths(dirs) {
		for _, entry := range readDir(&rows, checkInstallBackup, dir) {
			if original, _, found := strings.Cut(entry.Name(), installBackupMarker); found {
				rows = append(rows, staleRow(
					checkInstallBackup,
					filepath.Join(dir, entry.Name()),
					"pfm install's backup of "+original,
				))
			}
		}
	}
	return rows, nil
}

// stateBackupProblems names each kind installer.StateBackups lists.
var stateBackupProblems = map[string]string{
	"migration-backup":    "pre-migration database copy",
	"retired-fleet-db":    "backup of the retired fleet.db",
	"retired-chat-skills": "an older pfm's chat-skill retirement dir",
	"retired-archive":     "pfm install's archive of a retired file",
}

// staleStateBackups lists the backups pfm install expires after 30 days
// (installer.StateBackups, the one registry both read), whatever their age,
// except doctor's own move-aside dirs: --purge cannot move a dir into itself,
// and install never expires them (doctor promised they are never deleted).
func staleStateBackups(env Env) ([]Row, error) {
	var rows []Row
	for _, backup := range installer.StateBackups(env.Home, env.StateDB, env.CacheDB) {
		switch {
		case backup.Err != nil:
			rows = append(rows, unreadable(backup.Check, backup.Path, backup.Err))
		case !backup.DoctorAside:
			rows = append(rows, staleRow(backup.Check, backup.Path, stateBackupProblems[backup.Check]))
		}
	}
	return rows, nil
}

// staleDeadRegistryLinks lists the DEAD class of install's registry-ownership
// scan; STORE and ORPHAN links are doctor's failure rows and install retires them.
func staleDeadRegistryLinks(env Env) ([]Row, error) {
	result, err := installer.ScanRegistryOwnership(env.Home, env.Store, installer.RetiredRegistryNames{})
	if err != nil {
		return nil, fmt.Errorf("inspect dead registry links: %w", err)
	}
	var rows []Row
	for _, link := range result.Findings {
		if link.Class == installer.RegistryDead {
			rows = append(rows, staleRow("dead-registry-link", link.Path, "pfm link to a target gone: "+link.Target))
		}
	}
	return rows, nil
}

// staleDeadAccountDirs: pfm install creates {home}/.cc/{N} for each configured
// account; one whose account left the config is dead.
func staleDeadAccountDirs(env Env) ([]Row, error) {
	var rows []Row
	root := filepath.Dir(config.DefaultAccountDir(env.Home, 1))
	if len(env.Accounts) == 0 {
		return append(rows, Row{
			Block, checkDeadAccountDir, root,
			"no account configured: cannot tell a live account dir from a dead one",
			"fix the config's accounts, then rerun",
		}), nil
	}
	live := map[string]bool{}
	for _, account := range env.Accounts {
		live[paths.PhysicalPath(account.ConfigDir)] = true
	}
	for _, entry := range readDir(&rows, checkDeadAccountDir, root) {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if !live[paths.PhysicalPath(path)] {
			rows = append(rows, staleRow(checkDeadAccountDir, path, "account dir no account in the config names"))
		}
	}
	return rows, nil
}
