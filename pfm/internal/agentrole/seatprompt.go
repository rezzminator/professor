package agentrole

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// ValidateSeatPromptPolicy refuses engines that cannot carry the role and a
// Claude policy that does not stage the fleet prompt composition needs.
func ValidateSeatPromptPolicy(engineID pfmengine.ID, claudePolicy string) error {
	switch engineID {
	case pfmengine.OpenCode:
		return errors.New(
			"--agent-role is not supported for opencode: pfm cannot launch an OpenCode seat, so no prompt channel exists to carry a role",
		)
	case pfmengine.Claude:
		if claudePolicy == "" {
			claudePolicy = pfmconfig.SystemPromptProduction
		}
		if claudePolicy != pfmconfig.SystemPromptProfessor {
			return fmt.Errorf(
				"--agent-role needs claude.systemPrompt=professor (current: %s): "+
					"the role prompt is composed onto the staged fleet prompt, which that policy does not stage",
				claudePolicy,
			)
		}
	}
	return nil
}

const (
	seatPromptPrefix     = "<!-- pfm agent-role: "
	seatPromptSuffix     = " -->"
	seatPromptSeparator  = "\n\n---\n\n"
	seatPromptFilePrefix = "role-prompt-"
	seatPromptFileSuffix = ".md"
)

// SeatPromptPath returns the self-describing prompt file for one immutable
// socket/pane identity. A socket path is accepted, but only its basename is
// part of the durable key.
func SeatPromptPath(sidDir, socket, pane string) (string, error) {
	if strings.TrimSpace(socket) == "" {
		return "", errors.New("agent role: seat prompt socket identity is empty")
	}
	socket = filepath.Base(socket)
	if socket == "." || socket == ".." || socket == string(filepath.Separator) {
		return "", fmt.Errorf("agent role: seat prompt socket identity %q is invalid", socket)
	}
	identity := socket
	if pane != "" {
		identity += "-" + pane
	}
	candidate := filepath.Join(sidDir, seatPromptFilePrefix+identity+seatPromptFileSuffix)
	if strings.Contains(pane, string(filepath.Separator)) {
		return "", fmt.Errorf("agent role: seat prompt path escapes SIDDir: %s", candidate)
	}
	if err := validateSeatPromptCandidate(sidDir, candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func validateSeatPromptCandidate(sidDir, candidate string) error {
	rel, err := filepath.Rel(sidDir, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("agent role: seat prompt path escapes SIDDir: %s", candidate)
	}
	return nil
}

// IsSeatPromptPath reports whether path has the canonical role-seat filename.
func IsSeatPromptPath(path string) bool {
	base := filepath.Base(path)
	identity := strings.TrimSuffix(strings.TrimPrefix(base, seatPromptFilePrefix), seatPromptFileSuffix)
	return strings.HasPrefix(base, seatPromptFilePrefix) &&
		strings.HasSuffix(base, seatPromptFileSuffix) && identity != ""
}

// ComposeSeatPrompt builds the file body and the engine prompt value it
// contains. Claude needs the staged fleet prompt before the role body; a
// Codex constitution already carries both (readTOMLConstitution) and must not
// receive the fleet prompt twice.
func ComposeSeatPrompt(engineID pfmengine.ID, role, constitution, stagedFleetPrompt string) (string, error) {
	marker := seatPromptPrefix + role + seatPromptSuffix + "\n"
	switch engineID {
	case pfmengine.Claude:
		return marker + stagedFleetPrompt + seatPromptSeparator + constitution, nil
	case pfmengine.Codex:
		return marker + constitution, nil
	case pfmengine.OpenCode:
		return "", errors.New("agent role: opencode has no role prompt channel")
	default:
		return "", fmt.Errorf("agent role: engine %q has no role prompt channel", engineID)
	}
}

// WriteSeatPrompt atomically publishes one composed role prompt.
func WriteSeatPrompt(sidDir, socket, pane, body string) error {
	path, err := SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		return err
	}
	return writeSeatPromptFile(sidDir, path, body)
}

func writeSeatPromptFile(sidDir, path, body string) error {
	if err := validateSeatPromptCandidate(sidDir, path); err != nil {
		return err
	}
	if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("agent role: write seat prompt %s: %w", path, err)
	}
	return nil
}

// ReadSeatPrompt returns the marker's role and every byte after its first
// line. found distinguishes a missing prompt from one that exists but cannot
// be read or parsed.
func ReadSeatPrompt(sidDir, socket, pane string) (role, prompt, path string, found bool, err error) {
	paths, err := seatPromptCandidates(sidDir, socket, pane)
	if err != nil {
		return "", "", "", false, err
	}
	for _, candidate := range paths {
		role, prompt, found, err = parseSeatPromptFile(candidate)
		if found || err != nil {
			return role, prompt, candidate, found, err
		}
	}
	return "", "", "", false, nil
}

// ReadSeatPromptFile reads one exact canonical file. It is used by doctor,
// whose source of truth is the path observed in a live process's argv.
func ReadSeatPromptFile(path string) (role, prompt string, found bool, err error) {
	if !IsSeatPromptPath(path) {
		return "", "", false, fmt.Errorf("agent role: seat prompt path is not canonical: %s", path)
	}
	return parseSeatPromptFile(path)
}

func parseSeatPromptFile(path string) (role, prompt string, found bool, err error) {
	raw, readErr := os.ReadFile(path)
	if errors.Is(readErr, fs.ErrNotExist) {
		return "", "", false, nil
	}
	if readErr != nil {
		return "", "", true, fmt.Errorf("agent role: read seat prompt %s: %w", path, readErr)
	}
	first, rest, hasPrompt := strings.Cut(string(raw), "\n")
	if !strings.HasPrefix(first, seatPromptPrefix) || !strings.HasSuffix(first, seatPromptSuffix) {
		return "", "", true, fmt.Errorf("agent role: seat prompt %s has no valid role marker", path)
	}
	role = strings.TrimSuffix(strings.TrimPrefix(first, seatPromptPrefix), seatPromptSuffix)
	if role == "" {
		return "", "", true, fmt.Errorf("agent role: seat prompt %s has an empty role marker", path)
	}
	if !hasPrompt {
		return "", "", true, fmt.Errorf("agent role: seat prompt %s has no prompt after its role marker", path)
	}
	return role, rest, true, nil
}

func seatPromptCandidates(sidDir, socket, pane string) ([]string, error) {
	bare, err := SeatPromptPath(sidDir, socket, "")
	if err != nil {
		return nil, err
	}
	if pane == "" {
		return []string{bare}, nil
	}
	specific, err := SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		return nil, err
	}
	return []string{specific, bare}, nil
}

// RemoveSeatPrompt removes the pane-specific prompt and its bare-socket
// fallback. Absence is already the desired end state.
func RemoveSeatPrompt(sidDir, socket, pane string) error {
	paths, err := seatPromptCandidates(sidDir, socket, pane)
	if err != nil {
		return err
	}
	var removeErrs []error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removeErrs = append(removeErrs, fmt.Errorf("agent role: remove seat prompt %s: %w", path, err))
		}
	}
	return errors.Join(removeErrs...)
}
