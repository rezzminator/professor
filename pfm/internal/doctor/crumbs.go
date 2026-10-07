package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const probeHomeLeakAge = 10 * time.Minute

func printLeakedProbeHomes(stdout io.Writer, sidDir string, now time.Time) int {
	return printLeakedProbeHomesWith(stdout, sidDir, now, os.ReadDir)
}

func printLeakedProbeHomesWith(
	stdout io.Writer,
	sidDir string,
	now time.Time,
	readDir func(string) ([]os.DirEntry, error),
) int {
	entries, err := readDir(sidDir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: warning probe_home could not look: %v\n", err)
		return 1
	}
	warnings := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), paths.SIDEngineProbeHomePrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			fmt.Fprintf(stdout, "doctor: warning probe_home could not look: %v\n", err)
			warnings++
			continue
		}
		age := now.Sub(info.ModTime())
		if age <= probeHomeLeakAge {
			continue
		}
		path := filepath.Join(sidDir, entry.Name())
		fmt.Fprintf(
			stdout,
			"doctor: warning probe_home %s left %s ago by a probe that never cleaned up — remove it: rm -rf %s\n",
			path,
			age.Truncate(time.Second),
			path,
		)
		warnings++
	}
	return warnings
}

func metaCounter(
	ctx context.Context,
	database *store.Store,
	key string,
) (int64, error) {
	value, found, err := database.Meta(ctx, key)
	if err != nil || !found {
		return 0, err
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("%s has invalid value %q", key, value)
	}
	return count, nil
}

func crumbHealth(path string) (entries, invalid int, err error) {
	return crumbHealthWith(path, os.Stat, os.ReadDir)
}

func crumbHealthWith(
	path string,
	stat func(string) (os.FileInfo, error),
	readDir func(string) ([]os.DirEntry, error),
) (entries, invalid int, err error) {
	info, err := stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("%s is not a directory", path)
	}
	directory, err := readDir(path)
	if err != nil {
		return 0, 0, err
	}
	for _, entry := range directory {
		entries++
		name := entry.Name()
		// A dot prefix marks sid bookkeeping rather than a crumb: the .lock
		// files and the open-lock directories the zsh creates with mkdir.
		if name == "" || name[0] == '.' {
			continue
		}
		if entry.IsDir() {
			if !slices.Contains(paths.SIDScratchDirs(), name) &&
				!strings.HasPrefix(name, paths.SIDHarnessConfigDirPrefix) &&
				!strings.HasPrefix(name, paths.SIDEngineProbeHomePrefix) {
				invalid++
			}
			continue
		}
		if _, _, ok := gather.ParseCrumbName(name); ok {
			continue
		}
		if filepath.Ext(name) == ".lock" ||
			nonFleetServerCrumb(name) ||
			knownSIDMetadata(name) ||
			agentrole.IsSeatPromptPath(name) ||
			agentrole.IsHarnessPromptRecordPath(name) ||
			sidScratchFile(name) {
			continue
		}
		invalid++
	}
	return entries, invalid, nil
}

// nonFleetServerCrumb reports whether a crumb names a tmux server the fleet
// deliberately excludes. The statusline writes a crumb for every Claude chat
// it sees, including chats on the vsct bunker, so those names are ordinary
// sid traffic rather than rot.
func nonFleetServerCrumb(name string) bool {
	socket := name
	if marker := strings.LastIndex(name, ".%"); marker >= 0 {
		socket = name[:marker]
	}
	return strings.HasPrefix(socket, "vsct")
}

// sidScratchFile reports whether name is a headless scratch file — a prepared
// exchange or a live pane capture — written by the headless writers' patterns.
func sidScratchFile(name string) bool {
	for _, pattern := range []string{paths.SIDExchangeScratchPattern, paths.SIDCaptureScratchPattern} {
		if matched, err := filepath.Match(pattern, name); err == nil && matched {
			return true
		}
	}
	return false
}

func knownSIDMetadata(name string) bool {
	for _, prefix := range []string{paths.SIDEffortPrefix} {
		if session, ok := strings.CutPrefix(name, prefix); ok {
			return strings.TrimSpace(session) != ""
		}
	}
	const reloadPrefix = "reload-"
	const logSuffix = ".log"
	if strings.HasPrefix(name, reloadPrefix) && strings.HasSuffix(name, logSuffix) {
		socket := strings.TrimSuffix(strings.TrimPrefix(name, reloadPrefix), logSuffix)
		if _, paneID, ok := gather.ParseCrumbName(socket); ok && paneID == "" {
			return true
		}
		return nonFleetServerCrumb(socket)
	}

	const suffix = ".then-failed"
	if !strings.HasSuffix(name, suffix) {
		return false
	}
	socket := strings.TrimSuffix(name, suffix)
	_, paneID, ok := gather.ParseCrumbName(socket)
	return ok && paneID == ""
}
