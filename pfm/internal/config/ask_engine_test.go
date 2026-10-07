package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsExplicitAskEngineWithEmptyRoster(t *testing.T) {
	for _, testCase := range []struct {
		engine string
		claude bool
		codex  bool
		want   string
		fix    string
	}{
		{engine: "codex", claude: true, want: `ask.engine "codex" has zero Codex accounts; authenticate the default Codex home, add codex.homes, or choose claude`, fix: "codex.homes"},
		{engine: "claude", codex: true, want: `ask.engine "claude" has zero Claude accounts`, fix: "accounts"},
		{engine: "opencode", claude: true, want: `ask.engine "opencode" has zero OpenCode accounts`, fix: "opencode.db"},
	} {
		t.Run(testCase.engine, func(t *testing.T) {
			home := t.TempDir()
			if testCase.codex {
				writeCodexAuthFixture(t, filepath.Join(home, ".codex"))
			}
			accounts := "[]"
			if testCase.claude {
				accounts = `[{"id":1,"configDir":"~/claude-one"}]`
			}
			path := filepath.Join(t.TempDir(), "config.json")
			content := fmt.Sprintf(`{"version":2,"accounts":%s,"ask":{"engine":%q}}`, accounts, testCase.engine)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, home, nil)
			if err == nil || !strings.Contains(err.Error(), testCase.want) ||
				!strings.Contains(err.Error(), testCase.fix) {
				t.Fatalf("Load() error=%v, want %q and fix %q", err, testCase.want, testCase.fix)
			}
			if testCase.engine == "codex" && err.Error() != "config "+path+": "+testCase.want {
				t.Fatalf("Load() error=%v, want %q", err, "config "+path+": "+testCase.want)
			}
		})
	}
}

func TestAskCodexDefaultHomeRecognizesLoginModes(t *testing.T) {
	for _, tc := range []struct{ name, auth, prefs string }{
		{"tokens", `{"tokens":{"access_token":"token","account_id":"account"}}`, ""},
		{"api key", `{"OPENAI_API_KEY":"fixture-key"}`, ""},
		{"keyring", "", `cli_auth_credentials_store = "keyring"`},
		{"auto", "", `cli_auth_credentials_store = "auto"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			codex := filepath.Join(home, ".codex")
			if err := os.MkdirAll(codex, 0o700); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"auth.json": tc.auth, "config.toml": tc.prefs} {
				if content != "" {
					if err := os.WriteFile(filepath.Join(codex, name), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			path := filepath.Join(home, FileName)
			if err := os.WriteFile(
				path,
				[]byte(`{"version":2,"accounts":[],"ask":{"engine":"codex"}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, home, nil, codex)
			if err != nil || len(got.CodexAccounts) != 1 || got.CodexAccounts[0].Home != codex {
				t.Fatalf("default roster: %#v error=%v", got.CodexAccounts, err)
			}
		})
	}
}
