package themesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// autoTheme is the theme value that follows the system appearance; Claude
// Code's built-ins are the six in builtinThemes plus this one.
const autoTheme = "auto"

// themeFileSuffix ends a custom theme file's name; the rest is its stem.
const themeFileSuffix = ".json"

// ThemeKind sorts a selectable theme: a custom file in the accounts' themes
// dirs, or one of Claude Code's built-ins (auto included).
type ThemeKind string

const (
	KindCustom  ThemeKind = "custom"
	KindBuiltin ThemeKind = "builtin"
)

// ThemeChoice is one theme `pfm theme use` accepts. Name is what the user
// types (a custom file's stem, or the built-in id); Value is what lands in
// each settings.json `theme` (`custom:{stem}` or the built-in id); Title is
// the custom file's `name`, else Name. Missing lists the account config dirs
// (~-shortened) whose themes dir lacks a custom theme; use refuses those.
type ThemeChoice struct {
	Name    string    `json:"name"`
	Value   string    `json:"value"`
	Title   string    `json:"title"`
	Kind    ThemeKind `json:"kind"`
	Current bool      `json:"current"`
	Missing []string  `json:"missing,omitempty"`
}

// ThemeListError is a themes dir or settings file the list could not read:
// the path and the failure, drawn as an ERROR row, never as absence.
type ThemeListError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// ThemeList is `pfm theme list`: Current is the governing account's value
// ("" when no account resolves), Themes custom first then built-in, each
// sorted by Name, and Errors every read that failed.
type ThemeList struct {
	Current string           `json:"current"`
	Themes  []ThemeChoice    `json:"themes"`
	Errors  []ThemeListError `json:"errors"`
}

// SettingsEdit edits one settings.json in place, atomically (the installer's
// EditSettings); themesync takes it injected because the installer imports
// themesync.
type SettingsEdit func(path string, apply func(document map[string]any) error) error

// UseResult is what `pfm theme use` wrote: the value, and each account
// settings.json it set (Written) or refused with the reason (Refused).
type UseResult struct {
	Value   string           `json:"value"`
	Written []string         `json:"written"`
	Refused []ThemeListError `json:"refused"`
}

// themeScan is what the accounts' themes dirs offer: every account
// settings.json, the choices (custom first, then built-in; Current unset) and
// every themes dir or theme file that could not be read.
type themeScan struct {
	paths   []string
	choices []ThemeChoice
	errors  []ThemeListError
}

func (scan *themeScan) fail(path, reason string) {
	scan.errors = append(scan.errors, ThemeListError{Path: path, Error: reason})
}

// cause is the failure under a themesync error: the read or parse error
// without the "themesync: read {path}:" prefix an ERROR row already carries.
func cause(err error) string {
	if inner := errors.Unwrap(err); inner != nil {
		return inner.Error()
	}
	return err.Error()
}

// scanThemes reads the themes dir of every account. The custom themes are the
// union of the `*.json` stems; a dir shared by symlink between accounts is
// read, and its failures recorded, once. Only the account listing failing is
// an error.
func scanThemes(env Env) (themeScan, error) {
	paths, err := accountSettings(env.Home)
	if err != nil {
		return themeScan{}, err
	}
	scan := themeScan{paths: paths, errors: []ThemeListError{}}
	offered := map[string]map[string]string{} // real themes dir -> stem -> title
	dirs := make([]string, len(paths))
	titles := map[string]string{}
	for index, path := range paths {
		dirs[index] = themesDir(filepath.Dir(path))
		if _, read := offered[dirs[index]]; !read {
			offered[dirs[index]] = scan.readThemesDir(dirs[index])
		}
		for stem, title := range offered[dirs[index]] {
			if known, seen := titles[stem]; !seen || known == "" {
				titles[stem] = title
			}
		}
	}
	stems := make([]string, 0, len(titles))
	for stem := range titles {
		stems = append(stems, stem)
	}
	sort.Strings(stems)
	for _, stem := range stems {
		choice := ThemeChoice{Name: stem, Value: customPrefix + stem, Title: titles[stem], Kind: KindCustom}
		if choice.Title == "" {
			choice.Title = stem
		}
		for index, path := range paths {
			if _, has := offered[dirs[index]][stem]; !has {
				choice.Missing = append(choice.Missing, shortHome(env.Home, filepath.Dir(path)))
			}
		}
		scan.choices = append(scan.choices, choice)
	}
	names := []string{autoTheme}
	for name := range builtinThemes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		scan.choices = append(scan.choices, ThemeChoice{Name: name, Value: name, Title: name, Kind: KindBuiltin})
	}
	return scan, nil
}

// readThemesDir lists one themes dir as stem -> title ("" for a file without
// a `name`). An absent dir offers nothing and is no failure; a dir that cannot
// be read, a dangling link or a file in its place is recorded, never taken for
// absent.
func (scan *themeScan) readThemesDir(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		if _, linkErr := os.Lstat(dir); linkErr == nil {
			scan.fail(dir, "dangling symlink: "+err.Error())
		} else if !errors.Is(linkErr, os.ErrNotExist) {
			scan.fail(dir, linkErr.Error())
		}
		return nil
	default:
		scan.fail(dir, err.Error())
		return nil
	}
	themes := map[string]string{}
	for _, entry := range entries {
		stem, isTheme := strings.CutSuffix(entry.Name(), themeFileSuffix)
		if !isTheme {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if stem == "" {
			scan.fail(path, "a theme file needs a name before "+themeFileSuffix)
			continue
		}
		title, listed, err := themeTitle(path)
		if err != nil {
			scan.fail(path, err.Error())
		}
		if listed {
			themes[stem] = title
		}
	}
	return themes
}

// themeTitle reads the `name` of one custom theme file. A file that does
// not parse is still listed (listed true, with its failure); a path Claude cannot
// read as a theme file, a dangling link or a directory, is not.
func themeTitle(path string) (title string, listed bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s is not a regular file (%s)", path, info.Mode().Type())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", true, err
	}
	var file customFile
	if err := json.Unmarshal(content, &file); err != nil {
		return "", true, err
	}
	return file.Name, true, nil
}

// ListThemes is `pfm theme list`: every theme `use` accepts, the accounts
// that lack a custom one, every read that failed, and which one is current —
// the value of the governing account, chosen as resolveSource chooses it from
// the stored memory while the sync is on, from none while it is off. It
// writes nothing.
func ListThemes(env Env) (ThemeList, error) {
	scan, err := scanThemes(env)
	if err != nil {
		return ThemeList{}, err
	}
	list := ThemeList{Themes: scan.choices, Errors: scan.errors}
	var accounts []account
	for _, path := range scan.paths {
		read, err := readAccount(path)
		if err != nil {
			list.Errors = append(list.Errors, ThemeListError{Path: path, Error: cause(err)})
			continue
		}
		accounts = append(accounts, read)
	}
	store := stateStore{dir: env.withStateDir().StateDir}
	state, found, err := store.load()
	if err != nil {
		list.Errors = append(list.Errors, ThemeListError{Path: store.statePath(), Error: cause(err)})
	}
	if !found || !state.Enabled {
		state = themeState{}
	}
	if len(accounts) == 0 {
		return list, nil
	}
	previous := ""
	if state.Source != nil {
		previous = state.Source.Path
	}
	governing, _ := govern(state.Accounts, previous, accounts)
	list.Current = governing.Value
	if list.Current == "" {
		list.Current = builtinDefault
	}
	for index := range list.Themes {
		list.Themes[index].Current = list.Themes[index].Value == list.Current
	}
	return list, nil
}

// typedName is how a user names a theme: its Name, or its Value when a
// custom stem shares its Name with a built-in (that one needs `custom:`).
func typedName(choice ThemeChoice) string {
	if _, builtin := builtinThemes[choice.Name]; choice.Kind == KindCustom && (builtin || choice.Name == autoTheme) {
		return choice.Value
	}
	return choice.Name
}

// chooseByName resolves what the user typed: an exact Value first (`custom:x`,
// a built-in id, `auto`), then a custom stem.
func chooseByName(choices []ThemeChoice, name string) (ThemeChoice, error) {
	for _, choice := range choices {
		if choice.Value == name {
			return choice, nil
		}
	}
	for _, choice := range choices {
		if choice.Kind == KindCustom && choice.Name == name {
			return choice, nil
		}
	}
	valid := make([]string, 0, len(choices))
	for _, choice := range choices {
		valid = append(valid, typedName(choice))
	}
	return ThemeChoice{}, fmt.Errorf("unknown theme %q; valid names: %s", name, strings.Join(valid, ", "))
}

// themeFileProblem says why account dir cannot use the custom theme stem:
// its themes dir lacks the file, or the file is not one Claude reads. ""
// when the file is there.
func themeFileProblem(dir, stem string) string {
	file := filepath.Join(themesDir(dir), stem+themeFileSuffix)
	info, err := os.Stat(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("theme file %s%s is missing from %s", stem, themeFileSuffix, filepath.Dir(file))
	case err != nil:
		return err.Error()
	case !info.Mode().IsRegular():
		return fmt.Sprintf("%s is not a regular file (%s)", file, info.Mode().Type())
	}
	return ""
}

// UseTheme is `pfm theme use`: it sets the `theme` key of every account
// settings.json through edit, touching no other key. An account whose themes
// dir lacks the custom theme's file is refused, never given a copy (the themes
// dirs are the installer's; themesync only reads them), and an account whose
// edit fails is refused with the failure while the run goes on. The result
// comes back with every refusal even when the error says nothing was written.
func UseTheme(env Env, name string, edit SettingsEdit) (UseResult, error) {
	scan, err := scanThemes(env)
	if err != nil {
		return UseResult{}, err
	}
	choice, err := chooseByName(scan.choices, name)
	if err != nil {
		return UseResult{}, err
	}
	result := UseResult{Value: choice.Value, Written: []string{}, Refused: []ThemeListError{}}
	for _, path := range scan.paths {
		if choice.Kind == KindCustom {
			if problem := themeFileProblem(filepath.Dir(path), choice.Name); problem != "" {
				result.Refused = append(result.Refused, ThemeListError{Path: path, Error: problem})
				continue
			}
		}
		if err := edit(path, func(document map[string]any) error {
			document["theme"] = choice.Value
			return nil
		}); err != nil {
			result.Refused = append(result.Refused, ThemeListError{Path: path, Error: err.Error()})
			continue
		}
		result.Written = append(result.Written, path)
	}
	if len(result.Written) == 0 {
		return result, fmt.Errorf("no account settings.json was written for theme %s", choice.Value)
	}
	return result, nil
}
