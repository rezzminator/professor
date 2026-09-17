// Package semver parses the vMAJOR.MINOR.PATCH versions shared by updates and
// installed Claude builds.
package semver

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Version is a parsed vMAJOR.MINOR.PATCH version.
type Version struct {
	major int
	minor int
	patch int
}

// Less reports whether version orders before other.
func (version Version) Less(other Version) bool {
	if version.major != other.major {
		return version.major < other.major
	}
	if version.minor != other.minor {
		return version.minor < other.minor
	}
	return version.patch < other.patch
}

// ParseVersion parses a vMAJOR.MINOR.PATCH tag; ok is false for anything else.
func ParseVersion(tag string) (Version, bool) {
	parts := strings.Split(strings.TrimSpace(tag), ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "v") {
		return Version{}, false
	}
	major, err := strconv.Atoi(strings.TrimPrefix(parts[0], "v"))
	if err != nil || major < 0 {
		return Version{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return Version{}, false
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil || patch < 0 {
		return Version{}, false
	}
	return Version{major: major, minor: minor, patch: patch}, true
}

// SelectHighest returns the highest vMAJOR.MINOR.PATCH tag among tags.
func SelectHighest(tags []string) (string, error) {
	ordered := append([]string(nil), tags...)
	sort.Strings(ordered)
	var selected string
	var selectedVersion Version
	for _, tag := range ordered {
		version, ok := ParseVersion(tag)
		if !ok {
			continue
		}
		if selected == "" || selectedVersion.Less(version) {
			selected, selectedVersion = tag, version
		}
	}
	if selected == "" {
		return "", errors.New("no semantic-version tags (expected vMAJOR.MINOR.PATCH)")
	}
	return selected, nil
}
