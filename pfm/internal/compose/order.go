package compose

import (
	"sort"
)

type projectDirectory struct {
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
			Kind:    NewOpencode,
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
		project = projectName(output.fallbackDir)
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
	current := projectName(currentDir)
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
	for _, row := range output.Rows {
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
	return kind == NewClaude || kind == NewCodex || kind == NewOpencode
}

func sortProjectRows(rows []Row) ([]Row, []string) {
	rowsByProject := make(map[string][]Row)
	for _, row := range rows {
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
	directories := make(map[string]projectDirectory)
	if input.Options.CurrentDir != "" {
		project := projectName(input.Options.CurrentDir)
		directories[project] = projectDirectory{
			path:   cleanPath(input.Options.CurrentDir),
			seeded: true,
		}
	}
	for _, transcript := range input.Transcripts {
		rememberProjectDir(directories, transcript.CWD, transcript.EffectiveActivityNS())
	}
	for _, rollout := range input.Rollouts {
		if rollout.UserThread {
			rememberProjectDir(directories, rollout.CWD, rollout.MTimeNS)
		}
	}
	result := make(map[string]string, len(directories))
	for project, directory := range directories {
		result[project] = directory.path
	}
	return result
}

func rememberProjectDir(
	directories map[string]projectDirectory,
	path string,
	activityNS int64,
) {
	if path == "" {
		return
	}
	project := projectName(path)
	incumbent, found := directories[project]
	if found && (incumbent.seeded || incumbent.activityNS > activityNS) {
		return
	}
	directories[project] = projectDirectory{
		path:       cleanPath(path),
		activityNS: activityNS,
	}
}
