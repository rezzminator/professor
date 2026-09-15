package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

func TestLoadEngineRosterMatrixAndDefaultEngine(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		claude      bool
		codex       bool
		askEngine   string
		wantCounts  EngineCounts
		wantDefault pfmengine.ID
		wantError   string
	}{
		{name: "0/0", wantCounts: EngineCounts{}, wantError: "Claude roster empty"},
		{name: "N/0", claude: true, wantCounts: EngineCounts{pfmengine.Claude: 1}, wantDefault: pfmengine.Claude},
		{name: "0/N", codex: true, wantCounts: EngineCounts{pfmengine.Codex: 1}, wantDefault: pfmengine.Codex},
		{name: "N/N", claude: true, codex: true, askEngine: "claude", wantCounts: EngineCounts{pfmengine.Claude: 1, pfmengine.Codex: 1}, wantDefault: pfmengine.Claude},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.codex {
				writeCodexAuthFixture(t, filepath.Join(home, ".codex"))
			}
			accounts := "[]"
			if testCase.claude {
				accounts = `[{"id":7,"configDir":"~/claude-seven","emoji":"C"}]`
			}
			ask := ""
			if testCase.askEngine != "" {
				ask = fmt.Sprintf(`,"ask":{"engine":%q}`, testCase.askEngine)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			content := fmt.Sprintf(`{"version":2,"accounts":%s%s}`, accounts, ask)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			machine, err := Load(path, home, nil)
			if err != nil {
				t.Fatalf("Load() error=%v", err)
			}
			if got := machine.Engines(); !reflect.DeepEqual(got, testCase.wantCounts) {
				t.Fatalf("Engines()=%#v, want %#v", got, testCase.wantCounts)
			}
			engine, defaultErr := machine.DefaultEngine()
			if testCase.wantError != "" {
				if defaultErr == nil || !strings.Contains(defaultErr.Error(), testCase.wantError) ||
					!strings.Contains(defaultErr.Error(), "Codex roster empty") {
					t.Fatalf("DefaultEngine()=(%q,%v), want error naming both empty rosters", engine, defaultErr)
				}
				return
			}
			if defaultErr != nil || engine != testCase.wantDefault {
				t.Fatalf("DefaultEngine()=(%q,%v), want (%q,nil)", engine, defaultErr, testCase.wantDefault)
			}
		})
	}
}

func TestLoadRejectsExplicitAskEngineWithEmptyRoster(t *testing.T) {
	for _, testCase := range []struct {
		engine string
		claude bool
		codex  bool
		want   string
		fix    string
	}{
		{engine: "claude", codex: true, want: "zero Claude accounts", fix: "accounts"},
		{engine: "codex", claude: true, want: "zero Codex accounts", fix: "codex.homes"},
		{engine: "opencode", claude: true, want: "zero OpenCode accounts", fix: "opencode.db"},
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
		})
	}
}

func TestConfiguredCodexHomesAreCredentialedRosterEntriesWithPrefs(t *testing.T) {
	home := t.TempDir()
	writeCodexAuthFixture(t, filepath.Join(home, ".codex"))
	extra := filepath.Join(home, "codex-two")
	writeCodexAuthFixture(t, extra)
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "version": 2,
  "accounts": [],
  "codex": {
    "binary": "codex-global",
    "homes": [{
      "id": 2,
      "home": "~/codex-two",
      "emoji": "X",
      "prefs": {"yolo": false, "binary": "codex-two"}
    }]
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []CodexAccount{
		{ID: 1, Home: filepath.Join(home, ".codex"), Emoji: "🥇"},
		{ID: 2, Home: extra, Emoji: "X", Prefs: &CodexPrefs{Yolo: false, Binary: "codex-two"}},
	}
	if !reflect.DeepEqual(machine.CodexAccounts, want) {
		t.Fatalf("CodexAccounts=%#v, want %#v", machine.CodexAccounts, want)
	}
	if got := machine.EffectiveCodex(2); got != (CodexPrefs{Yolo: false, Binary: "codex-two"}) {
		t.Fatalf("EffectiveCodex(2)=%#v", got)
	}
	if got := machine.EffectiveCodex(1); got != (CodexPrefs{Yolo: true, Binary: "codex-global"}) {
		t.Fatalf("EffectiveCodex(1)=%#v", got)
	}
	if !reflect.DeepEqual(machine.CodexAccountIDs(), []int{1, 2}) || machine.CodexEmojiFor(2) != "X" {
		t.Fatalf("Codex ids/emojis=%v %q", machine.CodexAccountIDs(), machine.CodexEmojiFor(2))
	}
}

// An explicitly present empty roster is an operator decision, not a request
// to rediscover the default home. This is the config-level exclusion path for
// a host that has Codex state on disk but does not want PFM to use it.
func TestExplicitlyEmptyCodexHomesClearsDiscoveredDefault(t *testing.T) {
	home := t.TempDir()
	writeCodexAuthFixture(t, filepath.Join(home, ".codex"))
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"accounts":[],"codex":{"homes":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("Load() error=%v", err)
	}
	if len(machine.CodexAccounts) != 0 {
		t.Fatalf("CodexAccounts=%#v, want an authoritative empty roster", machine.CodexAccounts)
	}
	if machine.Source("codex.homes") != SourceFile {
		t.Fatalf("Source(codex.homes)=%q, want %q", machine.Source("codex.homes"), SourceFile)
	}
}

func TestExplicitlyEmptyCodexHomesSurvivesMarshalRoundTrip(t *testing.T) {
	home := t.TempDir()
	writeCodexAuthFixture(t, filepath.Join(home, ".codex"))
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"accounts":[],"codex":{"homes":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf("initial Load() error=%v", err)
	}
	encoded, err := Marshal(machine, false)
	if err != nil {
		t.Fatalf("Marshal() error=%v", err)
	}
	roundTripPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(roundTripPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Load(roundTripPath, home, nil)
	if err != nil {
		t.Fatalf("round-trip Load() error=%v\n%s", err, encoded)
	}
	if len(roundTrip.CodexAccounts) != 0 {
		t.Fatalf("round-trip CodexAccounts=%#v, want authoritative empty roster\n%s", roundTrip.CodexAccounts, encoded)
	}
}

func TestConfiguredCodexHomeRehomesAutoDiscoveredAccount(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		metadata  string
		wantEmoji string
		wantPrefs *CodexPrefs
	}{
		{name: "default metadata", wantEmoji: DefaultEmoji(1)},
		{
			name:      "explicit metadata",
			metadata:  `,"emoji":"X","prefs":{"yolo":false,"binary":"codex-replacement"}`,
			wantEmoji: "X",
			wantPrefs: &CodexPrefs{Yolo: false, Binary: "codex-replacement"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMUX_TMPDIR", t.TempDir())
			home := t.TempDir()
			oldHome := filepath.Join(home, ".codex")
			replacement := filepath.Join(home, "codex-replacement")
			writeCodexAuthFixture(t, oldHome)
			writeCodexAuthFixture(t, replacement)

			path := filepath.Join(t.TempDir(), "config.json")
			content := fmt.Sprintf(
				`{"version":2,"accounts":[],"codex":{"homes":[{"id":1,"home":"~/codex-replacement"%s}]}}`,
				testCase.metadata,
			)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			machine, err := Load(path, home, nil)
			if err != nil {
				t.Fatalf("Load() error=%v", err)
			}
			if len(machine.CodexAccounts) != 1 {
				t.Fatalf("CodexAccounts=%#v, want exactly one account", machine.CodexAccounts)
			}
			got := machine.CodexAccounts[0]
			if got.ID != 1 || got.Home != replacement || got.Home == oldHome {
				t.Fatalf(
					"CodexAccounts=%#v, want id 1 at replacement %q and not old home %q",
					machine.CodexAccounts,
					replacement,
					oldHome,
				)
			}
			if got.Emoji != testCase.wantEmoji {
				t.Fatalf("Codex emoji=%q, want %q", got.Emoji, testCase.wantEmoji)
			}
			if !reflect.DeepEqual(got.Prefs, testCase.wantPrefs) {
				t.Fatalf("Codex prefs=%#v, want %#v", got.Prefs, testCase.wantPrefs)
			}
		})
	}
}

func TestConfiguredCodexHomeWithoutCredentialsIsAConfigError(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":2,"accounts":[],"codex":{"homes":[{"id":2,"home":"~/missing"}]}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, home, nil)
	if err == nil || !strings.Contains(err.Error(), "codex.homes[0]") ||
		!strings.Contains(err.Error(), "valid auth.json") {
		t.Fatalf("Load() error=%v", err)
	}
}

// TestCodexAuthAccountIDNestedUnderTokensIsDiscovered pins a regression: the
// real Codex CLI never writes account_id at the TOP level of auth.json — it
// writes it INSIDE tokens, alongside id_token/access_token/refresh_token. A
// Codex home carrying only that real shape must still be discovered as one
// valid, credentialed account: Engines().Codex == 1 and DefaultEngine()
// returns "codex" when ask.engine is codex. Before the fix this fails Load()
// itself with 'ask.engine "codex" has zero Codex accounts', because
// hasValidCodexCredentials (config.go) only ever looked at a top-level
// account_id field that the real CLI does not produce.
func TestCodexAuthAccountIDNestedUnderTokensIsDiscovered(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	// Keys only, invented placeholder values — never real tokens. Mirrors the
	// real Codex CLI's auth.json shape: account_id lives under tokens, not
	// at the top level.
	content := `{
  "auth_mode": "chatgpt",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "regression-id-token",
    "access_token": "regression-access-token",
    "refresh_token": "regression-refresh-token",
    "account_id": "regression-account-id"
  },
  "last_refresh": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"accounts":[],"ask":{"engine":"codex"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatalf(
			"Load() error=%v, want nil — a Codex auth.json with account_id nested under tokens (the real CLI shape) must be discovered",
			err,
		)
	}
	if got := machine.Engines(); !reflect.DeepEqual(got, EngineCounts{pfmengine.Codex: 1}) {
		t.Fatalf("Engines()=%#v, want {Codex: 1} — the .codex account with a real-shape auth.json was not counted", got)
	}
	if engine, defaultErr := machine.DefaultEngine(); defaultErr != nil || engine != pfmengine.Codex {
		t.Fatalf("DefaultEngine()=(%q,%v), want (%q, nil)", engine, defaultErr, pfmengine.Codex)
	}
}

func writeCodexAuthFixture(t *testing.T, home string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"tokens":{"access_token":"fixture-token","account_id":"fixture-account"}}`
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
