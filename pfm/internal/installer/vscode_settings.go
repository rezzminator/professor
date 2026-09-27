package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func (installer *engine) vscodeSettingsWritePaths(path string) ([]string, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return []string{path}, nil
	} else if err != nil {
		return nil, err
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve VS Code settings %s: %w", path, err)
	}
	return []string{physical, availableBackup(physical, installer.stamp)}, nil
}

// writeVSCodeSettings backs up and atomically rewrites a VS Code settings file
// THROUGH any symlink, as writeMCPFile does for a linked MCP registry. Dotfile
// managers link settings.json into a repository; renaming over the link would
// sever it, leaving VS Code on a detached copy the managed file never sees. A
// file that does not exist yet is created at path with owner-only access.
func (installer *engine) writeVSCodeSettings(path string, content []byte) error {
	physical, mode := path, fs.FileMode(0o600)
	info, err := os.Stat(path)
	switch {
	case err == nil:
		mode = info.Mode().Perm()
		if physical, err = filepath.EvalSymlinks(path); err != nil {
			return fmt.Errorf("resolve VS Code settings %s: %w", path, err)
		}
		if err := copyBackup(physical, availableBackup(physical, installer.stamp)); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return atomicfile.Write(physical, content, mode)
}

func (installer *engine) writeVSCodeOwnership(
	path string,
	existing []byte,
	ownership map[string]vscodeOwnershipRecord,
	extensions, indexRegistrations []string,
) error {
	if len(ownership) == 0 && len(extensions) == 0 && len(indexRegistrations) == 0 {
		if len(existing) == 0 {
			return nil
		}
		return installer.changePaths("remove "+path, []string{path}, func() error { return os.Remove(path) })
	}
	document := vscodeOwnershipDocument{Version: vscodeOwnershipVersion}
	for _, record := range ownership {
		document.Files = append(document.Files, record)
	}
	sort.Slice(document.Files, func(i, j int) bool { return document.Files[i].Path < document.Files[j].Path })
	if len(extensions) != 0 {
		document.Extensions = append([]string(nil), extensions...)
		sort.Strings(document.Extensions)
	}
	if len(indexRegistrations) != 0 {
		document.IndexRegistrations = append([]string(nil), indexRegistrations...)
		sort.Strings(document.IndexRegistrations)
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if bytes.Equal(existing, encoded) && sameFile(path, encoded, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.changePaths("write "+path, []string{path}, func() error {
		return atomicfile.Write(path, encoded, 0o600)
	})
}

func readVSCodeOwnership(path string) (map[string]vscodeOwnershipRecord, []string, []string, []byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]vscodeOwnershipRecord{}, nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var document vscodeOwnershipDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, nil, nil, nil, err
	}
	if document.Version != vscodeOwnershipVersion {
		return nil, nil, nil, nil, fmt.Errorf("unsupported version %d", document.Version)
	}
	records := make(map[string]vscodeOwnershipRecord, len(document.Files))
	for _, record := range document.Files {
		if !filepath.IsAbs(record.Path) ||
			(record.Platform != vscodePlatformLinux && record.Platform != vscodePlatformOSX) {
			return nil, nil, nil, nil, fmt.Errorf("invalid record path/platform %q/%q", record.Path, record.Platform)
		}
		if _, duplicate := records[record.Path]; duplicate {
			return nil, nil, nil, nil, fmt.Errorf("duplicate record %s", record.Path)
		}
		records[record.Path] = record
	}
	extensions := make([]string, 0, len(document.Extensions))
	seenExtensions := make(map[string]bool, len(document.Extensions))
	for _, extension := range document.Extensions {
		if !filepath.IsAbs(extension) {
			return nil, nil, nil, nil, fmt.Errorf("invalid extension link path %q", extension)
		}
		if seenExtensions[extension] {
			return nil, nil, nil, nil, fmt.Errorf("duplicate extension link %s", extension)
		}
		seenExtensions[extension] = true
		extensions = append(extensions, extension)
	}
	indexRegistrations := make([]string, 0, len(document.IndexRegistrations))
	seenIndexes := make(map[string]bool, len(document.IndexRegistrations))
	for _, indexPath := range document.IndexRegistrations {
		if !filepath.IsAbs(indexPath) {
			return nil, nil, nil, nil, fmt.Errorf("invalid index registration path %q", indexPath)
		}
		if seenIndexes[indexPath] {
			return nil, nil, nil, nil, fmt.Errorf("duplicate index registration %s", indexPath)
		}
		seenIndexes[indexPath] = true
		indexRegistrations = append(indexRegistrations, indexPath)
	}
	return records, extensions, indexRegistrations, raw, nil
}
