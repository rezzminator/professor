//go:build linux

package gather

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type linuxProcFS struct{ RealProcFS }

// nativeProcFS returns the kernel's own process table. On Linux that is /proc,
// augmented with identity metadata that does not require protected argv.
func nativeProcFS() ProcFS { return linuxProcFS{RealProcFS: RealProcFS{}} }

// The host readers' native source on Linux is the same proc tree a jail
// fakes, read at the system root.
func nativeHostMemory() (Memory, error) { return readLinuxMemory(paths.SystemProcRoot()) }

func nativeHostCPU() (CPU, error) { return readLinuxCPU(paths.SystemProcRoot()) }

func nativeHostCPUPressure() (float64, error) { return readLinuxCPUPressure(paths.SystemProcRoot()) }

func nativeProcessCounters() (map[int]ProcessCounter, []string, error) {
	return readLinuxProcessCounters(paths.SystemProcRoot())
}

func nativeUnixSockets() ([]string, error) { return readLinuxUnixSockets(paths.SystemProcRoot()) }

// Started reads a process's start tick from /proc/<pid>/stat and places it on
// the wall clock through CLOCK_BOOTTIME, the clock the tick counts from: btime
// is whole seconds, too coarse to order a hook against an engine's own
// millisecond timestamps.
func (proc linuxProcFS) Started(pid int) (time.Time, error) {
	stat, err := proc.Stat(pid)
	if err != nil {
		return time.Time{}, err
	}
	var boot unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &boot); err != nil {
		return time.Time{}, fmt.Errorf("read the boot clock for pid %d's start: %w", pid, err)
	}
	now := clock.Real.Now()
	age := time.Duration(boot.Nano()) - time.Duration(stat.StartTime)*(time.Second/userHZ)
	return now.Add(-age), nil
}

// ProcessIdentity reads the short command name and effective uid from status.
func (proc linuxProcFS) ProcessIdentity(pid int) (ProcessIdentity, error) {
	path := proc.path(pid, "status")
	content, err := os.ReadFile(path)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read process identity for pid %d from %s: %w", pid, path, err)
	}
	var identity ProcessIdentity
	foundName := false
	foundUID := false
	for _, line := range strings.Split(string(content), "\n") {
		if value, found := strings.CutPrefix(line, "State:"); found {
			fields := strings.Fields(value)
			identity.Zombie = len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
		}
		if value, found := strings.CutPrefix(line, "Name:"); found {
			identity.Command = strings.TrimSpace(value)
			foundName = identity.Command != ""
			continue
		}
		if value, found := strings.CutPrefix(line, "Uid:"); found {
			fields := strings.Fields(value)
			if len(fields) < 2 {
				return ProcessIdentity{}, fmt.Errorf(
					"parse effective uid for pid %d from %s: malformed Uid field",
					pid,
					path,
				)
			}
			effectiveUID, parseErr := strconv.ParseUint(fields[1], 10, 32)
			if parseErr != nil {
				return ProcessIdentity{}, fmt.Errorf("parse effective uid for pid %d from %s: %w", pid, path, parseErr)
			}
			identity.EffectiveUID = uint32(effectiveUID)
			foundUID = true
		}
	}
	if !foundName {
		return ProcessIdentity{}, fmt.Errorf("read process identity for pid %d from %s: missing Name field", pid, path)
	}
	if !foundUID {
		return ProcessIdentity{}, fmt.Errorf("read process identity for pid %d from %s: missing Uid field", pid, path)
	}
	return identity, nil
}
