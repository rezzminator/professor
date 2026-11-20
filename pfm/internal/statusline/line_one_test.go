package statusline

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestIdentityLineGivesUpPartsAsTheTerminalNarrows(t *testing.T) {
	var data input
	data.Model.DisplayName = "Fable"
	data.Thinking.Enabled = true
	data.Effort.Level = "xhigh"
	facts := identityFacts{
		badge: "🥈 ", data: data, directory: "professor-workbench", worktree: "strip-look-every-width",
		git:   gitState{branch: "flight/strip-look-every-width", staged: 2, modified: 5},
		agent: "foreman-sonnet", vim: "INSERT", session: "A long enough session title", fleet: "fleet 3",
	}
	// Each rank at the fewest columns that hold it (its cells and the footer's
	// four) and one column fewer, where the next rank takes over; 0 is a width
	// not known, and 1 a terminal too narrow for any rank.
	for _, testcase := range []struct {
		columns int
		want    string
	}{
		{0, "🥈 ✦ Fable·▰▰▰▰ xhigh │ professor-workbench │ 🌳 strip-look-every-width │ 🌿 flight/strip-look-every-width +2 ~5 │ ⚡foreman-sonnet │ INSERT │ 🔖 A long enough session title │ fleet 3"},   // whole
		{187, "🥈 ✦ Fable·▰▰▰▰ xhigh │ professor-workbench │ 🌳 strip-look-every-width │ 🌿 flight/strip-look-every-width +2 ~5 │ ⚡foreman-sonnet │ INSERT │ 🔖 A long enough session title │ fleet 3"}, // whole
		{186, "🥈 ✦ Fable·▰▰▰▰ xhigh │ professor-workb… │ 🌳 strip-look-ever… │ 🌿 flight/strip-lo… +2 ~5 │ ⚡foreman-son… │ INSERT │ 🔖 A long enough session t… │ fleet 3"},                            // names cut
		{160, "🥈 ✦ Fable·▰▰▰▰ xhigh │ professor-workb… │ 🌳 strip-look-ever… │ 🌿 flight/strip-lo… +2 ~5 │ ⚡foreman-son… │ INSERT │ 🔖 A long enough session t… │ fleet 3"},                            // names cut
		{159, "🥈 ✦ Fable·▰▰▰▰ │ professor-workb… │ 🌳 strip-look-ever… │ 🌿 flight/strip-lo… +2 ~5 │ ⚡foreman-son… │ INSERT │ 🔖 A long enough session t… │ fleet 3"},                                  // effort bar without its word
		{154, "🥈 ✦ Fable·▰▰▰▰ │ professor-workb… │ 🌳 strip-look-ever… │ 🌿 flight/strip-lo… +2 ~5 │ ⚡foreman-son… │ INSERT │ 🔖 A long enough session t… │ fleet 3"},                                  // effort bar without its word
		{153, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT│🔖 A long enough session t…│fleet 3"},                                                // tight separators
		{140, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT│🔖 A long enough session t…│fleet 3"},                                                // tight separators
		{139, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT│fleet 3"},                                                                           // session dropped
		{112, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT│fleet 3"},                                                                           // session dropped
		{111, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT"},                                                                                   // fleet dropped
		{104, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│⚡foreman-son…│INSERT"},                                                                                   // fleet dropped
		{103, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│INSERT"},                                                                                                 // agent dropped
		{89, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5│INSERT"},                                                                                                  // agent dropped
		{88, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5"},                                                                                                         // vim dropped
		{82, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌳 strip-look-ever…│🌿 flight/strip-lo… +2 ~5"},                                                                                                         // vim dropped
		{81, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌿 flight/strip-lo… +2 ~5"},                                                                                                                            // worktree dropped
		{62, "🥈 ✦ Fable·▰▰▰▰│professor-workb…│🌿 flight/strip-lo… +2 ~5"},                                                                                                                            // worktree dropped
		{61, "🥈 ✦ Fable·▰▰▰▰│professor-workb…"}, // git dropped
		{36, "🥈 ✦ Fable·▰▰▰▰│professor-workb…"}, // git dropped
		{35, "🥈 ✦ Fable·▰▰▰▰"},                  // directory dropped
		{19, "🥈 ✦ Fable·▰▰▰▰"},                  // directory dropped
		{18, "✦ Fable·▰▰▰▰"},                    // badge dropped
		{16, "✦ Fable·▰▰▰▰"},                    // badge dropped
		{1, "✦ Fable·▰▰▰▰"},                     // badge dropped
	} {
		if got := ansi.Strip(identityLine(Runtime{Columns: testcase.columns}, facts)); got != testcase.want {
			t.Errorf("line 1 at %d columns =\n%q\nwant\n%q", testcase.columns, got, testcase.want)
		}
	}
}
