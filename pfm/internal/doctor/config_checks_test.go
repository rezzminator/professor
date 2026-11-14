package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorReportsCodexLoginStates(t *testing.T) {
	for _, tc := range []struct {
		name, auth, config, want string
		failures                 int
		authDir                  bool
	}{
		{name: "missing", want: "doctor: codex-login codex[2] {root}/auth.json missing — run codex login\n", failures: 1},
		{name: "invalid", auth: `{}`, want: "doctor: codex-login codex[2] {root}/auth.json has no tokens.access_token and tokens.account_id — run codex login\n", failures: 1},
		{name: "corrupt", auth: `{`, want: "doctor: codex-login codex[2] {root}/auth.json UNREADABLE error=unexpected end of JSON input — run codex login\n", failures: 1},
		{
			name:     "wrong shape",
			auth:     `{"tokens":"fixture"}`,
			want:     "doctor: codex-login codex[2] {root}/auth.json UNREADABLE error=json: cannot unmarshal string into Go struct field .tokens of type struct { AccessToken string \"json:\\\"access_token\\\"\"; AccountID string \"json:\\\"account_id\\\"\" } — run codex login\n",
			failures: 1,
		},
		{name: "valid", auth: `{"tokens":{"access_token":"fixture-token","account_id":"fixture-account"}}`, want: "doctor: codex-login codex[2] ok\n", failures: 0},
		{
			name:     "corrupt config.toml",
			config:   `cli_auth_credentials_store = `,
			want:     "doctor: codex-login codex[2] {root}/config.toml UNREADABLE error=parse {root}/config.toml: toml: line 1 (last key \"cli_auth_credentials_store\"): unexpected EOF; expected value\n",
			failures: 1,
		},
		{name: "unreadable", authDir: true, want: "doctor: codex-login codex[2] {root}/auth.json UNREADABLE error=is a directory\n", failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.authDir {
				if err := os.Mkdir(filepath.Join(home, "auth.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.auth != "" {
				if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(tc.auth), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runtime := config.Runtime{Config: config.Config{CodexAccounts: []config.CodexAccount{{ID: 2, Home: home}}}}
			var output bytes.Buffer
			_, failures := PrintConfig(&output, runtime)
			if got := rooted(
				doctorRows(output.String(), "doctor: codex-login"),
				home,
			); failures != tc.failures ||
				got != tc.want {
				t.Errorf("failures=%d rows = %q, want %q", failures, got, tc.want)
			}
		})
	}
}

func TestDoctorConfigFileRows(t *testing.T) {
	t.Setenv(paths.EnvConfig, "")
	root := t.TempDir()
	path := filepath.Join(root, "pfm.config.json")
	var output bytes.Buffer
	PrintConfig(&output, config.Runtime{Config: config.Config{Path: path}})
	if !strings.Contains(output.String(), "config: missing "+path+" — run pfm install") {
		t.Fatalf("missing file row absent: %s", output.String())
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	PrintConfig(&output, config.Runtime{Config: config.Config{Path: path}})
	if !strings.Contains(output.String(), "config: unreadable "+path) {
		t.Fatalf("unreadable file row absent: %s", output.String())
	}
	for _, tc := range []struct {
		name, content, override, want string
		counted, directory            bool
		warnings, failures            int
	}{
		{
			name: "malformed already counted", content: "{", counted: true,
			want: "doctor: config: unreadable %s error=unexpected end of JSON input",
		},
		{
			name: "malformed uncounted", content: "{", failures: 1,
			want: "doctor: config: unreadable %s error=unexpected end of JSON input",
		},
		{
			name: "null", content: "null", failures: 1,
			want: "doctor: config: unreadable %s error=config is JSON null, not an object",
		},
		{
			name: "read failure already counted", directory: true, counted: true,
		},
		{
			name: "no source repo", warnings: 1,
			want: "doctor: config: missing (no source repo recorded) — run pfm install",
		},
		{
			name: "resolved not loaded", override: "/srv/cfg/pfm.config.json", warnings: 1,
			want: "doctor: config: missing (resolves to /srv/cfg/pfm.config.json, not loaded) — run pfm install",
		},
		{
			name: "invalid override", override: "relative.json", warnings: 1,
			want: `doctor: config: missing (PFM_CONFIG must be an absolute path: "relative.json") — run pfm install`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(paths.EnvConfig, tc.override)
			runtime := config.Runtime{Paths: paths.Values{Home: t.TempDir()}}
			want := tc.want
			if tc.content != "" || tc.directory {
				runtime.Config.Path = filepath.Join(runtime.Paths.Home, "pfm.config.json")
				if tc.directory {
					if err := os.Mkdir(runtime.Config.Path, 0o700); err != nil {
						t.Fatal(err)
					}
					_, err := os.ReadFile(runtime.Config.Path)
					want = fmt.Sprintf("doctor: config: unreadable %s error=%v", runtime.Config.Path, err)
				} else {
					if err := os.WriteFile(runtime.Config.Path, []byte(tc.content), 0o600); err != nil {
						t.Fatal(err)
					}
					want = fmt.Sprintf(want, runtime.Config.Path)
				}
			}
			if tc.counted {
				runtime.ConfigError = errors.New("config " + runtime.Config.Path + ": unexpected end of JSON input")
			}
			var output bytes.Buffer
			warnings, failures := PrintConfig(&output, runtime)
			row, _, _ := strings.Cut(output.String(), "\n")
			if row != want || warnings != tc.warnings || failures != tc.failures {
				t.Fatalf("row=%q warnings=%d failures=%d, want %q %d/%d",
					row, warnings, failures, want, tc.warnings, tc.failures)
			}
		})
	}
}

func TestDoctorReportsMissingConfigKeysOnlyAfterParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	warnings, failures := PrintConfig(&output, config.Runtime{Config: config.Config{Path: path}})
	if got, want := doctorRows(
		output.String(),
		"doctor: config: ",
	), "doctor: config: missing key theme (default default) — informational: unset, its default applies\ndoctor: config: missing key chat.backend (default auto) — informational: unset, its default applies\ndoctor: config: missing key chat.herdrTabStatus (default emoji) — informational: unset, its default applies\ndoctor: config: missing key state.db (default ~/.local/state/pfm/pfm.db) — informational: unset, its default applies\ndoctor: config: missing key state.cacheDb (default ~/.local/state/pfm/pfm-cache.db) — informational: unset, its default applies\ndoctor: config: missing key claude.permissionMode (default bypass) — informational: unset, its default applies\ndoctor: config: missing key claude.binary (default claude) — informational: unset, its default applies\ndoctor: config: missing key claude.theme (default <nil>) — informational: unset, its default applies\ndoctor: config: missing key claude.cache1h (default true) — informational: unset, its default applies\ndoctor: config: missing key claude.nativeCursor (default false) — informational: unset, its default applies\ndoctor: config: missing key claude.remoteControl (default true) — informational: unset, its default applies\ndoctor: config: missing key claude.systemPrompt (default <nil>) — informational: unset, its default applies\ndoctor: config: missing key claude.webSearchesPerSession (default 9007199254740991) — informational: unset, its default applies\ndoctor: config: missing key claude.autoCompactWindow (default 100000) — informational: unset, its default applies\ndoctor: config: missing key claude.helperModel (default claude-haiku-5-5) — informational: unset, its default applies\ndoctor: config: missing key claude.tmuxTruecolor (default true) — informational: unset, its default applies\ndoctor: config: missing key claude.cleanupPeriodDays (default 36500) — informational: unset, its default applies\ndoctor: config: missing key claude.requireManagedCleanup (default true) — informational: unset, its default applies\ndoctor: config: missing key claude.maxSubagentSpawnDepth (default 8) — informational: unset, its default applies\ndoctor: config: missing key claude.maxConcurrentSubagents (default <nil>) — informational: unset, its default applies\ndoctor: config: missing key claude.pluginCheckoutRoot (default <nil>) — informational: unset, its default applies\ndoctor: config: missing key claude.plugins (default [cache-live-control forward-compaction auto-effort tool-visibility-controller engine-sync flights]) — informational: unset, its default applies\ndoctor: config: missing key claude.env (default map[]) — informational: unset, its default applies\ndoctor: config: missing key claude.costLedger (default true) — informational: unset, its default applies\ndoctor: config: missing key claude.gptAgents (default false) — informational: unset, its default applies\ndoctor: config: missing key codex.yolo (default true) — informational: unset, its default applies\ndoctor: config: missing key codex.binary (default codex) — informational: unset, its default applies\ndoctor: config: missing key opencode.binary (default opencode) — informational: unset, its default applies\ndoctor: config: missing key tmux.titles.enabled (default true) — informational: unset, its default applies\ndoctor: config: missing key nameSync.interval (default 15m0s) — informational: unset, its default applies\ndoctor: config: missing key mcp.servers.chat.enabled (default false) — informational: unset, its default applies\ndoctor: config: missing key mcp.http.port (default 18377) — informational: unset, its default applies\ndoctor: config: missing key mcp.thirdParty (default map[]) — informational: unset, its default applies\ndoctor: config: missing key ask.engine (default codex) — informational: unset, its default applies\ndoctor: config: missing key ask.claude.model (default claude-haiku-4-5) — informational: unset, its default applies\ndoctor: config: missing key ask.claude.effort (default low) — informational: unset, its default applies\ndoctor: config: missing key ask.codex.model (default gpt-5.6-luna) — informational: unset, its default applies\ndoctor: config: missing key ask.codex.effort (default low) — informational: unset, its default applies\ndoctor: config: missing key ask.opencode.model (default gpt-5.6-luna) — informational: unset, its default applies\ndoctor: config: missing key ask.opencode.effort (default low) — informational: unset, its default applies\ndoctor: config: missing key log.level (default <nil>) — informational: unset, its default applies\ndoctor: config: missing key log.components (default map[]) — informational: unset, its default applies\ndoctor: config: missing key log.keepFiles (default 10) — informational: unset, its default applies\ndoctor: config: missing key log.maxMB (default 8) — informational: unset, its default applies\ndoctor: config: missing key doctor.ignoreWarnings (default []) — informational: unset, its default applies\ndoctor: config: missing key doctor.acceptedMCP (default []) — informational: unset, its default applies\n"; warnings != 0 || failures != 0 ||
		got != want {
		t.Errorf("warnings=%d failures=%d config rows = %q, want %q", warnings, failures, got, want)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	PrintConfig(&output, config.Runtime{Config: config.Config{Path: path}})
	if got, want := rooted(
		doctorRows(output.String(), "config: "),
		filepath.Dir(path),
	), "doctor: config: unreadable {root}/pfm.config.json error=unexpected end of JSON input\n"; got != want {
		t.Errorf("malformed config rows = %q, want %q", got, want)
	}
}

func TestDoctorAdvisesWhenConfiguredSeatsShareOAuthLogin(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	first := config.DefaultAccountDir(home, 1)
	second := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(first, ".claude.json"), filepath.Join(second, ".claude.json")} {
		if err := os.WriteFile(
			path,
			[]byte(`{"oauthAccount":{"emailAddress":"fixture@example.invalid"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	machine := config.Config{Accounts: []config.Account{
		{ID: 1, ConfigDir: first},
		{ID: 2, ConfigDir: second},
	}}
	runtime := config.Runtime{Paths: paths.Values{Home: home}, Config: machine}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	for _, want := range []string{"duplicate-seat-login", "fixture@example.invalid", "seats=1:", "2:", "share one OAuth usage cap"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
}

func TestDoctorCountsUnreadableSeatRegistryAsWarning(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configDir := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, ".claude.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{
		Paths:  paths.Values{Home: home},
		Config: config.Config{Accounts: []config.Account{{ID: 2, ConfigDir: configDir}}},
	}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "state=unreadable") {
		t.Fatalf("output missing unreadable state: %s", output.String())
	}
}

// TestDoctorReportsUnavailableSeatRegistryAsWarning is a REGRESSION test for
// the unreadable-registry branch: a registry path that cannot be read at all
// is state=unavailable, distinct from malformed JSON's state=unreadable.
// A directory gives os.ReadFile a deterministic EISDIR even in the root-owned
// fence, so this does not depend on permission bits being enforced by the
// test user.
func TestDoctorReportsUnavailableSeatRegistryAsWarning(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	configDir := filepath.Join(home, ".cc", "2")
	if err := os.MkdirAll(filepath.Join(configDir, ".claude.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{
		Paths:  paths.Values{Home: home},
		Config: config.Config{Accounts: []config.Account{{ID: 2, ConfigDir: configDir}}},
	}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 1 {
		t.Fatalf("warnings=%d, want 1\n%s", warnings, output.String())
	}
	if !strings.Contains(output.String(), "state=unavailable") {
		t.Fatalf("output missing unavailable state: %s", output.String())
	}
}

// TestDoctorReportsUnavailableWhenGitIsMissing is a REGRESSION test for the
// pre-push dependency boundary. deps.RealRunner wraps a missing executable as
// exec.ErrNotFound; doctor must preserve that honest absence as unavailable,
// not render it as an unreadable probe failure.
func TestDoctorReportsUnavailableWhenGitIsMissing(t *testing.T) {
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"git"},
		deps.RunResult{ExitCode: -1},
		&exec.Error{Name: "git", Err: exec.ErrNotFound},
	)
	gate := inspectPrePushGateWithRunner(context.Background(), runner)
	if gate.State != StateUnavailable {
		t.Fatalf(
			"inspectPrePushGateWithRunner() state=%q error=%v, want %q for missing git",
			gate.State,
			gate.Error,
			StateUnavailable,
		)
	}
}

func TestDoctorSortsDuplicateSeatLoginAdvisoriesByEmail(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	accounts := []config.Account{
		{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)},
		{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
		{ID: 3, ConfigDir: filepath.Join(home, ".cc", "3")},
		{ID: 4, ConfigDir: filepath.Join(home, ".cc", "4")},
	}
	for _, account := range accounts {
		directory := account.ConfigDir
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, email := range map[string]string{
		filepath.Join(config.DefaultAccountDir(home, 1), ".claude.json"): "z@example.invalid",
		filepath.Join(home, ".cc", "2", ".claude.json"):                  "a@example.invalid",
		filepath.Join(home, ".cc", "3", ".claude.json"):                  "a@example.invalid",
		filepath.Join(home, ".cc", "4", ".claude.json"):                  "z@example.invalid",
	} {
		if err := os.WriteFile(
			path,
			[]byte(fmt.Sprintf(`{"oauthAccount":{"emailAddress":%q}}`, email)),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	runtime := config.Runtime{Paths: paths.Values{Home: home}, Config: config.Config{Accounts: accounts}}
	var output bytes.Buffer
	if warnings := printDuplicateSeatLogins(&output, runtime, &paths.MapEnv{}); warnings != 2 {
		t.Fatalf("warnings=%d, want 2\n%s", warnings, output.String())
	}
	first := strings.Index(output.String(), "email=a@example.invalid")
	second := strings.Index(output.String(), "email=z@example.invalid")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("advisories are not sorted by email:\n%s", output.String())
	}
}
