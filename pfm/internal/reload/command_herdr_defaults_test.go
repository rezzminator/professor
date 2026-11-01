package reload

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			wantStderr := ""
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
			result, err := RunHerdr(context.Background(), f.request, f.options, &stderr)
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
		})
	}
}
