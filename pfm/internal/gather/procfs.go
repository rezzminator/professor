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
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
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
	// Group is the process group; TerminalGroup the foreground group of its
	// controlling terminal (-1 without one). Darwin leaves both 0.
	Group, TerminalGroup int
}

// ProcFS abstracts all /proc access used by gather.
type ProcFS interface {
	PIDs() ([]int, error)
	Cmdline(pid int) ([]string, error)
	Environ(pid int) (map[string]string, error)
	FDLinks(pid int) ([]FDLink, error)
	Stat(pid int) (ProcStat, error)
}

// ProcessIdentity is process metadata available without reading argv.
type ProcessIdentity struct {
	// Zombie marks a terminal zombie or dead process (Linux Z/X); it cannot do work.
	Zombie       bool
	EffectiveUID uint32
	Command      string
}

// ProcIdentity is the optional ProcFS extension that identifies a process
// without reading its protected command line.
type ProcIdentity interface {
	ProcessIdentity(pid int) (ProcessIdentity, error)
}

// ProcBirth is the optional ProcFS extension that reports when a process was
// created, in epoch seconds. Only Codex thread identification needs a wall
// clock, so a ProcFS that cannot supply one stays usable everywhere else.
type ProcBirth interface {
	Birth(pid int) (int64, error)
}

// ProcStart is the optional ProcFS extension that reports when the kernel
// created a process, to its clock tick: a hook's spawn moment, which a slow
// exec and runtime start under load do not move. Only the kernel's own table
// has one; a jail's fixture tree has no clock its ticks count from.
type ProcStart interface {
	Started(pid int) (time.Time, error)
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
// ProcExecutable exposes the kernel executable path, never argv[0].
type ProcExecutable interface{ Executable(pid int) (string, error) }

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
	id, err := FileIDOfInfo(info)
	if err != nil {
		return FileID{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return id, nil
}

// FileIDOfInfo is the identity a stat result already carries.
func FileIDOfInfo(info os.FileInfo) (FileID, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, fmt.Errorf("%s: no device and inode on this platform", info.Name())
	}
	return FileID{Device: fileIDDevice(stat.Dev), Inode: stat.Ino}, nil
}

// ErrNoProcRoot marks a configured proc root that does not exist: a jail
// whose fixture tree was never staged. It wraps fs.ErrNotExist.
var ErrNoProcRoot = errors.New("gather: proc root does not exist")

// ErrForeignProcess marks a process whose environment the kernel withholds
// from the caller: a different user's process, one that made itself
// non-dumpable (sshd's session), or one holding capabilities the caller lacks
// (the systemd user manager). It can never be a pane the caller owns.
var ErrForeignProcess = errors.New(
	"gather: process environment withheld: another uid's, non-dumpable or privileged",
)

// ProcTerminals is the optional ProcFS extension that maps each pid to the
// terminal its standard input names, "/dev/" trimmed.
type ProcTerminals interface {
	Terminals() (map[int]string, error)
}

// ProcFDNames is the optional ProcFS extension that lists every descriptor
// entry of a process, readable or not: the census FDLinks is checked against.
type ProcFDNames interface {
	FDNames(pid int) ([]string, error)
}

// jailRoot is the Linux-shaped tree a proc root names, or "" when the root
// means this kernel's own table: unset, or the system proc root itself.
func jailRoot(root string) string {
	if root == "" || filepath.Clean(root) == paths.SystemProcRoot() {
		return ""
	}
	return root
}

// OpenProcFS returns the process-table reader for a proc root. "" (or the
// system proc root) is the kernel's own table: /proc on Linux, sysctl on
// macOS. Any other root is a jail, read as a Linux proc tree on either OS; a
// jail root that does not exist is ErrNoProcRoot, so a caller keeps its own
// answer for "no table" instead of reading the live kernel.
func OpenProcFS(root string) (ProcFS, error) {
	jail := jailRoot(root)
	if jail == "" {
		return nativeProcFS(), nil
	}
	info, err := os.Stat(jail)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("open proc root %s: %w: %w", jail, ErrNoProcRoot, err)
	}
	if err != nil {
		return nil, fmt.Errorf("open proc root %s: %w", jail, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open proc root %s: not a directory", jail)
	}
	return RealProcFS{Root: jail}, nil
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
	if table, err := OpenProcFS(root); err == nil {
		return table
	}
	return nativeProcFS()
}

// RealProcFS reads a Linux proc filesystem. Root defaults to /proc.
type RealProcFS struct {
	Root string
}

func (proc RealProcFS) root() string {
	return paths.ProcRootOr(proc.Root)
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
	return SplitNUL(content), nil
}

// Environ returns the process environment keyed before the first equals sign.
func (proc RealProcFS) Environ(pid int) (map[string]string, error) {
	content, err := os.ReadFile(proc.path(pid, "environ"))
	if err != nil {
		return nil, proc.withheld(pid, err)
	}
	environment := make(map[string]string)
	for _, entry := range SplitNUL(content) {
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
	fields, err := proc.statFields(pid)
	if err != nil {
		return ProcStat{}, err
	}
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
	group, err := strconv.Atoi(fields[2])
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse process group for %d: %w", pid, err)
	}
	terminalGroup, err := strconv.Atoi(fields[5])
	if err != nil {
		return ProcStat{}, fmt.Errorf("parse terminal foreground group for %d: %w", pid, err)
	}
	return ProcStat{ParentPID: parentPID, StartTime: startTime, Group: group, TerminalGroup: terminalGroup}, nil
}

// statFields are /proc/<pid>/stat's fields after the command name, state first.
func (proc RealProcFS) statFields(pid int) ([]string, error) {
	content, err := os.ReadFile(proc.path(pid, "stat"))
	if err != nil {
		return nil, err
	}
	closeParen := strings.LastIndex(string(content), ") ")
	if closeParen < 0 {
		return nil, fmt.Errorf("malformed proc stat for pid %d", pid)
	}
	return strings.Fields(string(content[closeParen+2:])), nil
}

// withheld tags a refused environment read of a process that can never be a
// pane of the caller's: another uid's or a non-dumpable one, or one with no
// terminal holding capabilities this process lacks (the systemd user manager a
// herdr-shield holder runs under). Any other refusal stays a read failure.
func (proc RealProcFS) withheld(pid int, err error) error {
	if !errors.Is(err, os.ErrPermission) {
		return err
	}
	// A non-dumpable process's proc files belong to root.
	foreign := false
	if info, statErr := os.Stat(proc.path(pid, "environ")); statErr == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		foreign = ok && int(stat.Uid) != os.Geteuid()
	}
	if foreign || proc.privileged(pid) {
		return fmt.Errorf("%w: %w", ErrForeignProcess, err)
	}
	return err
}

// privileged reports a process with no controlling terminal whose effective
// capabilities exceed this process's; this process absent from the proc root
// holds none. A pane's process has a terminal, so it is never passed over.
func (proc RealProcFS) privileged(pid int) bool {
	fields, err := proc.statFields(pid)
	if err != nil || len(fields) < 5 || fields[4] != "0" {
		return false
	}
	theirs, err := proc.effectiveCapabilities(pid)
	if err != nil {
		return false
	}
	ours, err := proc.effectiveCapabilities(os.Getpid())
	if errors.Is(err, os.ErrNotExist) {
		ours = 0
	} else if err != nil {
		return false
	}
	return theirs&^ours != 0
}

func (proc RealProcFS) effectiveCapabilities(pid int) (uint64, error) {
	path := proc.path(pid, "status")
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if value, found := strings.CutPrefix(line, "CapEff:"); found {
			return strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		}
	}
	return 0, fmt.Errorf("no CapEff line in %s", path)
}

// Terminals maps each pid to the target of its fd 0, "/dev/" trimmed: the
// pts a pane's process reads. A pid whose fd 0 cannot be read has no entry.
func (proc RealProcFS) Terminals() (map[int]string, error) {
	pids, err := proc.PIDs()
	if err != nil {
		return nil, err
	}
	terminals := make(map[int]string, len(pids))
	for _, pid := range pids {
		if target, err := os.Readlink(filepath.Join(proc.path(pid, "fd"), "0")); err == nil {
			terminals[pid] = strings.TrimPrefix(target, "/dev/")
		}
	}
	return terminals, nil
}

// FDNames lists every entry of /proc/<pid>/fd, readable link or not.
func (proc RealProcFS) FDNames(pid int) ([]string, error) {
	entries, err := os.ReadDir(proc.path(pid, "fd"))
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names, nil
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

// SplitNUL decodes a NUL-delimited kernel vector, preserving meaningful empty
// fields and removing only trailing delimiters.
func SplitNUL(content []byte) []string {
	parts := strings.Split(string(content), "\x00")
	for len(parts) != 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func (proc RealProcFS) Executable(pid int) (string, error) {
	path, err := os.Readlink(proc.path(pid, "exe"))
	if err != nil {
		return "", fmt.Errorf("read process %d executable: %w", pid, err)
	}
	return strings.TrimSuffix(path, " (deleted)"), nil
}

// procExecutablePath reads exec_path from the documented kern.procargs2 layout.
// argv[0] is operator-controlled and cannot prove an environment is in use.
func procExecutablePath(buffer []byte) (string, error) {
	if len(buffer) < 5 {
		return "", fmt.Errorf("short kern.procargs2 executable buffer: %d bytes", len(buffer))
	}
	path, _, found := strings.Cut(string(buffer[4:]), "\x00")
	if !found || !filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid kern.procargs2 executable path %q", path)
	}
	return filepath.Clean(path), nil
}

// processReadError distinguishes a vanished process using an independent probe.
func processReadError(pid int, readErr, probeErr error) error {
	if pid > 0 && errors.Is(probeErr, syscall.ESRCH) {
		// Both sentinels: callers test fs.ErrNotExist (the Linux procfs shape) or syscall.ESRCH.
		return fmt.Errorf("process %d disappeared after %v: %w: %w", pid, readErr, probeErr, fs.ErrNotExist)
	}
	if probeErr != nil && !errors.Is(probeErr, syscall.EPERM) {
		return errors.Join(readErr, fmt.Errorf("probe process %d existence: %w", pid, probeErr))
	}
	return readErr
}

// environWithheld tags a failed kern.procargs2 read (EINVAL for any refusal): unsignalable (EPERM) or
// another effective uid's (setuid) is foreign, as RealProcFS.withheld on Linux; a zombie is gone.
func environWithheld(readErr, probeErr error, identity ProcessIdentity, identityErr error) error {
	switch {
	case errors.Is(probeErr, syscall.EPERM):
		return fmt.Errorf("%w: %w (probe: %w)", ErrForeignProcess, readErr, probeErr)
	case probeErr != nil || identityErr != nil:
		return errors.Join(readErr, identityErr)
	case identity.EffectiveUID != uint32(os.Geteuid()):
		return fmt.Errorf("%w: %w (effective uid %d: %w)",
			ErrForeignProcess, readErr, identity.EffectiveUID, fs.ErrPermission)
	case identity.Zombie:
		return fmt.Errorf("%w (a zombie: %w)", readErr, fs.ErrNotExist)
	}
	return fmt.Errorf("%w (command %q, effective uid %d)", readErr, identity.Command, identity.EffectiveUID)
}
