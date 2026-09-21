//go:build linux

package gather

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type linuxProcFS struct{ RealProcFS }

// nativeProcFS returns the kernel's own process table. On Linux that is /proc,
// augmented with identity metadata that does not require protected argv.
func nativeProcFS() ProcFS { return linuxProcFS{RealProcFS: RealProcFS{}} }

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
