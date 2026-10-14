package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestManagedCleanupChecks(t *testing.T) {
	for _, scenario := range []string{
		"missing", "wrong", "null", "ok", "off", "relative", "unreadable",
		"no-prepend", "wrong-no-prepend", "other-source", "other-source-unreadable", "prepend-not-a-list",
	} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pfm.json")
			runtime := config.Runtime{
				Paths:  paths.Values{ManagedSettingsDir: dir},
				Config: config.Config{Claude: config.Claude{CleanupPeriodDays: 36500, RequireManagedCleanup: true}},
			}
			const (
				consequence = "Claude Code 2.1.299+ seats cc-plugin-sec-default and skips your plugin hooks"
				bothKeys    = `{"cleanupPeriodDays":36500,"prependPlugins":[]}`
			)
			fix := "sudo mkdir -p " + dir + " && printf '%s\\n' '" + bothKeys + "' | sudo tee " + path + " >/dev/null"
			var content, other string
			warnings, failures := 0, 0
			want := "managed-cleanup: " + path + " ok\n"
			switch scenario {
			case "missing":
				warnings = 1
				want = "managed-cleanup: " + path + " missing — transcripts older than 30 days are deleted by any Claude launch outside pfm; " +
					"prependPlugins is unset too — " + consequence + "; run: " + fix + "\n"
			case "wrong":
				content = `{"cleanupPeriodDays":30,"prependPlugins":[]}`
				warnings = 1
				want = "managed-cleanup: " + path + " cleanupPeriodDays=30, want 36500; run: " + fix + "\n"
			case "wrong-no-prepend":
				content = `{"cleanupPeriodDays":30}`
				warnings = 1
				want = "managed-cleanup: " + path + " cleanupPeriodDays=30, want 36500; prependPlugins is unset too — " +
					consequence + "; run: " + fix + "\n"
			case "null":
				content = `null`
				warnings = 1
				want = "managed-cleanup: " + path + " has no cleanupPeriodDays, want 36500; prependPlugins is unset too — " +
					consequence + "; run: " + fix + "\n"
			case "ok":
				content = bothKeys
			case "no-prepend":
				content = `{"cleanupPeriodDays":36500}`
				warnings = 1
				want = "managed-cleanup: " + path + " has no prependPlugins — " + consequence + "; run: " + fix + "\n"
			case "other-source":
				content = `{"cleanupPeriodDays":36500}`
				other = `{"prependPlugins":["org@market"]}`
				want = "managed-cleanup: " + path + " ok\n" +
					"managed-cleanup: prependPlugins is set by " + filepath.Join(dir, "10-org.json") + " — pfm leaves it\n"
			case "prepend-not-a-list":
				content = `{"cleanupPeriodDays":36500,"prependPlugins":null}`
				warnings = 1
				want = "managed-cleanup: " + path + " ok\n" +
					"managed-cleanup: " + path + " prependPlugins is not a list of plugin ids, which Claude Code ignores — " +
					consequence + "; set it to [] or a list of plugin ids\n"
			case "other-source-unreadable":
				content = `{"cleanupPeriodDays":36500}`
				other = "{"
				failures = 1
				want = "managed-cleanup: " + path + " ok\n" +
					"managed-cleanup: " + filepath.Join(dir, "10-org.json") + " UNREADABLE error=unexpected end of JSON input\n"
			case "off":
				runtime.Config.Claude.RequireManagedCleanup = false
				want = "managed-cleanup: check off by config\n"
			case "relative":
				runtime.Paths.ManagedSettingsDir = "relative-dir"
				want = "managed-cleanup: managed settings dir relative-dir is not absolute — nothing written\n"
			case "unreadable":
				content = "{"
				failures = 1
				want = "managed-cleanup: " + path + " UNREADABLE error=unexpected end of JSON input\n"
			}
			for file, body := range map[string]string{path: content, filepath.Join(dir, "10-org.json"): other} {
				if body == "" {
					continue
				}
				if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			gotWarnings, gotFailures := printManagedCleanupChecks(&output, runtime)
			if output.String() != want || gotWarnings != warnings || gotFailures != failures {
				t.Fatalf(
					"row=%q tally=(%d,%d), want=%q (%d,%d)",
					output.String(),
					gotWarnings,
					gotFailures,
					want,
					warnings,
					failures,
				)
			}
		})
	}
}

func TestManagedCleanupUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pfm.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := config.Runtime{
		Paths:  paths.Values{ManagedSettingsDir: dir},
		Config: config.Config{Claude: config.Claude{CleanupPeriodDays: 36500, RequireManagedCleanup: true}},
	}
	var output bytes.Buffer
	w, f := printManagedCleanupChecks(&output, runtime)
	want := fmt.Sprintf("managed-cleanup: %s UNREADABLE error=not a regular file\n", path)
	if w != 0 || f != 1 || strings.TrimSpace(output.String()) != strings.TrimSpace(want) {
		t.Fatalf("row=%q tally=(%d,%d)", output.String(), w, f)
	}
}
