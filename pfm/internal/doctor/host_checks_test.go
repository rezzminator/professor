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
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestPrintHostChecks(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, scenario := range []string{"clean", "BLOCK", "WARN", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
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
				path = filepath.Join(installer.ClaudeStore(home), ".credentials.json")
				body = `{}`
				account := config.DefaultAccountDir(home, 1)
				want = "host-check: BLOCK store-identity " + path + " — .credentials.json is account identity inside the store, " +
					"written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)\n" +
					"host-check:   fix: mkdir -m 700 -p " + account + " && mv " + path + " " + filepath.Join(
					account,
					".credentials.json",
				) + "\n"
				wantFailures = 1
			case "WARN":
				path = filepath.Join(home, ".claude.json.tmp.fixture")
				want = "host-check: WARN stale-state-tmp " + path + " — Claude's stale state temp file\n" +
					"host-check:   fix: rm " + path + "\n"
				wantWarnings = 1
			case "unreadable":
				// The account roots' parent as a file: stray-dir cannot look
				// under it, while the configured account lives elsewhere.
				runtime.Config.Accounts[0].ConfigDir = filepath.Join(home, "account")
				path = filepath.Dir(config.DefaultAccountDir(home, 1))
				body = "not a directory"
				want = ""
				for _, name := range []string{".git", ".codex", ".agents"} {
					unreadable := filepath.Join(path, name)
					want += "host-check: BLOCK stray-dir " + unreadable + " — UNREADABLE " + unreadable + ": " + syscall.ENOTDIR.Error() + "\n" +
						"host-check:   fix: make " + unreadable + " readable to you, then rerun\n"
				}
				wantFailures = 3

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
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			account := stageExternalDoctorAccount(t, runtime.Paths.Home)
			runtime.Config.Accounts = []config.Account{{ID: 1, ConfigDir: account}}
			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			var path, body string
			switch scenario.name {
			case "BLOCK":
				path = filepath.Join(installer.ClaudeStore(runtime.Paths.Home), ".credentials.json")
				body = `{}`
			case "WARN":
				path = filepath.Join(runtime.Paths.Home, ".claude.json.tmp.fixture")

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
			dependencies := testDependencies()
			dependencies.Runner = healthyDoctorRunner()
			dependencies.Clock = clock.NewFake(now)
			var stdout, stderr bytes.Buffer
			code := Run(nil, &stdout, &stderr, runtime, dependencies)
			lines := strings.SplitAfter(stdout.String(), "\n")
			if summary := lines[len(lines)-2]; code != scenario.code || summary != scenario.summary {
				t.Errorf(
					"code=%d summary=%q, want %d %q; stderr=%q",
					code,
					summary,
					scenario.code,
					scenario.summary,
					stderr.String(),
				)
			}
		})
	}
}
