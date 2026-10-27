package themesync

import (
	"bytes"
	"encoding/json"
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
	files := ProfileFiles()
	if want := 7 + len(workbenchGroups) + len(syntaxFamilyNames()); len(files) != want {
		t.Fatalf("ProfileFiles has %d files, want %d: 7, one per workbench group and one per syntax family: %v",
			len(files), want, files)
	}
	listed := "\n" + strings.Join(files, "\n") + "\n"
	for _, want := range []string{
		"theme.json", "claude.json", "terminal.json", "tmux.json", "herdr.json", "vscode/chrome.json",
		"vscode/semantic.json", "vscode/syntax/comment.json", "vscode/syntax/meta.json", "vscode/syntax/other.json",
		"vscode/workbench/editor.json", "vscode/workbench/tabs.json",
	} {
		if !strings.Contains(listed, "\n"+want+"\n") {
			t.Errorf("ProfileFiles lacks %s: %v", want, files)
		}
	}
	if strings.Contains(listed, "\nvscode/syntax.json\n") {
		t.Errorf("ProfileFiles still holds the single vscode/syntax.json: %v", files)
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
			want: "missing [vscode/workbench/tabs.json], extra []",
		},
		{
			name:   "extra file",
			damage: func(t *testing.T, dir string) { write(t, dir, "notes.json", "{}\n") },
			want:   "missing [], extra [notes.json]",
		},
		{
			name:   "extra workbench file",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/workbench/misc.json", "{}\n") },
			want:   "missing [], extra [vscode/workbench/misc.json]",
		},
		{
			name:   "extra directory",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/extra/editor.json", "{}\n") },
			want:   "missing [], extra [vscode/extra/]",
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
			want: "missing [tmux.json], extra [tmux.json]",
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
			want: `vscode/workbench/editor.json: colour "tab.activeBackground" belongs in vscode/workbench/tabs.json`,
		},
		{
			name: "missing syntax family file",
			damage: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "vscode", "syntax", "meta.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing [vscode/syntax/meta.json], extra []",
		},
		{
			name:   "the single syntax file beside the family files",
			damage: func(t *testing.T, dir string) { write(t, dir, "vscode/syntax.json", "[]\n") },
			want:   "missing [], extra [vscode/syntax.json]",
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
			want: `vscode/syntax/meta.json: rule "0000" (first scope "comment") belongs in vscode/syntax/comment.json`,
		},
		{
			name: "syntax ordinal used twice",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["0001"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: `vscode/syntax/keyword.json: rule "0001" holds an ordinal vscode/syntax/comment.json already holds`,
		},
		{
			name: "syntax ordinal gap",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["9000"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: `vscode/syntax: no rule holds ordinal "0002"`,
		},
		{
			name: "syntax ordinal not four digits",
			damage: func(t *testing.T, dir string) {
				comments := syntaxRules(t, read(t, dir, "vscode/syntax/comment.json"))
				comments["2"] = comments["0002"]
				delete(comments, "0002")
				write(t, dir, "vscode/syntax/comment.json", syntaxFileOrFail(t, comments))
			},
			want: `vscode/syntax/comment.json: rule "2": an ordinal is four digits`,
		},
		{
			name: "file not in canonical form",
			damage: func(t *testing.T, dir string) {
				write(t, dir, "theme.json", `{"name":"Tokyo Night · neon pink","base":"dark"}`)
			},
			want: "theme.json: not in canonical form",
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
			want: `theme.json: json: unknown field "colour"`,
		},
		{
			name: "unknown colour id",
			damage: func(t *testing.T, dir string) {
				write(t, dir, "vscode/workbench/editor.json", "{\n  \"editors.x\": \"#000000\"\n}\n")
			},
			want: `vscode/workbench/editor.json: VS Code colour "editors.x": no vscode/workbench group claims "editors"`,
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
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), dir) {
				t.Fatalf("ReadProfileDir = %v, want an error naming %s and %q", err, dir, tc.want)
			}
		})
	}
}

// Semantic validation must retain the source file, not just the assembled
// profile name: an author needs to know which of the forty files to fix.
func TestReadProfileDirNamesInvalidPart(t *testing.T) {
	cases := []struct {
		name, file, content string
	}{
		{"missing name", "theme.json", `{"base":"dark"}`},
		{"invalid base", "theme.json", `{"name":"Broken","base":"no-such-base"}`},
		{"unknown Claude token", "claude.json", `{"noSuchToken":"#123456"}`},
		{"invalid Claude colour", "claude.json", `{"claude":"not-a-colour"}`},
		{"unknown tmux pin", "tmux.json", `{"no-such-option":"#123456"}`},
		{"unknown Herdr pin", "herdr.json", `{"noSuchToken":"#123456"}`},
		{"unknown terminal pin", "terminal.json", `{"noSuchToken":"#123456"}`},
		{"unknown chrome pin", "vscode/chrome.json", `{"noSuchToken":"#123456"}`},
		{"invalid semantic colour", "vscode/semantic.json", `{"class":"not-a-colour"}`},
		{"invalid workbench colour", "vscode/workbench/editor.json", `{"editor.background":"not-a-colour"}`},
		{
			"invalid syntax colour", "vscode/syntax/comment.json",
			`{"0000":{"scope":["comment"],"settings":{"foreground":"not-a-colour"}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyShippedTheme(t, "tokyo-night")
			file := filepath.Join(dir, filepath.FromSlash(tc.file))
			if err := os.WriteFile(file, []byte(canonicalOrFail(t, tc.content)), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ReadProfileDir(dir)
			if err == nil || !strings.Contains(err.Error(), tc.file+":") {
				t.Fatalf("ReadProfileDir = %v, want an error naming %s", err, tc.file)
			}
		})
	}
}

func TestReadProfileDirRefusesASingleFileProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokyo-night.json")
	if err := os.WriteFile(path, []byte(`{"name":"N","base":"dark","overrides":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProfileDir(path); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("ReadProfileDir(file) = %v, want not a directory", err)
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
