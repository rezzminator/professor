package gather

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
)

// FDLink is one numeric process file descriptor and its symlink target.
type FDLink struct {
	FD     int
	Target string
}

// ProcStat contains the process relationship and birth fields gather needs.
type ProcStat struct {
	ParentPID int
	StartTime uint64
}

// ProcFS abstracts all /proc access used by gather.
type ProcFS interface {
	PIDs() ([]int, error)
	Cmdline(pid int) ([]string, error)
	Environ(pid int) (map[string]string, error)
	FDLinks(pid int) ([]FDLink, error)
	Stat(pid int) (ProcStat, error)
}

// ProcBirth is the optional ProcFS extension that reports when a process was
// created, in epoch seconds. Only Codex thread identification needs a wall
// clock, so a ProcFS that cannot supply one stays usable everywhere else.
type ProcBirth interface {
	Birth(pid int) (int64, error)
}

// ProcMemory is the optional ProcFS extension that reports a process's
// resident set size in kilobytes. Only the reaper needs it — the RAM a socket
// holds is the whole reason to reap one — so a ProcFS without it stays usable
// everywhere else and the reaper reports no RAM rather than refusing to run.
type ProcMemory interface {
	RSSKB(pid int) (int64, error)
}

// FileID names one file by device and inode: the identity an install's
// rename-over gives the binary's path anew, and a process already running the
// old image keeps.
type FileID struct {
	Device uint64
	Inode  uint64
}

// ProcImage is the optional ProcFS extension that reports which file a
// process is EXECUTING. Only the stale sweep needs it; a table without it is
// refused there rather than read as "every process is fresh".
type ProcImage interface {
	Image(pid int) (FileID, error)
}

func fileIDDevice[T ~int32 | ~uint32 | ~uint64](device T) uint64 { return uint64(device) }

// FileIDOf is the identity of the file at path, following symlinks.
func FileIDOf(path string) (FileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileID{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, fmt.Errorf("stat %s: no device and inode on this platform", path)
	}
	return FileID{Device: fileIDDevice(stat.Dev), Inode: stat.Ino}, nil
}

// NewProcFS returns the process-table reader for a given proc root.
//
// A root that EXISTS is always honoured, because that is how the jail works:
// PFM_PROC_ROOT points at a fixture tree and the suite reads files instead of a
// kernel. When it does not exist, the platform's native reader answers instead
// — on Linux that is still /proc, on macOS it is sysctl, since there is no /proc
// to fall back to.
//
// The fallback is deliberately not silent-empty. A reader that returned "no
// processes" on a kernel it cannot read would render as a fleet with no chats,
// which is the exact failure the root law forbids: a probe that could not run
// never returns "nothing found".
func NewProcFS(root string) ProcFS {
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return RealProcFS{Root: root}
		}
	}
	return nativeProcFS()
}

// RealProcFS reads a Linux proc filesystem. Root defaults to /proc.
type RealProcFS struct {
	Root string
}

func (proc RealProcFS) root() string {
	if proc.Root == "" {
		return "/proc"
	}
	return proc.Root
}

// PIDs returns numeric process directories in ascending order.
func (proc RealProcFS) PIDs() ([]int, error) {
	entries, err := os.ReadDir(proc.root())
	if err != nil {
		return nil, fmt.Errorf("read proc root: %w", err)
	}
	pids := make([]int, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids, nil
}

// Cmdline returns NUL-delimited argv.
func (proc RealProcFS) Cmdline(pid int) ([]string, error) {
	content, err := os.ReadFile(proc.path(pid, "cmdline"))
	if err != nil {
		return nil, err
	}
	return splitNUL(content), nil
}

// Environ returns the process environment keyed before the first equals sign.
func (proc RealProcFS) Environ(pid int) (map[string]string, error) {
	content, err := os.ReadFile(proc.path(pid, "environ"))
	if err != nil {
		return nil, err
	}
	environment := make(map[string]string)
	for _, entry := range splitNUL(content) {
		key, value, found := strings.Cut(entry, "=")
		if found {
			environment[key] = value
		}
	}
	return environment, nil
}

// FDLinks returns readable fd links in numeric order. Descriptors that vanish
// during the walk are ignored.
func (proc RealProcFS) FDLinks(pid int) ([]FDLink, error) {
	directory := proc.path(pid, "fd")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	links := make([]FDLink, 0, len(entries))
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(directory, entry.Name()))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if err != nil {
			continue
		}
		links = append(links, FDLink{FD: fd, Target: target})
	}
	sort.Slice(links, func(left, right int) bool {
		return links[left].FD < links[right].FD
	})
	return links, nil
}

// Stat reads parent pid and kernel start ticks from /proc/<pid>/stat.
func (proc RealProcFS) Stat(pid int) (ProcStat, error) {
	content, err := os.ReadFile(proc.path(pid, "stat"))
	if err != nil {
		return ProcStat{}, err
	}
	closeParen := strings.LastIndex(string(content), ") ")
	if closeParen < 0 {
		return ProcStat{}, fmt.Errorf("malformed proc stat for pid %d", pid)
	}
	fields := strings.Fields(string(content[closeParen+2:]))
	if len(fields) <= 19 {
		return ProcStat{}, fmt.Errorf("short proc stat for pid %d", pid)
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse parent pid for %d: %w", pid, err)
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse start time for %d: %w", pid, err)
	}
	return ProcStat{ParentPID: parentPID, StartTime: startTime}, nil
}

// userHZ is USER_HZ, the fixed tick rate the kernel reports /proc times in
// (<asm/param.h>): 100 on every Linux architecture pfm builds for (amd64,
// arm64). It is an ABI constant, not the kernel's internal CONFIG_HZ.
const userHZ = 100

// Birth returns the process start time in epoch seconds: the boot time from
// /proc/stat plus the start tick /proc/<pid>/stat records. The /proc/<pid>
// directory's own mtime is NOT a birth stamp — procfs instantiates that inode
// lazily on first lookup, and again after cache eviction, so it reads a
// process as minutes or hours younger than it is (measured: kernel threads
// 318 s late on an ordinary host).
func (proc RealProcFS) Birth(pid int) (int64, error) {
	stat, err := proc.Stat(pid)
	if err != nil {
		return 0, err
	}
	boot, err := proc.bootTime()
	if err != nil {
		return 0, err
	}
	return boot + int64(stat.StartTime/userHZ), nil
}

// bootTime reads the "btime" line of /proc/stat: the boot moment in epoch
// seconds every start tick counts from.
func (proc RealProcFS) bootTime() (int64, error) {
	path := filepath.Join(proc.root(), "stat")
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read boot time: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if value, found := strings.CutPrefix(line, "btime "); found {
			boot, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse btime in %s: %w", path, err)
			}
			return boot, nil
		}
	}
	return 0, errors.New("no btime line in " + path)
}

// RSSKB returns a process's resident set size in kilobytes, read from
// /proc/<pid>/statm — the cheapest of the three files that carry it (one line,
// no parsing beyond a field split) and the one whose page counts scale with
// the kernel's own page size rather than assuming 4 KiB.
func (proc RealProcFS) RSSKB(pid int) (int64, error) {
	content, err := os.ReadFile(proc.path(pid, "statm"))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed proc statm for pid %d", pid)
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse resident pages for %d: %w", pid, err)
	}
	return pages * int64(os.Getpagesize()) / 1024, nil
}

// Image is the file /proc/<pid>/exe resolves to. The kernel keeps a replaced
// image's inode alive for its process, so the identity survives the install
// that unlinked its path.
func (proc RealProcFS) Image(pid int) (FileID, error) {
	return FileIDOf(proc.path(pid, "exe"))
}

func (proc RealProcFS) path(pid int, element string) string {
	return filepath.Join(proc.root(), strconv.Itoa(pid), element)
}

func splitNUL(content []byte) []string {
	parts := strings.Split(string(content), "\x00")
	for len(parts) != 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
