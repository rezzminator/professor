package update

import (
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// TestRetiredRegistryNames: an agent is its file stem (a contextual spelling
// by its probe), a command its path below the commands registry, a skill its
// directory; a phrase, a tool, a phase or a path is no registry entry.
func TestRetiredRegistryNames(t *testing.T) {
	t.Parallel()
	names := append([]retired.Name{
		{Name: "skills/transcript", Kind: retired.KindSkill, Match: retired.MatchExact},
		{Name: "transcript skill", Kind: retired.KindSkill, Match: retired.MatchExact},
		{Name: "/flights:init", Kind: retired.KindCommand, Match: retired.MatchExact},
	}, fixtureRetiredNames...)
	want := installer.RetiredRegistryNames{
		Agents: map[string]bool{
			"mono-architect": true,
			"rndier":         true,
			"rr-super":       true,
			"ultimate":       true,
			"imagine":        true,
		},
		Commands: map[string]bool{
			"flights/init":    true,
			"wave":            true,
			"jc":              true,
			"qa/live":         true,
			"pfm/refresh":     true,
			"quality/forlint": true,
			"pcm":             true,
			"chat":            true,
			"documenter":      true,
			"km":              true,
			"rnd":             true,
			"tokens":          true,
		},
		Skills: map[string]bool{"plot-inventory": true, "transcript": true, "wave-builder": true},
	}
	if got := RetiredRegistryNames(names); !reflect.DeepEqual(got, want) {
		t.Fatalf("RetiredRegistryNames() =\n%+v\nwant\n%+v", got, want)
	}
}
