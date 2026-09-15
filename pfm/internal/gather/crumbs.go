package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
)

const maxCrumbNameLength = 128

// ParseCrumbName validates the strict crumb filename grammar.
func ParseCrumbName(name string) (socket, paneID string, ok bool) {
	if name == "" || len(name) > maxCrumbNameLength ||
		strings.ContainsAny(name, "/\x00\n\r\t") {
		return "", "", false
	}

	socket = name
	if marker := strings.LastIndex(name, ".%"); marker >= 0 {
		socket = name[:marker]
		number := name[marker+2:]
		if number == "" || !allDigits(number) {
			return "", "", false
		}
		paneID = "%" + number
	}
	if !validCrumbSocket(socket) {
		return "", "", false
	}
	return socket, paneID, true
}

func validCrumbSocket(socket string) bool {
	if strings.HasPrefix(socket, "cc-new-") {
		name := strings.TrimPrefix(socket, "cc-new-")
		if name == "" {
			return false
		}
		for _, character := range name {
			if (character < 'a' || character > 'z') &&
				(character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') &&
				character != '_' &&
				character != '-' {
				return false
			}
		}
		return true
	}

	parts := strings.Split(socket, "-")
	if len(parts) != 4 {
		return false
	}
	if _, ok := pfmengine.FromSocket(socket); !ok {
		return false
	}
	return allDigits(parts[1]) && allDigits(parts[2]) && allDigits(parts[3])
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// ReadCrumbs reads valid crumbs and removes pane crumbs whose pane vanished.
func ReadCrumbs(sidDir string, panes []Pane) (CrumbProbe, error) {
	return readCrumbs(sidDir, panes, true)
}

// ReadCrumbsReadOnly reads live crumbs but leaves stale pane crumbs in place.
func ReadCrumbsReadOnly(sidDir string, panes []Pane) (CrumbProbe, error) {
	return readCrumbs(sidDir, panes, false)
}

func readCrumbs(sidDir string, panes []Pane, sweep bool) (CrumbProbe, error) {
	var result CrumbProbe
	entries, err := os.ReadDir(sidDir)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read sid directory: %w", err)
	}

	livePanes := make(map[string]struct{}, len(panes))
	for _, pane := range panes {
		livePanes[pane.Socket+"\x00"+pane.PaneID] = struct{}{}
	}

	for _, entry := range entries {
		result.FilesSeen++
		if !entry.Type().IsRegular() {
			result.Ignored++
			continue
		}
		socket, paneID, ok := ParseCrumbName(entry.Name())
		if !ok {
			result.Ignored++
			continue
		}

		path := filepath.Join(sidDir, entry.Name())
		if paneID != "" {
			if _, live := livePanes[socket+"\x00"+paneID]; !live {
				if sweep {
					if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
						return result, fmt.Errorf("remove stale pane crumb %q: %w", entry.Name(), err)
					}
					result.StaleSwept = append(result.StaleSwept, entry.Name())
				}
				continue
			}
		}

		content, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("read crumb %q: %w", entry.Name(), err)
		}
		transcriptPath := strings.TrimRight(string(content), "\r\n")
		if transcriptPath == "" {
			result.Ignored++
			continue
		}
		result.Crumbs = append(result.Crumbs, Crumb{
			Filename:       entry.Name(),
			Socket:         socket,
			PaneID:         paneID,
			TranscriptPath: transcriptPath,
		})
		result.Accepted++
	}

	sort.Slice(result.Crumbs, func(left, right int) bool {
		return result.Crumbs[left].Filename < result.Crumbs[right].Filename
	})
	sort.Strings(result.StaleSwept)
	return result, nil
}
