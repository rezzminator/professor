package gather

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

var errHeldSubagents = errors.New("held rollouts identify only subagents")

// heldCodexRoot examines every held rollout. Descriptor order cannot establish
// authority: a parent commonly opens its children's transcripts as well.
func heldCodexRoot(links []FDLink, roots []string) (string, error) {
	seen := map[string]bool{}
	selected := ""
	observed := false
	unwritten := ""
	for _, link := range links {
		path := filepath.Clean(link.Target)
		if seen[path] {
			continue
		}
		matches := false
		for _, root := range roots {
			if strings.TrimSpace(root) != "" && isRolloutUnder(filepath.Join(root, "sessions"), path) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		observed = true
		seen[path] = true
		header, err := codexmeta.ReadHeader(path)
		if errors.Is(err, codexmeta.ErrNoSessionMeta) {
			// Opened and not yet headed — a child the parent just spawned. It
			// refuses only when no other held rollout verifies as the root.
			unwritten = path
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read held rollout %s: %w", path, err)
		}
		if header.Kind == codexmeta.Subagent {
			continue
		}
		if header.Kind != codexmeta.User || header.ID == "" || header.ID != CodexRolloutID(path) {
			return "", fmt.Errorf("held rollout %s has unverified root metadata", path)
		}
		if selected != "" && CodexRolloutID(selected) != header.ID {
			return "", fmt.Errorf("conflicting held root rollouts %s and %s", selected, path)
		}
		selected = path
	}
	if selected == "" && unwritten != "" {
		return "", fmt.Errorf("read held rollout %s: %w", unwritten, codexmeta.ErrNoSessionMeta)
	}
	if observed && selected == "" {
		return "", errHeldSubagents
	}
	return selected, nil
}

func hasCodexAncestor(proc ProcFS, pid, panePID int, cmdlines map[int][]string, binaries []string) bool {
	for depth := 0; depth < 4 && pid != panePID; depth++ {
		stat, err := proc.Stat(pid)
		if err != nil || stat.ParentPID <= 1 || stat.ParentPID == pid {
			return false
		}
		pid = stat.ParentPID
		if IsCodexCommand(cmdlines[pid], binaries...) {
			return true
		}
	}
	return false
}
