package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
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
	if chats.starting {
		wait = startingChat(dir)
		installer.skip(wait)
		return wait, release, nil
	}
	live, err := chats.live()
	if err != nil {
		return "", release, err
	}
	if len(live) != 0 {
		wait = "account " + dir + " has live chats; close them and rerun pfm install --yes"
		installer.skip(wait)
	}
	return wait, release, nil
}
