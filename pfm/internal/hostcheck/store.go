package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func accountIsStore(env Env) ([]Row, error) {
	var rows []Row
	for _, account := range sortedAccounts(env) {
		inspected := claudelaunch.InspectConfigDir(env.Store, account.ConfigDir)
		if inspected.State == claudelaunch.ConfigDirUnreadable {
			rows = append(rows, unreadable("account-is-store", account.ConfigDir, inspected.Err))
			continue
		}
		if inspected.State != claudelaunch.ConfigDirStore {
			continue
		}
		info := inspectPath(&rows, "account-is-store", account.ConfigDir)
		if info == nil {
			continue
		}
		fix := fmt.Sprintf(
			"point accounts[%d].configDir in %s at %s",
			account.ID,
			env.ConfigPath,
			config.DefaultAccountDir(env.Home, account.ID),
		)
		if info.Mode()&os.ModeSymlink != 0 {
			fix = "rm " + account.ConfigDir + " && mkdir -m 700 " + account.ConfigDir
		}
		rows = append(
			rows,
			Row{
				Block,
				"account-is-store",
				account.ConfigDir,
				fmt.Sprintf("account %d's config dir resolves to the store %s", account.ID, env.Store),
				fix,
			},
		)
	}
	return rows, nil
}

func storeIdentity(env Env) ([]Row, error) {
	var rows []Row
	for _, entry := range installer.AccountEntries {
		if entry == "state" {
			entry = filepath.Join(entry, "mcp-discover-verdicts.json")
		}
		path := filepath.Join(env.Store, entry)
		if inspectPath(&rows, "store-identity", path) == nil {
			continue
		}
		target := filepath.Join(firstAccountDir(env), entry)
		before := len(rows)
		info := inspectPath(&rows, "store-identity", target)
		if len(rows) != before {
			continue
		}
		// Account 1's dir is usually absent on a first migration, and pfm
		// install, which would create it, is refused by this row: the fix
		// makes it (0700, nested state/ too) before the move.
		dirs := firstAccountDir(env)
		if parent := filepath.Dir(target); parent != dirs {
			dirs += " " + parent
		}
		fix := "mkdir -m 700 -p " + dirs + " && mv " + path + " " + target
		if info != nil {
			fix = "keep " + target + "; after checking, rm -r " + path
		}
		rows = append(rows, Row{Block, "store-identity", path, entry + " is account identity inside the store", fix})
	}
	return rows, nil
}

func homeStateFile(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.Home, ".claude.json")
	if _, ok := readFile(&rows, "home-state-file", path); ok {
		rows = append(
			rows,
			Row{
				Warn,
				"home-state-file",
				path,
				"a Claude launched without CLAUDE_CONFIG_DIR wrote this state file",
				"check it names the same oauthAccount as " + filepath.Join(
					firstAccountDir(env),
					".claude.json",
				) + ", then rm " + path,
			},
		)
	}
	return rows, nil
}

func accountEntryReal(env Env) ([]Row, error) {
	var rows []Row
	for _, account := range sortedAccounts(env) {
		if claudelaunch.InspectConfigDir(env.Store, account.ConfigDir).State == claudelaunch.ConfigDirStore {
			continue
		}
		for _, entry := range installer.StoreEntries {
			path := filepath.Join(account.ConfigDir, entry.Name)
			info := inspectPath(&rows, "account-entry-real", path)
			if info == nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			kind := "file"
			if info.IsDir() {
				kind = "dir"
			}
			rows = append(
				rows,
				Row{
					Block,
					"account-entry-real",
					path,
					entry.Name + " is a real " + kind + "; it belongs in the store",
					sharedEntryFix(env.Store, path, entry.Name),
				},
			)
		}
	}
	return rows, nil
}

func sharedEntryFix(store, path, name string) string {
	target := filepath.Join(store, name)
	switch name {
	case "projects",
		"file-history",
		"tasks",
		"session-env",
		"paste-cache",
		"shell-snapshots",
		"plans",
		"uploads",
		"downloads",
		"teams":
		return "union into the store: cp -an " + path + "/. " + target + "/ ; diff -rq " + path + " " + target + " | grep -v '^Only in " + target + "' (empty: nothing differs) ; rm -r " + path
	case "history.jsonl":
		return "interleave by timestamp: jq -c -s 'sort_by(.timestamp)[]' " + target + " " + path + " > " + target + ".new && mv " + target + ".new " + target + " && rm " + path
	case "plugins":
		return "the store keeps its copy (reinstallable): rm -r " + path
	case "settings.json":
		return "copy any key you keep into " + target + ", then rm " + path
	case "CLAUDE.md":
		return "append what you keep to " + target + ", then rm " + path
	case "agents", "commands", "skills", "rules", "themes":
		return "move what the store lacks: mv -n " + path + "/* " + target + "/ ; compare what is left, then rm -r " + path
	default:
		return "a cache: rm " + path
	}
}

func unclassified(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range claudeDirs(env) {
		for _, entry := range readDir(&rows, classUnclassified, dir) {
			name := entry.Name()
			if installer.EntryClass(name) != classUnclassified || backupName(name) ||
				strings.HasPrefix(name, ".claude.json.tmp.") {
				continue
			}
			rows = append(
				rows,
				Row{
					Warn,
					classUnclassified,
					filepath.Join(dir, name),
					"UNCLASSIFIED — on neither the shared nor the per-account list",
					"keep it; pfm doctor names it until a pfm release classifies it",
				},
			)
		}
	}
	return rows, nil
}

func staleStateTmp(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range uniquePaths(append(claudeDirs(env), env.Home)) {
		for _, entry := range readDir(&rows, "stale-state-tmp", dir) {
			if !strings.HasPrefix(entry.Name(), ".claude.json.tmp.") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info := inspectPath(&rows, "stale-state-tmp", path)
			if info != nil && info.ModTime().Before(env.Now.Add(-24*time.Hour)) {
				rows = append(rows, Row{Warn, "stale-state-tmp", path, "Claude's stale state temp file", "rm " + path})
			}
		}
	}
	return rows, nil
}

func backupName(name string) bool {
	for _, pattern := range []string{"*.pre-professor-*", "*.bak-*", "*.before-*"} {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func besideBackup(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range claudeDirs(env) {
		for _, entry := range readDir(&rows, "beside-backup", dir) {
			if !backupName(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			rows = append(
				rows,
				Row{
					Warn,
					"beside-backup",
					path,
					"a backup beside the file",
					"rm -r " + path + " once you no longer need it",
				},
			)
		}
	}
	return rows, nil
}
