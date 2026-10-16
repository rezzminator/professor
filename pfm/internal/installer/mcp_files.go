package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func readMCPFile(path string) ([]byte, bool, error) {
	raw, _, err := readRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("MCP config is a dangling symlink: %s", path)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, false, statErr
		}
		return nil, false, nil
	}
	return raw, err == nil, err
}

// changeMCPFile reports one writeMCPFile write.
func (installer *engine) changeMCPFile(message, path string, original, wanted []byte, existed bool) error {
	return installer.change(message, func() error {
		return installer.writeMCPFile(path, original, wanted, existed)
	})
}

// writeMCPFile rechecks the planned preimage before preserving a backup and
// atomically writing the physical file. Native clients may update their registry.
func (installer *engine) writeMCPFile(path string, original, wanted []byte, existed bool) error {
	return installer.writeMCPFileIf(path, original, wanted, existed, nil)
}

// writeMCPFileIf checks the native lock before reading and immediately before
// atomic publication, so losing it during a slow backup cannot publish a save.
func (installer *engine) writeMCPFileIf(path string, original, wanted []byte, existed bool, check func() error) error {
	return installer.writeRegistryFileIf(path, original, wanted, existed, 0o600, check)
}

// writeRegistryFileIf shares the guarded registry save while retaining the caller's file permissions.
func (installer *engine) writeRegistryFileIf(
	path string,
	original, wanted []byte,
	existed bool,
	mode fs.FileMode,
	check func() error,
) error {
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	latest, present, err := readMCPFile(path)
	if err != nil {
		return fmt.Errorf("read MCP config %s before write: %w", path, err)
	}
	if present != existed || !bytes.Equal(latest, original) {
		return fmt.Errorf("MCP config changed while planning install: %s; retry", path)
	}
	if existed {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		path = target
		if err := copyBackup(path, availableBackup(path, installer.stamp), check); err != nil {
			return err
		}
	}
	return atomicfile.WriteIf(path, wanted, mode, func() error {
		// Refuse a lost lock before touching a potentially replaced registry.
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		latest, present, err := readMCPFile(path)
		if err != nil {
			return fmt.Errorf("recheck MCP config %s before write: %w", path, err)
		}
		if present != existed || !bytes.Equal(latest, original) {
			return fmt.Errorf("MCP config changed while planning install: %s; retry", path)
		}
		if check != nil {
			return check()
		}
		return nil
	})
}

// withClaudeMCPLocks interoperates with Claude's proper-lockfile protocol:
// mkdir <config>.lock, refresh mtime every five seconds (ten-second stale
// threshold), then rmdir. Reclaim stale empty directories just as Claude does.
// Explicit lockfilePath in Claude preserves file aliases; resolve parents only.
func withClaudeMCPLocks(paths []string, write func(check func() error) error) (result error) {
	unique := map[string]bool{}
	for _, path := range paths {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("resolve Claude MCP lock parent %s: %w", path, err)
		}
		unique[filepath.Join(parent, filepath.Base(path))+".lock"] = true
	}
	var names []string
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	owned := map[string]os.FileInfo{}
	defer func() {
		for name, original := range owned {
			current, err := os.Lstat(name)
			if err == nil && (!os.SameFile(original, current) || !original.ModTime().Equal(current.ModTime())) {
				err = fmt.Errorf("lock replaced or modified; kept replacement")
			}
			if err == nil {
				err = os.Remove(name)
			}
			if err != nil {
				result = errors.Join(result, fmt.Errorf("release Claude MCP lock %s: %w", name, err))
			}
		}
	}()
	for _, name := range names {
		if err := acquireClaudeMCPLock(name); err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return fmt.Errorf("inspect acquired Claude MCP lock %s: %w", name, err)
		}
		owned[name] = info
	}
	var mutex sync.Mutex
	var compromised error
	validate := func(refresh *time.Time) error {
		mutex.Lock()
		defer mutex.Unlock()
		if compromised != nil {
			return compromised
		}
		for name, original := range owned {
			if clock.Real.Now().Sub(original.ModTime()) > claudeMCPLockStale {
				compromised = fmt.Errorf("validate Claude MCP lock %s: ownership exceeded native stale window", name)
				return compromised
			}
			current, err := os.Lstat(name)
			if err == nil && (!os.SameFile(original, current) || !original.ModTime().Equal(current.ModTime())) {
				err = fmt.Errorf("lock replaced or modified")
			}
			if err == nil && refresh != nil {
				err = os.Chtimes(name, *refresh, *refresh)
				if err == nil {
					var updated os.FileInfo
					updated, err = os.Lstat(name)
					if err == nil && !os.SameFile(original, updated) {
						err = fmt.Errorf("lock replaced during refresh")
					}
					if err == nil && clock.Real.Now().Sub(original.ModTime()) > claudeMCPLockStale {
						err = fmt.Errorf("refresh exceeded native stale window")
					}
					if err == nil {
						owned[name] = updated
					}
				}
			}
			if err != nil {
				compromised = fmt.Errorf("validate Claude MCP lock %s: %w", name, err)
				return compromised
			}
		}
		return nil
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := clock.Real.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- validate(nil)
				return
			case now := <-ticker.C():
				if err := validate(&now); err != nil {
					done <- err
					return
				}
			}
		}
	}()
	defer func() {
		close(stop)
		result = errors.Join(result, <-done)
	}()
	return write(func() error { return validate(nil) })
}

// Claude's proper-lockfile default is stale: 10000 milliseconds; its stale
// takeover is rmdir followed by a single mkdir, never recursive removal.
const claudeMCPLockStale = 10 * time.Second

func acquireClaudeMCPLock(name string) error {
	if err := os.Mkdir(name, 0o700); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("acquire Claude MCP lock %s: %w", name, err)
	}
	info, err := os.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// The native owner released it after mkdir failed; retry only once.
	case err != nil:
		return fmt.Errorf("inspect Claude MCP lock %s: %w", name, err)
	default:
		if !info.ModTime().Before(clock.Real.Now().Add(-claudeMCPLockStale)) {
			return fmt.Errorf(
				"acquire Claude MCP lock %s: native lock is active; retry after the writer releases it",
				name,
			)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("reclaim stale Claude MCP lock %s: not a lock directory", name)
		}
		if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("reclaim stale Claude MCP lock %s: %w", name, err)
		}
	}
	if err := os.Mkdir(name, 0o700); err != nil {
		return fmt.Errorf("acquire Claude MCP lock %s after stale-lock retry: %w", name, err)
	}
	return nil
}

// enabledMCPNames lists one registration per enabled family.
func enabledMCPNames(servers map[string]bool) []string {
	var names []string
	for _, family := range []string{chatName, mcpServerHarvester} {
		if servers[family] {
			names = append(names, pfmconfig.MCPRegistrationName(family))
		}
	}
	return names
}

func mcpFamilyStdioArgs(name string) []string {
	return append(append([]string(nil), mcpStdioArgs...), "--family", strings.TrimSuffix(name, "-professor"))
}

func isFamilyRegistration(name string) bool {
	return name == pfmconfig.MCPRegistrationName(chatName) || name == pfmconfig.MCPRegistrationName(mcpServerHarvester)
}
