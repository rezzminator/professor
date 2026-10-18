package themesync_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// The tests live in the external package: the writer under test is the
// installer's EditSettings, which imports themesync.

// chooseHome is a temp home with two Claude accounts, ~/.claude and ~/.cc/1.
type chooseHome struct {
	t      *testing.T
	home   string
	claude string // ~/.claude
	cc1    string // ~/.cc/1
}

func newChooseHome(t *testing.T, claudeSettings, cc1Settings string) chooseHome {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir()) // a symlinked temp root would not read as ~/…
	if err != nil {
		t.Fatal(err)
	}
	h := chooseHome{
		t: t, home: root,
		claude: filepath.Join(root, ".claude"),
		cc1:    filepath.Join(root, ".cc", "1"),
	}
	h.write(filepath.Join(h.claude, "settings.json"), claudeSettings)
	h.write(filepath.Join(h.cc1, "settings.json"), cc1Settings)
	return h
}

func (h chooseHome) write(path, body string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// theme writes {dir}/themes/{stem}.json; title "" leaves the `name` field out.
func (h chooseHome) theme(dir, stem, title string) {
	h.t.Helper()
	body := `{"base":"dark","overrides":{}}`
	if title != "" {
		body = `{"name":"` + title + `","base":"dark","overrides":{}}`
	}
	h.write(filepath.Join(dir, "themes", stem+".json"), body)
}

func (h chooseHome) settings(dir string) string { return filepath.Join(dir, "settings.json") }

func (h chooseHome) env() themesync.Env {
	return themesync.Env{Home: h.home, StateDir: themesync.DefaultStateDir(h.home)}
}

func (h chooseHome) read(path string) string {
	h.t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(content)
}

// themeKey is the `theme` key of one settings.json.
func (h chooseHome) themeKey(dir string) string {
	h.t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(h.read(h.settings(dir))), &document); err != nil {
		h.t.Fatal(err)
	}
	value, _ := document["theme"].(string)
	return value
}

func (h chooseHome) list() themesync.ThemeList {
	h.t.Helper()
	list, err := themesync.ListThemes(h.env())
	if err != nil {
		h.t.Fatalf("ListThemes: %v", err)
	}
	return list
}

func (h chooseHome) use(name string) (themesync.UseResult, error) {
	return themesync.UseTheme(h.env(), name, installer.EditSettings)
}

// rows is a list as `name|value|kind|title` rows, `*` ahead of the current one.
func rows(list themesync.ThemeList) []string {
	out := make([]string, 0, len(list.Themes))
	for _, choice := range list.Themes {
		mark := ""
		if choice.Current {
			mark = "*"
		}
		out = append(out, mark+choice.Name+"|"+choice.Value+"|"+string(choice.Kind)+"|"+choice.Title)
	}
	return out
}

func choiceOf(t *testing.T, list themesync.ThemeList, name string, kind themesync.ThemeKind) themesync.ThemeChoice {
	t.Helper()
	for _, choice := range list.Themes {
		if choice.Name == name && choice.Kind == kind {
			return choice
		}
	}
	t.Fatalf("no %s theme %q in %v", kind, name, rows(list))
	return themesync.ThemeChoice{}
}

var builtinRows = []string{
	"auto|auto|builtin|auto",
	"dark|dark|builtin|dark",
	"dark-ansi|dark-ansi|builtin|dark-ansi",
	"dark-daltonized|dark-daltonized|builtin|dark-daltonized",
	"light|light|builtin|light",
	"light-ansi|light-ansi|builtin|light-ansi",
	"light-daltonized|light-daltonized|builtin|light-daltonized",
}

func TestListThemesListsCustomThenBuiltinWithTheCurrentMarked(t *testing.T) {
	h := newChooseHome(t, `{"theme":"custom:hibiscus-night"}`, `{"theme":"custom:hibiscus-night"}`)
	for _, dir := range []string{h.claude, h.cc1} {
		h.theme(dir, "hibiscus-night", "Hibiscus Night")
		h.theme(dir, "ember", "")
	}
	list := h.list()
	want := append([]string{
		"ember|custom:ember|custom|ember",
		"*hibiscus-night|custom:hibiscus-night|custom|Hibiscus Night",
	}, builtinRows...)
	if got := rows(list); !reflect.DeepEqual(got, want) {
		t.Fatalf("themes\n got %q\nwant %q", got, want)
	}
	if list.Current != "custom:hibiscus-night" {
		t.Errorf("Current = %q, want custom:hibiscus-night", list.Current)
	}
	if len(list.Errors) != 0 {
		t.Errorf("Errors = %+v, want none", list.Errors)
	}
	if list.Errors == nil || list.Themes == nil {
		t.Errorf("Themes and Errors must be empty slices, never nil: %+v", list)
	}
	for _, choice := range list.Themes {
		if len(choice.Missing) != 0 {
			t.Errorf("%s: Missing = %v, want none", choice.Name, choice.Missing)
		}
	}
}

func TestListThemesCountsAnAbsentThemeKeyAsDarkAndAnAbsentThemesDirAsNoError(t *testing.T) {
	h := newChooseHome(t, `{"other":1}`, `{"other":2}`)
	list := h.list()
	want := append([]string(nil), builtinRows...)
	want[1] = "*" + want[1]
	if got := rows(list); !reflect.DeepEqual(got, want) {
		t.Fatalf("themes\n got %q\nwant %q", got, want)
	}
	if list.Current != "dark" || len(list.Errors) != 0 {
		t.Errorf("Current = %q Errors = %+v, want dark and none", list.Current, list.Errors)
	}
}

func TestListThemesNamesTheAccountsLackingACustomTheme(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h chooseHome)
	}{
		{"the themes dir is absent", func(chooseHome) {}},
		{"the themes dir lacks the stem", func(h chooseHome) { h.theme(h.cc1, "other", "Other") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newChooseHome(t, `{}`, `{}`)
			h.theme(h.claude, "hibiscus-night", "Hibiscus Night")
			tc.setup(h)
			list := h.list()
			offered := choiceOf(t, list, "hibiscus-night", themesync.KindCustom)
			if want := []string{"~/.cc/1"}; !reflect.DeepEqual(offered.Missing, want) {
				t.Errorf("hibiscus-night Missing = %v, want %v", offered.Missing, want)
			}
			if len(list.Errors) != 0 {
				t.Errorf("a missing theme is no read failure: Errors = %+v", list.Errors)
			}
		})
	}
}

func TestListThemesTurnsAThemesDirItCannotReadIntoAnErrorRow(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, themes string)
	}{
		{"not a directory", func(t *testing.T, themes string) {
			if err := os.WriteFile(themes, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a dangling symlink", func(t *testing.T, themes string) {
			if err := os.Symlink(filepath.Join(filepath.Dir(themes), "gone"), themes); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newChooseHome(t, `{}`, `{}`)
			h.theme(h.cc1, "hibiscus-night", "Hibiscus Night")
			themes := filepath.Join(h.claude, "themes")
			tc.setup(t, themes)
			list := h.list()
			if len(list.Errors) != 1 || list.Errors[0].Path != themes || list.Errors[0].Error == "" {
				t.Fatalf("Errors = %+v, want one row naming %s with its failure", list.Errors, themes)
			}
			offered := choiceOf(t, list, "hibiscus-night", themesync.KindCustom)
			if want := []string{"~/.claude"}; !reflect.DeepEqual(offered.Missing, want) {
				t.Errorf("hibiscus-night Missing = %v, want the unreadable account %v", offered.Missing, want)
			}
		})
	}
}

func TestListThemesRecordsAThemeFileItCannotParseAndStillListsIt(t *testing.T) {
	h := newChooseHome(t, `{}`, `{}`)
	h.write(filepath.Join(h.claude, "themes", "broken.json"), `{`)
	list := h.list()
	broken := filepath.Join(h.claude, "themes", "broken.json")
	if len(list.Errors) != 1 || list.Errors[0].Path != broken {
		t.Fatalf("Errors = %+v, want one row naming %s", list.Errors, broken)
	}
	choice := choiceOf(t, list, "broken", themesync.KindCustom)
	if choice.Title != "broken" || choice.Value != "custom:broken" {
		t.Errorf("broken = %+v, want the stem as title and value custom:broken", choice)
	}
}

func TestListThemesTurnsASettingsFileItCannotParseIntoAnErrorRow(t *testing.T) {
	h := newChooseHome(t, `{"theme":"light"}`, `{`)
	list := h.list()
	bad := h.settings(h.cc1)
	if len(list.Errors) != 1 || list.Errors[0].Path != bad {
		t.Fatalf("Errors = %+v, want one row naming %s", list.Errors, bad)
	}
	if list.Current != "light" {
		t.Errorf("Current = %q, want the readable account's light", list.Current)
	}
}

func TestListThemesFailsWithoutAnyAccount(t *testing.T) {
	root := t.TempDir()
	_, err := themesync.ListThemes(themesync.Env{Home: root, StateDir: themesync.DefaultStateDir(root)})
	if err == nil || !strings.Contains(err.Error(), "no Claude account settings.json") {
		t.Fatalf("err = %v, want the missing-accounts error", err)
	}
}

func TestListThemesCurrentIsTheGoverningAccountsValue(t *testing.T) {
	older := time.Now().Add(-time.Hour)
	cases := []struct {
		name    string
		state   func(h chooseHome) string // state.json body, "" for none
		current string
	}{
		{"no state: the newest settings.json of differing accounts", func(chooseHome) string { return "" }, "light"},
		{"sync off: the state is ignored", func(h chooseHome) string {
			return `{"enabled":false,"source":{"path":"` + h.settings(h.cc1) + `"}}`
		}, "light"},
		{"sync on: the account that governed before", func(h chooseHome) string {
			return `{"enabled":true,"source":{"path":"` + h.settings(h.cc1) + `"}}`
		}, "dark-ansi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newChooseHome(t, `{"theme":"light"}`, `{"theme":"dark-ansi"}`)
			if err := os.Chtimes(h.settings(h.cc1), older, older); err != nil {
				t.Fatal(err)
			}
			state := tc.state(h)
			stateFile := filepath.Join(h.env().StateDir, themesync.StateFileName)
			if state != "" {
				h.write(stateFile, state)
			}
			list := h.list()
			if list.Current != tc.current {
				t.Fatalf("Current = %q, want %q", list.Current, tc.current)
			}
			if marked := choiceOf(t, list, tc.current, themesync.KindBuiltin); !marked.Current {
				t.Errorf("%s is not marked current: %v", tc.current, rows(list))
			}
			if state != "" && h.read(stateFile) != state {
				t.Errorf("list wrote the state file: %s", h.read(stateFile))
			}
		})
	}
}

func TestUseThemeWritesEveryAccountAndKeepsTheOtherKeys(t *testing.T) {
	const original = `{
  "zeta": [1, 2.0],
  "n": 1.50,
  "big": 12345678901234567890,
  "env": {"KEEP": "yes"},
  "theme": "dark"
}`
	h := newChooseHome(t, original, original)
	h.theme(h.claude, "hibiscus-night", "Hibiscus Night")
	h.theme(h.cc1, "hibiscus-night", "Hibiscus Night")
	result, err := h.use("hibiscus-night")
	if err != nil {
		t.Fatalf("UseTheme: %v", err)
	}
	wantWritten := []string{h.settings(h.cc1), h.settings(h.claude)} // sorted by real path
	if result.Value != "custom:hibiscus-night" || !reflect.DeepEqual(result.Written, wantWritten) ||
		result.Refused == nil || len(result.Refused) != 0 {
		t.Fatalf("result = %+v, want custom:hibiscus-night written to %v and no refusals", result, wantWritten)
	}
	for _, dir := range []string{h.claude, h.cc1} {
		content := h.read(h.settings(dir))
		for _, kept := range []string{`"n": 1.50`, `"big": 12345678901234567890`, `2.0`, `"KEEP": "yes"`} {
			if !strings.Contains(content, kept) {
				t.Errorf("%s lost %s:\n%s", dir, kept, content)
			}
		}
		if got := h.themeKey(dir); got != "custom:hibiscus-night" {
			t.Errorf("%s theme = %q, want custom:hibiscus-night", dir, got)
		}
		var document map[string]any
		if err := json.Unmarshal([]byte(content), &document); err != nil {
			t.Fatal(err)
		}
		if len(document) != 5 {
			t.Errorf("%s holds keys %v, want the original five", dir, document)
		}
	}
}

func TestUseThemeRefusesTheAccountLackingTheFileAndWritesTheOther(t *testing.T) {
	h := newChooseHome(t, `{"theme":"dark"}`, `{"theme":"dark"}`)
	h.theme(h.claude, "hibiscus-night", "Hibiscus Night")
	result, err := h.use("custom:hibiscus-night")
	if err != nil {
		t.Fatalf("UseTheme: %v (one account was written)", err)
	}
	if !reflect.DeepEqual(result.Written, []string{h.settings(h.claude)}) {
		t.Errorf("Written = %v, want only ~/.claude", result.Written)
	}
	if len(result.Refused) != 1 || result.Refused[0].Path != h.settings(h.cc1) ||
		!strings.Contains(result.Refused[0].Error, "hibiscus-night.json") ||
		!strings.Contains(result.Refused[0].Error, filepath.Join(h.cc1, "themes")) {
		t.Fatalf("Refused = %+v, want ~/.cc/1 naming its themes dir and hibiscus-night.json", result.Refused)
	}
	if got := h.themeKey(h.claude); got != "custom:hibiscus-night" {
		t.Errorf("~/.claude theme = %q, want custom:hibiscus-night", got)
	}
	if got := h.themeKey(h.cc1); got != "dark" {
		t.Errorf("the refused account was written: theme = %q", got)
	}
	if _, err := os.Stat(filepath.Join(h.cc1, "themes", "hibiscus-night.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a theme file was copied between accounts: stat err = %v", err)
	}
}

func TestUseThemeContinuesPastAnEditFailureAndErrorsWhenNothingWasWritten(t *testing.T) {
	h := newChooseHome(t, `{"theme":"dark"}`, `{"theme":"dark"}`)
	failing := func(path string, apply func(map[string]any) error) error {
		if path == h.settings(h.claude) {
			return errors.New("disk full")
		}
		return installer.EditSettings(path, apply)
	}
	result, err := themesync.UseTheme(h.env(), "light", failing)
	if err != nil {
		t.Fatalf("UseTheme: %v (the other account was written)", err)
	}
	if !reflect.DeepEqual(result.Written, []string{h.settings(h.cc1)}) ||
		len(result.Refused) != 1 || result.Refused[0].Path != h.settings(h.claude) ||
		!strings.Contains(result.Refused[0].Error, "disk full") {
		t.Fatalf("result = %+v, want ~/.cc/1 written and ~/.claude refused with the cause", result)
	}

	always := func(string, func(map[string]any) error) error { return errors.New("disk full") }
	result, err = themesync.UseTheme(h.env(), "light", always)
	if err == nil || len(result.Written) != 0 || len(result.Refused) != 2 {
		t.Fatalf("err = %v result = %+v, want an error with both accounts refused", err, result)
	}
}

func TestUseThemeUnknownNameErrorsListingTheValidNames(t *testing.T) {
	h := newChooseHome(t, `{"theme":"dark"}`, `{"theme":"dark"}`)
	h.theme(h.claude, "hibiscus-night", "Hibiscus Night")
	h.theme(h.claude, "light", "Shadowing Light") // collides with a builtin
	_, err := h.use("nonsense")
	if err == nil {
		t.Fatal("an unknown name must error")
	}
	for _, want := range []string{`"nonsense"`, "hibiscus-night", "auto", "dark-daltonized", "light-ansi", "custom:light"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	for _, dir := range []string{h.claude, h.cc1} {
		if got := h.themeKey(dir); got != "dark" {
			t.Errorf("%s was written for an unknown name: theme = %q", dir, got)
		}
	}
}

func TestUseThemeResolvesAStemCollidingWithABuiltinOnlyWithItsPrefix(t *testing.T) {
	cases := []struct{ name, want string }{
		{"light", "light"},
		{"custom:light", "custom:light"},
		{"custom:hibiscus-night", "custom:hibiscus-night"},
		{"hibiscus-night", "custom:hibiscus-night"},
		{"auto", "auto"},
		{"dark-ansi", "dark-ansi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newChooseHome(t, `{"theme":"dark"}`, `{"theme":"dark"}`)
			for _, dir := range []string{h.claude, h.cc1} {
				h.theme(dir, "light", "Shadowing Light")
				h.theme(dir, "hibiscus-night", "Hibiscus Night")
			}
			result, err := h.use(tc.name)
			if err != nil {
				t.Fatalf("UseTheme(%q): %v", tc.name, err)
			}
			if result.Value != tc.want || h.themeKey(h.claude) != tc.want || h.themeKey(h.cc1) != tc.want {
				t.Fatalf("UseTheme(%q) = %+v, claude %q cc1 %q, want %q everywhere",
					tc.name, result, h.themeKey(h.claude), h.themeKey(h.cc1), tc.want)
			}
		})
	}
}

func TestListThemesShowsAStemCollidingWithABuiltinNextToIt(t *testing.T) {
	h := newChooseHome(t, `{"theme":"custom:light"}`, `{"theme":"custom:light"}`)
	for _, dir := range []string{h.claude, h.cc1} {
		h.theme(dir, "light", "Shadowing Light")
	}
	list := h.list()
	custom := choiceOf(t, list, "light", themesync.KindCustom)
	builtin := choiceOf(t, list, "light", themesync.KindBuiltin)
	if custom.Value != "custom:light" || !custom.Current || builtin.Value != "light" || builtin.Current {
		t.Fatalf("custom = %+v builtin = %+v, want only custom:light current", custom, builtin)
	}
}

func TestThemesDirsSymlinkedBetweenAccountsAreOneDirNoMissingAndOneErrorRow(t *testing.T) {
	h := newChooseHome(t, `{"theme":"dark"}`, `{"theme":"dark"}`)
	h.theme(h.claude, "hibiscus-night", "Hibiscus Night")
	h.write(filepath.Join(h.claude, "themes", "broken.json"), `{`)
	if err := os.Symlink(filepath.Join(h.claude, "themes"), filepath.Join(h.cc1, "themes")); err != nil {
		t.Fatal(err)
	}
	list := h.list()
	if len(list.Errors) != 1 {
		t.Errorf("Errors = %+v, want the one broken file once", list.Errors)
	}
	if offered := choiceOf(t, list, "hibiscus-night", themesync.KindCustom); len(offered.Missing) != 0 {
		t.Errorf("hibiscus-night Missing = %v, want none behind the shared dir", offered.Missing)
	}
	result, err := h.use("hibiscus-night")
	if err != nil || len(result.Written) != 2 || len(result.Refused) != 0 {
		t.Fatalf("UseTheme = %+v, %v, want both accounts written", result, err)
	}
}
