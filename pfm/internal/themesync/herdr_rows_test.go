package themesync

import (
	"fmt"
	"strings"
	"testing"
)

var testHues = map[string]string{
	"pfm_working": "#75a3d0", "pfm_blocked": "#d49104", "pfm_done": "#4eba65", "pfm_unknown": "#9e9e9e",
}

func styled(token string) string {
	return `{ token = "$` + token + `", fg = "` + testHues[token] + `" }`
}

func TestHerdrRowsStyledRewritesOnlyIndicatorEntriesInTheSidebarTables(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			"bare", "[ui.sidebar.agents]\nrows = [[\"$pfm_working\", \"agent\"]]\n",
			"[ui.sidebar.agents]\nrows = [[" + styled("pfm_working") + ", \"agent\"]]\n",
		},
		{
			"bare literal string", "[ui.sidebar.spaces]\nrows = [['$pfm_done']]\n",
			"[ui.sidebar.spaces]\nrows = [[" + styled("pfm_done") + "]]\n",
		},
		{
			"inline with other keys",
			"[ui.sidebar.agents]\nrows = [[{ token = \"$pfm_blocked\", bold = true, fg = \"#000000\" }]]\n",
			"[ui.sidebar.agents]\nrows = [[" + styled("pfm_blocked") + "]]\n",
		},
		{
			"already current", "[ui.sidebar.agents]\nrows = [[" + styled("pfm_unknown") + "]]\n",
			"[ui.sidebar.agents]\nrows = [[" + styled("pfm_unknown") + "]]\n",
		},
		{
			"stale fg", "[ui.sidebar.agents]\nrows = [[{ token = \"$pfm_working\", fg = \"#111111\" }]]\n",
			"[ui.sidebar.agents]\nrows = [[" + styled("pfm_working") + "]]\n",
		},
		{
			"nested sidebar table and arrays of tables",
			"[ui.sidebar.agents.rows_by_agent]\nclaude = [\n  [\"$pfm_done\"]\n]\n[[ui.sidebar.extra]]\nx = [\"$pfm_done\"]\n",
			"[ui.sidebar.agents.rows_by_agent]\nclaude = [\n  [" + styled(
				"pfm_done",
			) + "]\n]\n[[ui.sidebar.extra]]\nx = [" + styled(
				"pfm_done",
			) + "]\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := herdrRowsStyled(tc.in, testHues)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("styled\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestHerdrRowsStyledLeavesEverythingElseByteForByte(t *testing.T) {
	untouched := []struct {
		name, in string
	}{
		{
			"no pfm token",
			"[ui.sidebar.agents]\nrows = [[\"agent\", { token = \"name\", fg = \"#ffd4ee\", bold = true }]]\n",
		},
		{"other pfm-looking token", "[ui.sidebar.agents]\nrows = [[\"$pfm_other\", { token = \"$pfm_working2\" }]]\n"},
		{
			"token outside the sidebar tables",
			"[ui]\nrows = [[\"$pfm_working\"]]\n[keys]\nx = [\"$pfm_done\"]\n[ui.sidebarish]\ny = [\"$pfm_done\"]\n",
		},
		{"token at the root", "rows = [\"$pfm_working\"]\n[ui.sidebar.agents]\nkey = \"$pfm_working\"\n"},
		{
			"comment naming the token",
			"[ui.sidebar.agents]\n# rows = [[\"$pfm_working\"]]\nrows = [[\"agent\"]] # \"$pfm_blocked\"\n",
		},
		{
			"strings containing the token",
			"[ui.sidebar.agents]\nrows = [[\"a $pfm_working b\", 'x\"$pfm_done']]\nname = \"$pfm_working\"\n",
		},
		{
			"multi-line strings",
			"[ui.sidebar.agents]\nnote = \"\"\"\n[ui.sidebar.fake]\nrows = [\"$pfm_working\"]\n\"\"\"\nlit = '''\n[\"$pfm_done\"]\n'''\n",
		},
		{"nested braces", "[ui.sidebar.agents]\nrows = [[{ token = \"$pfm_working\", style = { bold = true } }]]\n"},
		{
			"the pfm block",
			"# >>> pfm theme · x · gen 1 >>>\n[ui.sidebar.agents]\nrows = [[\"$pfm_working\"]]\n# <<< pfm theme <<<\n",
		},
		{"escaped quote in a string", "[ui.sidebar.agents]\nrows = [[\"a\\\"$pfm_working\", \"agent\"]]\n"},
		{"empty file", ""},
	}
	for _, tc := range untouched {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := herdrRowsStyled(tc.in, testHues)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.in {
				t.Fatalf("styled\n%q\nwant it untouched", got)
			}
		})
	}
}

func TestHerdrRowsStyledKeepsTheRestOfAMixedFile(t *testing.T) {
	in := "onboarding = false\n\n[ui.sidebar.agents]\nrows = [\n  [\"$pfm_working\", \"agent\"], # keep\n  [\"$pfm_done\"]\n]\n" +
		"x = \"$pfm_working\"\n\n[keys]\nnew_tab = [\"$pfm_working\"]\n"
	want := "onboarding = false\n\n[ui.sidebar.agents]\nrows = [\n  [" + styled(
		"pfm_working",
	) + ", \"agent\"], # keep\n  [" +
		styled(
			"pfm_done",
		) + "]\n]\nx = \"$pfm_working\"\n\n[keys]\nnew_tab = [\"$pfm_working\"]\n"
	got, _, err := herdrRowsStyled(in, testHues)
	if err != nil || got != want {
		t.Fatalf("styled = %q, %v\nwant\n%q", got, err, want)
	}
}

// A row naming a token the applied theme has no hue for stays as it is and is
// named; the rows beside it are still styled.
func TestHerdrRowsStyledLeavesAHuelessRowBareAndStylesTheRest(t *testing.T) {
	hues := map[string]string{"pfm_done": testHues["pfm_done"]}
	got, notes, err := herdrRowsStyled("[ui.sidebar.agents]\nrows = [[\"$pfm_working\", \"$pfm_done\"]]\n", hues)
	want := "[ui.sidebar.agents]\nrows = [[\"$pfm_working\", " + styled("pfm_done") + "]]\n"
	wantNotes := []string{
		"herdr sidebar row names $pfm_working but the applied theme carries no hue for it (left unstyled)",
	}
	if err != nil || got != want || strings.Join(notes, "|") != strings.Join(wantNotes, "|") {
		t.Fatalf("styled = %q, %q, %v\nwant %q, %q", got, notes, err, want, wantNotes)
	}
}

func TestHerdrRowsStyledErrorsNameWhatIsWrong(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"string":         {"[ui.sidebar.agents]\nok = 1\nrows = [[\"$pfm_working]]\n", "themesync: herdr config: string opened at line 3 never closes"},
		"multi-line":     {"[ui.sidebar.agents]\nnote = \"\"\"\nrows\n", "themesync: herdr config: multi-line string opened at line 2 never closes"},
		"inline table":   {"[ui.sidebar.agents]\nrows = [{ token = \"$pfm_working\"\n", "themesync: herdr config: inline table opened at line 2 never closes"},
		"literal string": {"[ui.sidebar.agents]\nrows = [['$pfm_working]]\n", "themesync: herdr config: string opened at line 2 never closes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := herdrRowsStyled(tc.in, testHues)
			if got := fmt.Sprint(err); got != tc.want {
				t.Fatalf("err = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHerdrRowsBareTurnsStyledEntriesBackAndKeepsTheRest(t *testing.T) {
	in := "[ui.sidebar.agents]\nrows = [[" + styled(
		"pfm_working",
	) + ", \"agent\", { token = \"$pfm_done\", fg = \"#fff\", bold = true }, " +
		"{ token = \"name\", fg = \"#000\" }]]\n[keys]\nx = [" + styled(
		"pfm_done",
	) + "]\n"
	want := "[ui.sidebar.agents]\nrows = [[\"$pfm_working\", \"agent\", { token = \"$pfm_done\", fg = \"#fff\", bold = true }, " +
		"{ token = \"name\", fg = \"#000\" }]]\n[keys]\nx = [" + styled(
		"pfm_done",
	) + "]\n"
	got, err := herdrRowsBare(in)
	if err != nil || got != want {
		t.Fatalf("bare = %q, %v\nwant\n%q", got, err, want)
	}
	again, _, err := herdrRowsStyled(got, testHues)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, styled("pfm_working")) {
		t.Fatalf("styled after bare = %q, want the working entry styled again", again)
	}
}
