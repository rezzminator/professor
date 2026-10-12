package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestReportEngineSyncCompatibilityAndVersionSeats(t *testing.T) {
	for _, name := range []string{"older required", "two versions", "backwards stable", "leading-zero cache"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_DATA_HOME", "")
			t.Setenv("ENGINE_SYNC_HOME", "")
			esHome := filepath.Join(home, ".local", "share", "engine-sync")
			stableVersion := "0.3.0-alpha.3"
			if name == "older required" {
				stableVersion = "0.1.0"
			}
			binary := filepath.Join(esHome, "bin", "engine-sync")
			script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'engine-sync " + stableVersion + "'; else echo '{\"ok\":true,\"rows\":[]}'; fi\n"
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := testjail.WriteExecutable(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ENGINE_SYNC_BIN", binary)
			clone := filepath.Join(home, ".professor")
			writeFixture(
				t,
				filepath.Join(
					clone,
					"plugins",
					"engine-sync",
					"plugins",
					"engine-sync",
					".claude-plugin",
					"plugin.json",
				),
				`{"version":"0.1.0"}`,
			)
			if name != "older required" {
				version := "0.1.0"
				switch name {
				case "backwards stable":
					version = "0.3.0"
				case "leading-zero cache":
					version = "0.3.0-01"
				}
				cached := filepath.Join(esHome, "versions", version, "engine-sync")
				if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(
					cached,
					[]byte("#!/bin/sh\necho 'engine-sync "+version+"'\n"),
					0o755,
				); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			warnings, failures := ReportGlobalAgents(&output, home, false, paths.OSEnv{}, nil)
			text := output.String()
			switch name {
			case "older required":
				if failures == 0 || !strings.Contains(text, "engine-sync 0.1.0 is older than required 0.3.0-alpha.3") {
					t.Fatalf("doctor %d/%d: %s", warnings, failures, text)
				}
			case "two versions":
				if warnings == 0 || !strings.Contains(text, "stable 0.3.0-alpha.3") ||
					!strings.Contains(text, "plugin cache 0.1.0") ||
					!strings.Contains(text, "ENGINE_SYNC_BIN") {
					t.Fatalf("doctor %d/%d: %s", warnings, failures, text)
				}
			case "leading-zero cache":
				want := "cache " + filepath.Join(esHome, "versions", "0.3.0-01") + " not probed: " +
					`invalid engine-sync version "0.3.0-01"`
				if warnings == 0 || !strings.Contains(text, want) {
					t.Fatalf("doctor %d/%d, want %q: %s", warnings, failures, want, text)
				}
			case "backwards stable":
				if warnings == 0 || !strings.Contains(text, "stable link moved backwards") ||
					!strings.Contains(text, "0.3.0") ||
					!strings.Contains(
						text,
						"start a new Claude session so the engine-sync plugin wrapper moves the stable link forward",
					) {
					t.Fatalf("doctor %d/%d: %s", warnings, failures, text)
				}
			}
		})
	}
}
