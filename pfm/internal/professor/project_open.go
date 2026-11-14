package professor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// projectUnreadable is a pin or store template the report could not read:
// never review, never clean — the report fails while the rest still shows.
const projectUnreadable projectStatus = "UNREADABLE"

// OpenRow is one decision a project still owes: Status is the report's row
// class (UPDATED, NEW, GONE-UPSTREAM, MOVED, LOCAL-DELETED, RETIRED-NAME,
// INCOMPLETE or UNREADABLE) and Name the file it names — a local path, a template for a NEW
// row, a retired-name file with its hit count.
type OpenRow struct {
	Status string
	Name   string
}

// OpenProjectRows is the project report without its diffs, reduced to the
// rows no verb has closed: every review row, every pin or template the report
// could not read, every file with a retired-name
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
		case projectUnreadable:
			rows = append(rows, OpenRow{Status: string(item.Status), Name: item.unreadableName()})
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

func buildProjectReportWithContext(ctx context.Context, root, home string) (projectReport, error) {
	baseline, err := Load(root)
	if err != nil {
		return projectReport{}, err
	}
	store, err := resolveStoreWithContext(ctx, root, home)
	if err != nil {
		return projectReport{}, err
	}
	report := projectReport{
		Root:     root,
		Store:    store,
		Baseline: baseline,
		Counts:   make(map[projectStatus]int, len(projectStatusOrder)),
	}
	for _, s := range projectStatusOrder {
		report.Counts[s] = 0
	}
	pinned := make(map[string]bool, len(baseline.Files))
	ignored := make(map[string]bool, len(baseline.Ignored))
	for _, v := range baseline.Ignored {
		ignored[v] = true
	}
	locals := make([]string, 0, len(baseline.Files))
	for local := range baseline.Files {
		locals = append(locals, local)
	}
	sort.Strings(locals)
	for _, rawLocal := range locals {
		pin := baseline.Files[rawLocal]
		local, err := safeProjectRelative(rawLocal)
		if err != nil {
			return projectReport{}, fmt.Errorf("baseline local %q: %w", rawLocal, err)
		}
		template, err := safeTemplateRelative(pin.Template)
		if err != nil {
			return projectReport{}, fmt.Errorf("baseline template %q: %w", pin.Template, err)
		}
		pinned[template] = true
		status, move, err := pinStatus(store.Templates, root, local, template, pin, pinned)
		if err != nil {
			report.addUnreadable(projectReportItem{Local: local, Template: template, Pin: pin}, err)
			continue
		}
		report.addKept(status, local, template, pin)
		report.Counts[status]++
		if status != projectCurrent {
			report.Items = append(
				report.Items,
				projectReportItem{Status: status, Local: local, Template: template, Pin: pin, projectMove: move},
			)
		}
	}
	projectTemplates := filepath.Join(store.Templates, "project")
	if err := filepath.WalkDir(projectTemplates, func(path string, entry fs.DirEntry, walkErr error) error {
		relative, err := filepath.Rel(store.Templates, path)
		if err != nil {
			return err
		}
		template := filepath.ToSlash(relative)
		// One unreadable entry is one UNREADABLE row; the walk goes on (a
		// directory that could not be listed is skipped by returning nil).
		if walkErr != nil {
			report.addUnreadable(
				projectReportItem{Template: template},
				fmt.Errorf("UNREADABLE %s: %w", path, walkErr),
			)
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report.addUnreadable(
				projectReportItem{Template: template},
				fmt.Errorf("UNREADABLE %s: symlink templates are unsupported", path),
			)
			return nil
		}
		if pinned[template] {
			return nil
		}
		if ignored[template] {
			report.Counts[projectIgnored]++
			return nil
		}
		if _, err := HashTemplate(path); err != nil {
			report.addUnreadable(projectReportItem{Template: template}, err)
			return nil
		}
		report.Counts[projectNew]++
		report.Items = append(report.Items, projectReportItem{Status: projectNew, Template: template})
		return nil
	}); err != nil {
		return projectReport{}, err
	}
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Status != report.Items[j].Status {
			return statusIndex(report.Items[i].Status) < statusIndex(report.Items[j].Status)
		}
		if report.Items[i].Local != report.Items[j].Local {
			return report.Items[i].Local < report.Items[j].Local
		}
		return report.Items[i].Template < report.Items[j].Template
	})
	return report, nil
}

// pinStatus classifies one pinned file against the store; an error is a
// template or local file that could not be read, never a status.
func pinStatus(
	templates, root, local, template string,
	pin FilePin,
	pinned map[string]bool,
) (projectStatus, projectMove, error) {
	templatePath := filepath.Join(templates, filepath.FromSlash(template))
	if _, err := os.Stat(templatePath); errors.Is(err, fs.ErrNotExist) {
		return goneOrMoved(templates, template, pinned)
	} else if err != nil {
		return "", projectMove{}, fmt.Errorf("UNREADABLE %s: %w", templatePath, err)
	}
	localPath := filepath.Join(root, filepath.FromSlash(local))
	if _, err := os.Stat(localPath); errors.Is(err, fs.ErrNotExist) {
		return projectLocalDeleted, projectMove{}, nil
	} else if err != nil {
		return "", projectMove{}, fmt.Errorf("UNREADABLE %s: %w", localPath, err)
	}
	hash, err := HashTemplate(templatePath)
	if err != nil {
		return "", projectMove{}, err
	}
	if hash != pin.TemplateHash {
		return projectUpdated, projectMove{}, nil
	}
	return projectCurrent, projectMove{}, nil
}

// addUnreadable records item as one UNREADABLE row carrying err.
func (r *projectReport) addUnreadable(item projectReportItem, err error) {
	item.Status = projectUnreadable
	item.Error = err.Error()
	r.Counts[projectUnreadable]++
	r.Items = append(r.Items, item)
}

// unreadableName is the name an UNREADABLE row goes by: its local file when
// it is a pin, else its template.
func (item projectReportItem) unreadableName() string {
	if item.Local != "" {
		return item.Local
	}
	return item.Template
}

func writeUnreadableHuman(stdout io.Writer, r projectReport) {
	if r.Counts[projectUnreadable] == 0 {
		return
	}
	fmt.Fprintf(stdout, "  %-13s %d\n", projectUnreadable, r.Counts[projectUnreadable])
	for index := range r.Items {
		if item := &r.Items[index]; item.Status == projectUnreadable {
			fmt.Fprintf(stdout, "    %s — %s\n", item.unreadableName(), item.Error)
		}
	}
}
