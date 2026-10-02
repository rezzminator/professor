package claudelaunch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	mcpFilePrefix = "claude-mcp-"
	mcpFileSuffix = ".json"
	// mcpFileMaxAge bounds how long a launch's MCP file outlives its render;
	// Claude reads it once at startup, so an older file serves no session.
	mcpFileMaxAge = 7 * 24 * time.Hour
)

// writeMCPFile publishes payload as a fresh 0600 file in paths.ClaudeMCPConfigDir (0700) and
// returns its path for --mcp-config. A third-party entry's env values and
// headers ride the payload; on argv /proc/{pid}/cmdline and ps would show them
// to every local user. Files past mcpFileMaxAge are pruned first.
func writeMCPFile(home string, payload []byte) (string, error) {
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home %q is not an absolute path", home)
	}
	dir := paths.ClaudeMCPConfigDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dir, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is not a real directory — run pfm doctor", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure %s: %w", dir, err)
	}
	pruneMCPFiles(dir, clock.Real.Now().Add(-mcpFileMaxAge))
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("name an MCP file in %s: %w", dir, err)
	}
	path := filepath.Join(dir, mcpFilePrefix+hex.EncodeToString(nonce[:])+mcpFileSuffix)
	if err := atomicfile.Write(path, payload, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// pruneMCPFiles removes launch MCP files last written before cutoff. A file it
// cannot judge or remove never blocks the launch: the failure is logged with
// its path and the file stays for the next launch's prune.
func pruneMCPFiles(dir string, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		warnMCPPrune(fmt.Errorf("enumerate %s: %w", dir, err))
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, mcpFilePrefix) || !strings.HasSuffix(name, mcpFileSuffix) {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			warnMCPPrune(fmt.Errorf("inspect %s: %w", path, err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnMCPPrune(fmt.Errorf("remove %s: %w", path, err))
		}
	}
}

func warnMCPPrune(err error) {
	obs.Logger(context.Background()).Warn("claudelaunch: stale MCP file prune failed", obs.FieldErr, err.Error())
}
