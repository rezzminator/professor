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

const (
	checkGoodProfile = `{"name":"Paper Day","base":"light","overrides":{"claude":"#c2410c"}}`
	checkBadProfile  = `{"name":"Typo","base":"dark","overides":{"claude":"#ff0000"}}`
)

// checkDeps is a run whose home is home; check reads nothing else from Deps.
func checkDeps(home string) Deps {
	return Deps{Env: func() (themesync.Env, error) { return themesync.Env{Home: home}, nil }}
}

func writeProfile(t *testing.T, dir, file, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	target := filepath.Join(dir, file)
	if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	return target
}

func runCheck(t *testing.T, deps Deps, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunVerb(context.Background(), append([]string{verbCheck}, args...), deps, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestCheckPassesAProfileAndRefusesAnUnknownKeyByFileAndKey(t *testing.T) {
	dir := t.TempDir()
	good := writeProfile(t, dir, "paper-day.json", checkGoodProfile)
	bad := writeProfile(t, dir, "typo.json", checkBadProfile)

	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), good, bad)
	if code != 1 {
		t.Fatalf("one profile refused must exit 1, got %d\n%s", code, stdout)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("want an ok row, a FAIL row and the tally, got %q", lines)
	}
	if !strings.HasPrefix(lines[0], "ok") || !strings.Contains(lines[0], "Professor · Paper Day") ||
		!strings.Contains(lines[0], " vs ") || !strings.HasSuffix(lines[0], good) {
		t.Fatalf("ok row must name the VS Code theme, its light base and the file: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "FAIL "+bad) || !strings.Contains(lines[1], "overides") {
		t.Fatalf("FAIL row must name the file and the unknown key: %q", lines[1])
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
		t.Fatalf("--json of a passing profile: code %d, %+v", code, got)
	}
}

func TestCheckRefusesANonJSONFileAFileThatIsNotThereAndAPipe(t *testing.T) {
	dir := t.TempDir()
	readme := writeProfile(t, dir, "README.md", "# Themes\n")
	missing := filepath.Join(dir, "gone.json")
	pipe := filepath.Join(dir, "pipe.json")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("mkfifo %s: %v", pipe, err)
	}
	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), readme, missing, pipe)
	if code != 1 || !strings.Contains(stdout, "FAIL "+readme+": not a profile") ||
		!strings.Contains(stdout, "FAIL read profile "+missing) ||
		!strings.Contains(stdout, "FAIL "+pipe+": not a profile: not a regular file") ||
		!strings.Contains(stdout, "0 of 3 profiles pass") {
		t.Fatalf("a non-JSON file, a missing file and a pipe must each FAIL by path: code %d\n%s", code, stdout)
	}
}

// TestCheckFailsAProfileWhoseThemeCannotBeRead: a theme that renders but
// draws text a reader cannot read is a FAIL naming the place, never ok; a
// light palette whose Clawd is dark ink still renders readable inputs.
func TestCheckFailsAProfileWhoseThemeCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	ink := writeProfile(t, dir, "ink.json", `{"name":"Ink","base":"dark","overrides":{},"surfaces":{"vscode":{"theme":`+
		`{"colors":{"input.background":"#2a241f","input.foreground":"#2a241f"}}}}}`)
	lantern := writeProfile(t, dir, "lantern.json",
		`{"name":"Lantern","base":"light","overrides":{"clawd_background":"#2a241f"}}`)
	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), ink, lantern)
	if code != 1 || !strings.Contains(stdout, "FAIL "+ink+": readability:") ||
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
	writeProfile(t, themes, "README.md", "not a profile")
	good := writeProfile(t, themes, "paper-day.json", checkGoodProfile)
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
	writeProfile(t, filepath.Join(emptyRepo, "templates", "themes"), "README.md", "not a profile")
	if err := os.MkdirAll(filepath.Dir(paths.SourceRepoPath(emptyHome)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(emptyHome, emptyRepo); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCheck(t, checkDeps(emptyHome)); code != 1 || !strings.Contains(stderr, "no profile in") {
		t.Fatalf("an empty templates/themes/ must fail, never pass empty: code %d %s", code, stderr)
	}
	if code, _, stderr := runCheck(t, checkDeps(t.TempDir())); code != 1 ||
		!strings.Contains(stderr, "no source clone to scan") {
		t.Fatalf("no recorded clone must fail by name: code %d %s", code, stderr)
	}
}

// TestCheckPassesEveryShippedProfile is the author's gate run here: every
// profile in this repo's templates/themes/ passes install's checks.
func TestCheckPassesEveryShippedProfile(t *testing.T) {
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
	if code != 0 || strings.Contains(stdout, "FAIL") {
		t.Fatalf("a shipped profile fails pfm theme check: code %d\n%s%s", code, stdout, stderr)
	}
}

// A tab glyph the Herdr tab label cannot carry fails the check by file and
// key; a valid one passes.
func TestCheckJudgesHerdrTabGlyphs(t *testing.T) {
	dir := t.TempDir()
	profile := func(glyphs string) string {
		return `{"name":"Glyphs","base":"dark","overrides":{},"surfaces":{"herdr":{"tabGlyphs":` + glyphs + `}}}`
	}
	good := writeProfile(t, dir, "good.json", profile(`{"blocked":"🔴","blockedOff":"⭕"}`))
	empty := writeProfile(t, dir, "empty.json", profile(`{"blockedOff":""}`))
	wide := writeProfile(t, dir, "wide.json", profile(`{"error":"🟥🟥🟥🟥🟥🟥🟥🟥🟥"}`))

	code, stdout, _ := runCheck(t, checkDeps(t.TempDir()), good, empty, wide)
	if code != 1 || !strings.Contains(stdout, "ok") || !strings.HasSuffix(strings.Split(stdout, "\n")[0], good) {
		t.Fatalf("a valid glyph must pass and the bad ones exit 1: code %d\n%s", code, stdout)
	}
	for file, want := range map[string]string{empty: `"blockedOff": empty`, wide: `"error": glyph`} {
		if !strings.Contains(stdout, "FAIL "+file) || !strings.Contains(stdout, want) {
			t.Errorf("%s must FAIL naming %q:\n%s", file, want, stdout)
		}
	}
}
