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

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func liveChatPIDs(procRoot, configDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(configDir, "sessions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pids := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		pid := strings.TrimSuffix(entry.Name(), ".json")
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		_, err := os.Stat(filepath.Join(procRoot, pid))
		switch {
		case err == nil:
			pids = append(pids, pid)
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, err
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

func dbHolderPIDs(procRoot, db string) ([]string, error) {
	pids, err := processIDs(procRoot)
	if err != nil {
		return nil, err
	}
	holders := []string{}
	for _, pid := range pids {
		fds, err := os.ReadDir(filepath.Join(procRoot, pid, "fd"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(procRoot, pid, "fd", fd.Name()))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if target == db || target == db+"-wal" || target == db+"-shm" {
				holders = append(holders, pid)
				break
			}
		}
	}
	return holders, nil
}

func stagedPromptUsers(procRoot, staged string) (int, error) {
	pids, err := processIDs(procRoot)
	if err != nil {
		return 0, err
	}
	users := 0
	for _, pid := range pids {
		argv, err := os.ReadFile(filepath.Join(procRoot, pid, "cmdline"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, word := range strings.Split(string(argv), "\x00") {
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
