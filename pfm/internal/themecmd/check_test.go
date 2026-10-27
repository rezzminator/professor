package themecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

const checkGoodProfile = `{"name":"Paper Day","base":"light","overrides":{"claude":"#c2410c"}}`

// checkDeps is a run whose home is home; check reads nothing else from Deps.
func checkDeps(home string) Deps {
	return Deps{Env: func() (themesync.Env, error) { return themesync.Env{Home: home}, nil }}
}

// writeTheme writes profile as the theme directory dir/name/ pfm theme check
// reads (themesync.SplitProfile), then each of replace's files (slash path ->
// body, an empty body removing the file) over it, and returns its path.
func writeTheme(t *testing.T, dir, name, profile string, replace map[string]string) string {
	t.Helper()
	files, err := themesync.SplitProfile([]byte(profile))
	if err != nil {
		t.Fatalf("split fixture profile %s: %v", name, err)
	}
	theme := filepath.Join(dir, name)
	for file, body := range files {
		writeFile(t, filepath.Join(theme, filepath.FromSlash(file)), string(body))
	}
	for file, body := range replace {
		target := filepath.Join(theme, filepath.FromSlash(file))
		if body == "" {
			if err := os.Remove(target); err != nil {
				t.Fatalf("remove %s: %v", target, err)
			}
			continue
		}
		writeFile(t, target, body)
	}
	return theme
}

func writeFile(t *testing.T, target, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
}

// canonical is value as a theme file holds it: keys sorted, two-space indent,
// one trailing newline.
func canonical(t *testing.T, value any) string {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode %v: %v", value, err)
	}
	return string(content) + "\n"
}

func runCheck(t *testing.T, deps Deps, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), append([]string{verbCheck}, args...), deps, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestCheckPassesAThemeAndRefusesAnUnknownKeyByFileAndKey(t *testing.T) {
	dir := t.TempDir()
	good := writeTheme(t, dir, "paper-day", checkGoodProfile, nil)
	bad := writeTheme(t, dir, "typo", checkGoodProfile, map[string]string{
		"theme.json": canonical(t, map[string]string{"name": "Typo", "base": "dark", "basee": "light"}),
	})

	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), good, bad)
	if code != 1 {
		t.Fatalf("one theme refused must exit 1, got %d\n%s", code, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("want an ok row, a FAIL row and the tally, got %q", lines)
	}
	if !strings.HasPrefix(lines[0], "ok") || !strings.Contains(lines[0], "Professor · Paper Day") ||
		!strings.Contains(lines[0], " vs ") || !strings.HasSuffix(lines[0], good) {
		t.Fatalf("ok row must name the VS Code theme, its light base and the directory: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "FAIL theme "+bad+": theme.json:") || !strings.Contains(lines[1], `"basee"`) {
		t.Fatalf("FAIL row must name the directory, the file and the unknown key: %q", lines[1])
	}
	if lines[2] != "1 of 2 profiles pass" {
		t.Fatalf("tally=%q", lines[2])
	}

	code, stdout, _ = runCheck(t, checkDeps(t.TempDir()), good, "--json")
	var outcome checkOutcome
	if err := json.Unmarshal([]byte(stdout), &outcome); err != nil {
		t.Fatalf("--json: %v\n%s", err, stdout)
	}
	got := outcome.Profiles
	if code != 0 || len(got) != 1 || !got[0].OK || got[0].ID != "professor-paper-day" || got[0].UITheme != "vs" ||
		got[0].Colors < 300 || got[0].TokenColors < 30 || got[0].SemanticTokenColors < 20 {
		t.Fatalf("--json of a passing theme: code %d, %+v", code, got)
	}
}

// TestCheckRefusesAThemeWhoseFilesLeaveTheLayout: a theme missing a file,
// holding one more, or with a workbench colour in another group's file each
// FAILs naming the file — the layout is enforced, never a convention.
func TestCheckRefusesAThemeWhoseFilesLeaveTheLayout(t *testing.T) {
	dir := t.TempDir()
	const pinned = `{"name":"Pinned","base":"dark","overrides":{"claude":"#c2410c"},` +
		`"surfaces":{"vscode":{"theme":{"colors":{"tab.activeBackground":"#101820"}}}}}`
	missing := writeTheme(t, dir, "missing", pinned, map[string]string{"tmux.json": ""})
	extra := writeTheme(t, dir, "extra", pinned, map[string]string{"vscode/notes.json": "{}\n"})
	wrong := writeTheme(t, dir, "wrong", pinned, map[string]string{
		"vscode/workbench/tabs.json":   "{}\n",
		"vscode/workbench/editor.json": canonical(t, map[string]string{"tab.activeBackground": "#101820"}),
	})
	good := writeTheme(t, dir, "good", pinned, nil)

	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), missing, extra, wrong, good)
	if code != 1 || !strings.Contains(stdout, "1 of 4 profiles pass") {
		t.Fatalf("three broken layouts must FAIL beside one passing theme: code %d\n%s", code, stdout)
	}
	for theme, want := range map[string]string{
		missing: "missing [tmux.json], extra []",
		extra:   "missing [], extra [vscode/notes.json]",
		wrong:   `vscode/workbench/editor.json: colour "tab.activeBackground" belongs in vscode/workbench/tabs.json`,
	} {
		if !strings.Contains(stdout, "FAIL theme "+theme+": ") || !strings.Contains(stdout, want) {
			t.Errorf("%s must FAIL naming %q:\n%s", theme, want, stdout)
		}
	}
}

func TestCheckRefusesAFileAPathThatIsNotThereAndAPipe(t *testing.T) {
	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	writeFile(t, readme, "# Themes\n")
	missing := filepath.Join(dir, "gone")
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", pipe, err)
	}
	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), readme, missing, pipe)
	if code != 1 || !strings.Contains(stdout, "FAIL theme "+readme+": not a directory") ||
		!strings.Contains(stdout, "FAIL theme "+missing+": lstat "+missing) ||
		!strings.Contains(stdout, "FAIL theme "+pipe+": not a directory") ||
		!strings.Contains(stdout, "0 of 3 profiles pass") {
		t.Fatalf("a file, a missing path and a pipe must each FAIL by path: code %d\n%s", code, stdout)
	}
}

// TestCheckFailsAProfileWhoseThemeCannotBeRead: a theme that renders but
// draws text a reader cannot read is a FAIL naming the place, never ok; a
// light palette whose Clawd is dark ink still renders readable inputs.
func TestCheckFailsAProfileWhoseThemeCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	ink := writeTheme(t, dir, "ink", `{"name":"Ink","base":"dark","overrides":{},"surfaces":{"vscode":{"theme":`+
		`{"colors":{"input.background":"#2a241f","input.foreground":"#2a241f"}}}}}`, nil)
	lantern := writeTheme(t, dir, "lantern",
		`{"name":"Lantern","base":"light","overrides":{"clawd_background":"#2a241f"}}`, nil)
	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), ink, lantern)
	if code != 1 || !strings.Contains(stdout, "FAIL theme "+ink+": readability:") ||
		!strings.Contains(stdout, `colors "input.foreground" #2a241f on input.background #2a241f: contrast 1.00`) {
		t.Fatalf("an unreadable theme must FAIL naming the place: code %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "Professor · Lantern") || !strings.Contains(stdout, "1 of 2 profiles pass") {
		t.Fatalf("a light palette with a dark Clawd must pass: code %d\n%s", code, stdout)
	}
}

func TestCheckWithoutFilesScansTheSourceClonesProfiles(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	themes := filepath.Join(repo, "templates", "themes")
	writeFile(t, filepath.Join(themes, "README.md"), "not a theme")
	good := writeTheme(t, themes, "paper-day", checkGoodProfile, nil)
	if err := os.MkdirAll(filepath.Dir(paths.SourceRepoPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatalf("record source clone: %v", err)
	}
	code, stdout, stderr := runCheck(t, checkDeps(home))
	if code != 0 || !strings.Contains(stdout, good) || !strings.Contains(stdout, "1 of 1 profile pass") {
		t.Fatalf("scan must check exactly the one profile: code %d\n%s%s", code, stdout, stderr)
	}

	emptyHome, emptyRepo := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(emptyRepo, "templates", "themes", "README.md"), "not a theme")
	if err := os.MkdirAll(filepath.Dir(paths.SourceRepoPath(emptyHome)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(emptyHome, emptyRepo); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCheck(t, checkDeps(emptyHome)); code != 1 || !strings.Contains(stderr, "no theme in") {
		t.Fatalf("an empty templates/themes/ must fail, never pass empty: code %d %s", code, stderr)
	}
	if code, _, stderr := runCheck(t, checkDeps(t.TempDir())); code != 1 ||
		!strings.Contains(stderr, "no source clone to scan") {
		t.Fatalf("no recorded clone must fail by name: code %d %s", code, stderr)
	}
}

// TestCheckPassesEveryShippedTheme is the author's gate run here: every
// theme directory in this repo's templates/themes/ passes install's checks.
func TestCheckPassesEveryShippedTheme(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(paths.SourceRepoPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCheck(t, checkDeps(home))
	if code != 0 || strings.Contains(stdout, "FAIL") || !strings.Contains(stdout, "9 of 9 profiles pass") {
		t.Fatalf("a shipped theme fails pfm theme check, or not all nine ran: code %d\n%s%s", code, stdout, stderr)
	}
}

// glyphTheme writes a theme whose herdr.json pins tabGlyphs glyphs, written
// as the file itself so a glyph the profile parser refuses still reaches the
// check.
func glyphTheme(t *testing.T, dir, name string, glyphs any) string {
	t.Helper()
	return writeTheme(t, dir, name, `{"name":"Glyphs","base":"dark","overrides":{}}`, map[string]string{
		"herdr.json": canonical(t, map[string]any{"tabGlyphs": glyphs}),
	})
}

// A tab glyph the Herdr tab label cannot carry fails the check by file and
// key; a valid one passes.
func TestCheckJudgesHerdrTabGlyphs(t *testing.T) {
	dir := t.TempDir()
	good := glyphTheme(t, dir, "good", map[string]string{"blocked": "🔴", "error": "🟥"})
	empty := glyphTheme(t, dir, "empty", map[string]string{"blocked": ""})
	wide := glyphTheme(t, dir, "wide", map[string]string{"error": "🟥🟥🟥🟥🟥🟥🟥🟥🟥"})

	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), good, empty, wide)
	if code != 1 || !strings.Contains(stdout, "ok") || !strings.HasSuffix(strings.Split(stdout, "\n")[0], good) {
		t.Fatalf("a valid glyph must pass and the bad ones exit 1: code %d\n%s", code, stdout)
	}
	for theme, want := range map[string]string{empty: `"blocked": frame 0: empty`, wide: `"error": frame 0: glyph`} {
		if !strings.Contains(stdout, "FAIL theme "+theme) || !strings.Contains(stdout, want) {
			t.Errorf("%s must FAIL naming %q:\n%s", theme, want, stdout)
		}
	}
}

// A tab state takes a glyph string or an array of frames: both pass, an empty
// array fails naming its key, and a bad frame fails naming its index.
func TestCheckJudgesHerdrTabGlyphFrames(t *testing.T) {
	dir := t.TempDir()
	still := glyphTheme(t, dir, "still", map[string]string{"idle": "🌿"})
	frames := glyphTheme(t, dir, "frames", map[string][]string{"blocked": {"🔴", "⭕"}})
	code, stdout, stderr := runCheck(t, checkDeps(t.TempDir()), still, frames)
	if code != 0 || strings.Contains(stdout, "FAIL") {
		t.Fatalf("a glyph string and a frame array must both pass: code %d\n%s%s", code, stdout, stderr)
	}
	empty := glyphTheme(t, dir, "empty", map[string][]string{"working": {}})
	bad := glyphTheme(t, dir, "bad", map[string][]string{"error": {"🟥", "❌ x"}})
	working := glyphTheme(t, dir, "animated-working", map[string][]string{"working": {"🌑", "🌓", "🌕"}})
	code, stdout, _ = runCheck(t, checkDeps(t.TempDir()), empty, bad, working)
	if code != 1 {
		t.Fatalf("an empty array, a bad frame and animated working must exit 1: code %d\n%s", code, stdout)
	}
	for theme, want := range map[string]string{
		empty: `"working": no frames`, bad: `"error": frame 1: glyph`,
		working: `"working": tab state "working" requires exactly one frame: only blocked and error may animate`,
	} {
		if !strings.Contains(stdout, "FAIL theme "+theme) || !strings.Contains(stdout, want) {
			t.Errorf("%s must FAIL naming %q:\n%s", theme, want, stdout)
		}
	}
}
