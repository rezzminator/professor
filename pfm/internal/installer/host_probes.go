package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ManagedRoot is the install ownership ledger directory.
func ManagedRoot(home string) string { return managedRootForHome(home) }

// PFMSettingsLeftovers reports only settings owned by pfm, through links.
func PFMSettingsLeftovers(home, path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	ownershipPath := settingsHookOwnershipPath(ManagedRoot(home))
	ownership, _, ledgerErr := readSettingsHookOwnership(ownershipPath)
	if ledgerErr != nil {
		return nil, probePathError(ownershipPath, ledgerErr)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	names, err := accountSettingsLeftovers(raw, home, ownership[physicalSettingsPath(path)], true)
	if err != nil {
		return nil, probePathError(path, err)
	}
	return names, nil
}

// PFMMCPLeftovers judges a Claude registry by the ledger and pfm's shapes.
func PFMMCPLeftovers(home string, port int, path string) ([]string, error) {
	ledger := filepath.Join(ManagedRoot(home), mcpOwnershipName)
	ownership, err := readMCPOwnership(ledger)
	if err != nil {
		return nil, probePathError(ledger, err)
	}
	owned := ledgerOwnedMCP(ownership.Registrations, physicalSettingsPath(path))
	return probeMCPFile(home, port, path, owned)
}

// PFMHomeMCPLeftovers also reports the retired ledger clients list.
func PFMHomeMCPLeftovers(home string, port int) (names, clients []string, err error) {
	ledger := filepath.Join(ManagedRoot(home), mcpOwnershipName)
	ownership, err := readMCPOwnership(ledger)
	if err != nil {
		return nil, nil, probePathError(ledger, err)
	}
	names, err = probeMCPFile(home, port, filepath.Join(home, ".mcp.json"), nil)
	return names, ownership.Clients, err
}

func probeMCPFile(home string, port int, path string, owned map[string][]any) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read MCP registry %s: %w", path, err)
	}
	names, err := accountMCPLeftovers(raw, owned, func(name string, registration map[string]any) bool {
		return pfmClaudeMCPShape(name, registration, home, port)
	})
	if err != nil {
		return nil, probePathError(path, err)
	}
	return names, nil
}

func probePathError(path string, err error) error {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		err = &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return fmt.Errorf("probe %s: %w", path, err)
}

// RetiredMemoryHelper recognises only the pinned historical helper bytes.
func RetiredMemoryHelper(path string) (newName string, matched bool, err error) {
	for _, helper := range retiredMemoryHelpers {
		if filepath.Base(path) != helper.oldName {
			continue
		}
		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read retired helper %s: %w", path, err)
		}
		fingerprint, err := normalizedMemoryHelperFingerprint(content)
		if err != nil {
			return "", false, nil
		}
		return helper.newName, fingerprint == helper.normalizedSHA256, nil
	}
	return "", false, nil
}

// IsStagedShimLine matches fleet sources outside the clone's canonical shim.
func IsStagedShimLine(line string) bool {
	if !isFleetSourceLine(line) {
		return false
	}
	for _, field := range strings.Fields(line) {
		path := strings.Trim(field, "\"';()")
		if strings.HasSuffix(path, "pfm/internal/installer/assets/shim/pfm.zsh") {
			return false
		}
	}
	return true
}
