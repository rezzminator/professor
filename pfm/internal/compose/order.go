package compose

import (
	"sort"
)

type projectDir struct {
	path       string
	activityNS int64
	seeded     bool
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func withNewRows(output Output) Output {
	chatRows := output.Rows
	for len(chatRows) > 0 &&
		isNewChatKind(chatRows[0].Kind) {
		chatRows = chatRows[1:]
	}
	newCount := boolInt(output.includeNewClaude) + boolInt(output.includeNewCodex) + boolInt(output.includeNewOpenCode)
	rows := make([]Row, 0, newCount+len(chatRows))
	project, directory := newTarget(output)
	if output.includeNewClaude {
		rows = append(rows, Row{
			Kind:    NewClaude,
			Name:    "New Claude chat",
			Project: project,
			CWD:     directory,
			Account: output.primaryAccount,
		})
	}
	if output.includeNewCodex {
		rows = append(rows, Row{
			Kind:    NewCodex,
			Name:    "New Codex chat",
			Project: project,
			CWD:     directory,
			Account: output.primaryCodex,
		})
	}
	if output.includeNewOpenCode {
		rows = append(rows, Row{
			Kind:    NewOpenCode,
			Name:    "New OpenCode chat",
			Project: project,
			CWD:     directory,
			Account: output.primaryOpenCode,
		})
	}
	rows = append(rows, chatRows...)
	output.Rows = rows
	return output
}

func newTarget(output Output) (string, string) {
	project := ""
	if len(output.ProjectOrder) > 0 {
		project = output.ProjectOrder[0]
	}
	if project == "" {
		project = output.projects.of(output.fallbackDir)
	}
	directory := output.ProjectDirs[project]
	if directory == "" {
		directory = output.fallbackDir
	}
	return project, directory
}

// leadWithCurrentProject puts the project the picker was OPENED IN at the top,
// ahead of activity order, and keeps every other project ranked by activity
// behind it.
//
// Without it the busiest project leads, and since the two generated new-chat
// rows target ProjectOrder[0], opening the list inside one project offered a
// "New chat" that would launch in ANOTHER — the picker answering a question
// nobody asked. The current project leads even with no chats of its own: it is
// still where a new chat belongs.
func leadWithCurrentProject(output Output, currentDir string) Output {
	if currentDir == "" {
		return output
	}
	current := output.projects.of(currentDir)
	if current == "" || current == "?" {
		return output
	}
	if len(output.ProjectOrder) > 0 && output.ProjectOrder[0] == current {
		return output
	}

	order := make([]string, 0, len(output.ProjectOrder)+1)
	order = append(order, current)
	for _, project := range output.ProjectOrder {
		if project != current {
			order = append(order, project)
		}
	}

	rowsByProject := make(map[string][]Row, len(order))
	for index := range output.Rows {
		row := output.Rows[index]
		if isNewChatKind(row.Kind) {
			continue
		}
		rowsByProject[row.Project] = append(rowsByProject[row.Project], row)
	}
	rows := make([]Row, 0, len(output.Rows))
	for _, project := range order {
		rows = append(rows, rowsByProject[project]...)
	}

	output.Rows = rows
	output.ProjectOrder = order
	if _, found := output.ProjectDirs[current]; !found {
		if output.ProjectDirs == nil {
			output.ProjectDirs = make(map[string]string, 1)
		}
		output.ProjectDirs[current] = cleanPath(currentDir)
	}
	return output
}

func isNewChatKind(kind Kind) bool {
	return kind == NewClaude || kind == NewCodex || kind == NewOpenCode
}

func sortProjectRows(rows []Row) ([]Row, []string) {
	rowsByProject := make(map[string][]Row)
	for index := range rows {
		row := rows[index]
		rowsByProject[row.Project] = append(rowsByProject[row.Project], row)
	}
	type projectBlock struct {
		name     string
		newestNS int64
		rows     []Row
	}
	blocks := make([]projectBlock, 0, len(rowsByProject))
	for project, projectRows := range rowsByProject {
		sortRowsByActivity(projectRows)
		newest := int64(0)
		if len(projectRows) > 0 {
			newest = projectRows[0].ActivityNS
		}
		blocks = append(blocks, projectBlock{
			name:     project,
			newestNS: newest,
			rows:     projectRows,
		})
	}
	sort.Slice(blocks, func(left, right int) bool {
		if blocks[left].newestNS != blocks[right].newestNS {
			return blocks[left].newestNS > blocks[right].newestNS
		}
		return blocks[left].name < blocks[right].name
	})

	sortedRows := make([]Row, 0, len(rows))
	order := make([]string, 0, len(blocks))
	for _, block := range blocks {
		order = append(order, block.name)
		sortedRows = append(sortedRows, block.rows...)
	}
	return sortedRows, order
}

func projectDirs(input Input) map[string]string {
	names := projectNames{}
	directories := make(map[string]projectDir)
	if input.Options.CurrentDir != "" {
		project := names.of(input.Options.CurrentDir)
		directories[project] = projectDir{
			path:   cleanPath(input.Options.CurrentDir),
			seeded: true,
		}
	}
	for index := range input.Transcripts {
		transcript := input.Transcripts[index]
		rememberProjectDir(names, directories, transcript.CWD, transcript.EffectiveActivityNS())
	}
	for index := range input.Rollouts {
		rollout := input.Rollouts[index]
		if rollout.UserThread {
			rememberProjectDir(names, directories, rollout.CWD, rollout.MTimeNS)
		}
	}
	result := make(map[string]string, len(directories))
	for project, directory := range directories {
		result[project] = directory.path
	}
	return result
}

func rememberProjectDir(
	names projectNames,
	directories map[string]projectDir,
	path string,
	activityNS int64,
) {
	if path == "" {
		return
	}
	ref := names.resolve(path)
	incumbent, found := directories[ref.name]
	if found && (incumbent.seeded || incumbent.activityNS > activityNS) {
		return
	}
	// A project's launch directory is its repository root: a new chat in
	// the repo never opens inside a worktree that a merge will delete.
	directories[ref.name] = projectDir{
		path:       cleanPath(ref.root),
		activityNS: activityNS,
	}
}
