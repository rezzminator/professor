package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	claudeSettingsName      = "settings.json"
	claudeLocalSettingsName = "settings.local.json"
	stateStore              = "store"
	stateElsewhere          = "elsewhere"
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
	{Name: ".last-update-result.json"},
	{Name: "gh-pr-status-cache.json"},
}

var AccountEntries = []string{
	".credentials.json", ".claude.json", ".claude.json.backup", "backups",
	"sessions", "daemon", "daemon.log", "daemon-auth-status.json",
	"daemon-auth-cooldown", "jobs", "cache", "state", "mcp-needs-auth-cache.json",
	"telemetry", "feedback",
}

var IgnoredEntries = []string{"ide", ".cc-new-children", ".cc-pane-children", claudeLocalSettingsName}

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
		if errors.Is(err, os.ErrNotExist) {
			state.State = string(HostOverlayMissing)
		} else if err != nil {
			state.State, state.Err = stateUnreadable, err
		}
		report.Entries = append(report.Entries, state)
	}
	ordered := append([]pfmconfig.Account(nil), accounts...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, account := range ordered {
		state := AccountLinks{ID: account.ID, Dir: account.ConfigDir, State: "ok"}
		// A not-dir account stays "ok": its links then name ENOTDIR each.
		inspected := claudelaunch.InspectConfigDir(store, state.Dir)
		switch inspected.State {
		case claudelaunch.ConfigDirMissing:
			state.State = string(HostOverlayMissing)
		case claudelaunch.ConfigDirUnreadable:
			state.State, state.Err = stateUnreadable, inspected.Err
		case claudelaunch.ConfigDirStore:
			state.State = stateStore
		}
		// A relative link target resolves against the real dir, not the
		// literal path, when the account dir is itself a symlink.
		base := state.Dir
		if inspected.Real != "" {
			base = inspected.Real
		}
		if state.State != stateStore && state.State != stateUnreadable {
			for _, entry := range StoreEntries {
				link := LinkState{Entry: entry.Name, Path: filepath.Join(state.Dir, entry.Name), State: "ok"}
				info, err := os.Lstat(link.Path)
				switch {
				case errors.Is(err, os.ErrNotExist):
					link.State = string(HostOverlayMissing)
				case err != nil:
					link.State, link.Err = stateUnreadable, err
				case info.Mode()&os.ModeSymlink == 0:
					link.State = "real"
				default:
					link.Target, err = os.Readlink(link.Path)
					if err != nil {
						link.State, link.Err = stateUnreadable, err
					} else {
						target := link.Target
						if !filepath.IsAbs(target) {
							target = filepath.Join(base, target)
						}
						if paths.PhysicalPath(target) != paths.PhysicalPath(filepath.Join(store, entry.Name)) {
							link.State = stateElsewhere
						}
					}
				}
				state.Links = append(state.Links, link)
			}
		}
		report.Accounts = append(report.Accounts, state)
	}
	return report
}

func (installer *engine) wireClaudeStore() error {
	store := installer.options.ConfigDir
	report := InspectClaudeStore(store, installer.options.ClaudeAccounts)
	for index, entry := range report.Entries {
		switch entry.State {
		case "ok":
			installer.ok(entry.Path)
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
	for _, account := range report.Accounts {
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
	}
	for _, account := range report.Accounts {
		if account.State == stateStore {
			continue
		}
		for _, link := range account.Links {
			target := filepath.Join(store, link.Entry)
			switch link.State {
			case "ok":
				installer.ok(link.Path)
			case stateUnreadable:
				return fmt.Errorf("inspect account link %s: %w", link.Path, link.Err)
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
			case string(HostOverlayMissing), stateElsewhere:
				message := fmt.Sprintf("link %s -> %s", link.Path, target)
				if link.State == stateElsewhere {
					message = fmt.Sprintf("repoint %s -> %s (was %s)", link.Path, target, link.Target)
				}
				if err := installer.change(message, func() error {
					if link.State == stateElsewhere {
						if err := os.Remove(link.Path); err != nil {
							return err
						}
					}
					return os.Symlink(target, link.Path)
				}); err != nil {
					return fmt.Errorf("link account entry %s -> %s: %w", link.Path, target, err)
				}
			}
		}
	}
	return nil
}
