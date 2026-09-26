package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestDefaultsDiscoversOpenCodeSubscriptionAuthFromJailedRoot(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	openCodeRoot := filepath.Join(t.TempDir(), "opencode")
	t.Setenv("PFM_OPENCODE_ROOT", openCodeRoot)
	if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(openCodeRoot, "auth.json"),
		[]byte(`{"tokens":{"access_token":"subscription-fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	got := Defaults(home, nil)
	want := []OpenCodeAccount{{ID: 1, Home: openCodeRoot}}
	if !reflect.DeepEqual(got.OpenCodeAccounts, want) {
		t.Fatalf("OpenCodeAccounts = %#v, want %#v", got.OpenCodeAccounts, want)
	}
	if got.Engines()[pfmengine.OpenCode] != 1 {
		t.Fatalf("Engines() = %#v, want one OpenCode account", got.Engines())
	}
	if got.Source("ask.opencode.model") != SourceDefault || got.Ask.PrefsFor(pfmengine.OpenCode).Model == "" {
		t.Fatalf("OpenCode ask defaults/source = %#v/%q", got.Ask.Prefs, got.Source("ask.opencode.model"))
	}
}

func TestDefaultsDoesNotInventOpenCodeAccountWithoutStore(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	openCodeRoot := filepath.Join(t.TempDir(), "opencode")
	t.Setenv("PFM_OPENCODE_ROOT", openCodeRoot)
	if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	got := Defaults(home, nil)
	if len(got.OpenCodeAccounts) != 0 {
		t.Fatalf("OpenCodeAccounts = %#v, want no account without auth/store", got.OpenCodeAccounts)
	}
}

// A DIRECTORY named opencode.db or auth.json is not a store. Stat succeeds on
// it, so a discovery that only asked "does the path exist" would invent an
// account with no credentials behind it.
func TestDefaultsDoesNotReadADirectoryAsAnOpenCodeStore(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	openCodeRoot := filepath.Join(t.TempDir(), "opencode")
	t.Setenv("PFM_OPENCODE_ROOT", openCodeRoot)
	if err := os.MkdirAll(filepath.Join(openCodeRoot, "auth.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(openCodeRoot, "opencode.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := Defaults(home, nil)
	if len(got.OpenCodeAccounts) != 0 {
		t.Fatalf("OpenCodeAccounts = %#v, want none when both candidates are directories", got.OpenCodeAccounts)
	}
}
