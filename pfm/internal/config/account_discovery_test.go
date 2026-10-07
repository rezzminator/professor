package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestCodexLoginError(t *testing.T) {
	for _, tc := range []struct {
		name, auth, config, want string
		authDirectory, prefix    bool
		loggedOut                bool
	}{
		{
			name: "subscription tokens",
			auth: `{"tokens":{"access_token":"a","account_id":"b"}}`,
		},
		{
			name: "API-key login",
			auth: `{"OPENAI_API_KEY":"sk-fixture","tokens":null}`,
		},
		{
			name:   "keyring store",
			config: `cli_auth_credentials_store = "keyring"`,
		},
		{
			name:   "auto store",
			config: `cli_auth_credentials_store = "auto"`,
		},
		{
			name:      "file store, no auth.json",
			config:    `cli_auth_credentials_store = "file"`,
			loggedOut: true,
		},
		{
			name:      "nothing there",
			loggedOut: true,
		},
		{
			name:      "blank shapes",
			auth:      `{"OPENAI_API_KEY":"  ","tokens":{"access_token":"a"}}`,
			loggedOut: true,
		},
		{
			name:      "keyring store, auth.json present without login",
			auth:      `{}`,
			config:    `cli_auth_credentials_store = "keyring"`,
			loggedOut: true,
		},
		{
			name: "corrupt auth.json",
			auth: `{`,
			want: "parse {home}/auth.json: unexpected end of JSON input",
		},
		{
			name:          "unreadable auth.json",
			authDirectory: true,
			want:          "read {home}/auth.json: ",
			prefix:        true,
		},
		{
			name:   "corrupt config.toml",
			config: `cli_auth_credentials_store = `,
			want:   "parse {home}/config.toml: ",
			prefix: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			authPath := filepath.Join(home, "auth.json")
			if tc.authDirectory {
				if err := os.Mkdir(authPath, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if tc.auth != "" {
				if err := os.WriteFile(authPath, []byte(tc.auth), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			want := strings.ReplaceAll(tc.want, "{home}", home)
			if tc.loggedOut {
				want = authPath +
					" must contain tokens.access_token with tokens.account_id, or OPENAI_API_KEY — run codex login"
			}
			err := CodexLoginError(home)
			got := ""
			if err != nil {
				got = err.Error()
			}
			matches := got == want
			if tc.prefix {
				matches = strings.HasPrefix(got, want)
			}
			if !matches || errors.Is(err, ErrCodexLoggedOut) != tc.loggedOut {
				t.Fatalf("CodexLoginError() = %v, loggedOut=%t; want %q, prefix=%t, loggedOut=%t",
					err, errors.Is(err, ErrCodexLoggedOut), want, tc.prefix, tc.loggedOut)
			}
		})
	}
}

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
