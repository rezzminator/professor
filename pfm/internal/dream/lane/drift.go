package lane

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"hostops/pfm/internal/deps"
)

var (
	gitObjectIDPattern    = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	driftAnchorRowPattern = regexp.MustCompile("^- `([^`]+)` — (?:blob|tree) `([0-9a-f]{12})`$")
)

type DriftResult struct {
	Surface     string
	DriftedMaps int
	Fallback    bool
}

type driftCount struct {
	moved      int
	total      int
	movedPaths []string
}

// movedList names the moved anchors so a consumer can re-verify precisely
// instead of diffing every anchor. Basenames keep the row short; the map's own
// Anchors section carries the full paths. Capped so one wide map cannot bloat
// the surface.
func (count driftCount) movedList() string {
	const namedCap = 3
	names := make([]string, 0, namedCap)
	for index, moved := range count.movedPaths {
		if index == namedCap {
			names = append(names, fmt.Sprintf("+%d more", len(count.movedPaths)-namedCap))
			break
		}
		names = append(names, filepath.Base(moved))
	}
	return strings.Join(names, ", ")
}

type driftAnchor struct {
	lookupPath string
	hash       string
}

// AnnotateDrift reads maps from the base organ while resolving each anchor in
// the explicitly supplied live worktree. If any map or Git probe is
// untrustworthy it returns the original surface byte-for-byte, sets Fallback,
// and returns a non-nil error; partial annotations are never exposed.
func AnnotateDrift(worktreeRoot, mapsDirectory, surface string) (DriftResult, error) {
	fallback := func(err error) (DriftResult, error) {
		return DriftResult{Surface: surface, Fallback: true}, err
	}
	canonical, err := ValidSurface(surface)
	if err != nil {
		return fallback(err)
	}
	if _, missing, err := resolveGitObject(worktreeRoot, "HEAD"); err != nil {
		return fallback(fmt.Errorf("verify live worktree Git HEAD: %w", err))
	} else if missing {
		return fallback(fmt.Errorf("verify live worktree Git HEAD: repository has no HEAD"))
	}

	files, err := mapFiles(mapsDirectory)
	if err != nil {
		return fallback(err)
	}
	counts := make(map[string]driftCount, len(files))
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			return fallback(fmt.Errorf("read drift map %s: %w", path, err))
		}
		anchors, err := parseDriftAnchors(string(body))
		if err != nil {
			return fallback(fmt.Errorf("parse drift map %s: %w", path, err))
		}
		count := driftCount{total: len(anchors)}
		for _, anchor := range anchors {
			current, missing, err := resolveGitObject(worktreeRoot, "HEAD:"+anchor.lookupPath)
			if err != nil {
				return fallback(fmt.Errorf("resolve live anchor %s for %s: %w", anchor.lookupPath, path, err))
			}
			if missing || !strings.HasPrefix(current, anchor.hash) {
				count.moved++
				count.movedPaths = append(count.movedPaths, anchor.lookupPath)
			}
		}
		counts[strings.TrimSuffix(filepath.Base(path), ".md")] = count
	}

	if canonical == "" {
		return DriftResult{Surface: surface}, nil
	}
	rows := strings.Split(canonical, "\n")
	drifted := 0
	for index, row := range rows {
		// ValidSurface admits AgentSurfaceHeader as line 1; it carries no map
		// pointer, so it is never a drift-annotation target.
		if index == 0 && row == AgentSurfaceHeader {
			continue
		}
		match := surfaceRowPattern.FindStringSubmatch(row)
		// ValidSurface already proved this; retaining the check makes this
		// consumer fail closed if the parser contract is ever changed.
		if match == nil {
			return fallback(fmt.Errorf("surface row became unparsable at line %d", index+1))
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(match[2], "maps/"), ".md")
		count, ok := counts[slug]
		if !ok {
			return fallback(fmt.Errorf("surface points to missing map: maps/%s.md", slug))
		}
		if count.moved > 0 {
			rows[index] = fmt.Sprintf(
				"%s ⚠ DRIFTED (%d/%d anchors moved: %s)",
				row,
				count.moved,
				count.total,
				count.movedList(),
			)
			drifted++
		}
	}
	annotated := strings.Join(rows, "\n")
	if strings.HasSuffix(surface, "\n") {
		annotated += "\n"
	}
	return DriftResult{Surface: annotated, DriftedMaps: drifted}, nil
}

// parseDriftAnchors intentionally parses only the legacy hook's Anchors
// surface, not the whole modern map schema. Existing organs can contain maps
// written before Question/Answer/Derivation became mandatory; those maps are
// invalid as new staged artifacts but their canonical anchor rows must not
// suppress drift annotations for every other live map.
func parseDriftAnchors(body string) ([]driftAnchor, error) {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	inAnchors := false
	found := false
	var anchors []driftAnchor
	for _, line := range lines {
		if line == "## Anchors" {
			if found {
				return nil, errors.New("Anchors heading count is not one")
			}
			found = true
			inAnchors = true
			continue
		}
		if inAnchors && strings.HasPrefix(line, "## ") {
			inAnchors = false
		}
		if !inAnchors || strings.TrimSpace(line) == "" {
			continue
		}
		match := driftAnchorRowPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("invalid anchor row %q", line)
		}
		lookup := strings.TrimSuffix(match[1], "/")
		if colon := strings.LastIndexByte(lookup, ':'); colon >= 0 {
			lookup = lookup[:colon]
		}
		if lookup == "" {
			return nil, fmt.Errorf("anchor row has empty lookup path %q", line)
		}
		anchors = append(anchors, driftAnchor{lookupPath: lookup, hash: match[2]})
	}
	if !found || len(anchors) == 0 {
		return nil, errors.New("missing anchors")
	}
	return anchors, nil
}

func resolveGitObject(repository, revision string) (objectID string, missing bool, err error) {
	command := exec.Command(deps.Executable("git"), "-C", repository, "rev-parse", "--verify", "-q", revision)
	command.Env = gitReadEnvironment()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, runErr := command.Output()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			return "", true, nil
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return "", false, fmt.Errorf("git rev-parse %s failed: %s", revision, detail)
	}
	value := strings.TrimSuffix(string(output), "\n")
	if !gitObjectIDPattern.MatchString(value) {
		return "", false, fmt.Errorf("git rev-parse %s returned invalid object id %q", revision, value)
	}
	return value, false, nil
}

func gitReadEnvironment() []string {
	environment := os.Environ()
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "GIT_OPTIONAL_LOCKS=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GIT_OPTIONAL_LOCKS=0")
}
