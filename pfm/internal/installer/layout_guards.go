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

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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

func procFDHolders(procRoot, db string) ([]string, error) {
	probed := map[string]string{}
	for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
		physical := paths.PhysicalPath(db + suffix)
		probed[physical] = filepath.Base(physical)
	}
	pids, err := processIDs(procRoot)
	if err != nil {
		return nil, err
	}
	holders := []string{}
	for _, pid := range pids {
		// Another user's process is unreadable (EACCES) to a normal user, and it
		// cannot hold a database under this user's HOME.
		fds, err := os.ReadDir(filepath.Join(procRoot, pid, "fd"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(procRoot, pid, "fd", fd.Name()))
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			if err != nil {
				return nil, err
			}
			match := false
			if _, ok := probed[target]; ok {
				match = true
			} else {
				for physical, base := range probed {
					if filepath.Base(target) == base && paths.PhysicalPath(target) == physical {
						match = true
						break
					}
				}
			}
			if match {
				holders = append(holders, pid)
				break
			}
		}
	}
	return holders, nil
}

func lsofHolderTargets(db string) ([]string, error) {
	targets := []string{}
	for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
		candidate := db + suffix
		if _, err := os.Lstat(candidate); err == nil {
			targets = append(targets, candidate)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return targets, nil
}

func parseLsofHolders(stdout, stderr string, exitCode int) ([]string, error) {
	if exitCode == 1 && strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == "" {
		return nil, nil
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("database holder probe: lsof exited %d: %s", exitCode, strings.TrimSpace(stderr))
	}
	holders := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		pid := strings.TrimSpace(line)
		if pid == "" {
			continue
		}
		num, err := strconv.Atoi(pid)
		if err != nil || num <= 0 {
			return nil, fmt.Errorf("database holder probe: lsof printed %q", pid)
		}
		holders[pid] = true
	}
	pids := make([]string, 0, len(holders))
	for pid := range holders {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	return pids, nil
}

func stagedPromptUsers(procRoot, staged string) (int, error) {
	proc := gather.NewProcFS(procRoot)
	pids, err := proc.PIDs()
	if err != nil {
		return 0, err
	}
	users := 0
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) ||
			errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EINVAL) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, word := range argv {
			if strings.HasPrefix(word, staged+string(filepath.Separator)) {
				users++
				break
			}
		}
	}
	return users, nil
}

func moveSpaceGuard(env LayoutEnv, finding LayoutFinding) (string, error) {
	target := finding.Path
	if finding.Row == "memory-helpers" {
		prefix := pfmengine.MustLookup(pfmengine.Claude).SocketPrefix
		name := strings.TrimPrefix(filepath.Base(finding.Source), prefix)
		target = filepath.Join(filepath.Dir(finding.Source), name)
	}
	var sourceInfo fs.FileInfo
	var bytesNeeded int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if suffix != "" && finding.Row != "state-db" && finding.Row != "cache-db" {
			continue
		}
		info, err := os.Stat(finding.Source + suffix)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if sourceInfo == nil {
			sourceInfo = info
		}
		bytesNeeded += info.Size()
	}
	if sourceInfo == nil {
		return "", fmt.Errorf("source %s disappeared before free-space probe", finding.Source)
	}
	different, available, err := env.probeMove(finding.Source, target)
	if err != nil {
		return "", err
	}
	if different && available < uint64(bytesNeeded) {
		return fmt.Sprintf("insufficient free space: need %d bytes, have %d", bytesNeeded, available), nil
	}
	return "", nil
}

func (env LayoutEnv) probeMove(source, target string) (bool, uint64, error) {
	if env.moveProbe != nil {
		return env.moveProbe(source, target)
	}
	if _, err := os.Stat(source); err != nil {
		return false, 0, err
	}
	sourceDevice, _, err := env.probeSpace(source)
	if err != nil {
		return false, 0, err
	}
	destinationDevice, available, err := env.probeSpace(filepath.Dir(target))
	if err != nil {
		return false, 0, err
	}
	if sourceDevice == destinationDevice {
		return false, 0, nil
	}
	return true, available, nil
}
