package hostcheck

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
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
		{checkLegacyHarvesterCache, staleHarvesterCache},
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
		{"migration-backup", staleMigrationBackups},
		{"retired-archive", func(env Env) ([]Row, error) {
			return stalePresent(
				"retired-archive",
				"pfm install's archive of retired files",
				installer.RetiredStoreArchive(env.Home),
				filepath.Join(env.Home, ".local", "state", "pfm", "retired-commands"),
			), nil
		}},
		{"dead-registry-link", staleDeadRegistryLinks},
		{checkDeadAccountDir, staleDeadAccountDirs},
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

// staleLegacyConfigDir: pfm config init wrote pfm.config.json, config.json
// and harvester.config.json here until the config moved into the clone.
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
	harvester := filepath.Join(dir, config.HarvesterFileName)
	target := filepath.Join(filepath.Dir(env.ConfigPath), config.HarvesterFileName)
	before := len(rows)
	if inspectPath(&rows, checkLegacyConfigDir, harvester) != nil &&
		inspectPath(&rows, checkLegacyConfigDir, target) == nil && len(rows) == before {
		return append(rows, heldRow(checkLegacyConfigDir, dir, target, "mv "+harvester+" "+target)), nil
	}
	if len(rows) != before {
		return rows, nil
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

func staleHarvesterCache(env Env) ([]Row, error) {
	var rows []Row
	const check = checkLegacyHarvesterCache
	path, target := paths.LegacyHarvesterCacheDir(env.Home), paths.HarvesterCacheDir(env.Home)
	if env.HarvesterCacheDir != "" && paths.PhysicalPath(env.HarvesterCacheDir) == paths.PhysicalPath(path) {
		return rows, nil
	}
	if inspectPath(&rows, check, path) == nil {
		return rows, nil
	}
	if env.HarvesterCacheDir == "" && inspectPath(&rows, check, target) == nil {
		if len(rows) != 0 {
			return rows, nil
		}
		return append(rows, heldRow(check, path, target, "mv "+path+" "+target)), nil
	}
	return append(rows, staleRow(check, path, "pre-rename harvester cache dir")), nil
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

// staleMigrationBackups: fleetdb's pre-migration copy {db}.bak-before-v{N}.
func staleMigrationBackups(env Env) ([]Row, error) {
	var rows []Row
	for _, db := range uniquePaths([]string{env.StateDB, env.CacheDB}) {
		matches, err := filepath.Glob(db + ".bak-before-v*")
		if err != nil {
			return rows, fmt.Errorf("glob migration backups of %s: %w", db, err)
		}
		for _, path := range matches {
			rows = append(rows, staleRow("migration-backup", path, "pre-migration database copy"))
		}
	}
	return rows, nil
}

// staleDeadRegistryLinks reuses install's dead-link rule with the repo set
// doctor's registry check passes it.
func staleDeadRegistryLinks(env Env) ([]Row, error) {
	repo, err := installer.GlobalSourceRepo(env.Home)
	if err != nil {
		return nil, fmt.Errorf("resolve the global source repo for dead registry links: %w", err)
	}
	repos := []string{repo}
	if fallback := filepath.Join(env.Home, ".professor"); repo != fallback {
		repos = append(repos, fallback)
	}
	dead, err := installer.InspectDeadRegistryLinks(env.Home, env.Store, repos)
	if err != nil {
		return nil, fmt.Errorf("inspect dead registry links: %w", err)
	}
	rows := make([]Row, 0, len(dead))
	for _, link := range dead {
		rows = append(rows, staleRow("dead-registry-link", link.Path, "pfm link to a target gone: "+link.Target))
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
