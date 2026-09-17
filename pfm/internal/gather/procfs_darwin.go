//go:build darwin

package gather

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"hostops/pfm/internal/deps"
)

// DarwinProcFS answers the same questions RealProcFS answers, on a kernel that
// has no /proc at all. Every field comes from sysctl, which is why this file
// needs no cgo — the binary must stay CGO_ENABLED=0 to ship static.
//
// One asymmetry is load-bearing and cannot be papered over: macOS hands another
// process's ENVIRONMENT to a same-uid caller only when the target is not a
// SIP-protected platform binary. `/bin/sleep` withholds it; a user-installed
// binary does not — and `claude`, `codex` and `node` are all user-installed, so
// the CLAUDE_CONFIG_DIR that decides a chat's account is readable for exactly
// the processes the fleet cares about. A process whose environment is withheld
// returns an error here and is SKIPPED by the detectors, which is the correct
// outcome: it is not a chat.
type DarwinProcFS struct {
	residentOnce sync.Once
	resident     map[int]int64
	residentErr  error
}

// NewDarwinProcFS returns a process table reader for macOS. It carries a small
// per-instance cache, so callers should keep one for the duration of a scan
// rather than creating one per pid.
func NewDarwinProcFS() *DarwinProcFS { return &DarwinProcFS{} }

// PIDs returns every process the caller can see, in ascending order.
func (proc *DarwinProcFS) PIDs() ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes via kern.proc.all: %w", err)
	}
	pids := make([]int, 0, len(processes))
	for index := range processes {
		if pid := int(processes[index].Proc.P_pid); pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids, nil
}

// Cmdline returns a process's argv.
func (proc *DarwinProcFS) Cmdline(pid int) ([]string, error) {
	argv, _, err := procArgs(pid)
	return argv, err
}

// Environ returns a process's environment keyed before the first equals sign.
func (proc *DarwinProcFS) Environ(pid int) (map[string]string, error) {
	_, env, err := procArgs(pid)
	if err != nil {
		return nil, err
	}
	environment := make(map[string]string, len(env))
	for _, entry := range env {
		if key, value, found := strings.Cut(entry, "="); found {
			environment[key] = value
		}
	}
	return environment, nil
}

// Stat returns the parent pid and a birth stamp.
//
// StartTime is NOT the same unit as Linux's: there it is kernel ticks since
// boot, here it is microseconds since the epoch. Nothing compares the two —
// the field exists to tell two same-pid generations apart — but a future
// consumer that does arithmetic on it must read this comment first.
func (proc *DarwinProcFS) Stat(pid int) (ProcStat, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcStat{}, fmt.Errorf("read kern.proc.pid for %d: %w", pid, err)
	}
	started := process.Proc.P_starttime
	return ProcStat{
		ParentPID: int(process.Eproc.Ppid),
		StartTime: uint64(started.Sec)*1_000_000 + uint64(started.Usec),
	}, nil
}

// Birth returns the process start time in epoch seconds.
func (proc *DarwinProcFS) Birth(pid int) (int64, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("read kern.proc.pid for %d: %w", pid, err)
	}
	return process.Proc.P_starttime.Sec, nil
}

// RSSKB returns a process's resident set size in kilobytes.
//
// The kernel does not carry usable resident memory in kinfo_proc — that lives
// behind task_info, which needs cgo — so this shells out to ps ONCE and keeps
// the whole table. The reaper sums a process subtree, so the alternative was a
// fork per pid; one snapshot is also more honest, because every pid in a
// subtree is then measured at the same instant.
func (proc *DarwinProcFS) RSSKB(pid int) (int64, error) {
	proc.residentOnce.Do(proc.loadResident)
	if proc.residentErr != nil {
		return 0, proc.residentErr
	}
	kilobytes, found := proc.resident[pid]
	if !found {
		return 0, fmt.Errorf("no resident size for pid %d", pid)
	}
	return kilobytes, nil
}

func (proc *DarwinProcFS) loadResident() {
	result, err := deps.RealRunner{}.Run(
		context.Background(),
		[]string{deps.Executable("ps"), "-A", "-o", "pid=,rss="},
		deps.RunOptions{},
	)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		proc.residentErr = fmt.Errorf("sample resident memory via ps: %w", err)
		return
	}
	output := result.Stdout
	proc.resident = make(map[int]int64)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		kilobytes, sizeErr := strconv.ParseInt(fields[1], 10, 64)
		if pidErr == nil && sizeErr == nil {
			proc.resident[pid] = kilobytes
		}
	}
}

// FDLinks returns a process's numeric file descriptors and their targets.
//
// Linux reads /proc/<pid>/fd; macOS keeps that behind libproc, which is cgo, so
// lsof is the only pure-Go route. A missing or failing lsof returns an ERROR
// rather than an empty set: the callers use this to find which rollout or
// transcript a live chat holds open, and "no open files" is a claim, not a
// shrug — reporting it falsely would let archive evict a file still in use.
func (proc *DarwinProcFS) FDLinks(pid int) ([]FDLink, error) {
	result, err := deps.RealRunner{}.Run(
		context.Background(),
		[]string{deps.Executable("lsof"), "-w", "-n", "-P", "-p", strconv.Itoa(pid), "-F", "fn"},
		deps.RunOptions{},
	)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	output := result.Stdout
	if err != nil {
		// lsof exits non-zero when a pid owns no matching files at all, and it
		// still prints what it found. Only an empty result alongside an error is
		// a probe that could not run.
		if len(output) == 0 {
			return nil, fmt.Errorf("list open files for pid %d via lsof: %w", pid, err)
		}
	}
	links := make([]FDLink, 0)
	currentFD := -1
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'f':
			// Non-numeric descriptors (cwd, txt, mem) have no /proc/<pid>/fd
			// equivalent and are deliberately dropped.
			if fd, convErr := strconv.Atoi(line[1:]); convErr == nil && fd >= 0 {
				currentFD = fd
			} else {
				currentFD = -1
			}
		case 'n':
			if currentFD >= 0 {
				links = append(links, FDLink{FD: currentFD, Target: line[1:]})
				currentFD = -1
			}
		}
	}
	sort.Slice(links, func(left, right int) bool { return links[left].FD < links[right].FD })
	return links, nil
}

// Image is the file a process executes, read from lsof's program-text entry:
// macOS keeps the executable's vnode behind libproc (cgo), and lsof lists the
// executable as the FIRST txt file. Its device prints in hex; its inode is
// the one the process still holds after an install renamed a new file over
// the path. A missing entry is an error, never a zero identity.
func (proc *DarwinProcFS) Image(pid int) (FileID, error) {
	result, err := deps.RealRunner{}.Run(
		context.Background(),
		[]string{deps.Executable("lsof"), "-w", "-n", "-P", "-a", "-p", strconv.Itoa(pid), "-d", "txt", "-F", "Di"},
		deps.RunOptions{},
	)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	output := result.Stdout
	if err != nil && len(output) == 0 {
		return FileID{}, fmt.Errorf("read the executable of pid %d via lsof: %w", pid, err)
	}
	var id FileID
	var sawDevice, sawInode bool
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'D':
			if sawDevice {
				continue
			}
			device, parseErr := strconv.ParseUint(strings.TrimPrefix(line[1:], "0x"), 16, 64)
			if parseErr != nil {
				return FileID{}, fmt.Errorf("parse lsof device %q for pid %d: %w", line[1:], pid, parseErr)
			}
			id.Device, sawDevice = device, true
		case 'i':
			if sawInode {
				continue
			}
			inode, parseErr := strconv.ParseUint(line[1:], 10, 64)
			if parseErr != nil {
				return FileID{}, fmt.Errorf("parse lsof inode %q for pid %d: %w", line[1:], pid, parseErr)
			}
			id.Inode, sawInode = inode, true
		}
		if sawDevice && sawInode {
			return id, nil
		}
	}
	return FileID{}, fmt.Errorf("lsof listed no executable for pid %d", pid)
}

// procArgs decodes one process's argv and environment from kern.procargs2.
//
// The buffer layout is Apple's, and the order matters:
//
//	int32 argc | exec_path NUL | NUL padding | argc×(argv NUL) | env NUL...
//
// argv entries may legitimately be EMPTY strings, so the argc count is what
// separates argv from the environment — skipping empty fields to find the
// boundary would silently promote an environment variable into argv.
func procArgs(pid int) (argv, env []string, err error) {
	buffer, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, nil, fmt.Errorf("read kern.procargs2 for %d: %w", pid, err)
	}
	if len(buffer) < 4 {
		return nil, nil, fmt.Errorf("short kern.procargs2 buffer for %d: %d bytes", pid, len(buffer))
	}
	argc := int(binary.LittleEndian.Uint32(buffer[:4]))
	if argc < 0 {
		return nil, nil, fmt.Errorf("negative argc for %d", pid)
	}
	fields := strings.Split(string(buffer[4:]), "\x00")

	index := 0
	if index < len(fields) {
		index++ // exec_path, which argv[0] repeats
	}
	for index < len(fields) && fields[index] == "" {
		index++ // alignment padding between exec_path and argv
	}
	for counted := 0; counted < argc && index < len(fields); counted++ {
		argv = append(argv, fields[index])
		index++
	}
	for ; index < len(fields); index++ {
		if fields[index] == "" {
			break // the terminator that ends the environment
		}
		env = append(env, fields[index])
	}
	return argv, env, nil
}
