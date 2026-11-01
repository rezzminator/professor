package engine

import (
	"path/filepath"
	"strings"
	"uuid"
)

// MatchCommand is the one executable matcher for engine registry matchers and process scanners.
// VersionNamed enables
// Claude's native-install convention where argv[0] is a dotted version number.
func MatchCommand(id ID, argv []string, versionNamed bool, binaries ...string) bool {
	if len(argv) == 0 {
		return false
	}
	descriptor := MustLookup(id)
	executable := filepath.ToSlash(argv[0])
	name := filepath.Base(executable)
	if name == descriptor.Binary {
		return true
	}
	for _, hint := range descriptor.BinaryPathHints {
		if strings.Contains(executable, hint) {
			return true
		}
	}
	for _, binary := range binaries {
		if binary != "" && name == filepath.Base(binary) {
			return true
		}
	}
	if !versionNamed {
		return false
	}
	dot := strings.IndexByte(name, '.')
	if dot <= 0 {
		return false
	}
	for _, character := range name[:dot] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// IsUUID reports whether value has the 8-4-4-4-12 hex shape of a Claude
// session id or a Codex thread id — the one spelling every package that
// validates an engine-written id shares.
func IsUUID(value string) bool {
	_, err := uuid.Parse(value)
	return len(value) == 36 && err == nil
}
