package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPrintHostChecks(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"clean", "BLOCK", "WARN", "unreadable", "pre-split config"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			runtime := config.Runtime{
				Config: config.Defaults(home, nil),
				Paths: paths.Values{
					Home:    home,
					StateDB: paths.DefaultStateDB(home),
					CacheDB: paths.DefaultCacheDB(home),
				},
			}
			runtime.Config.Path = filepath.Join(home, "clone", config.FileName)
			runtime.Config.Accounts = []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)}}
			want := fmt.Sprintf("host-check: ok (%d checks)\n", len(hostcheck.Detectors()))
			wantWarnings, wantFailures := 0, 0
			var path, body string
			switch scenario {
			case "BLOCK":
				path = filepath.Join(config.LegacyConfigDir(paths.OSEnv{}, home), config.FileName)
				body = `{}`
				want = "host-check: BLOCK legacy-config " + path + " — legacy pfm config outside the clone\n" +
					"host-check:   fix: mv " + path + " " + runtime.Config.Path + "\n"
				wantFailures = 1
			case "WARN":
				path = filepath.Join(home, ".claude.json.tmp.fixture")
				want = "host-check: WARN stale-state-tmp " + path + " — Claude's stale state temp file\n" +
					"host-check:   fix: rm " + path + "\n"
				wantWarnings = 1
			case "unreadable":
				path = filepath.Dir(runtime.Config.Path)
				body = "not a directory"
				want = ""
				for _, name := range []string{config.FileName, config.LegacyFileName} {
					unreadable := filepath.Join(path, name)
					want += "host-check: BLOCK pre-split-config " + unreadable + " — UNREADABLE " + unreadable + ": " + syscall.ENOTDIR.Error() + "\n" +
						"host-check:   fix: make " + unreadable + " readable to you, then rerun\n"
				}
				wantFailures = 2
			case "pre-split config":
				path = runtime.Config.Path
				body = `{"mcp":{"servers":{"harvester":{"enabled":true}}}}`
				want = "host-check: BLOCK pre-split-config " + path + " — mcp.servers.harvester belongs in harvester.config.json\n" +
					"host-check:   fix: move mcp.servers.harvester.enabled (true) to \"enabled\" in " + filepath.Join(
					filepath.Dir(path),
					config.HarvesterFileName,
				) + ", then delete mcp.servers.harvester from " + path + "\n"
				wantFailures = 1
			}
			if path != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "WARN" {
				old := now.Add(-25 * time.Hour)
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			warnings, failures := printHostChecks(&stdout, runtime, now)
			if stdout.String() != want || warnings != wantWarnings || failures != wantFailures {
				t.Fatalf(
					"output=%q warnings=%d failures=%d; want %q warnings=%d failures=%d",
					stdout.String(),
					warnings,
					failures,
					want,
					wantWarnings,
					wantFailures,
				)
			}
		})
	}
}

func TestHostChecksDoctorTallies(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		code    int
		summary string
	}{
		{"clean", 0, "doctor: clean\n"},
		{"BLOCK", 3, "doctor: failures=1\n"},
		{"WARN", 1, "doctor: warnings=1\n"},
		{"pre-split config", 3, "doctor: failures=1\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			account := stageExternalDoctorAccount(t, runtime.Paths.Home)
			runtime.Config.Accounts = []config.Account{{ID: 1, ConfigDir: account}}
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			var path, body string
			switch scenario.name {
			case "BLOCK":
				path = filepath.Join(config.LegacyConfigDir(paths.OSEnv{}, runtime.Paths.Home), config.FileName)
				body = `{}`
			case "WARN":
				path = filepath.Join(runtime.Paths.Home, ".claude.json.tmp.fixture")
			case "pre-split config":
				path = runtime.Config.Path
				body = `{"version":2,"mcp":{"servers":{"harvester":{"enabled":true}}}}`
			}
			if path != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				old := now.Add(-25 * time.Hour)
				if err := os.Chtimes(path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			var expected bytes.Buffer
			printHostChecks(&expected, runtime, now)
			dependencies := testDependencies()
			dependencies.Clock = clock.NewFake(now)
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, dependencies)
			if code != scenario.code || !strings.HasSuffix(stdout.String(), scenario.summary) ||
				!strings.Contains(stdout.String(), expected.String()) {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q; want code=%d summary=%q host rows=%q",
					code,
					stdout.String(),
					stderr.String(),
					scenario.code,
					scenario.summary,
					expected.String(),
				)
			}
		})
	}
}
