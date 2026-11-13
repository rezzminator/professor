package themesync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyShippedTheme copies the shipped theme name into a temp directory named
// after it, for a test to break.
func copyShippedTheme(t *testing.T, name string) string {
	t.Helper()
	source := filepath.Join(shippedThemesDir, name)
	target := filepath.Join(t.TempDir(), name)
	err := filepath.WalkDir(source, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, at)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o755)
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, relative), content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", source, err)
	}
	return target
}

func TestProfileFilesIsTheFixedSet(t *testing.T) {
	if got, want := strings.Join(
		ProfileFiles(),
		"\n",
	), "claude.json\nherdr.json\nterminal.json\ntheme.json\ntmux.json\nvscode/chrome.json\nvscode/semantic.json\nvscode/syntax/comment.json\nvscode/syntax/constant.json\nvscode/syntax/diagnostic.json\nvscode/syntax/entity.json\nvscode/syntax/keyword.json\nvscode/syntax/markup.json\nvscode/syntax/meta.json\nvscode/syntax/other.json\nvscode/syntax/punctuation.json\nvscode/syntax/source.json\nvscode/syntax/string.json\nvscode/syntax/support.json\nvscode/syntax/variable.json\nvscode/workbench/activitybar.json\nvscode/workbench/base.json\nvscode/workbench/chat.json\nvscode/workbench/controls.json\nvscode/workbench/debug-testing.json\nvscode/workbench/diff-merge.json\nvscode/workbench/editor-guides.json\nvscode/workbench/editor-gutter.json\nvscode/workbench/editor-widgets.json\nvscode/workbench/editor.json\nvscode/workbench/notebook.json\nvscode/workbench/notifications.json\nvscode/workbench/panel.json\nvscode/workbench/quick-input.json\nvscode/workbench/sidebar-explorer.json\nvscode/workbench/statusbar.json\nvscode/workbench/symbols.json\nvscode/workbench/tabs.json\nvscode/workbench/terminal.json\nvscode/workbench/titlebar-menus.json"; got != want {
		t.Errorf("ProfileFiles = %q, want %q", got, want)
	}
}

// maxThemeFileLines bounds every shipped theme file: each one reads in a
// single sitting, so no part of a theme grows back into one long file.
const maxThemeFileLines = 450

// TestEveryShippedThemeFileStaysReadable holds every file of every shipped
// theme to maxThemeFileLines.
func TestEveryShippedThemeFileStaysReadable(t *testing.T) {
	judged := 0
	err := filepath.WalkDir(shippedThemesDir, func(at string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(at, ".json") {
			return err
		}
		content, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		judged++
		if lines := bytes.Count(content, []byte("\n")); lines > maxThemeFileLines {
			t.Errorf("%s: %d lines, over the %d a theme file may hold", at, lines, maxThemeFileLines)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if judged == 0 {
		t.Fatalf("no theme file under %s: the bound above judged nothing", shippedThemesDir)
	}
}

// TestEveryShippedThemeIsACanonicalDirectory: every theme in
// templates/themes/ holds exactly ProfileFiles, each in canonical form, and
// splitting the profile it assembles writes those files back byte for byte.
func TestEveryShippedThemeIsACanonicalDirectory(t *testing.T) {
	t.Parallel() // read-only over the shipped themes; no process state
	entries, err := os.ReadDir(shippedThemesDir)
	if err != nil {
		t.Fatal(err)
	}
	themes := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			if strings.HasSuffix(entry.Name(), ".json") {
				t.Errorf("%s: a single-file profile beside the theme directories", entry.Name())
			}
			continue
		}
		themes++
		dir := filepath.Join(shippedThemesDir, entry.Name())
		content, err := ReadProfileDir(dir)
		if err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}
		profile, err := ParseProfile(content)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		files, err := themeDirFiles(profile)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		for _, file := range ProfileFiles() {
			disk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
			if err != nil || !bytes.Equal(disk, files[file]) {
				t.Errorf("%s/%s: on disk differs from its split (%v)", entry.Name(), file, err)
			}
		}
	}
	if themes == 0 {
		t.Fatalf("no theme directory in %s: the check above judged nothing", shippedThemesDir)
	}
}

// TestReadProfileDirRefusesABrokenTheme breaks one copy of a shipped theme
// per case; each must fail naming what is wrong.
func TestReadProfileDirRefusesABrokenTheme(t *testing.T) {
	t.Parallel() // read-only over the shipped themes; no process state
	write := func(t *testing.T, dir, file, content string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(t *testing.T, dir, file string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	canonical := func(file, content string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) { write(t, dir, file, canonicalOrFail(t, content)) }
	}
	cases := []struct {
		name   string
		damage func(t *testing.T, dir string)
		want   string
	}{
		{
			name: "missing file",
			damage: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "vscode", "workbench", "tabs.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [vscode/workbench/tabs.json], extra []",
		},
		{
			name:   "extra file",
			damage: func(t *testing.T, dir string) { write(t, dir, "notes.json", "{}\n") },
			want:   "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [], extra [notes.json]",
		},
		{
			name:   "extra workbench file",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/workbench/misc.json", "{}\n") },
			want:   "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [], extra [vscode/workbench/misc.json]",
		},
		{
			name:   "extra directory",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/extra/editor.json", "{}\n") },
			want:   "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [], extra [vscode/extra/]",
		},
		{
			name: "symlinked file",
			damage: func(t *testing.T, dir string) {
				path := filepath.Join(dir, "tmux.json")
				if err := os.Rename(path, filepath.Join(dir, "..", "tmux.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join("..", "tmux.json"), path); err != nil {
					t.Fatal(err)
				}
			},
			want: "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [tmux.json], extra [tmux.json]",
		},
		{
			name: "colour in the wrong group's file",
			damage: func(t *testing.T, dir string) {
				editor := read(t, dir, "vscode/workbench/editor.json")
				tabs := read(t, dir, "vscode/workbench/tabs.json")
				line := ""
				for _, candidate := range strings.Split(tabs, "\n") {
					if strings.Contains(candidate, `"tab.activeBackground"`) {
						line = strings.TrimSuffix(strings.TrimSpace(candidate), ",")
					}
				}
				if line == "" {
					t.Fatal("tokyo-night sets no tab.activeBackground to move")
				}
				write(t, dir, "vscode/workbench/tabs.json", canonicalOrFail(t,
					strings.Replace(strings.Replace(tabs, line+",", "", 1), line, `"tab.border": "#000000"`, 1)))
				write(t, dir, "vscode/workbench/editor.json", canonicalOrFail(t,
					strings.Replace(editor, "{\n", "{\n  "+line+",\n", 1)))
			},
			want: "theme {dir}: vscode/workbench/editor.json: colour \"tab.activeBackground\" belongs in vscode/workbench/tabs.json",
		},
		{
			name: "missing syntax family file",
			damage: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "vscode", "syntax", "meta.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [vscode/syntax/meta.json], extra []",
		},
		{
			name:   "the single syntax file beside the family files",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/syntax.json", "[]\n") },
			want:   "theme {dir}: the file set differs from every theme's (ProfileFiles): missing [], extra [vscode/syntax.json]",
		},
		{
			name: "syntax rule in the wrong family's file",
			damage: func(t *testing.T, dir string) {
				comments, metas := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json")),
					syntaxRules(t, read(t, dir, "vscode/syntax/meta.json"))
				metas["0000"] = comments["0000"]
				delete(comments, "0000")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
				write(t, dir, "vscode/syntax/meta.json", syntaxFileOrFail(t, metas))
			},
			want: "theme {dir}: vscode/syntax/meta.json: rule \"0000\" (first scope \"comment\") belongs in vscode/syntax/comment.json",
		},
		{
			name: "syntax ordinal used twice",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["0001"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: "theme {dir}: vscode/syntax/keyword.json: rule \"0001\" holds an ordinal vscode/syntax/comment.json already holds",
		},
		{
			name: "syntax ordinal gap",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["9000"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: "theme {dir}: vscode/syntax: no rule holds ordinal \"0002\": the ordinals run from 0000 without a gap",
		},
		{
			name: "syntax ordinal not four digits",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["2"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: "theme {dir}: vscode/syntax/comment.json: rule \"2\": an ordinal is four digits, the rule's index in tokenColors",
		},
		{
			name: "file not in canonical form",
			damage: func(t *testing.T, dir string) {
				write(t, dir, "theme.json", `{"name":"Tokyo Night · neon pink","base":"dark"}`)
			},
			want: "theme {dir}: theme.json: not in canonical form, keys sorted, two-space indent, one trailing newline (jq -S --indent 2 . writes it): line 1 should read \"{\"",
		},
		{
			name: "unknown key",
			damage: func(t *testing.T, dir string) {
				write(
					t,
					dir,
					"theme.json",
					"{\n  \"base\": \"dark\",\n  \"colour\": \"#000000\",\n  \"name\": \"N\"\n}\n",
				)
			},
			want: "theme {dir}: theme.json: json: unknown field \"colour\"",
		},
		{
			name: "unknown colour id",
			damage: func(t *testing.T, dir string) {
				write(t, dir, "vscode/workbench/editor.json", "{\n  \"editors.x\": \"#000000\"\n}\n")
			},
			want: "theme {dir}: vscode/workbench/editor.json: VS Code colour \"editors.x\": no vscode/workbench group claims \"editors\" (profile_groups.go)",
		},
		{
			name:   "invalid base",
			damage: canonical("theme.json", `{"name":"Broken","base":"no-such-base"}`),
			want:   "theme {dir}: theme.json: base \"no-such-base\": not a Claude Code built-in theme (dark, dark-ansi, dark-daltonized, light, light-ansi, light-daltonized)",
		},
		{
			name:   "unknown Claude token",
			damage: canonical("claude.json", `{"noSuchToken":"#123456"}`),
			want:   "theme {dir}: claude.json: overrides \"noSuchToken\": not a Claude Code theme token (claude_token_ids.txt)",
		},
		{
			name:   "invalid Claude colour",
			damage: canonical("claude.json", `{"claude":"not-a-colour"}`),
			want:   "theme {dir}: claude.json: overrides \"claude\": themesync: colour \"not-a-colour\" is not rgb(), #rgb, #rrggbb, ansi256() or ansi:name",
		},
		{
			name:   "unknown tmux pin",
			damage: canonical("tmux.json", `{"no-such-option":"#123456"}`),
			want:   "theme {dir}: tmux.json: tmux pin \"no-such-option\": tmux renders no such key",
		},
		{
			name:   "unknown Herdr pin",
			damage: canonical("herdr.json", `{"noSuchToken":"#123456"}`),
			want:   "theme {dir}: herdr.json: herdr pin \"noSuchToken\": herdr renders no such key",
		},
		{
			name:   "unknown terminal pin",
			damage: canonical("terminal.json", `{"noSuchToken":"#123456"}`),
			want:   "theme {dir}: terminal.json: vscode colors pin \"noSuchToken\": vscode colors renders no such key",
		},
		{
			name:   "unknown chrome pin",
			damage: canonical("vscode/chrome.json", `{"noSuchToken":"#123456"}`),
			want:   "theme {dir}: vscode/chrome.json: vscode chrome pin \"noSuchToken\": vscode chrome renders no such key",
		},
		{
			name:   "invalid semantic colour",
			damage: canonical("vscode/semantic.json", `{"class":"not-a-colour"}`),
			want:   "theme {dir}: vscode/semantic.json: vscode theme semanticTokenColors \"class\": foreground \"not-a-colour\" is not a hex colour",
		},
		{
			name:   "invalid workbench colour",
			damage: canonical("vscode/workbench/editor.json", `{"editor.background":"not-a-colour"}`),
			want:   "theme {dir}: vscode/workbench/editor.json: vscode theme colors \"editor.background\": \"not-a-colour\" is not #rgb, #rgba, #rrggbb or #rrggbbaa",
		},
		{
			name: "invalid syntax colour",
			damage: canonical(
				"vscode/syntax/comment.json",
				`{"0000":{"scope":["comment"],"settings":{"foreground":"not-a-colour"}}}`,
			),
			want: "theme {dir}: vscode/syntax/comment.json: rule \"0000\": vscode theme tokenColors[0]: foreground \"not-a-colour\" is not a hex colour",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyShippedTheme(t, "tokyo-night")
			if _, err := ReadProfileDir(dir); err != nil {
				t.Fatalf("intact copy refused: %v", err)
			}
			tc.damage(t, dir)
			_, err := ReadProfileDir(dir)
			if got := strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}"); got != tc.want {
				t.Fatalf("ReadProfileDir = %q, want %q", got, tc.want)
			}
		})
	}
}

// The name is a title only: a theme.json without one reads, and the profile it
// assembles is unnamed (its stem titles it), where it once was refused whole.
func TestReadProfileDirAcceptsAThemeWithoutAName(t *testing.T) {
	dir := copyShippedTheme(t, "tokyo-night")
	themeFile := filepath.Join(dir, "theme.json")
	original, err := os.ReadFile(themeFile)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(original, &meta); err != nil {
		t.Fatal(err)
	}
	delete(meta, "name")
	body, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(themeFile, []byte(canonicalOrFail(t, string(body))), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := ReadProfileDir(dir)
	if err != nil {
		t.Fatalf("ReadProfileDir of an unnamed theme = %v, want it read", err)
	}
	profile, err := ParseProfile(document)
	if err != nil || profile.Name != "" || profile.Base != "dark" {
		t.Fatalf("unnamed theme = %+v, %v; want no name and base dark", profile, err)
	}
}

func TestReadProfileDirRefusesASingleFileProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokyo-night.json")
	if err := os.WriteFile(path, []byte(`{"name":"N","base":"dark","overrides":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadProfileDir(path)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		dir,
		"{dir}",
	), "theme {dir}/tokyo-night.json: not a directory: a theme is a directory of theme.json"; got != want {
		t.Fatalf("ReadProfileDir(file) = %q, want %q", got, want)
	}
}

// TestAssembleProfileRoundTripsASplit: a profile setting nothing past its
// palette splits into the full file set, `{}` and `[]` where it sets nothing,
// and assembles back to the same document.
func TestAssembleProfileRoundTripsASplit(t *testing.T) {
	profile, err := ParseProfile([]byte(`{"name":"Bare","base":"light","overrides":{"claude":"#123456"}}`))
	if err != nil {
		t.Fatal(err)
	}
	files, err := themeDirFiles(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["vscode/syntax/meta.json"]) != "{}\n" || string(files["herdr.json"]) != "{}\n" ||
		string(files["vscode/workbench/editor.json"]) != "{}\n" {
		t.Fatalf("empty parts = %q %q %q", files["vscode/syntax/meta.json"], files["herdr.json"],
			files["vscode/workbench/editor.json"])
	}
	document, err := AssembleProfile(func(file string) ([]byte, error) { return files[file], nil })
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"name\": \"Bare\",\n  \"base\": \"light\",\n  \"overrides\": {\n    \"claude\": \"#123456\"\n  }\n}\n"
	if string(document) != want {
		t.Fatalf("assembled =\n%s\nwant\n%s", document, want)
	}
}

// syntaxRules decodes a vscode/syntax family file: its rules by ordinal.
func syntaxRules(t *testing.T, content string) map[string]json.RawMessage {
	t.Helper()
	rules := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(content), &rules); err != nil {
		t.Fatalf("syntax file %q: %v", content, err)
	}
	return rules
}

// syntaxFileOrFail encodes rules as a canonical vscode/syntax family file.
func syntaxFileOrFail(t *testing.T, rules map[string]json.RawMessage) string {
	t.Helper()
	content, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalOrFail(t, string(content))
}

func canonicalOrFail(t *testing.T, content string) string {
	t.Helper()
	canonical, err := canonicalJSON([]byte(content))
	if err != nil {
		t.Fatalf("canonical %q: %v", content, err)
	}
	return string(canonical)
}
