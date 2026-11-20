package installer

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// TestUnownedShippedThemesNamesEveryShippedThemeInstallLeftAlone proves a
// shipped theme counts as unowned through either of its files, an owned or
// absent one never does, and a host with no clone listing says it could not
// compare rather than "none".
func TestUnownedShippedThemesNamesEveryShippedThemeInstallLeftAlone(t *testing.T) {
	home := t.TempDir()
	if _, err := UnownedShippedThemes(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no clone listing: err = %v, want fs.ErrNotExist", err)
	}
	for _, name := range []string{"owned", "stranger", "absent", "copy-only"} {
		writeFixture(t, filepath.Join(home, ".professor", "templates", "themes", name, "theme.json"), "{}")
	}
	owned := filepath.Join(home, ".claude", "themes", "owned.json")
	writeFixture(t, owned, "{}")
	writeFixture(t, filepath.Join(home, ".claude", "themes", "stranger.json"), "{}")
	writeFixture(t, themesync.ProfilePath(home, "copy-only"), "{}")
	writeFixture(t, filepath.Join(home, ".claude", "themes", "unshipped.json"), "{}")
	if err := writeThemeOwnership(filepath.Join(ManagedRoot(home), themeOwnershipName), map[string]themeOwnershipRecord{
		"owned": {Path: owned, SHA256: contentSHA256([]byte("{}"))},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := UnownedShippedThemes(home)
	if err != nil || !slices.Equal(got, []string{"copy-only", "stranger"}) {
		t.Fatalf("UnownedShippedThemes = %v, %v; want [copy-only stranger], nil", got, err)
	}
}

func TestThemeStaleCopyHoldsOnlyKeysWithTheShippedValue(t *testing.T) {
	shipped := []byte(
		`{"name": "Night", "base": "dark", "overrides": {"claude": "#ff5fa2", "promptBorder": "#ff8fc0"}}`,
	)
	cases := []struct {
		name, existing string
		want           bool
	}{
		{name: "every key matches", existing: `{"base": "dark", "overrides": {"claude": "#ff5fa2"}}`, want: true},
		{
			name:     "byte-different but equal",
			existing: `{"overrides":{"promptBorder":"#ff8fc0","claude":"#ff5fa2"}}`,
			want:     true,
		},
		{name: "changed value", existing: `{"base": "light"}`},
		{name: "changed nested value", existing: `{"overrides": {"claude": "#000000"}}`},
		{name: "extra key", existing: `{"base": "dark", "mine": true}`},
		{name: "extra nested key", existing: `{"overrides": {"claude": "#ff5fa2", "mine": "#000000"}}`},
		{name: "not an object", existing: `["dark"]`},
		{name: "malformed", existing: `{"base": `},
	}
	for _, testCase := range cases {
		if got := themeStaleCopy([]byte(testCase.existing), shipped); got != testCase.want {
			t.Errorf("%s: themeStaleCopy(%s) = %v, want %v", testCase.name, testCase.existing, got, testCase.want)
		}
	}
}
