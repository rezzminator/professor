package ui

import (
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func placeFixtureRows() ([]compose.Row, map[string]string) {
	rows := []compose.Row{
		{Kind: compose.NewClaude, Name: "New Claude chat", Project: "app", CWD: "/src/app"},
		{Kind: compose.LiveClaude, ID: "here", Name: "here", Project: "app", CWD: "/src/app/pfm", ActivityNS: 50},
		{
			Kind:       compose.ResumeClaude,
			ID:         "nested",
			Name:       "nested",
			Project:    "gw",
			CWD:        "/src/app/plugins/gw",
			ActivityNS: 40,
		},
		{Kind: compose.LiveCodex, ID: "away", Name: "away", Project: "ops", CWD: "/src/ops", ActivityNS: 90},
		{Kind: compose.ResumeClaude, ID: "gone", Name: "gone", Project: "ops", CWD: "/src/ops", Killed: true},
	}
	return rows, map[string]string{"app": "/src/app", "gw": "/src/app/plugins/gw", "ops": "/src/ops"}
}

func TestBuildPlacesOpensOnTheStartingProjectWithItsNestedFamily(t *testing.T) {
	rows, dirs := placeFixtureRows()
	built := buildPlaces(
		rows,
		dirs,
		"/jail/me",
		"/src/app",
		"app",
		nil,
		func(row compose.Row) bool { return !row.Killed },
	)
	labels := make([]string, 0, len(built.places))
	for _, entry := range built.places {
		labels = append(labels, entry.label+" "+placeTally(entry))
	}
	// The opening place leads although ops is busier; ✱ all closes the rail;
	// the killed row counts nowhere.
	if want := []string{"app ●1 ↻1", "ops ●1", "✱ all ●2 ↻1"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("rail = %q, want %q", labels, want)
	}
	if built.open != "/src/app" || built.rowPlace[2] != "/src/app" || built.rowPlace[0] != "" {
		t.Fatalf(
			"open %q, nested row filed under %q, launch row under %q",
			built.open,
			built.rowPlace[2],
			built.rowPlace[0],
		)
	}
}

func TestBuildPlacesZoomsIntoANestedOpeningProject(t *testing.T) {
	rows, dirs := placeFixtureRows()
	built := buildPlaces(rows, dirs, "/jail/me", "/src/app/plugins/gw", "gw", nil, nil)
	if built.open != "/src/app/plugins/gw" {
		t.Fatalf("open = %q, want the nested project itself", built.open)
	}
	zoom := built.places[1]
	if zoom.root != "/src/app/plugins/gw" || !zoom.adhoc || zoom.label != "app › plugins/gw" || zoom.resume != 1 {
		t.Fatalf("zoomed stop = %#v, want app › plugins/gw beside its owner", zoom)
	}
}

func TestBuildPlacesInHomeOpensEverywhere(t *testing.T) {
	rows, dirs := placeFixtureRows()
	if built := buildPlaces(rows, dirs, "/jail/me", "/jail/me", "u", nil, nil); built.open != allPlaceRoot {
		t.Fatalf("a picker opened in ~ opened on %q, want everywhere", built.open)
	}
}

func TestTypedPathResolvesTildeAbsoluteAndRelative(t *testing.T) {
	for _, test := range []struct {
		query, want string
		ok          bool
	}{
		{"/data/new/", "/data/new", true},
		{"~/src", "/jail/me/src", true},
		{"~", "/jail/me", true},
		{"./plugins", "/src/app/plugins", true},
		{"../ops", "/src/ops", true},
		{"host", "", false},
	} {
		got, ok := typedPath(test.query, "/jail/me", "/src/app")
		if got != test.want || ok != test.ok {
			t.Errorf("typedPath(%q) = %q/%v, want %q/%v", test.query, got, ok, test.want, test.ok)
		}
	}
}
