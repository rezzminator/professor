package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func liveChatPIDs(procRoot, configDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(configDir, "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var live map[int]bool
	pids := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		pid := strings.TrimSuffix(entry.Name(), ".json")
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		if live == nil {
			processes, err := gather.NewProcFS(procRoot).PIDs()
			if err != nil {
				return nil, err
			}
			live = make(map[int]bool, len(processes))
			for _, process := range processes {
				live[process] = true
			}
		}
		id, _ := strconv.Atoi(pid)
		if live[id] {
			pids = append(pids, pid)
		}
	}
	sort.Strings(pids)
	return pids, nil
}

// accountChats is one account's answer from the live-chat door.
type accountChats struct {
	procRoot, dir string
	// guard is dir's account guard, held until release; nil when none was taken.
	guard *gather.AccountGuard
	// starting reports the guard busy: a chat on dir is starting.
	starting bool
}

// openAccountChats is the installer's one live-chat door for account dir.
// On an applied run with guarded set it takes dir's account guard, so no chat
// starts before the change it gates, and reports a busy guard as starting.
// A dry run or an unguarded open takes nothing and writes nothing: its live
// read is the read-only probe of the sessions the apply reads, so a plan
// prints the rows its apply prints.
func (installer *engine) openAccountChats(dir string, guarded bool) (*accountChats, error) {
	chats := &accountChats{procRoot: installer.options.ProcRoot, dir: dir}
	if !guarded || !installer.apply {
		return chats, nil
	}
	guard, err := gather.AcquireAccountGuard(dir, false)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		chats.starting = true
		return chats, nil
	}
	if err != nil {
		return chats, err
	}
	chats.guard = guard
	return chats, nil
}

// live returns the pids of dir's live chats: its live sessions, then, under a
// held guard, the live launch claims (whose read removes exited claims).
func (chats *accountChats) live() ([]string, error) {
	live, err := liveChatPIDs(chats.procRoot, chats.dir)
	if err != nil || chats.guard == nil {
		return live, err
	}
	claims, err := chats.guard.Active(gather.NewProcFS(chats.procRoot))
	if err != nil {
		return nil, err
	}
	for _, pid := range claims {
		live = append(live, strconv.Itoa(pid))
	}
	return live, nil
}

// release frees the guard, if one was taken.
func (chats *accountChats) release() error {
	return chats.guard.Close()
}

// startingChat is the skip reason for an account whose guard a starting chat holds.
func startingChat(dir string) string {
	return "account " + dir + " is starting a chat; rerun pfm install --yes"
}

// deferral is the account-level answer of the door: why a change to what
// dir's chats use must wait (a chat starting, or one live), or "".
func (chats *accountChats) deferral() (string, error) {
	if chats.starting {
		return startingChat(chats.dir), nil
	}
	live, err := chats.live()
	if err != nil || len(live) == 0 {
		return "", err
	}
	return "account " + chats.dir + " has live chats; close them and rerun pfm install --yes", nil
}

// liveChatDeferral asks the door, guarded, why a change to what dir's chats
// use must wait (a chat starting, or one live), its skip line printed, or ""
// with the guard held so no chat starts before the change. release frees the
// guard, and is always set.
func (installer *engine) liveChatDeferral(dir string) (wait string, release func() error, err error) {
	chats, err := installer.openAccountChats(dir, true)
	release = chats.release
	if err != nil {
		return "", release, err
	}
	wait, err = chats.deferral()
	if wait != "" {
		installer.skip(wait)
	}
	return wait, release, err
}

// LiveChatDeferral is liveChatDeferral's read-only answer for doctor: the
// same door, unguarded, so it takes no lock and writes nothing.
func LiveChatDeferral(procRoot, dir string) (string, error) {
	return (&accountChats{procRoot: procRoot, dir: dir}).deferral()
}

// RetiredFolderMark is the file install writes into a retired plugin folder (a
// former name's copy, a legacy -dev marketplace) the first time a live chat
// holds its removal back. Its mtime is the moment the folder's id was already
// disabled and uninstalled, so a chat started later never loaded it.
const RetiredFolderMark = ".pfm-retired"

// PluginChats names the accounts whose live chats may run a plugin copy and
// the proc root their liveness and start times are read from.
type PluginChats struct {
	ProcRoot string
	Dirs     []string
}

// accountDirs is chats.Dirs once each (liveChatAccounts).
func (chats PluginChats) accountDirs() ([]string, error) {
	return liveChatAccounts(chats.Dirs)
}

// liveChatAccounts is the one choice of which account dirs the live-chat door
// is asked about: each once, by physical path, so no account's guard is taken
// twice (a second flock on one dir reads as a starting chat), without the
// empty and the missing: a missing account has no chats.
func liveChatAccounts(accounts []string) ([]string, error) {
	var dirs []string
	seen := map[string]bool{}
	for _, dir := range accounts {
		physical := paths.PhysicalPath(dir)
		if dir == "" || seen[physical] {
			continue
		}
		seen[physical] = true
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("inspect account %s: %w", dir, err)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// RetiredFolderDeferral is retiredFolderWait's read-only answer for doctor:
// the accounts' doors opened unguarded, so it takes no lock and writes nothing.
func RetiredFolderDeferral(chats PluginChats, folder string, now time.Time) (string, error) {
	dirs, err := chats.accountDirs()
	if err != nil {
		return "", err
	}
	accounts := make([]*accountChats, 0, len(dirs))
	for _, dir := range dirs {
		accounts = append(accounts, &accountChats{procRoot: chats.ProcRoot, dir: dir})
	}
	return retiredFolderWait(accounts, folder, now)
}

// retiredFolderWait is the door's answer for a retired plugin folder a chat
// may run its hooks from: the reason it must stay, or "" when no live chat can
// run from it. Only a chat started before the folder's RetiredFolderMark (now
// when it has none) can have loaded it: its id was disabled before the mark,
// and a chat reads its plugins once, at start. A chat starting under a busy
// guard, or one whose start time cannot be read, keeps the folder; a folder
// that is gone holds no chat.
func retiredFolderWait(accounts []*accountChats, folder string, now time.Time) (string, error) {
	if _, err := os.Lstat(folder); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("inspect %s: %w", folder, err)
	}
	retired := now
	if info, err := os.Stat(filepath.Join(folder, RetiredFolderMark)); err == nil {
		retired = info.ModTime()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read the retirement mark of %s: %w", folder, err)
	}
	for _, chats := range accounts {
		if chats.starting {
			return startingChat(chats.dir), nil
		}
		live, err := chats.live()
		if err != nil {
			return "", fmt.Errorf("read live chats in %s: %w", chats.dir, err)
		}
		proc := gather.NewProcFS(chats.procRoot)
		births, _ := proc.(gather.ProcBirth)
		var held []string
		for _, pid := range live {
			id, _ := strconv.Atoi(pid)
			if births == nil {
				held = append(held, pid+" (start time unreadable: no process start times on this platform)")
				continue
			}
			born, err := births.Birth(id)
			if err != nil {
				held = append(held, pid+" (start time unreadable: "+err.Error()+")")
				continue
			}
			if time.Unix(born, 0).Before(retired) {
				held = append(held, pid)
			}
		}
		if len(held) != 0 {
			return "account " + chats.dir + " has live chats started before " + folder + " was retired, which may " +
				"run from it: " + strings.Join(held, ", ") + "; close them and rerun pfm install --yes", nil
		}
	}
	return "", nil
}
