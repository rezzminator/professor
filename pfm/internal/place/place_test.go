package place

import (
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestIsUnderRespectsSegmentBoundaries(t *testing.T) {
	for _, test := range []struct {
		path, root string
		want       bool
	}{
		{"/src/app", "/src/app", true},
		{"/src/app/plugins/gw", "/src/app", true},
		{"/src/apple", "/src/app", false},
		{"/src", "/src/app", false},
		{"/anything", "/", true},
		{"", "/src", false},
		{"/src", "", false},
	} {
		if got := IsUnder(test.path, test.root); got != test.want {
			t.Errorf("IsUnder(%q, %q) = %v, want %v", test.path, test.root, got, test.want)
		}
	}
}

func TestFoldRootsFoldsNestedProjectsButNeverIntoHome(t *testing.T) {
	folded := Fold([]string{
		"/jail/me/src/app/plugins/gw", "/jail/me/src/app", "/jail/me", "/jail/me/ops", "/jail/me/src/app/.worktrees/f",
	}, "/jail/me")
	want := map[string]string{
		"/jail/me/src/app":              "/jail/me/src/app",
		"/jail/me/src/app/plugins/gw":   "/jail/me/src/app",
		"/jail/me/src/app/.worktrees/f": "/jail/me/src/app",
		"/jail/me":                      "/jail/me",
		"/jail/me/ops":                  "/jail/me/ops", // home holds it on disk, never on the rail
	}
	if !reflect.DeepEqual(folded, want) {
		t.Fatalf("Fold = %v\nwant %v", folded, want)
	}
}

// fleetFixture is a framework repo with a plugin repo nested inside it, a
// sibling project, and the rows that never file anywhere.
func fleetFixture() Fleet {
	return Fleet{
		Rows: []compose.Row{
			{Kind: compose.NewClaude, Project: "ops", CWD: "/jail/me/ops"},
			{Kind: compose.ProfessorUpdate, CWD: "/jail/me/elsewhere"},
			{Kind: compose.LiveClaude, Project: "meter", CWD: "/jail/me/fw/plugins/meter", Backend: "herdr"},
			{Kind: compose.ResumeClaude, Project: ".fw", CWD: "/jail/me/fw/pfm"},
			{Kind: compose.LiveCodex, Project: "ops", CWD: "/jail/me/ops/deep"},
		},
		ProjectDirs: map[string]string{
			".fw":   "/jail/me/fw",
			"meter": "/jail/me/fw/plugins/meter",
			"ops":   "/jail/me/ops",
		},
		Home: "/jail/me",
	}
}

// A chat in a repo nested in another root files under the outer root and
// wears its label, whichever directory any picker was opened in; launch rows
// and notices file nowhere.
func TestBuildFilesANestedRootUnderTheOuterPlace(t *testing.T) {
	fleet := fleetFixture()
	index := Build(fleet.Rows, fleet.ProjectDirs, fleet.Home)
	if want := []string{
		"",
		"",
		"/jail/me/fw",
		"/jail/me/fw",
		"/jail/me/ops",
	}; !reflect.DeepEqual(
		index.RowPlace,
		want,
	) {
		t.Fatalf("RowPlace = %q, want %q", index.RowPlace, want)
	}
	want := []Place{{Root: "/jail/me/fw", Label: ".fw"}, {Root: "/jail/me/ops", Label: "ops"}}
	if !reflect.DeepEqual(index.Places, want) {
		t.Fatalf("Places = %+v, want %+v", index.Places, want)
	}
	if got := index.PlaceOf(2); got != want[0] {
		t.Fatalf("PlaceOf(meter row) = %+v, want %+v", got, want[0])
	}
	if got := index.PlaceOf(0); got != (Place{}) {
		t.Fatalf("PlaceOf(launch row) = %+v, want none", got)
	}
}

func TestLabelNamesHomeAndBareRoots(t *testing.T) {
	for _, test := range []struct{ root, home, want string }{
		{"/jail/me", "/jail/me/", "~"},
		{"/jail/me/ops", "/jail/me", "ops"},
		{"/", "/jail/me", "/"},
	} {
		if got := RootLabel(test.root, test.home); got != test.want {
			t.Errorf("RootLabel(%q, %q) = %q, want %q", test.root, test.home, got, test.want)
		}
	}
	if got := Clean("  "); got != "" {
		t.Errorf("Clean(blank) = %q, want empty", got)
	}
}
