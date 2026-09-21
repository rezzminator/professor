package compose

import "testing"

// The picker is opened FROM a project, and that project must lead the list.
// Ordering by activity alone meant opening the list in projc led with proja —
// and, because the generated new-chat rows target ProjectOrder[0], offered a
// "New Claude chat" that would launch in proja too.

func projectRow(project, name string, activityNS int64) Row {
	return Row{
		Kind:       ResumeClaude,
		ID:         project + "-" + name,
		Name:       name,
		Project:    project,
		CWD:        "/work/" + project,
		ActivityNS: activityNS,
	}
}

func orderOf(output Output) []string { return output.ProjectOrder }

func TestTheProjectYouOpenedInLeadsTheList(t *testing.T) {
	rows := []Row{
		projectRow("proja", "STM", 900),
		projectRow("projc", "DEPLOY", 100),
		projectRow("projd", "MAINT", 500),
	}
	sorted, order := sortProjectRows(rows)
	if order[0] != "proja" {
		t.Fatalf("setup: activity order should lead with proja, got %q", order)
	}

	output := leadWithCurrentProject(Output{
		Rows:         sorted,
		ProjectOrder: order,
		ProjectDirs:  map[string]string{"projc": "/work/projc"},
	}, "/work/projc")

	if got := orderOf(output); got[0] != "projc" {
		t.Fatalf("opened in projc, list leads with %q", got)
	}
	// Everything else keeps its activity ranking behind the current project.
	if got := orderOf(output); got[1] != "proja" || got[2] != "projd" {
		t.Fatalf("activity order lost behind the current project: %q", got)
	}
	if output.Rows[0].Project != "projc" {
		t.Fatalf("rows were not reordered to match: %#v", output.Rows[0])
	}
	if len(output.Rows) != len(rows) {
		t.Fatalf("reordering lost rows: %d of %d", len(output.Rows), len(rows))
	}

	// The new-chat rows follow the head of the order, so they now launch where
	// the picker was opened.
	withNew := withNewRows(Output{
		Rows:             output.Rows,
		ProjectOrder:     output.ProjectOrder,
		ProjectDirs:      output.ProjectDirs,
		includeNewClaude: true,
	})
	if withNew.Rows[0].Kind != NewClaude ||
		withNew.Rows[0].Project != "projc" ||
		withNew.Rows[0].CWD != "/work/projc" {
		t.Fatalf("New chat targets the wrong project: %#v", withNew.Rows[0])
	}
}

// A project with no chats of its own is still where a new chat belongs, so it
// leads with just the generated rows — and no existing row may be dropped on
// the way (they are rebuilt from ProjectOrder, so an order missing a project
// would silently delete its chats).
func TestAProjectWithNoChatsStillLeadsAndLosesNothing(t *testing.T) {
	rows := []Row{
		projectRow("proja", "STM", 900),
		projectRow("projd", "MAINT", 500),
	}
	sorted, order := sortProjectRows(rows)

	output := leadWithCurrentProject(Output{
		Rows:         sorted,
		ProjectOrder: order,
		ProjectDirs:  map[string]string{},
	}, "/work/fresh-thing")

	if got := orderOf(output); got[0] != "fresh-thing" {
		t.Fatalf("a chatless current project did not lead: %q", got)
	}
	if len(output.Rows) != len(rows) {
		t.Fatalf("reordering dropped rows: %d of %d", len(output.Rows), len(rows))
	}
	if output.ProjectDirs["fresh-thing"] != "/work/fresh-thing" {
		t.Fatalf("current project has no directory to launch in: %#v", output.ProjectDirs)
	}
}

// No launch directory (a headless or scripted compose) must not invent one.
func TestWithoutALaunchDirectoryActivityStillDecides(t *testing.T) {
	rows := []Row{
		projectRow("proja", "STM", 900),
		projectRow("projc", "DEPLOY", 100),
	}
	sorted, order := sortProjectRows(rows)
	output := leadWithCurrentProject(Output{
		Rows:         sorted,
		ProjectOrder: order,
	}, "")
	if got := orderOf(output); got[0] != "proja" {
		t.Fatalf("empty CurrentDir changed the order: %q", got)
	}
}
