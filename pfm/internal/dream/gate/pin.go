// Package gate implements the dreamer's deterministic mechanical gates.
package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// PinnedPaths is a syntax-checked paths artifact whose digest matches Pin.
// Raw remains available because the pin covers exact bytes, not a re-rendering.
type PinnedPaths struct {
	Paths  []string
	Raw    []byte
	Digest string
}

// Pin validates paths.txt and its one-line SHA-256 pin.
func Pin(pathsRaw, pinRaw []byte) (PinnedPaths, error) {
	if len(pinRaw) == 0 || pinRaw[len(pinRaw)-1] != '\n' {
		return PinnedPaths{}, fmt.Errorf("path pin is not one SHA-256")
	}
	pinLines := splitLines(pinRaw)
	if len(pinLines) != 1 || len(pinLines[0]) != sha256.Size*2 {
		return PinnedPaths{}, fmt.Errorf("path pin is not one SHA-256")
	}
	if _, err := hex.DecodeString(pinLines[0]); err != nil || strings.ToLower(pinLines[0]) != pinLines[0] {
		return PinnedPaths{}, fmt.Errorf("path pin is not one SHA-256")
	}

	paths := splitLines(pathsRaw)
	if len(paths) == 0 {
		return PinnedPaths{}, fmt.Errorf("paths file is empty")
	}
	if pathsRaw[len(pathsRaw)-1] != '\n' {
		return PinnedPaths{}, fmt.Errorf("paths file is not sorted and unique")
	}
	previous := ""
	for index, path := range paths {
		if path == "" || !strings.HasPrefix(path, "/") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return PinnedPaths{}, fmt.Errorf("paths file contains a blank, relative, or control-character path")
		}
		if index > 0 && path <= previous {
			return PinnedPaths{}, fmt.Errorf("paths file is not sorted and unique")
		}
		previous = path
	}
	digestBytes := sha256.Sum256(pathsRaw)
	digest := hex.EncodeToString(digestBytes[:])
	if digest != pinLines[0] {
		return PinnedPaths{}, fmt.Errorf("path pin mismatch: expected %s, got %s", pinLines[0], digest)
	}
	return PinnedPaths{
		Paths:  append([]string(nil), paths...),
		Raw:    append([]byte(nil), pathsRaw...),
		Digest: digest,
	}, nil
}

func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
