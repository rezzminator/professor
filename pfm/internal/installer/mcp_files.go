package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func readMCPFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
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
	latest, present, err := readMCPFile(path)
	if err != nil || present != existed || !bytes.Equal(latest, original) {
		return fmt.Errorf("MCP config changed while planning install: %s; retry", path)
	}
	if existed {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		path = target
		if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
			return err
		}
	}
	return atomicfile.Write(path, wanted, 0o600)
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
