package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	claudeSettingsName      = "settings.json"
	claudeLocalSettingsName = "settings.local.json"
	stateStore              = "store"
	stateElsewhere          = "elsewhere"
	stateForeign            = "foreign"
)

// ClaudeStore is the shared Claude data directory; claudelaunch owns it, since
// a launch refuses an account dir resolving into it.
var ClaudeStore = claudelaunch.ClaudeStore

// StoreEntry is one shared entry linked from every Claude account.
type StoreEntry struct {
	Name string
	Dir  bool
	Seed string
}

var StoreEntries = []StoreEntry{
	{Name: "agents", Dir: true},
	{Name: "commands", Dir: true},
	{Name: "skills", Dir: true},
	{Name: "rules", Dir: true},
	{Name: "plugins", Dir: true},
	{Name: "themes", Dir: true},
	{Name: "projects", Dir: true},
	{Name: "file-history", Dir: true},
	{Name: "tasks", Dir: true},
	{Name: "session-env", Dir: true},
	{Name: "plans", Dir: true},
	{Name: "paste-cache", Dir: true},
	{Name: "shell-snapshots", Dir: true},
	{Name: "uploads", Dir: true},
	{Name: "downloads", Dir: true},
	{Name: "teams", Dir: true},
	{Name: claudeSettingsName, Seed: "{}\n"},
	{Name: "CLAUDE.md"},
	{Name: "history.jsonl"},
	{Name: "stats-cache.json"},
	{Name: ".last-cleanup"},
	{Name: "gh-pr-status-cache.json"},
}

var AccountEntries = []string{
	".credentials.json", ".claude.json", ".claude.json.backup", "backups",
	"sessions", "daemon", "daemon.log", "daemon-auth-status.json",
	"daemon-auth-cooldown", "jobs", "cache", "state", "mcp-needs-auth-cache.json",
	"telemetry", "feedback", ".last-update-result.json", gather.AccountLaunchClaimsName,
}

// IgnoredEntries are neither shared nor per-account identity: IDE locks, pfm's
// pane bookkeeping, a project settings name, Claude Code's own runtime scratch
// (debug logs, the daemon's lock and status, temp files) and the native runtime
// and user customization paths it keeps in place (chrome, dev-mods, keybindings).
var IgnoredEntries = []string{
	"ide", ".cc-new-children", ".cc-pane-children", claudeLocalSettingsName,
	"debug", "daemon.lock", "daemon.status.json", "tmp",
	"chrome", "dev-mods", "keybindings.json",
}

// EntryClass classifies a top-level Claude entry.
func EntryClass(name string) string {
	for _, entry := range StoreEntries {
		if name == entry.Name {
			return "shared"
		}
	}
	for _, entry := range AccountEntries {
		if name == entry {
			return "account"
		}
	}
	for _, entry := range IgnoredEntries {
		if name == entry {
			return "ignored"
		}
	}
	return "unclassified"
}

// StoreEntryState is the inspected state of one shared entry.
type (
	StoreEntryState struct {
		Name, Path, State string
		Err               error
	}
	// LinkState is the inspected state of an account's shared link.
	LinkState struct {
		Entry, Path, State, Target string
		Err                        error
	}
	// AccountLinks describes one account directory and its shared links.
	AccountLinks struct {
		ID         int
		Dir, State string
		Err        error
		Links      []LinkState
	}
	// ClaudeStoreReport describes the store and its account links.
	ClaudeStoreReport struct {
		Entries  []StoreEntryState
		Accounts []AccountLinks
	}
)

// InspectClaudeStore inspects the shared store and its account links.
func InspectClaudeStore(store string, accounts []pfmconfig.Account) ClaudeStoreReport {
	report := ClaudeStoreReport{}
	for _, entry := range StoreEntries {
		state := StoreEntryState{Name: entry.Name, Path: filepath.Join(store, entry.Name), State: "ok"}
		_, err := os.Lstat(state.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			state.State = string(HostOverlayMissing)
		case err != nil:
			state.State, state.Err = stateUnreadable, err
		default:
			info, err := os.Stat(state.Path)
			switch {
			case errors.Is(err, os.ErrNotExist):
				state.State, state.Err = stateBroken, errors.New("a dangling link")
			case err != nil:
				state.State, state.Err = stateUnreadable, err
			case entry.Dir && !info.IsDir():
				state.State, state.Err = stateBroken, errors.New("a file where a directory belongs")
			case !entry.Dir && info.IsDir():
				state.State, state.Err = stateBroken, errors.New("a directory where a file belongs")
			}
		}
		report.Entries = append(report.Entries, state)
	}
	ordered := append([]pfmconfig.Account(nil), accounts...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, account := range ordered {
		state := AccountLinks{ID: account.ID, Dir: account.ConfigDir, State: "ok"}
		// A non-directory account is one failure, not one error per link.
		inspected := claudelaunch.InspectConfigDir(store, state.Dir)
		switch inspected.State {
		case claudelaunch.ConfigDirMissing:
			state.State = string(HostOverlayMissing)
		case claudelaunch.ConfigDirUnreadable:
			state.State, state.Err = stateUnreadable, inspected.Err
		case claudelaunch.ConfigDirNotDir:
			state.State = string(claudelaunch.ConfigDirNotDir)
		case claudelaunch.ConfigDirStore:
			state.State = stateStore
		}
		// A relative link target resolves against the real dir, not the
		// literal path, when the account dir is itself a symlink.
		base := state.Dir
		if inspected.Real != "" {
			base = inspected.Real
		}
		if state.State != stateStore && state.State != stateUnreadable &&
			inspected.State != claudelaunch.ConfigDirNotDir {
			for _, entry := range StoreEntries {
				state.Links = append(
					state.Links,
					InspectAccountLink(store, base, filepath.Join(state.Dir, entry.Name), entry.Name),
				)
			}
		}
		report.Accounts = append(report.Accounts, state)
	}
	return report
}

// InspectAccountLink inspects one account entry; base resolves relative targets.
func InspectAccountLink(store, base, path, entry string) LinkState {
	link := LinkState{Entry: entry, Path: path, State: "ok"}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		link.State = string(HostOverlayMissing)
	case err != nil:
		link.State, link.Err = stateUnreadable, err
	case info.Mode()&os.ModeSymlink == 0:
		link.State = "real"
	default:
		link.Target, err = os.Readlink(path)
		if err != nil {
			link.State, link.Err = stateUnreadable, err
			return link
		}
		target := link.Target
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		physical := paths.PhysicalPath(target)
		if physical == paths.PhysicalPath(filepath.Join(store, entry)) {
			return link
		}
		if _, err := os.Stat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			link.State, link.Err = stateUnreadable, err
			return link
		}
		storeReal := paths.PhysicalPath(store)
		if physical == storeReal || strings.HasPrefix(physical, storeReal+string(filepath.Separator)) {
			link.State = stateElsewhere
		} else {
			link.State = stateForeign
		}
	}
	return link
}

func (installer *engine) wireClaudeStore() error {
	store := installer.options.ConfigDir
	report := InspectClaudeStore(store, installer.options.ClaudeAccounts)
	for index, entry := range report.Entries {
		switch entry.State {
		case "ok":
			installer.ok(entry.Path)
		case stateBroken:
			return fmt.Errorf(
				"store entry %s broken: %v — remove %s, then run pfm install --yes",
				entry.Path,
				entry.Err,
				entry.Path,
			)
		case stateUnreadable:
			return fmt.Errorf("inspect store entry %s: %w", entry.Path, entry.Err)
		case string(HostOverlayMissing):
			shape := StoreEntries[index]
			if err := installer.change("create "+entry.Path, func() error {
				if shape.Dir {
					return os.MkdirAll(entry.Path, 0o700)
				}
				return atomicfile.Write(entry.Path, []byte(shape.Seed), 0o600)
			}); err != nil {
				return fmt.Errorf("create store entry %s: %w", entry.Path, err)
			}
		}
	}
	seen := make(map[string]int)
	accounts := make([]AccountLinks, 0, len(report.Accounts))
	for _, account := range report.Accounts {
		physical := paths.PhysicalPath(account.Dir)
		if first, ok := seen[physical]; ok {
			installer.skip(
				fmt.Sprintf("account %d %s is the same directory as account %d", account.ID, account.Dir, first),
			)
			continue
		}
		seen[physical] = account.ID
		switch account.State {
		case stateStore:
			installer.skip(
				fmt.Sprintf("account %d %s resolves to the store — pfm doctor names the fix", account.ID, account.Dir),
			)
			continue
		case stateUnreadable:
			return fmt.Errorf("inspect account %d %s: %w", account.ID, account.Dir, account.Err)
		case string(HostOverlayMissing):
			if err := installer.change(
				"create "+account.Dir,
				func() error { return os.MkdirAll(account.Dir, 0o700) },
			); err != nil {
				return fmt.Errorf("create account %s: %w", account.Dir, err)
			}
		case "ok":
			installer.ok(account.Dir)
		}
		accounts = append(accounts, account)
	}
	for _, account := range accounts {
		accountErr := func() (returnErr error) {
			var guard *gather.AccountGuard
			if installer.apply {
				var err error
				guard, err = gather.AcquireAccountGuard(account.Dir, false)
				if errors.Is(err, syscall.EWOULDBLOCK) {
					installer.skip("account " + account.Dir + " is starting a chat; rerun pfm install --yes")
					return nil
				}
				if err != nil {
					return err
				}
				defer func() { returnErr = errors.Join(returnErr, guard.Close()) }()
			}
			var live []string
			liveRead := false
			for _, link := range account.Links {
				target := filepath.Join(store, link.Entry)
				switch link.State {
				case "ok":
					installer.ok(link.Path)
				case stateUnreadable:
					return fmt.Errorf("inspect account link %s: %w", link.Path, link.Err)
				case stateForeign:
					installer.skip(
						fmt.Sprintf(
							"%s links to %s outside the store — pfm doctor names its merge",
							link.Path,
							link.Target,
						),
					)
				case "real":
					info, err := os.Lstat(link.Path)
					if err != nil {
						return fmt.Errorf("inspect real account entry %s: %w", link.Path, err)
					}
					kind := "file"
					if info.IsDir() {
						kind = "dir"
					}
					installer.skip(fmt.Sprintf("%s is a real %s — pfm doctor names its merge", link.Path, kind))
				case string(HostOverlayMissing):
					if err := installer.change(fmt.Sprintf("link %s -> %s", link.Path, target), func() error {
						return os.Symlink(target, link.Path)
					}); err != nil {
						return fmt.Errorf("link account entry %s -> %s: %w", link.Path, target, err)
					}
				case stateElsewhere:
					if !liveRead {
						var err error
						live, err = liveChatPIDs(installer.options.ProcRoot, account.Dir)
						if err != nil {
							return fmt.Errorf("read live chats in %s: %w", account.Dir, err)
						}
						if guard != nil {
							claims, claimErr := guard.Active(gather.NewProcFS(installer.options.ProcRoot))
							if claimErr != nil {
								return claimErr
							}
							for _, pid := range claims {
								live = append(live, fmt.Sprint(pid))
							}
						}
						liveRead = true
					}
					if len(live) > 0 {
						installer.skip(
							fmt.Sprintf(
								"repoint %s: live chats %s on %s — close them and rerun pfm install --yes",
								link.Path,
								strings.Join(live, ","),
								account.Dir,
							),
						)
						continue
					}
					message := fmt.Sprintf("repoint %s -> %s (was %s)", link.Path, target, link.Target)
					if err := installer.change(message, func() error {
						return installer.repointAccountLink(target, link.Path, link.Target)
					}); err != nil {
						return fmt.Errorf("link account entry %s -> %s: %w", link.Path, target, err)
					}
				}
			}
			return nil
		}()
		if accountErr != nil {
			return accountErr
		}
	}
	return nil
}
