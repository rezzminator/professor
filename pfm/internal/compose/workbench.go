package compose

import (
	"fmt"
	"sort"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func withWorkbenches(output Output, input Input) Output {
	owners := make(map[string]workbench.Bench)
	for i := range input.Workbenches {
		bench := &input.Workbenches[i]
		owners[bench.Key] = *bench
	}
	for i := range output.Rows {
		if bench, found := owners[output.Rows[i].Project]; found {
			output.Rows[i].Workbench = bench.Dir
		}
	}
	if input.Options.View == KilledView || len(input.Workbenches)+len(input.WorkbenchErrors) == 0 {
		return output
	}
	benches := append([]workbench.Bench(nil), input.Workbenches...)
	sort.Slice(benches, func(i, j int) bool { return benches[i].Dir < benches[j].Dir })
	families := make(map[string][]workbench.Bench)
	for i := range benches {
		bench := &benches[i]
		families[bench.Project] = append(families[bench.Project], *bench)
		output.ProjectDirs[bench.Key] = bench.Dir
		if output.ProjectDirs[bench.Project] == "" {
			output.ProjectDirs[bench.Project] = bench.Root
		}
	}
	rowsByProject := make(map[string][]Row)
	for i := range output.Rows {
		row := &output.Rows[i]
		rowsByProject[row.Project] = append(rowsByProject[row.Project], *row)
	}
	for i := range benches {
		bench := &benches[i]
		row := output.workbenchNewRow(*bench)
		rowsByProject[bench.Key] = append([]Row{row}, rowsByProject[bench.Key]...)
	}
	faults := make(map[string][]Row)
	for _, fault := range input.WorkbenchErrors {
		project := resolveProject(fault.Root).name
		if _, found := families[project]; !found {
			families[project] = nil
		}
		if output.ProjectDirs[project] == "" {
			output.ProjectDirs[project] = fault.Root
		}
		faults[project] = append(
			faults[project],
			Row{Kind: WorkbenchInvalid, Name: fault.Error(), Project: project, CWD: fault.Root},
		)
	}
	for project, rows := range faults {
		rowsByProject[project] = append(rows, rowsByProject[project]...)
	}
	var order []string
	seen := make(map[string]bool)
	emitFamily := func(project string) {
		if seen[project] {
			return
		}
		seen[project] = true
		order = append(order, project)
		for i := range families[project] {
			order = append(order, families[project][i].Key)
		}
	}
	for _, project := range output.ProjectOrder {
		family := project
		if bench, found := owners[project]; found {
			family = bench.Project
		}
		_, knownFamily := families[family]
		if knownFamily || len(rowsByProject[project]) != 0 {
			emitFamily(family)
		}
	}
	var remaining []string
	for project := range families {
		if !seen[project] {
			remaining = append(remaining, project)
		}
	}
	sort.Strings(remaining)
	for _, project := range remaining {
		emitFamily(project)
	}
	var rows []Row
	for _, project := range order {
		rows = append(rows, rowsByProject[project]...)
	}
	output.Rows, output.ProjectOrder = rows, order
	return output
}

func (output Output) workbenchNewRow(bench workbench.Bench) Row {
	row := Row{Project: bench.Key, CWD: bench.Dir, Workbench: bench.Dir}
	if bench.Err != nil {
		row.Kind, row.Name = WorkbenchInvalid, bench.Err.Error()
		return row
	}
	for _, id := range bench.Engines {
		if id == pfmengine.Claude && output.includeNewClaude || id == pfmengine.Codex && output.includeNewCodex ||
			id == pfmengine.OpenCode && output.includeNewOpenCode {
			row.Engines = append(row.Engines, id)
		}
	}
	if len(row.Engines) == 0 {
		var words []string
		for _, id := range bench.Engines {
			words = append(words, pfmengine.MustLookup(id).LongName)
		}
		row.Kind, row.Name = WorkbenchInvalid, fmt.Sprintf(
			"workbench %s enables %s, and this machine has no account for any of them",
			bench.Dir,
			strings.Join(words, ", "),
		)
		return row
	}
	id := row.Engines[0]
	row.Name = "New " + pfmengine.MustLookup(id).Short + " chat"
	switch id {
	case pfmengine.Claude:
		row.Kind, row.Account = NewClaude, output.primaryAccount
	case pfmengine.Codex:
		row.Kind, row.Account = NewCodex, output.primaryCodex
	case pfmengine.OpenCode:
		row.Kind, row.Account = NewOpenCode, output.primaryOpenCode
	}
	return row
}

func (output Output) workbenchTarget(project string) (string, string) {
	for i := range output.projects.benches {
		bench := &output.projects.benches[i]
		if project == bench.Key {
			return bench.Project, bench.Root
		}
	}
	return project, ""
}

// RepoRoots returns the distinct roots resolved while composing this frame.
func (output Output) RepoRoots() []string {
	seen := make(map[string]bool)
	for _, ref := range output.projects.resolved {
		if ref.root != "" {
			seen[ref.root] = true
		}
	}
	for i := range output.projects.benches {
		bench := &output.projects.benches[i]
		if bench.Root != "" {
			seen[bench.Root] = true
		}
	}
	roots := make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}
