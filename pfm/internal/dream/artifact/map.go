package artifact

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// lessonMaxBytes bounds the one-line rule the lane surface floats in every
// agent's context; the distill prompt states the same cap.
const lessonMaxBytes = 240

var (
	anchorPattern      = regexp.MustCompile("^- `([^`]+)` — (blob|tree) `([0-9a-f]{12})`$")
	mapFilenamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.md$`)
	terminalRange      = regexp.MustCompile(`^(.+):([0-9]+(?:-[0-9]+)?)$`)
	multipleRanges     = regexp.MustCompile(`:[0-9]+(?:-[0-9]+)?[,;:].+$`)
	cleanH1Pattern     = regexp.MustCompile(`^#[[:space:]]+[^#[:space:]].+$`)
	legacyTitlePrefix  = regexp.MustCompile(`^(C:|L:|M:|MAP[-_: ])`)
	provenancePattern  = regexp.MustCompile(`^Provenance: ([0-9]{4}-[0-9]{2}-[0-9]{2}) · sid ([0-9a-f]{8})$`)
)

// ValidMapFilename is the filename grammar shared by the anchor gate and the
// migration selector. Double hyphens are excluded even though the basic
// character grammar can represent them: they are not canonical map slugs.
func ValidMapFilename(name string) bool {
	return mapFilenamePattern.MatchString(name) && !strings.Contains(name, "--")
}

func AnchorLookupPath(displayPath string) string {
	match := terminalRange.FindStringSubmatch(displayPath)
	if match == nil {
		return displayPath
	}
	return match[1]
}

func ParseAnchorRow(row string) (AnchorRow, error) {
	match := anchorPattern.FindStringSubmatch(row)
	if match == nil {
		return AnchorRow{}, parseFailure("anchor row grammar mismatch")
	}
	displayPath := match[1]
	if strings.HasPrefix(displayPath, "/") || strings.HasPrefix(displayPath, "../") ||
		strings.Contains(displayPath, "/../") || strings.HasPrefix(displayPath, ".git/") {
		return AnchorRow{}, parseFailure("unsafe anchor path: " + displayPath)
	}
	if multipleRanges.MatchString(displayPath) {
		return AnchorRow{}, parseFailure("anchor row grammar mismatch")
	}
	lookupPath := AnchorLookupPath(displayPath)
	// A display path may carry one terminal line or line-range annotation, but
	// never stacked annotations such as path:10:12.
	if lookupPath != displayPath && terminalRange.MatchString(lookupPath) {
		return AnchorRow{}, parseFailure("anchor row grammar mismatch")
	}
	return AnchorRow{
		DisplayPath: displayPath,
		LookupPath:  lookupPath,
		ObjectType:  GitObjectType(match[2]),
		Hash:        match[3],
	}, nil
}

func RenderAnchorRow(row AnchorRow) string {
	return fmt.Sprintf("- `%s` — %s `%s`", row.DisplayPath, row.ObjectType, row.Hash)
}

// ParseMap validates canonical map syntax without consulting Git. The anchor
// gate must subsequently verify each returned path, hash, and object type at
// the pinned tree.
func ParseMap(text string) (Map, error) {
	rows := linesOf(text)
	if len(rows) == 0 {
		return Map{}, parseFailure("empty file")
	}
	if !cleanH1Pattern.MatchString(rows[0]) {
		return Map{}, parseFailure("missing clean H1")
	}
	// The H1 grammar guarantees a marker and one whitespace byte. Slicing the
	// marker mirrors the original artifact parser for either a space or tab.
	title := rows[0][2:]
	if legacyTitlePrefix.MatchString(title) {
		return Map{}, parseFailure("legacy title prefix")
	}

	headings := []string{"## Question", "## Answer", "## Derivation trail", "## Anchors"}
	for _, heading := range headings {
		if countExact(rows, heading) != 1 {
			return Map{}, parseFailure(strings.TrimPrefix(heading, "## ") + " heading count is not one")
		}
	}
	// Lesson is optional so maps written before the section existed still parse.
	// The distill prompt requires it; the surface renderer reports its absence.
	lessonCount := countExact(rows, "## Lesson")
	if lessonCount > 1 {
		return Map{}, parseFailure("Lesson heading count is not one")
	}
	known := append(append([]string(nil), headings...), "## Lesson")
	for _, row := range rows {
		if strings.HasPrefix(row, "## ") && !containsString(known, row) {
			return Map{}, parseFailure("unexpected section heading")
		}
	}

	provenanceIndex := -1
	provenanceCount := 0
	var provenance Provenance
	for index, row := range rows {
		if !strings.HasPrefix(row, "Provenance:") {
			continue
		}
		provenanceCount++
		provenanceIndex = index
		match := provenancePattern.FindStringSubmatch(row)
		if match != nil {
			provenance = Provenance{Date: match[1], SessionID: match[2]}
		}
	}
	if provenanceCount != 1 {
		return Map{}, parseFailure("Provenance line count is not one")
	}
	if !provenancePattern.MatchString(rows[provenanceIndex]) {
		return Map{}, parseFailure("Provenance grammar mismatch")
	}

	questionIndex := indexExact(rows, "## Question")
	answerIndex := indexExact(rows, "## Answer")
	trailIndex := indexExact(rows, "## Derivation trail")
	anchorsIndex := indexExact(rows, "## Anchors")
	if !(questionIndex < answerIndex && answerIndex < trailIndex && trailIndex < provenanceIndex && provenanceIndex < anchorsIndex) {
		return Map{}, parseFailure("section order mismatch")
	}
	firstSection := questionIndex
	lesson := ""
	if lessonCount == 1 {
		lessonIndex := indexExact(rows, "## Lesson")
		if lessonIndex > questionIndex {
			return Map{}, parseFailure("Lesson must precede Question")
		}
		firstSection = lessonIndex
		lesson = sectionBody(rows, lessonIndex, questionIndex)
		if lesson == "" {
			return Map{}, parseFailure("Lesson is empty")
		}
		if strings.Contains(lesson, "\n") {
			return Map{}, parseFailure("Lesson is more than one line")
		}
		if len(lesson) > lessonMaxBytes {
			return Map{}, parseFailure("Lesson exceeds " + strconv.Itoa(lessonMaxBytes) + " characters")
		}
	}
	for _, row := range rows[1:firstSection] {
		if strings.TrimSpace(row) != "" {
			return Map{}, parseFailure("legacy preamble before Question")
		}
	}
	if !hasBody(rows, questionIndex, answerIndex) || !hasBody(rows, answerIndex, trailIndex) ||
		!hasBody(rows, trailIndex, provenanceIndex) {
		return Map{}, parseFailure("Question, Answer, or Derivation trail is empty")
	}

	anchors := make([]AnchorRow, 0, 8)
	for _, row := range rows[anchorsIndex+1:] {
		if row == "" {
			continue
		}
		anchor, err := ParseAnchorRow(row)
		if err != nil {
			return Map{}, err
		}
		anchors = append(anchors, anchor)
	}
	if len(anchors) < 2 || len(anchors) > 8 {
		return Map{}, parseFailure("anchor count outside 2-8: " + strconv.Itoa(len(anchors)))
	}

	return Map{
		Title:           title,
		Lesson:          lesson,
		Question:        sectionBody(rows, questionIndex, answerIndex),
		Answer:          sectionBody(rows, answerIndex, trailIndex),
		DerivationTrail: sectionBody(rows, trailIndex, provenanceIndex),
		Provenance:      provenance,
		Anchors:         anchors,
	}, nil
}

func countExact(rows []string, want string) int {
	count := 0
	for _, row := range rows {
		if row == want {
			count++
		}
	}
	return count
}

func indexExact(rows []string, want string) int {
	for index, row := range rows {
		if row == want {
			return index
		}
	}
	return -1
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasBody(rows []string, start, end int) bool {
	for _, row := range rows[start+1 : end] {
		if strings.TrimSpace(row) != "" {
			return true
		}
	}
	return false
}

func sectionBody(rows []string, start, end int) string {
	body := append([]string(nil), rows[start+1:end]...)
	for len(body) > 0 && body[0] == "" {
		body = body[1:]
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
}
