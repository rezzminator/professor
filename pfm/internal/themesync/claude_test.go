package themesync

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// shippedClaudeTheme is the Claude theme file the blueprint's theme name
// installs: its directory under shippedThemesDir assembled by ReadProfileDir,
// parsed and rendered by ClaudeFile, read at test time so it never drifts.
// Memoized per name: the shipped files never change during a test run, and
// every harness renders tokyo-night, which dominated the package's CPU time.
func shippedClaudeTheme(t testing.TB, name string) string {
	t.Helper()
	if file, ok := shippedClaudeThemes.Load(name); ok {
		return file.(string)
	}
	content, err := ReadProfileDir(filepath.Join(shippedThemesDir, name))
	if err != nil {
		t.Fatalf("read shipped theme %s: %v", name, err)
	}
	profile, err := ParseProfile(content)
	if err != nil {
		t.Fatalf("parse shipped theme %s: %v", name, err)
	}
	file, err := profile.ClaudeFile(content)
	if err != nil {
		t.Fatalf("render shipped theme %s as a Claude theme file: %v", name, err)
	}
	shippedClaudeThemes.Store(name, string(file))
	return string(file)
}

// shippedClaudeThemes caches shippedClaudeTheme's successful renders by name.
var shippedClaudeThemes sync.Map

// tokyoNightApplied is the shipped tokyo-night theme resolved from its Claude
// theme file written into a temp account dir.
func tokyoNightApplied(t *testing.T) Applied {
	t.Helper()
	dir := t.TempDir()
	themes := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	theme := []byte(shippedClaudeTheme(t, tokyoNightName))
	if err := os.WriteFile(filepath.Join(themes, tokyoNightName+".json"), theme, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveValue("custom:"+tokyoNightName, dir)
	if err != nil {
		t.Fatal(err)
	}
	return got.Applied
}

func TestResolveValueReadsTheThemeSettingLikeClaudeDoes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "themes", "tokyo-night.json"),
		[]byte(shippedClaudeTheme(t, tokyoNightName)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, value               string
		title, accent, background string
		side                      Appearance
		auto                      bool
	}{
		{
			name: "custom", value: "custom:tokyo-night", title: "Tokyo Night · neon pink",
			accent: "#c95cff", background: "#0f0f1a", side: Dark,
		},
		{name: "dark", value: "dark", title: "dark", accent: "#d77757", background: "#000000", side: Dark},
		{name: "light", value: "light", title: "light", accent: "#d77757", background: "#ffffff", side: Light},
		{name: "absent key is dark", value: "", title: "dark", accent: "#d77757", background: "#000000", side: Dark},
		{
			name:       "unknown name is dark",
			value:      "nonsense",
			title:      "nonsense",
			accent:     "#d77757",
			background: "#000000",
			side:       Dark,
		},
		{name: "auto", value: "auto", title: "auto", accent: "#d77757", background: "#000000", side: Dark, auto: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveValue(tc.value, dir)
			if err != nil {
				t.Fatal(err)
			}
			applied := got.Applied
			if applied.Title != tc.title || applied.Primary.Accent != tc.accent ||
				applied.Primary.Background != tc.background || applied.Side != tc.side || applied.Auto != tc.auto {
				t.Fatalf("applied = %+v", applied)
			}
			if tc.auto && (applied.Alternate == nil || applied.Alternate.Background != "#ffffff") {
				t.Fatalf("auto must carry the light alternate, got %+v", applied.Alternate)
			}
			if tc.value == "custom:tokyo-night" &&
				got.ThemeFile != filepath.Join(dir, "themes", "tokyo-night.json") {
				t.Fatalf("theme file = %q", got.ThemeFile)
			}
		})
	}
	if dark, _, _ := builtinVariant("dark"); dark.Surface != "#373737" {
		t.Fatalf("dark userMessageBackground = %s, want #373737", dark.Surface)
	}
}

func TestResolveValueErrorsNameTheFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveValue("custom:missing", dir); err == nil ||
		err.Error() != "custom theme missing: open "+dir+"/themes/missing.json: no such file or directory" {
		t.Fatalf("missing theme error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "themes", "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveValue("custom:bad", dir); err == nil || !containsAll(err.Error(), "custom theme bad", bad) {
		t.Fatalf("unparsable theme error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "themes", "badcolour.json"),
		[]byte(`{"name":"x","base":"dark","overrides":{"claude":"purple"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveValue("custom:badcolour", dir); err == nil || !containsAll(err.Error(), "claude", "purple") {
		t.Fatalf("bad colour error = %v", err)
	}
	// A name is a file directly in themes/: one holding a path separator names
	// no theme, even when the file it points at parses.
	if err := os.WriteFile(filepath.Join(dir, "escape.json"),
		[]byte(`{"name":"x","base":"dark","overrides":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveValue("custom:../escape", dir); err == nil ||
		!containsAll(err.Error(), "custom theme ../escape", "path separator") {
		t.Fatalf("a theme name outside themes/ = %v, want an error naming the separator", err)
	}
	// A FIFO in place of a theme file is an error, never a read that blocks the
	// follower under the theme lock.
	fifo := filepath.Join(dir, "themes", "pipe.json")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := resolveValue("custom:pipe", dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !containsAll(err.Error(), "custom theme pipe", fifo, "not a regular file") {
			t.Fatalf("a FIFO theme file = %v, want an error naming it", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO theme file blocked: the follower would hang under the theme lock")
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func TestAccountSettingsDedupesASymlinkedAccountByRealPath(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{".claude", ".claude2"} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(home, rel, "settings.json"),
			[]byte(`{"theme":"dark"}`),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".cc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude2"), filepath.Join(home, ".cc", "2")); err != nil {
		t.Fatal(err)
	}
	got, err := accountSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(rel string) string {
		resolved, err := filepath.EvalSymlinks(filepath.Join(home, rel, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	want := []string{canonical(".claude"), canonical(".claude2")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("accountSettings = %v, want %v", got, want)
	}
}

func TestAccountSettingsWithNoAccountIsAnError(t *testing.T) {
	if _, err := accountSettings(t.TempDir()); err == nil ||
		err.Error() != "no Claude account settings.json under ~/.claude or ~/.cc" {
		t.Fatalf("err = %v", err)
	}
}

func TestGoverningAccountIsTheLastThemeChange(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 5, 1, hour, 0, 0, 0, time.UTC) }
	tokyoNight := "custom:tokyo-night"
	a := func(value string, hour int) account {
		return account{Path: "/a/settings.json", Value: value, ModTime: at(hour)}
	}
	b := func(value string, hour int) account {
		return account{Path: "/b/settings.json", Value: value, ModTime: at(hour)}
	}
	memory := map[string]string{"/a/settings.json": tokyoNight, "/b/settings.json": tokyoNight}

	got, next := govern(memory, "/a/settings.json", []account{a(tokyoNight, 10), b("light", 11)})
	if got.Path != "/b/settings.json" || next["/b/settings.json"] != "light" {
		t.Fatalf("B changed its value; governor = %s, memory %v", got.Path, next)
	}
	got, _ = govern(next, got.Path, []account{a(tokyoNight, 12), b("light", 11)})
	if got.Path != "/b/settings.json" {
		t.Fatalf("A rewrote the same value; B must keep governing, got %s", got.Path)
	}
	got, _ = govern(nil, "", []account{a(tokyoNight, 10), b("light", 11)})
	if got.Path != "/b/settings.json" {
		t.Fatalf("first run with disagreeing accounts follows the newest file, got %s", got.Path)
	}
	got, _ = govern(nil, "", []account{a(tokyoNight, 10), b(tokyoNight, 11)})
	if got.Path != "/a/settings.json" {
		t.Fatalf("first run with agreeing accounts follows the first, got %s", got.Path)
	}
	got, _ = govern(memory, "/gone/settings.json", []account{a(tokyoNight, 10), b("light", 11)})
	if got.Path != "/b/settings.json" {
		t.Fatalf("changed value wins even when the previous governor is gone, got %s", got.Path)
	}
}
