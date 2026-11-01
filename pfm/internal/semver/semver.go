// Package semver is pfm's one door to semantic versions, over
// golang.org/x/mod/semver: the vMAJOR.MINOR.PATCH release tags updates and
// installed Claude builds carry, and the prerelease versions engine-sync and
// the update notice compare.
package semver

import (
	"errors"
	"strings"

	modsemver "golang.org/x/mod/semver"
)

// Version is a release version, vMAJOR.MINOR.PATCH, as ParseVersion accepted it.
type Version string

// Less reports whether version orders before other.
func (version Version) Less(other Version) bool {
	return modsemver.Compare(string(version), string(other)) < 0
}

// Valid reports whether v is a full vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]
// version; x/mod alone also accepts the short forms v1 and v1.2.
func Valid(v string) bool {
	return v != "" && modsemver.Canonical(v) == strings.TrimSuffix(v, modsemver.Build(v))
}

// Compare orders two Valid versions by SemVer precedence: -1, 0 or +1.
func Compare(v, w string) int {
	return modsemver.Compare(v, w)
}

// ParseVersion parses a vMAJOR.MINOR.PATCH tag; ok is false for anything else,
// a prerelease or build suffix included.
func ParseVersion(tag string) (Version, bool) {
	tag = strings.TrimSpace(tag)
	if !Valid(tag) || modsemver.Prerelease(tag) != "" || modsemver.Build(tag) != "" {
		return "", false
	}
	return Version(tag), true
}

// SelectHighest returns the highest vMAJOR.MINOR.PATCH tag among tags.
func SelectHighest(tags []string) (string, error) {
	var selected string
	var selectedVersion Version
	for _, tag := range tags {
		version, ok := ParseVersion(tag)
		if ok && (selected == "" || selectedVersion.Less(version)) {
			selected, selectedVersion = tag, version
		}
	}
	if selected == "" {
		return "", errors.New("no semantic-version tags (expected vMAJOR.MINOR.PATCH)")
	}
	return selected, nil
}
