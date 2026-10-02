package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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

func processIDs(procRoot string) ([]string, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	pids := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err == nil {
			pids = append(pids, entry.Name())
		}
	}
	sort.Strings(pids)
	return pids, nil
}
