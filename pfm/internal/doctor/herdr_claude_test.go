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

// TestHerdrClaudeStoresDoctor is the Claude rows' table: two config dirs share
// one settings.json through symlinks; {first}, {second}, {settings} and
// {herdr} in a scenario stand for the dirs, the shared file and the herdr the
// host found.
func TestHerdrClaudeStoresDoctor(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		settings     string
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
			placeholders := strings.NewReplacer(
				"{first}", first, "{second}", second, "{settings}", settings, "{herdr}", binary,
			)
			for _, dir := range []string{first, second, filepath.Dir(settings)} {
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
			env := &paths.MapEnv{Values: map[string]string{}}
			if scenario.binEnv != "" {
				env.Values["HERDR_BIN_PATH"] = placeholders.Replace(scenario.binEnv)
			}
			var stdout bytes.Buffer
			warnings, failures := printHerdrClaudeStores(
				&stdout, Dependencies{Runner: runner, Env: env}, []string{first, second},
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
