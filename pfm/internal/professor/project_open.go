package professor

import (
	"context"
	"fmt"
	"sort"
)

// OpenRow is one decision a project still owes: Status is the report's row
// class (UPDATED, NEW, GONE-UPSTREAM, MOVED, LOCAL-DELETED, RETIRED-NAME,
// INCOMPLETE or UNREADABLE) and Name the file it names — a local path, a template for a NEW
// row, a retired-name file with its hit count.
type OpenRow struct {
	Status string
	Name   string
}

// OpenProjectRows is the project report without its diffs, reduced to the
// rows no verb has closed: every review row, every file with a retired-name
// hit left open (one row per file, with its hit count), every file the
// retired-name scan could not read and every file it read only in part — the
// report ends `clean` only without any of them. An error is a report that could not be
// built, never an empty list.
func OpenProjectRows(root, home string, scan RetiredNameScanner) ([]OpenRow, error) {
	return OpenProjectRowsWithContext(context.Background(), root, home, scan)
}

func buildProjectReport(root, home string) (projectReport, error) {
	return buildProjectReportWithContext(context.Background(), root, home)
}

// OpenProjectRowsWithContext bounds the blueprint git probe with ctx. A scanner
// that performs subprocess work must close over the same context.
func OpenProjectRowsWithContext(ctx context.Context, root, home string, scan RetiredNameScanner) ([]OpenRow, error) {
	report, err := buildProjectReportWithContext(ctx, root, home)
	if err != nil {
		return nil, err
	}
	captureRetiredNames(&report, scan)
	var rows []OpenRow
	for index := range report.Items {
		item := &report.Items[index]
		switch item.Status {
		case projectUpdated, projectGoneUpstream, projectMoved, projectLocalDeleted:
			rows = append(rows, OpenRow{Status: string(item.Status), Name: item.Local})
		case projectNew:
			rows = append(rows, OpenRow{Status: string(item.Status), Name: item.Template})
		}
	}
	if report.Retired != nil {
		perFile := map[string]int{}
		for _, hit := range report.Retired.Hits {
			perFile[hit.Path]++
		}
		files := make([]string, 0, len(perFile))
		for file := range perFile {
			files = append(files, file)
		}
		sort.Strings(files)
		for _, file := range files {
			rows = append(rows, OpenRow{Status: "RETIRED-NAME", Name: fmt.Sprintf("%s×%d", file, perFile[file])})
		}
		for _, failure := range report.Retired.Failures {
			rows = append(rows, OpenRow{Status: "UNREADABLE", Name: failure.Path})
		}
		for _, partial := range report.Retired.Incomplete {
			rows = append(rows, OpenRow{Status: "INCOMPLETE", Name: partial.Path})
		}
	}
	return rows, nil
}
