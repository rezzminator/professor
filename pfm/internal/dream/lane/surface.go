package lane

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hostops/pfm/internal/dream/artifact"
)

const SurfaceHeader = "# Index of maps/ — stale content: edit the map file directly."

// AgentSurfaceHeader is the first line of every agents/{lane}.md, so the rows
// reach an agent explained rather than as a list from nowhere.
const AgentSurfaceHeader = "Cached by the dreamer from earlier runs — read these first, before you act; if one answers your question, trust it and skip the re-derivation."

var (
	mapPointerPattern = regexp.MustCompile(` -> maps/[a-z0-9][a-z0-9-]*\.md$`)
	surfaceRowPattern = regexp.MustCompile(`^- ([^[:space:]].*) -> (maps/[a-z0-9][a-z0-9-]*\.md)$`)
)

type RenderedSurfaces struct {
	STM    string
	Agents map[string]string
	// MissingLesson counts maps that carry no `## Lesson`, whose surface row
	// therefore falls back to the bare title. A title names a subject and
	// teaches nothing until the body is opened, so this is a reported gap, not
	// an acceptable steady state.
	MissingLesson int
}

type surfaceRow struct {
	lane    string
	title   string
	mapFile string
	line    string
}

// RenderSurfaces creates the global STM index and each lane-only agent index.
// oldSTM is content, not a path, so callers retain control of all writes.
func RenderSurfaces(mapsDirectory, oldSTM string, membership artifact.LaneMembership) (RenderedSurfaces, error) {
	if err := validateMembership(membership); err != nil {
		return RenderedSurfaces{}, err
	}
	files, err := mapFiles(mapsDirectory)
	if err != nil {
		return RenderedSurfaces{}, err
	}
	rows := make([]surfaceRow, 0, len(files))
	titles := make(map[string]string, len(files))
	missingLesson := 0
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			return RenderedSurfaces{}, fmt.Errorf("read map %s: %w", path, err)
		}
		bodyRows := splitLines(string(body))
		if len(bodyRows) == 0 || !strings.HasPrefix(bodyRows[0], "# ") {
			return RenderedSurfaces{}, fmt.Errorf("map lacks H1 during surface render: %s", path)
		}
		title := strings.TrimPrefix(bodyRows[0], "# ")
		if err := validateTitle(title); err != nil {
			return RenderedSurfaces{}, fmt.Errorf("unsafe map title during surface render: %s: %w", path, err)
		}
		mapFile := filepath.Base(path)
		mapLane, ok := membership[mapFile]
		if !ok {
			return RenderedSurfaces{}, fmt.Errorf("map carries no lane row: %s", mapFile)
		}
		if previous, duplicate := titles[title]; duplicate {
			return RenderedSurfaces{}, fmt.Errorf(
				"duplicate map titles prevent deterministic surface generation: %s and %s",
				previous,
				mapFile,
			)
		}
		titles[title] = mapFile
		// The lesson is what an agent reads in context; the title is only a
		// label. Fall back to the title so a legacy map still indexes, and
		// count the fallback so the gap is reported.
		floated := lessonOf(bodyRows)
		if floated == "" {
			floated = title
			missingLesson++
		}
		rows = append(rows, surfaceRow{
			lane: mapLane, title: title, mapFile: mapFile,
			line: fmt.Sprintf("- %s -> maps/%s", floated, mapFile),
		})
	}

	agentRows := make(map[string][]surfaceRow)
	for _, row := range rows {
		agentRows[row.lane] = append(agentRows[row.lane], row)
	}
	agents := make(map[string]string, len(agentRows))
	for mapLane, laneRows := range agentRows {
		sort.Slice(laneRows, func(left, right int) bool {
			return laneRows[left].title < laneRows[right].title
		})
		lines := make([]string, 0, len(laneRows))
		for _, row := range laneRows {
			lines = append(lines, row.line)
		}
		agents[mapLane] = AgentSurfaceHeader + "\n" + strings.Join(lines, "\n") + "\n"
	}

	sort.Slice(rows, func(left, right int) bool { return rows[left].title < rows[right].title })
	stmRows := make([]string, 0, 1+len(rows))
	stmRows = append(stmRows, SurfaceHeader)
	for _, row := range rows {
		stmRows = append(stmRows, row.line)
	}
	for _, row := range splitLines(oldSTM) {
		if strings.HasPrefix(row, "- ") && !mapPointerPattern.MatchString(row) {
			stmRows = append(stmRows, row)
		}
	}
	return RenderedSurfaces{
		STM:           strings.Join(stmRows, "\n") + "\n",
		Agents:        agents,
		MissingLesson: missingLesson,
	}, nil
}

// lessonOf returns the single-line rule under `## Lesson`, or "" when the map
// predates the section. It scans rather than calling artifact.ParseMap so that
// surface rendering stays as permissive about the rest of the map as it was.
func lessonOf(bodyRows []string) string {
	for index, row := range bodyRows {
		if row != "## Lesson" {
			continue
		}
		for _, candidate := range bodyRows[index+1:] {
			if strings.HasPrefix(candidate, "## ") {
				return ""
			}
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				return trimmed
			}
		}
		return ""
	}
	return ""
}

// ValidSurface validates a nonempty lane surface as executable prompt data.
// Empty content remains an ordinary "no surface" result; malformed nonempty
// content is an error and must not be rendered as absence by its caller.
func ValidSurface(body string) (string, error) {
	if body == "" {
		return "", nil
	}
	if strings.Contains(body, "\r") {
		return "", fmt.Errorf("invalid nonempty lane surface: carriage return")
	}
	canonical := strings.TrimSuffix(body, "\n")
	if canonical == "" || strings.HasSuffix(canonical, "\n") {
		return "", fmt.Errorf("invalid nonempty lane surface: blank row")
	}
	titles := make(map[string]struct{})
	paths := make(map[string]struct{})
	for offset, row := range strings.Split(canonical, "\n") {
		if offset == 0 && row == AgentSurfaceHeader {
			continue
		}
		match := surfaceRowPattern.FindStringSubmatch(row)
		if match == nil {
			return "", fmt.Errorf("invalid nonempty lane surface line %d: %s", offset+1, row)
		}
		title, pointer := match[1], match[2]
		if err := validateTitle(title); err != nil {
			return "", fmt.Errorf("invalid nonempty lane surface line %d: %w", offset+1, err)
		}
		if _, duplicate := titles[title]; duplicate {
			return "", fmt.Errorf("invalid nonempty lane surface: duplicate title %s", title)
		}
		if _, duplicate := paths[pointer]; duplicate {
			return "", fmt.Errorf("invalid nonempty lane surface: duplicate map %s", pointer)
		}
		titles[title] = struct{}{}
		paths[pointer] = struct{}{}
	}
	return canonical, nil
}
