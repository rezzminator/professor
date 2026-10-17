package themesync

import (
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
			got, err := herdrRowsStyled(tc.in, testHues)
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
			got, err := herdrRowsStyled(tc.in, testHues)
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
	got, err := herdrRowsStyled(in, testHues)
	if err != nil || got != want {
		t.Fatalf("styled = %q, %v\nwant\n%q", got, err, want)
	}
}

func TestHerdrRowsStyledErrorsNameWhatIsWrong(t *testing.T) {
	if _, err := herdrRowsStyled(
		"[ui.sidebar.agents]\nrows = [[\"$pfm_working\"]]\n",
		map[string]string{},
	); err == nil ||
		!strings.Contains(err.Error(), "$pfm_working") {
		t.Fatalf("missing hue err = %v, want it to name $pfm_working", err)
	}
	for name, in := range map[string]string{
		"string":         "[ui.sidebar.agents]\nok = 1\nrows = [[\"$pfm_working]]\n",
		"multi-line":     "[ui.sidebar.agents]\nnote = \"\"\"\nrows\n",
		"inline table":   "[ui.sidebar.agents]\nrows = [{ token = \"$pfm_working\"\n",
		"literal string": "[ui.sidebar.agents]\nrows = [['$pfm_working]]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := herdrRowsStyled(in, testHues)
			if err == nil || !strings.Contains(err.Error(), "line 2") && !strings.Contains(err.Error(), "line 3") {
				t.Fatalf("err = %v, want it to name the line that never closes", err)
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
	again, err := herdrRowsStyled(got, testHues)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, styled("pfm_working")) {
		t.Fatalf("styled after bare = %q, want the working entry styled again", again)
	}
}
