package themesync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCurrentVariantUsesPersistedAppearanceAndDistinguishesOffFromFailure(t *testing.T) {
	primary, alternate := Variant{Background: "#101020"}, Variant{Background: "#faf8f0"}
	for _, tc := range []struct {
		name  string
		state themeState
		want  string
		fail  bool
	}{
		{"off", themeState{}, "", false},
		{"dark", themeState{Enabled: true, Applied: &Applied{Primary: primary}}, primary.Background, false},
		{"light", themeState{Enabled: true, Appearance: Light, Applied: &Applied{Primary: alternate}}, alternate.Background, false},
		{"auto-light", themeState{Enabled: true, Appearance: Light, Applied: &Applied{Auto: true, Primary: primary, Alternate: &alternate}}, alternate.Background, false},
		{"auto-dark", themeState{Enabled: true, Appearance: Dark, Applied: &Applied{Auto: true, Primary: primary, Alternate: &alternate}}, primary.Background, false},
		{"enabled-without-applied", themeState{Enabled: true}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := stateStore{dir: t.TempDir()}
			if got, err := CurrentVariant(store.dir); got != nil || err != nil {
				t.Fatalf("missing state = %v, %v", got, err)
			}
			if err := store.save(tc.state); err != nil {
				t.Fatal(err)
			}
			got, err := CurrentVariant(store.dir)
			if tc.fail {
				if err == nil || !strings.Contains(err.Error(), store.statePath()) {
					t.Fatalf("incomplete enabled state = %v, %v", got, err)
				}
				return
			}
			if err != nil || (tc.want == "" && got != nil) ||
				(tc.want != "" && (got == nil || got.Background != tc.want)) {
				t.Fatalf("CurrentVariant = %+v, %v; want background %q", got, err, tc.want)
			}
		})
	}
}

func TestStateStoreRoundTripAndMissing(t *testing.T) {
	store := stateStore{dir: filepath.Join(t.TempDir(), "theme")}
	if _, found, err := store.load(); found || err != nil {
		t.Fatalf("load of a missing state = found %v, err %v; want neither", found, err)
	}
	want := themeState{
		Enabled:    true,
		Appearance: Dark,
		Generation: 4,
		Source: &SourceInfo{
			Account: "~/.claude",
			Path:    "/h/.claude/settings.json",
			Value:   "dark",
			Title:   "dark",
			Side:    Dark,
		},
		Accounts: map[string]string{"/h/.claude/settings.json": "dark"},
		Applied:  &Applied{Value: "dark", Title: "dark", Side: Dark, Primary: Variant{Background: "#000000"}},
		Herdr:    &herdrSnapshot{Span: "[theme]\nname = \"x\"\n", Added: "", Backup: "/h/c.toml.pfm-theme-1"},
	}
	if err := store.save(want); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.load()
	if err != nil || !found {
		t.Fatalf("load = found %v, err %v", found, err)
	}
	if got.Generation != 4 || got.Herdr == nil || got.Herdr.Span != want.Herdr.Span ||
		got.Applied == nil || got.Applied.Primary.Background != "#000000" || got.Source == nil ||
		got.Source.Value != "dark" || got.Accounts["/h/.claude/settings.json"] != "dark" {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	info, err := os.Stat(filepath.Join(store.dir, StateFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

// A field the state no longer names (the retired palette, mode, claude, or a
// retired surface's snapshot) is ignored on read and gone on the next write.
func TestStateStoreDropsRetiredFieldsOnTheNextWrite(t *testing.T) {
	store := stateStore{dir: t.TempDir()}
	old := `{"enabled":true,"palette":"tokyo-night","mode":"auto","generation":4,` +
		`"claude":{"/p":"x"},"retired":{"config":"c","added":"a","backup":"b"}}`
	if err := os.WriteFile(filepath.Join(store.dir, StateFileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	state, found, err := store.load()
	if err != nil || !found || !state.Enabled || state.Generation != 4 || state.Applied != nil {
		t.Fatalf("load = %+v, %v, %v", state, found, err)
	}
	if err := store.save(state); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(store.dir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"palette", "mode", "claude", "retired"} {
		if strings.Contains(string(written), `"`+retired+`"`) {
			t.Errorf("the rewritten state still holds %q:\n%s", retired, written)
		}
	}
}

func TestStateStoreCorruptStateIsAnErrorNeverEmpty(t *testing.T) {
	store := stateStore{dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(store.dir, StateFileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.load(); err == nil {
		t.Fatal("a corrupt state file must be an error, not a fresh state")
	}
}

func TestStateStoreLockIsExclusiveUntilReleased(t *testing.T) {
	store := stateStore{dir: t.TempDir()}
	release, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	probe, err := os.OpenFile(filepath.Join(store.dir, "pfm-theme.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("lock file missing while held: %v", err)
	}
	defer func() { _ = probe.Close() }()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("second flock while held = %v, want EWOULDBLOCK", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock after release = %v, want nil", err)
	}
}
