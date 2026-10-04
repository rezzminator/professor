package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
		fix, ok := accountIsStoreFix(&rows, env, account, inspected.Real, info)
		if !ok {
			continue
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

// accountIsStoreFix gives an account dir resolving to the store a real dir
// outside it: a link becomes a real dir, a link above it is replaced the same
// guarded way with the account's dir moved out from behind it, and configDir is
// never pointed at another path resolving into the store.
func accountIsStoreFix(rows *[]Row, env Env, account config.Account, resolved string, info os.FileInfo) (string, bool) {
	dir := account.ConfigDir
	if info.Mode()&os.ModeSymlink != 0 {
		return unlinkAccountFix(dir), true
	}
	link, err := storeLinkAbove(env.Store, dir)
	if err != nil {
		*rows = append(*rows, unreadable("account-is-store", dir, err))
		return "", false
	}
	storeReal := paths.PhysicalPath(env.Store)
	separator := string(filepath.Separator)
	name, _, _ := strings.Cut(strings.TrimPrefix(resolved, storeReal+separator), separator)
	switch {
	case link != "" && (resolved == storeReal || installer.EntryClass(name) != classUnclassified):
		return link + " links into the store " + env.Store + ": replace it with a real dir by hand, moving out " +
			"only what " + dir + " holds, then rerun pfm doctor", true
	case link != "":
		fix := "[ ! -L " + link + " ] || { " + unlinkAccountFix(link) + "; }"
		if parent := filepath.Dir(dir); parent != link {
			fix += " && mkdir -m 700 -p " + parent
		}
		return fix + " && [ ! -e " + dir + " ] && mv " + resolved + " " + dir, true
	}
	target := config.DefaultAccountDir(env.Home, account.ID)
	if inStore(env.Store, target) {
		return fmt.Sprintf(
			"point accounts[%d].configDir in %s at a real dir outside the store %s; %s resolves into it",
			account.ID,
			env.ConfigPath,
			env.Store,
			target,
		), true
	}
	return fmt.Sprintf("point accounts[%d].configDir in %s at %s", account.ID, env.ConfigPath, target), true
}

// storeLinkAbove returns the nearest link above dir that resolves into the
// store, or "" when dir reaches the store some other way.
func storeLinkAbove(store, dir string) (string, error) {
	store = filepath.Clean(store)
	for path := filepath.Dir(dir); path != filepath.Dir(path); path = filepath.Dir(path) {
		if path == store || strings.HasPrefix(path, store+string(filepath.Separator)) {
			return "", nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", fmt.Errorf("inspect %s above %s: %w", path, dir, err)
		}
		if info.Mode()&os.ModeSymlink != 0 && inStore(store, path) {
			return path, nil
		}
	}
	return "", nil
}

// inStore reports whether path, existing or not, resolves to the store or
// inside it.
func inStore(store, path string) bool {
	storeReal, physical := paths.PhysicalPath(store), paths.PhysicalPath(path)
	return physical == storeReal || strings.HasPrefix(physical, storeReal+string(filepath.Separator))
}

// unlinkAccountFix is account-is-store's fix for a link into the store: it
// removes the link only and makes a real dir in its place.
func unlinkAccountFix(dir string) string {
	return "rm " + dir + " && mkdir -m 700 " + dir
}

// storeIdentityProblem names how identity reaches the store: pfm launches set
// CLAUDE_CONFIG_DIR to an account dir, so only a launch pointing it at the
// store writes there.
const storeIdentityProblem = " is account identity inside the store, written by a Claude launched with " +
	"CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)"

func storeIdentity(env Env) ([]Row, error) {
	var rows []Row
	acct := firstAccountDir(env)
	aliased := claudelaunch.InspectConfigDir(env.Store, acct).State == claudelaunch.ConfigDirStore
	var acctInfo os.FileInfo
	if aliased {
		if acctInfo = inspectPath(&rows, "store-identity", acct); acctInfo == nil {
			return rows, nil
		}
	}
	for _, entry := range installer.AccountEntries {
		if slices.Contains(installer.RetiredStoreEntries, entry) {
			// Its store copy is retired-store-entry's: pfm install archives it.
			continue
		}
		if entry == "state" {
			entry = filepath.Join(entry, "mcp-discover-verdicts.json")
		}
		path := filepath.Join(env.Store, entry)
		if inspectPath(&rows, "store-identity", path) == nil {
			continue
		}
		target := filepath.Join(acct, entry)
		// Account 1's dir is usually absent on a first migration, and pfm
		// install, which would create it, is refused by this row: the fix
		// makes it (0700, nested state/ too) before the move.
		dirs := acct
		if parent := filepath.Dir(target); parent != dirs {
			dirs += " " + parent
		}
		fix := "mkdir -m 700 -p " + dirs + " && mv " + path + " " + target
		switch {
		case aliased && acctInfo.Mode()&os.ModeSymlink != 0:
			// Through the link the store and account paths are one file: the
			// link becomes a real dir first, by account-is-store's own fix,
			// guarded so the fix also runs once that one has.
			fix = "[ ! -L " + acct + " ] || { " + unlinkAccountFix(acct) + "; } && " + fix
		case aliased:
			fix = "apply account-is-store's fix for " + acct + " first; pfm doctor then names this entry's move"
		default:
			before := len(rows)
			info := inspectPath(&rows, "store-identity", target)
			if len(rows) != before {
				continue
			}
			if info != nil {
				var ok bool
				fix, ok = removeKeeping(
					&rows,
					"store-identity",
					target,
					"keep "+target+"; after checking, rm -r "+path,
					path,
				)
				if !ok {
					continue
				}
			}
		}
		rows = append(rows, Row{Block, "store-identity", path, entry + storeIdentityProblem, fix})
	}
	return rows, nil
}

func homeStateFile(env Env) ([]Row, error) {
	var rows []Row
	path := filepath.Join(env.Home, ".claude.json")
	if _, ok := readFile(&rows, "home-state-file", path); !ok {
		return rows, nil
	}
	keep := filepath.Join(firstAccountDir(env), ".claude.json")
	fix, ok := removeKeeping(
		&rows,
		"home-state-file",
		keep,
		"check it names the same oauthAccount as "+keep+", then rm "+path,
		path,
	)
	if ok {
		rows = append(
			rows,
			Row{
				Warn,
				"home-state-file",
				path,
				"a Claude launched without CLAUDE_CONFIG_DIR wrote this state file",
				fix,
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
			fix, ok := removeKeeping(
				&rows,
				"account-entry-real",
				filepath.Join(env.Store, entry.Name),
				sharedEntryFix(env.Store, path, entry.Name),
				path,
			)
			if !ok {
				continue
			}
			rows = append(
				rows,
				Row{
					Block,
					"account-entry-real",
					path,
					entry.Name + " is a real " + kind + "; it belongs in the store",
					fix,
				},
			)
		}
	}
	return rows, nil
}

// sharedEntryFix is one shell line per entry: its delete is the last link of
// an && chain, so it runs only after the merge into the store succeeded.
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
		"teams",
		"agents",
		"commands",
		"skills",
		"rules",
		"themes":
		// diff's own errors join its output, so an unreadable file stops the rm too.
		return "mkdir -p " + target + " && cp -an " + path + "/. " + target + "/ && ! diff -rq " + path + " " +
			target + " 2>&1 | grep -v '^Only in " + target + "' && rm -r " + path +
			"  # union into the store; stops while a file differs"
	case "history.jsonl":
		return "jq -c -s 'sort_by(.timestamp)[]' " + target + " " + path + " > " + target + ".new && mv " +
			target + ".new " + target + " && rm " + path + "  # interleaved by timestamp"
	case "plugins":
		return "rm -r " + path + "  # the store keeps its copy (reinstallable)"
	case "settings.json":
		return "jq -e -s '.[0] as $s | .[1] | to_entries | " +
			"all(.key as $k | ($s | has($k) | not) or $s[$k] == .value)' " +
			target + " " + path + " > /dev/null && jq -s '.[0] * .[1]' " + target + " " + path + " > " + target +
			".new && mv " + target + ".new " + target + " && rm " + path +
			"  # adds the keys the store lacks; stops while a key differs"
	case "CLAUDE.md":
		return "cat " + path + " >> " + target + " && rm " + path +
			"  # appended whole; prune " + target + " as you like"
	default:
		return "rm " + path + "  # a cache"
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

// backupOriginal names the file a backup was taken of: the name before its
// first backup marker.
func backupOriginal(name string) string {
	end := len(name)
	for _, marker := range []string{".pre-professor-", ".bak-", ".before-"} {
		if i := strings.Index(name, marker); i >= 0 && i < end {
			end = i
		}
	}
	return name[:end]
}

func besideBackup(env Env) ([]Row, error) {
	var rows []Row
	for _, dir := range claudeDirs(env) {
		for _, entry := range readDir(&rows, "beside-backup", dir) {
			if !backupName(entry.Name()) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			fix, ok := removeKeeping(
				&rows,
				"beside-backup",
				filepath.Join(dir, backupOriginal(entry.Name())),
				"rm -r "+path+" once you no longer need it",
				path,
			)
			if ok {
				rows = append(rows, Row{Warn, "beside-backup", path, "a backup beside the file", fix})
			}
		}
	}
	return rows, nil
}
