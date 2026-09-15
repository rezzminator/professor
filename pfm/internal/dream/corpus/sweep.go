package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var sweepNamePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:-(\d+))?\.md$`)

type sweepCandidate struct {
	path     string
	date     string
	sequence int
}

// LatestAppliedSweep returns the newest completed sweep for lane. Sweeps that
// predate lane metadata belong to explorer. An empty return with nil error
// means the directory was scanned successfully and no matching sweep exists.
func LatestAppliedSweep(organ, lane string) (string, error) {
	latest, _, err := latestAppliedSweep(organ, lane)
	return latest, err
}

func latestAppliedSweep(organ, lane string) (string, []byte, error) {
	if !filepath.IsAbs(organ) {
		return "", nil, fmt.Errorf("organ requires an absolute path: %s", organ)
	}
	if lane == "" || hasControl(lane) {
		return "", nil, fmt.Errorf("invalid lane for sweep lookup")
	}
	dreamerDir := filepath.Join(organ, "dreamer")
	entries, err := os.ReadDir(dreamerDir)
	if err != nil {
		return "", nil, fmt.Errorf("read dreamer sweep directory %s: %w", dreamerDir, err)
	}

	var latest *sweepCandidate
	var latestRaw []byte
	for _, entry := range entries {
		match := sweepNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", nil, fmt.Errorf("stat sweep candidate %s: %w", filepath.Join(dreamerDir, entry.Name()), err)
		}
		if !info.Mode().IsRegular() {
			return "", nil, fmt.Errorf(
				"sweep candidate is not a regular file: %s",
				filepath.Join(dreamerDir, entry.Name()),
			)
		}
		sequence := 1
		if match[2] != "" {
			sequence, err = strconv.Atoi(match[2])
			if err != nil {
				return "", nil, fmt.Errorf("invalid sweep sequence in %s: %w", entry.Name(), err)
			}
		}
		path := filepath.Join(dreamerDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", nil, fmt.Errorf("read sweep candidate %s: %w", path, err)
		}
		completed, sweepLane, err := classifySweep(raw)
		if err != nil {
			return "", nil, fmt.Errorf("parse sweep candidate %s: %w", path, err)
		}
		if !completed || sweepLane != lane {
			continue
		}
		candidate := &sweepCandidate{path: path, date: match[1], sequence: sequence}
		if latest == nil || candidate.date > latest.date ||
			(candidate.date == latest.date && candidate.sequence > latest.sequence) {
			latest = candidate
			latestRaw = raw
		}
	}
	if latest == nil {
		return "", nil, nil
	}
	return latest.path, latestRaw, nil
}

func classifySweep(raw []byte) (completed bool, lane string, err error) {
	lane = "explorer"
	seenLane := false
	for _, line := range splitLines(raw) {
		if line == "END-OF-SWEEP" {
			completed = true
		}
		if !strings.HasPrefix(line, "lane\t") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || fields[1] == "" || hasControl(fields[1]) {
			return false, "", fmt.Errorf("invalid lane row")
		}
		if seenLane {
			return false, "", fmt.Errorf("duplicate lane row")
		}
		seenLane = true
		lane = fields[1]
	}
	return completed, lane, nil
}

// Cutoff derives the rolling window for lane. The newest completed sweep uses
// its last enumerated-at row when that row parses, then its last Applied row
// when that row parses, then the sweep filename's local midnight. With no
// sweep, the cutoff is now minus seven days and its stable display value is
// "7 days ago".
func Cutoff(organ, lane string, now time.Time) (Window, error) {
	if now.IsZero() {
		return Window{}, fmt.Errorf("cutoff time is missing")
	}
	latest, raw, err := latestAppliedSweep(organ, lane)
	if err != nil {
		return Window{}, err
	}
	if latest == "" {
		return Window{
			Mode:               WindowSweepCutoff,
			NewestAppliedSweep: windowNone,
			CutoffSource:       CutoffBootstrap,
			CutoffExclusive:    "7 days ago",
			CutoffTime:         now.Add(-7 * 24 * time.Hour),
		}, nil
	}

	lines := splitLines(raw)
	if value, parsed, ok := lastParsedCutoff(lines, "enumerated-at\t", now.Location()); ok {
		return Window{
			Mode:               WindowSweepCutoff,
			NewestAppliedSweep: filepath.Base(latest),
			CutoffSource:       CutoffEnumeratedAt,
			CutoffExclusive:    value,
			CutoffTime:         parsed,
		}, nil
	}
	if value, parsed, ok := lastParsedCutoff(lines, "Applied: ", now.Location()); ok {
		return Window{
			Mode:               WindowSweepCutoff,
			NewestAppliedSweep: filepath.Base(latest),
			CutoffSource:       CutoffApplied,
			CutoffExclusive:    value,
			CutoffTime:         parsed,
		}, nil
	}

	date := filepath.Base(latest)[:len("2006-01-02")]
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", date+" 00:00:00", now.Location())
	if err != nil {
		return Window{}, fmt.Errorf("derive filename cutoff from %s: %w", latest, err)
	}
	return Window{
		Mode:               WindowSweepCutoff,
		NewestAppliedSweep: filepath.Base(latest),
		CutoffSource:       CutoffFilenameDate,
		CutoffExclusive:    date + " 00:00:00",
		CutoffTime:         parsed,
	}, nil
}

func lastParsedCutoff(lines []string, prefix string, location *time.Location) (string, time.Time, bool) {
	value := ""
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			candidate := strings.TrimPrefix(line, prefix)
			if prefix != "enumerated-at\t" || !strings.Contains(candidate, "\t") {
				value = candidate
			}
		}
	}
	if value == "" {
		return "", time.Time{}, false
	}
	parsed, ok := parseTime(value, location)
	return value, parsed, ok
}

func parseTime(value string, location *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
