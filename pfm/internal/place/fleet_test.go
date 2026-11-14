package place

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testjail/fakegit"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// A newborn files where its row will: under the outer root when its repo is
// nested, as its own place when nothing holds it.
func TestFleetOfPlacesANewbornByTheFold(t *testing.T) {
	fleet := fleetFixture()
	for _, test := range []struct {
		cwd  string
		want Place
	}{
		{testjail.Place("/jail/me/fw/plugins/meter"), Place{Root: testjail.Place("/jail/me/fw"), Label: ".fw"}},
		{testjail.Place("/jail/me/fw"), Place{Root: testjail.Place("/jail/me/fw"), Label: ".fw"}},
		{testjail.Place("/jail/me/ops"), Place{Root: testjail.Place("/jail/me/ops"), Label: "ops"}},
		{testjail.Place("/jail/me/fresh"), Place{Root: testjail.Place("/jail/me/fresh"), Label: "fresh"}},
	} {
		if got := fleet.Of(test.cwd); got != test.want {
			t.Errorf("Of(%q) = %+v, want %+v", test.cwd, got, test.want)
		}
	}
}

// A chat anywhere under a project sits in that project's workspace, even when
// no chat ever ran at the project's top: a linked worktree, a workbench and a
// plain subdirectory roll up to the repository, as its root does.
func TestFleetOfRollsANestedDirectoryUpToItsProject(t *testing.T) {
	repo := testjail.GitRepoWithWorktrees(t, &fakegit.Runner{}, "lantern")
	bench := filepath.Join(repo.Repo, ".workbenches", "outreach")
	if err := os.MkdirAll(bench, 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := Fleet{
		Rows:    []compose.Row{{Kind: compose.LiveClaude, Project: "other-repo", CWD: repo.Other}},
		Home:    repo.Base,
		Benches: []workbench.Bench{{Dir: bench, Root: repo.Repo, Project: "lantern", Key: "lantern › outreach"}},
	}
	want := Place{Root: repo.Repo, Label: "lantern"}
	for _, test := range []struct{ name, cwd string }{
		{"the project root", repo.Repo},
		{"a linked worktree", repo.Inner},
		{"a workbench", bench},
		{"a plain subdirectory", repo.Subdir},
	} {
		if got := fleet.Of(test.cwd); got != want {
			t.Errorf("%s: Of(%q) = %+v, want %+v", test.name, test.cwd, got, want)
		}
	}
}

// Two places that share a base name both carry their parent, as the rail
// shows them; a fixed entry keeps its label but still counts.
func TestLabeledTellsNamesakesApart(t *testing.T) {
	fleet := Fleet{
		Rows: []compose.Row{
			{Kind: compose.LiveClaude, Project: "api", CWD: "/w/a/api"},
			{Kind: compose.LiveClaude, Project: "api", CWD: "/w/b/api"},
		},
		Home: "/w",
	}
	index := fleet.Labeled()
	want := []Place{{Root: "/w/a/api", Label: "a/api"}, {Root: "/w/b/api", Label: "b/api"}}
	if !reflect.DeepEqual(index.Places, want) {
		t.Fatalf("Places = %+v, want %+v", index.Places, want)
	}
	labels := []string{"api", "api"}
	Disambiguate([]string{"/w/a/api", "/w/b/api"}, labels, []bool{true, false})
	if want := []string{"api", "b/api"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("Disambiguate with a fixed entry = %q, want %q", labels, want)
	}
}
