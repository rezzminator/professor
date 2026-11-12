package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// shieldGuardedHook is Herdr's hook for dir as a pfm that ran herdr-shield left it.
func shieldGuardedHook(dir string) string {
	return `[ -n "${HERDR_SHIELD_INNER:-}" ] || ` + testjail.HerdrClaudeHook(dir)
}

// shieldGuardedSettings is testjail.HerdrClaudeSettings with dir's hook guarded.
func shieldGuardedSettings(dir string) string {
	return strings.Replace(testjail.HerdrClaudeSettings(dir),
		fmt.Sprintf("%q", testjail.HerdrClaudeHook(dir)), fmt.Sprintf("%q", shieldGuardedHook(dir)), 1)
}

// TestHerdrClaudeStoresDoctor is the Claude rows' table: two config dirs share
// one settings.json through symlinks; {first}, {second}, {settings} and
// {herdr} in a scenario stand for the dirs, the shared file and the herdr the
// host found, {config} for Herdr's config.
func TestHerdrClaudeStoresDoctor(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		settings     string
		herdrConfig  string // Herdr's config.toml; empty leaves none
		binEnv       string // HERDR_BIN_PATH; empty finds herdr on PATH
		noHerdr      bool
		wantRows     []string
		wantWarnings int
		wantFailures int
	}{
		{
			name:     "store without the herdr hook",
			settings: `{"theme":"dark"}`,
			wantRows: []string{
				herdrIntegrationRow + " FAIL need=herdr claude integration checked={settings} hooks=none — " +
					"fix: CLAUDE_CONFIG_DIR='{first}' herdr integration install claude",
			},
			wantFailures: 1,
		},
		{
			// The fix runs the herdr HERDR_BIN_PATH names, never one PATH lacks.
			name:     "store without the herdr hook on a HERDR_BIN_PATH host",
			settings: `{"theme":"dark"}`,
			binEnv:   "{herdr}",
			wantRows: []string{
				herdrIntegrationRow + " FAIL need=herdr claude integration checked={settings} hooks=none — " +
					"fix: CLAUDE_CONFIG_DIR='{first}' '{herdr}' integration install claude",
			},
			wantFailures: 1,
		},
		{
			name:     "store with one herdr hook from the second dir",
			settings: testjail.HerdrClaudeSettings("{second}"),
			wantRows: []string{
				herdrIntegrationRow + " ok claude: herdr integration hook in {settings}: " + testjail.HerdrClaudeHook("{second}"),
			},
		},
		{
			name:        "store with herdr-shield's guarded hook while Herdr still runs herdr-shield",
			settings:    shieldGuardedSettings("{second}"),
			herdrConfig: "[terminal]\ndefault_shell = \"/opt/fixture/herdr-shield\"\n",
			wantRows: []string{
				herdrIntegrationRow + " WARN herdr claude integration in {settings}: Herdr's default_shell in {config}" +
					" still names herdr-shield, so its guarded hook stays — end the chats in Herdr," +
					` remove default_shell = "/opt/fixture/herdr-shield" from {config}, restart Herdr,` +
					" then run pfm install --yes again",
			},
			wantWarnings: 1,
		},
		{
			name:     "store with herdr-shield's guarded hook once Herdr no longer runs it",
			settings: shieldGuardedSettings("{second}"),
			wantRows: []string{
				herdrIntegrationRow + " WARN need=plain herdr claude integration hook checked={settings} hook=" +
					fmt.Sprintf("%q", shieldGuardedHook("{second}")) + " — fix: pfm install --yes",
			},
			wantWarnings: 1,
		},
		{
			name:        "store with herdr-shield's guarded hook and an unreadable Herdr config",
			settings:    shieldGuardedSettings("{second}"),
			herdrConfig: "[terminal\n",
			wantRows: []string{
				herdrIntegrationRow + " FAIL herdr claude integration in {settings}: read Herdr's default_shell: " +
					"parse Herdr config {config}: toml: line 2: expected '.' or ']' to end table name, but got '\\n' instead",
			},
			wantFailures: 1,
		},
		{
			name:     "store with two herdr hooks",
			settings: testjail.HerdrClaudeSettings("{first}", "{second}"),
			wantRows: []string{
				herdrIntegrationRow + " WARN need=one herdr claude integration hook checked={settings} hooks=" +
					fmt.Sprintf("%q, %q", testjail.HerdrClaudeHook("{first}"), testjail.HerdrClaudeHook("{second}")) +
					" — fix: delete all but one from {settings}, then rerun pfm doctor",
			},
			wantWarnings: 1,
		},
		{
			name:     "store unreadable",
			settings: `{"hooks":`,
			wantRows: []string{
				herdrIntegrationRow + ` FAIL need=readable claude settings checked={settings} ` +
					`error="decode Claude settings {settings}: unexpected end of JSON input" — ` +
					`fix: repair {settings} so it decodes as JSON, then rerun pfm doctor`,
			},
			wantFailures: 1,
		},
		{name: "store on a host without herdr", settings: `{"theme":"dark"}`, noHerdr: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			first, second := filepath.Join(root, "cc1"), filepath.Join(root, "cc2")
			settings := filepath.Join(root, "store", "settings.json")
			binary := filepath.Join(root, "bin", "my herdr")
			herdrConfig := filepath.Join(root, "herdr", "config.toml")
			placeholders := strings.NewReplacer(
				"{first}", first, "{second}", second, "{settings}", settings, "{herdr}", binary,
				"{config}", herdrConfig,
			)
			for _, dir := range []string{first, second, filepath.Dir(settings), filepath.Dir(herdrConfig)} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(settings, []byte(placeholders.Replace(scenario.settings)), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{first, second} {
				if err := os.Symlink(settings, filepath.Join(dir, "settings.json")); err != nil {
					t.Fatal(err)
				}
			}
			runner := &deps.FakeRunner{}
			if !scenario.noHerdr {
				runner.ScriptLookPath("herdr", "/opt/herdr/bin/herdr", nil)
			}
			if scenario.herdrConfig != "" {
				if err := os.WriteFile(herdrConfig, []byte(scenario.herdrConfig), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := &paths.MapEnv{Values: map[string]string{"HERDR_CONFIG_PATH": herdrConfig}}
			if scenario.binEnv != "" {
				env.Values["HERDR_BIN_PATH"] = placeholders.Replace(scenario.binEnv)
			}
			var stdout bytes.Buffer
			warnings, failures := printHerdrClaudeStores(
				&stdout, Dependencies{Runner: runner, Env: env}, root, []string{first, second},
			)

			got := strings.TrimRight(stdout.String(), "\n")
			if want := placeholders.Replace(strings.Join(scenario.wantRows, "\n")); got != want {
				t.Fatalf("rows:\n%s\nwant:\n%s", got, want)
			}
			if warnings != scenario.wantWarnings || failures != scenario.wantFailures {
				t.Fatalf("warnings=%d failures=%d, want %d and %d",
					warnings, failures, scenario.wantWarnings, scenario.wantFailures)
			}
		})
	}
}
