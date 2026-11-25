package reload

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestRunHerdrPreservesBirthDefaults(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		accountGiven, cacheGiven, missing bool
		wantAccount                       int
		wantCache                         bool
		want                              string
	}{
		{name: "retain-account-and-cache", wantAccount: 1, wantCache: true},
		{name: "explicit-account-and-cache", accountGiven: true, cacheGiven: true, wantAccount: 2},
		{name: "explicit-account-retain-cache", accountGiven: true, wantAccount: 2, wantCache: true},
		{name: "explicit-cache-retain-account", cacheGiven: true, wantAccount: 1},
		{name: "missing-birth-metadata", missing: true, want: "account"},
		{name: "unknown-account", want: "account"},
		{name: "invalid-cache", wantAccount: 1},
		{name: "missing-cache", wantAccount: 1},
		{name: "stale-launch-record", wantAccount: 1, wantCache: true},
		// Only the new recipe must be absolute; the old one's cwd is a fallback.
		{name: "relative-old-cwd", wantAccount: 1, wantCache: true},
		// A full-copy recipe and the reloading chat both hold a variable the config
		// has since renamed; the new recipe holds pfm's part alone.
		{name: "config-dropped-env", wantAccount: 1, wantCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			wantStderr := ""
			dropped := tc.name == "config-dropped-env"
			if dropped {
				f.configDroppedEnv()
			}
			if !tc.missing {
				before := f.request
				before.Account = 1
				before.Cache1H = true
				spawn, err := claudeSpawn(before)
				if err != nil {
					t.Fatal(err)
				}
				f.old, err = spawn.SeatRun(before.CWD)
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "unknown-account" {
					f.request.Machine.Accounts = f.request.Machine.Accounts[1:]
				}
				if tc.name == "invalid-cache" || tc.name == "missing-cache" {
					key := ""
					for _, knob := range claudelaunch.Knobs {
						if knob.Name == "cache1h" {
							key = knob.Target
						}
					}
					for i, entry := range f.old.Env {
						if strings.HasPrefix(entry, key+"=") {
							recorded := ""
							if tc.name == "invalid-cache" {
								recorded = "not-a-cache"
								f.old.Env[i] = key + "=" + recorded
							} else {
								f.old.Env = append(f.old.Env[:i], f.old.Env[i+1:]...)
							}
							wantStderr = "pfm chat reload: the pane's recipe holds no valid cache (" + key + "=\"" +
								recorded + "\") — reloading with 5m; pass --cache to choose\n"
							break
						}
					}
				}
				if tc.name == "relative-old-cwd" {
					f.old.CWD = "relative/project"
				}
				if dropped {
					f.old = f.fullCopy(f.old)
				}
				if tc.name == "stale-launch-record" {
					values, err := paths.Resolve()
					if err != nil {
						t.Fatal(err)
					}
					if err := fleetdb.RecordLaunch(
						context.Background(),
						values,
						fleetdb.Launch{SessionID: f.seat.SessionID, Engine: f.request.Engine, Account: 2},
						1,
					); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.db.WriteSeatRun(f.seat.SeatID, f.old); err != nil {
					t.Fatal(err)
				}
			}
			f.request.AccountGiven = tc.accountGiven
			f.request.CacheGiven = tc.cacheGiven
			var stderr strings.Builder
			result, err := runHerdrOnFake(t, context.Background(), f.request, f.options, &stderr)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v want %q metadata diagnostic", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Account != tc.wantAccount || result.Cache1H != tc.wantCache ||
				(wantStderr != "" && !strings.Contains(stderr.String(), wantStderr)) {
				t.Fatalf("result=%+v want account=%d cache=%t\nstderr=%q\nwant line %q",
					result, tc.wantAccount, tc.wantCache, stderr.String(), wantStderr)
			}
			expected := f.request
			expected.Account = tc.wantAccount
			expected.Cache1H = tc.wantCache
			if dropped {
				expected.Envs = map[string]string{"AUTO_EFFORT_JUDGE": "true"}
			}
			spawn, err := claudeSpawn(expected)
			if err != nil {
				t.Fatal(err)
			}
			want, err := spawn.SeatRun(expected.CWD)
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.db.ReadSeatRun(f.seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("recipe=%+v want=%+v", got, want)
			}
			if dropped {
				assertDroppedEnv(t, got, "AUTO_EFFORT_JUDGE=true",
					"CLAUDE_CONFIG_DIR="+f.request.Machine.Accounts[0].ConfigDir,
					"CACHE_LIVE_CONTROL_MAIN_TTL=1h", "PFM_COST_LEDGER=off")
			}
		})
	}
}

// droppedSeatHost is the seat host's own environment in the config-dropped-env rows.
var droppedSeatHost = []string{"HOME=/fixture/home", "PATH=/herdr/bin", "USER_SHELL_MARK=kept"}

// assertDroppedEnv checks the engine environment run gives over droppedSeatHost:
// the host's own entries but PATH, the reloading chat's PATH, then assignments.
func assertDroppedEnv(t *testing.T, run fleetdb.SeatRun, assignments ...string) {
	t.Helper()
	want := append([]string{"HOME=/fixture/home", "USER_SHELL_MARK=kept", "PATH=/writer/bin"}, assignments...)
	if got := run.Environ(droppedSeatHost); !slices.Equal(got, want) {
		t.Fatalf("engine env over the seat host %q = %q, want %q\nrecipe=%+v", droppedSeatHost, got, want, run)
	}
}

// configDroppedEnv stages a reload from a chat whose own environment holds
// AUTO_EFFORT_CONTROL_JUDGE, which the cwd's pfm.config.json has since renamed
// AUTO_EFFORT_JUDGE, and whose search path is /writer/bin.
func (f *reloadHerdrFixture) configDroppedEnv() {
	f.t.Helper()
	f.t.Setenv("AUTO_EFFORT_CONTROL_JUDGE", "true")
	f.t.Setenv("PATH", "/writer/bin")
	// The writer presets no shell, so the recipe carries none (a gate's own preset would ride).
	f.t.Setenv("CLAUDE_CODE_SHELL", "")
	if err := os.Unsetenv("CLAUDE_CODE_SHELL"); err != nil {
		f.t.Fatalf("unset CLAUDE_CODE_SHELL: %v", err)
	}
	for path, body := range map[string]string{
		filepath.Join(f.request.CWD, ".professor", "baseline.json"): "{}",
		pfmconfig.PathIn(f.request.CWD):                             `{"envs":{"AUTO_EFFORT_JUDGE":"true"}}`,
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
}

// fullCopy is run as an older pfm stored it: its engine's whole environment,
// the dropped AUTO_EFFORT_CONTROL_JUDGE among it.
func (f *reloadHerdrFixture) fullCopy(run fleetdb.SeatRun) fleetdb.SeatRun {
	run.Env = append([]string{"HOME=/fixture/home", "AUTO_EFFORT_CONTROL_JUDGE=true"}, run.Env...)
	run.Unset, run.EnvDelta = nil, false
	return run
}
