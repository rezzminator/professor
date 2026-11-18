package claudeplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadMarketplace reads the .claude-plugin/marketplace.json under a
// marketplace folder; found is false when the file is missing, and a file that
// cannot be read or parsed returns its error with its path.
func ReadMarketplace(location string) (manifest Marketplace, found bool, err error) {
	path := filepath.Join(location, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Marketplace{}, false, nil
	}
	if err != nil {
		return Marketplace{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Marketplace{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return manifest, true, nil
}

// ListedEnabled is the names of market's plugins configDir both enables (as
// name@market) and finds in market's own listing, the marketplace.json under
// the folder known_marketplaces.json records for it; sorted. An unknown
// marketplace or a missing listing lists nothing, so a name the listing does
// not hold is never returned; a record that cannot be read returns its error.
func ListedEnabled(configDir, market string) ([]string, error) {
	enabled, err := EnabledPlugins(configDir)
	if err != nil {
		return nil, err
	}
	_, known, err := ReadKnownMarketplaces(configDir)
	if err != nil {
		return nil, err
	}
	record, isKnown := known[market]
	if !isKnown || record.InstallLocation == "" {
		return nil, nil
	}
	manifest, found, err := ReadMarketplace(record.InstallLocation)
	if err != nil || !found {
		return nil, err
	}
	listed := make(map[string]bool, len(manifest.Plugins))
	for _, entry := range manifest.Plugins {
		listed[entry.Name] = true
	}
	var names []string
	for _, id := range enabled {
		if name, of, _ := strings.Cut(id, "@"); of == market && listed[name] {
			names = append(names, name)
		}
	}
	return names, nil
}
