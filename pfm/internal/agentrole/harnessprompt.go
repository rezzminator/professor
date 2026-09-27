package agentrole

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// A harness prompt is a Claude system-prompt file a seat was launched on in
// place of the staged fleet prompt (`pfm chat new --harness-prompt`). The seat
// keeps a record naming that file beside its role prompt, so `pfm chat reload`
// reboots it on the same file instead of silently falling back to the staged
// one. The record holds the path, not a copy: an edit to the file reaches the
// seat at its next reload.
const (
	harnessRecordPrefix = "harness-prompt-"
	harnessRecordSuffix = ".path"
)

// HarnessPromptRecordPath is the record file for one socket/pane identity,
// keyed exactly as SeatPromptPath keys the role prompt.
func HarnessPromptRecordPath(sidDir, socket, pane string) (string, error) {
	seatPath, err := SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		return "", err
	}
	return harnessRecordFor(seatPath), nil
}

// harnessRecordFor maps a canonical role-prompt path to its sibling record.
func harnessRecordFor(seatPath string) string {
	identity := strings.TrimSuffix(
		strings.TrimPrefix(filepath.Base(seatPath), seatPromptFilePrefix),
		seatPromptFileSuffix,
	)
	return filepath.Join(filepath.Dir(seatPath), harnessRecordPrefix+identity+harnessRecordSuffix)
}

// IsHarnessPromptRecordPath reports whether path has the canonical record
// filename.
func IsHarnessPromptRecordPath(path string) bool {
	base := filepath.Base(path)
	identity := strings.TrimSuffix(strings.TrimPrefix(base, harnessRecordPrefix), harnessRecordSuffix)
	return strings.HasPrefix(base, harnessRecordPrefix) &&
		strings.HasSuffix(base, harnessRecordSuffix) && identity != ""
}

// LoadHarnessPrompt resolves path against the current directory and returns
// the absolute path with the file's body. A missing file, a directory and a
// file with no content are refusals: a seat launched on nothing would run on
// Claude's own prompt while its caller believes otherwise.
func LoadHarnessPrompt(path string) (absolute, body string, err error) {
	absolute, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("harness prompt %s: resolve absolute path: %w", path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", "", fmt.Errorf("harness prompt %s: %w", absolute, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("harness prompt %s is a directory, want a prompt file", absolute)
	}
	raw, err := os.ReadFile(absolute)
	if err != nil {
		return "", "", fmt.Errorf("harness prompt %s: %w", absolute, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", "", fmt.Errorf("harness prompt %s is empty", absolute)
	}
	return absolute, string(raw), nil
}

// LoadHarnessPromptFor is LoadHarnessPrompt for one launch: an empty path is
// no harness prompt, and only Claude takes a whole system prompt from a file.
// A harness prompt stands in for the staged fleet prompt, so a role seat born
// on one does not need the professor policy that stages it.
func LoadHarnessPromptFor(engineID pfmengine.ID, path string) (absolute, body string, err error) {
	if path == "" {
		return "", "", nil
	}
	if engineID != pfmengine.Claude {
		return "", "", fmt.Errorf(
			"--harness-prompt is supported for claude only (engine %s): it replaces Claude's --system-prompt-file, "+
				"and no other engine takes a whole system prompt from a file",
			engineID,
		)
	}
	return LoadHarnessPrompt(path)
}

// WriteHarnessPromptRecord atomically records the absolute prompt path a seat
// was launched on; an empty path records nothing.
func WriteHarnessPromptRecord(sidDir, socket, pane, promptPath string) error {
	if promptPath == "" {
		return nil
	}
	if !filepath.IsAbs(promptPath) {
		return fmt.Errorf("harness prompt record: %q is not an absolute path", promptPath)
	}
	path, err := HarnessPromptRecordPath(sidDir, socket, pane)
	if err != nil {
		return err
	}
	return writeSeatPromptFile(sidDir, path, promptPath+"\n")
}

// ReadHarnessPromptRecord returns the recorded prompt path, preferring the
// pane-specific record and falling back to the bare socket's, as
// ReadSeatPrompt does. found is false only when no record exists; an
// unreadable or malformed record is an error.
func ReadHarnessPromptRecord(sidDir, socket, pane string) (promptPath string, found bool, err error) {
	candidates, err := seatPromptCandidates(sidDir, socket, pane)
	if err != nil {
		return "", false, err
	}
	for _, seatPath := range candidates {
		record := harnessRecordFor(seatPath)
		raw, readErr := os.ReadFile(record)
		if errors.Is(readErr, fs.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return "", true, fmt.Errorf("harness prompt record %s: %w", record, readErr)
		}
		promptPath = strings.TrimSpace(string(raw))
		if !filepath.IsAbs(promptPath) {
			return "", true, fmt.Errorf("harness prompt record %s: %q is not an absolute path", record, promptPath)
		}
		return promptPath, true, nil
	}
	return "", false, nil
}
