package claudelaunch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
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

// emptyMCPPayload is the file body of a machine config that registers no server.
const emptyMCPPayload = `{"mcpServers":{}}`

// renderMCPPayload is the --mcp-config file body the machine config asks for,
// nil when it registers no server.
func renderMCPPayload(home string, machine pfmconfig.Config) ([]byte, error) {
	mcp := mcpConfig(home, machine)
	if len(mcp) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(map[string]any{"mcpServers": mcp})
	if err != nil {
		return nil, fmt.Errorf("render --mcp-config: %w", err)
	}
	return encoded, nil
}

// RefreshMCPFiles rewrites, in place, every launch MCP file a recorded Claude
// recipe names under paths.ClaudeMCPConfigDir(home) with the machine config's
// current rendering. A restore reuses a recipe long after pruneMCPFiles removed
// its file, and Claude refuses to start on a missing --mcp-config. The path
// stays, so the started argv still equals the recorded recipe; a path pfm did
// not name (the caller's own --mcp-config) is never touched.
func RefreshMCPFiles(args []string, home string, machine pfmconfig.Config) error {
	if !filepath.IsAbs(home) {
		return fmt.Errorf("home %q is not an absolute path", home)
	}
	dir := paths.ClaudeMCPConfigDir(home)
	var owned []string
	for index := 0; index < len(args); index++ {
		word := args[index]
		if word == "--" {
			break
		}
		value, inline := strings.CutPrefix(word, flagMCPConfig+"=")
		if !inline {
			if word != flagMCPConfig || index+1 >= len(args) {
				continue
			}
			index++
			value = args[index]
		}
		name := filepath.Base(value)
		// A recipe recorded under another spelling of home (a symlink to it) names the same directory.
		inDir := filepath.Dir(value) == dir || paths.PhysicalPath(filepath.Dir(value)) == paths.PhysicalPath(dir)
		if filepath.Clean(value) == value && inDir &&
			strings.HasPrefix(name, mcpFilePrefix) && strings.HasSuffix(name, mcpFileSuffix) {
			owned = append(owned, value)
		}
	}
	if len(owned) == 0 {
		return nil
	}
	payload, err := renderMCPPayload(home, machine)
	if err != nil {
		return err
	}
	if payload == nil {
		payload = []byte(emptyMCPPayload)
	}
	err = paths.EnsurePrivateDir("", dir)
	if errors.Is(err, paths.ErrNotRealDirectory) {
		return fmt.Errorf("%w — run pfm doctor", err)
	}
	if err != nil {
		return err
	}
	for _, path := range owned {
		if err := atomicfile.Write(path, payload, 0o600); err != nil {
			return fmt.Errorf("rewrite launch MCP file %s: %w", path, err)
		}
	}
	return nil
}

// writeMCPFile publishes payload as a fresh 0600 file in paths.ClaudeMCPConfigDir (0700) and
// returns its path for --mcp-config. A third-party entry's env values and
// headers ride the payload; on argv /proc/{pid}/cmdline and ps would show them
// to every local user. Files past mcpFileMaxAge are pruned first.
func writeMCPFile(home string, payload []byte) (string, error) {
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home %q is not an absolute path", home)
	}
	dir := paths.ClaudeMCPConfigDir(home)
	err := paths.EnsurePrivateDir("", dir)
	if errors.Is(err, paths.ErrNotRealDirectory) {
		return "", fmt.Errorf("%w — run pfm doctor", err)
	}
	if err != nil {
		return "", err
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
