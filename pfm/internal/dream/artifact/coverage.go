package artifact

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	positiveIntegerPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	laneSlugPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// ParseCoverage validates the complete seat-produced coverage artifact. Every
// supplied transcript index and each CONDUCT kind must occur exactly once.
func ParseCoverage(text string, transcriptCount int) (Coverage, error) {
	rows := linesOf(text)
	var problems []string
	if transcriptCount <= 0 {
		problems = append(problems, "coverage requires at least one supplied transcript")
	}
	if len(rows) == 0 || rows[len(rows)-1] != "END-OF-RUN" {
		problems = append(problems, "missing final END-OF-RUN")
	}
	endCount := 0
	for _, row := range rows {
		if row == "END-OF-RUN" {
			endCount++
		}
	}
	if endCount != 1 {
		problems = append(problems, "END-OF-RUN must occur exactly once")
	}

	artifact := Coverage{}
	for offset, row := range rows {
		if row == "END-OF-RUN" {
			continue
		}
		lineNumber := offset + 1
		fields := strings.Split(row, "\t")
		if fields[0] == "CONDUCT" {
			if len(fields) != 4 {
				problems = append(
					problems,
					fmt.Sprintf("line %d: expected CONDUCT<TAB>kind<TAB>slug|NONE<TAB>reason", lineNumber),
				)
				continue
			}
			kind, ok := parseConductKind(fields[1])
			if !ok {
				problems = append(
					problems,
					fmt.Sprintf("line %d: conduct kind is not technique, prior, or baseline", lineNumber),
				)
				continue
			}
			if fields[2] == "" || fields[3] == "" {
				problems = append(
					problems,
					fmt.Sprintf("line %d: conduct slug and reason are both required", lineNumber),
				)
				continue
			}
			if fields[2] != "NONE" && !laneSlugPattern.MatchString(fields[2]) {
				problems = append(
					problems,
					fmt.Sprintf("line %d: conduct slug is not lowercase kebab or NONE", lineNumber),
				)
				continue
			}
			artifact.Conduct = append(artifact.Conduct, ConductLine{Kind: kind, Slug: fields[2], Reason: fields[3]})
			continue
		}

		if len(fields) != 3 {
			problems = append(problems, fmt.Sprintf("line %d: expected index<TAB>READ|SKIP<TAB>reason", lineNumber))
			continue
		}
		if !positiveIntegerPattern.MatchString(fields[0]) {
			problems = append(problems, fmt.Sprintf("line %d: first field is not a transcript index", lineNumber))
			continue
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: first field is not a transcript index", lineNumber))
			continue
		}
		if index > transcriptCount {
			problems = append(
				problems,
				fmt.Sprintf(
					"line %d: index %d exceeds the %d supplied transcripts",
					lineNumber,
					index,
					transcriptCount,
				),
			)
			continue
		}
		status, ok := parseCoverageStatus(fields[1])
		if !ok {
			problems = append(problems, fmt.Sprintf("line %d: status is not READ or SKIP", lineNumber))
			continue
		}
		if fields[2] == "" {
			problems = append(problems, fmt.Sprintf("line %d: reason is empty", lineNumber))
			continue
		}
		artifact.Lines = append(artifact.Lines, CoverageLine{Index: index, Status: status, Reason: fields[2]})
	}

	indexCounts := make(map[int]int, len(artifact.Lines))
	for _, row := range artifact.Lines {
		indexCounts[row.Index]++
	}
	var duplicates, missing []string
	for index := 1; index <= transcriptCount; index++ {
		if indexCounts[index] > 1 {
			duplicates = append(duplicates, strconv.Itoa(index))
		}
		if indexCounts[index] == 0 {
			missing = append(missing, strconv.Itoa(index))
		}
	}
	if len(duplicates) != 0 {
		problems = append(problems, "DUPLICATE INDEXES:\n"+strings.Join(duplicates, "\n"))
	}
	if len(missing) != 0 {
		problems = append(problems, "UNRULED INDEXES:\n"+strings.Join(missing, "\n"))
	}

	conductCounts := make(map[ConductKind]int, len(artifact.Conduct))
	for _, row := range artifact.Conduct {
		conductCounts[row.Kind]++
	}
	for _, kind := range []ConductKind{ConductTechnique, ConductPrior, ConductBaseline} {
		switch conductCounts[kind] {
		case 0:
			problems = append(problems, "missing CONDUCT accounting for: "+string(kind))
		case 1:
		default:
			problems = append(problems, "CONDUCT accounting must occur exactly once for: "+string(kind))
		}
	}
	if len(problems) != 0 {
		return Coverage{}, parseFailure(problems...)
	}
	return artifact, nil
}

func RenderExpandedCoverage(artifact Coverage, paths []string) string {
	rows := append([]CoverageLine(nil), artifact.Lines...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	var rendered strings.Builder
	for _, row := range rows {
		path := ""
		if row.Index > 0 && row.Index <= len(paths) {
			path = paths[row.Index-1]
		}
		fmt.Fprintf(&rendered, "%s\t%s\t%s\n", path, row.Status, row.Reason)
	}
	return rendered.String()
}

func parseCoverageStatus(value string) (CoverageStatus, bool) {
	switch CoverageStatus(value) {
	case CoverageRead:
		return CoverageRead, true
	case CoverageSkip:
		return CoverageSkip, true
	default:
		return "", false
	}
}

func parseConductKind(value string) (ConductKind, bool) {
	switch ConductKind(value) {
	case ConductTechnique:
		return ConductTechnique, true
	case ConductPrior:
		return ConductPrior, true
	case ConductBaseline:
		return ConductBaseline, true
	default:
		return "", false
	}
}
