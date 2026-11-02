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

// The retired-path registry: every host path pfm leaves behind that the
// current pfm no longer uses, each kind cited to the code that wrote it. A
// stale path is a WARN row; a failed look is the UNREADABLE BLOCK. pfm doctor
// --stale prints the rows and --purge moves every WARN path into one backup dir.

const (
	checkInstallBackup  = "install-backup"
	checkDeadAccountDir = "dead-account-dir"
	// installBackupMarker is the name availableBackup gives every copy pfm
	// install sets aside (installer/files.go).
	installBackupMarker = ".pre-professor-"
	staleFix            = "pfm doctor --stale --purge moves it into one backup dir"
)

// RetiredPathDetectors is the registry; one entry per kind.
func RetiredPathDetectors() []Detector {
	return []Detector{
		{checkInstallBackup, staleInstallBackups},
		{"state-backup", staleStateBackups},
		{"dead-registry-link", staleDeadRegistryLinks},
		{checkDeadAccountDir, staleDeadAccountDirs},
	}
}

// RetiredPaths walks the registry.
func RetiredPaths(env Env) []Row { return runDetectors(env, RetiredPathDetectors()) }

func staleRow(check, path, problem string) Row {
	return Row{Warn, check, path, problem, staleFix}
}

// staleInstallBackups: every copy pfm install set aside beside a file it
// replaced — Claude dirs, ~/.zshrc, the launchd plists, the systemd units.
func staleInstallBackups(env Env) ([]Row, error) {
	var rows []Row
	dirs := append(claudeDirs(env),
		env.Home,
		paths.LaunchAgentsDir(env.Home),
		paths.SystemdUserDir(env.Home),
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
	"migration-backup": "pre-migration database copy",
	"retired-archive":  "pfm install's archive of a retired file",
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
