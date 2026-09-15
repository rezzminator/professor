package gate

import (
	"fmt"
	"sort"

	"hostops/pfm/internal/dream/artifact"
)

type ExpandedCoverageLine struct {
	Path   string
	Status artifact.CoverageStatus
	Reason string
}

type CoverageResult struct {
	Expanded []ExpandedCoverageLine
	Conduct  []artifact.ConductLine
}

// Coverage expands already syntax-validated, index-keyed model output back to
// the runner-owned paths. Parsing is artifact's responsibility and happens once.
func Coverage(paths PinnedPaths, coverage artifact.Coverage) (CoverageResult, error) {
	rows := append([]artifact.CoverageLine(nil), coverage.Lines...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Index < rows[j].Index })
	expanded := make([]ExpandedCoverageLine, 0, len(coverage.Lines))
	for _, line := range rows {
		if line.Index < 1 || line.Index > len(paths.Paths) {
			return CoverageResult{}, fmt.Errorf(
				"coverage index %d exceeds %d pinned paths",
				line.Index,
				len(paths.Paths),
			)
		}
		expanded = append(expanded, ExpandedCoverageLine{
			Path: paths.Paths[line.Index-1], Status: line.Status, Reason: line.Reason,
		})
	}
	return CoverageResult{Expanded: expanded, Conduct: append([]artifact.ConductLine(nil), coverage.Conduct...)}, nil
}
