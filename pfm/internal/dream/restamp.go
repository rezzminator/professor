package dream

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"hostops/pfm/internal/dream/apply"
	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/organ"
)

var restampMapNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.md$`)

// Restamp mechanically re-resolves every anchor of one organ map at the HEAD
// the caller's worktree sees, after an agent has judged the map's claims. The
// judgment is the agent's; the hashes are never the agent's — a hand-written
// stamp is the fabrication class the apply gate exists to kill.
func Restamp(mapArgument, workingDirectory string, now time.Time) (string, error) {
	hookContext, err := organ.ResolveHook(workingDirectory)
	if err != nil {
		return "", err
	}
	mapPath, err := resolveRestampTarget(mapArgument, hookContext.Organ)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(mapPath)
	if err != nil {
		return "", fmt.Errorf("read map: %w", err)
	}
	restamped, err := apply.RestampBytes(
		raw,
		now.Format("2006-01-02"),
		"HEAD",
		gate.CommandGitReader{Repo: hookContext.GitRoot},
	)
	if err != nil {
		return "", err
	}
	moved := countChangedRows(string(raw), string(restamped))
	if moved == 0 {
		return fmt.Sprintf("restamp %s: already current, no row changed\n", filepath.Base(mapPath)), nil
	}
	info, err := os.Stat(mapPath)
	if err != nil {
		return "", fmt.Errorf("stat map: %w", err)
	}
	temporary := mapPath + ".restamp.tmp"
	if err := os.WriteFile(temporary, restamped, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("write restamped map: %w", err)
	}
	if err := os.Rename(temporary, mapPath); err != nil {
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return "", errors.Join(
				fmt.Errorf("replace map: %w", err),
				fmt.Errorf("remove temporary map %s: %w", temporary, removeErr),
			)
		}
		return "", fmt.Errorf("replace map: %w", err)
	}
	return fmt.Sprintf("restamp %s: %d row(s) updated at HEAD\n", filepath.Base(mapPath), moved), nil
}

// resolveRestampTarget accepts the spellings an agent sees: a surface pointer
// (maps/{slug}.md), a bare filename, a bare slug, or an absolute path — and
// confines every one of them to this organ's maps directory.
func resolveRestampTarget(argument, organRoot string) (string, error) {
	if argument == "" {
		return "", fmt.Errorf("restamp requires a map argument")
	}
	name := argument
	if filepath.IsAbs(argument) {
		mapsDirectory := filepath.Join(organRoot, "maps")
		if filepath.Dir(argument) != mapsDirectory {
			return "", fmt.Errorf("map is outside this organ's maps directory: %s", argument)
		}
		name = filepath.Base(argument)
	} else {
		name = strings.TrimPrefix(name, "maps/")
		if !strings.HasSuffix(name, ".md") {
			name += ".md"
		}
	}
	if strings.ContainsRune(name, filepath.Separator) || !restampMapNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid map name: %s", argument)
	}
	return filepath.Join(organRoot, "maps", name), nil
}

func countChangedRows(before, after string) int {
	beforeRows := strings.Split(before, "\n")
	afterRows := strings.Split(after, "\n")
	if len(beforeRows) != len(afterRows) {
		// restampMap edits rows in place and never changes the row count; a
		// mismatch means the diff heuristic, not the map, is wrong — report
		// everything moved rather than silently reporting nothing.
		return len(afterRows)
	}
	changed := 0
	for index := range beforeRows {
		if beforeRows[index] != afterRows[index] {
			changed++
		}
	}
	return changed
}
