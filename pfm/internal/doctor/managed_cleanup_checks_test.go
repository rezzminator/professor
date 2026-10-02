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
	for _, scenario := range []string{"missing", "wrong", "ok", "off", "relative", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pfm.json")
			runtime := config.Runtime{
				Paths:  paths.Values{ManagedSettingsDir: dir},
				Config: config.Config{Claude: config.Claude{CleanupPeriodDays: 36500, RequireManagedCleanup: true}},
			}
			var content string
			warnings, failures := 0, 0
			want := "managed-cleanup: " + path + " ok\n"
			switch scenario {
			case "missing":
				warnings = 1
				want = "managed-cleanup: " + path + " missing — transcripts older than 30 days are deleted by any Claude launch outside pfm\n"
			case "wrong":
				content = `{"cleanupPeriodDays":30}`
				warnings = 1
				want = "managed-cleanup: " + path + " cleanupPeriodDays=30, want 36500\n"
			case "ok":
				content = `{"cleanupPeriodDays":36500}`
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
			if content != "" {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
