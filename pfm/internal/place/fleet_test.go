package place

import (
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// A newborn files where its row will: under the outer root when its repo is
// nested, as its own place when nothing holds it.
func TestFleetOfPlacesANewbornByTheFold(t *testing.T) {
	fleet := fleetFixture()
	for _, test := range []struct {
		cwd  string
		want Place
	}{
		{"/jail/me/fw/plugins/meter", Place{Root: "/jail/me/fw", Label: ".fw"}},
		{"/jail/me/fw", Place{Root: "/jail/me/fw", Label: ".fw"}},
		{"/jail/me/ops", Place{Root: "/jail/me/ops", Label: "ops"}},
		{"/jail/me/fresh", Place{Root: "/jail/me/fresh", Label: "fresh"}},
	} {
		if got := fleet.Of(test.cwd); got != test.want {
			t.Errorf("Of(%q) = %+v, want %+v", test.cwd, got, test.want)
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
