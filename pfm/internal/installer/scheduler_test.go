package installer

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// schedulerKinds are both service managers: a test of what the scheduled jobs
// do runs once per kind, so both install paths run on one host.
var schedulerKinds = []servicemanager.Kind{servicemanager.KindSystemd, servicemanager.KindLaunchd}

// themeFollowerCall is the marker the follower refresh seam records into the
// fake runner's call log, so its place in the install order is visible.
const themeFollowerCall = "theme follower refresh"

// Install drives the scheduled jobs through its kind's manager alone, in the
// order each kind's wiring runs: systemd links and reloads the units and
// restarts the MCP daemon after the MCP wiring; launchd bootstraps the
// name-sync, schedule and MCP agents, then refreshes the theme follower.
func TestUnitTransitionsUseOnlyTheInjectedManager(t *testing.T) {
	t.Parallel()
	domain := "gui/" + strconv.Itoa(os.Getuid())
	for _, test := range []struct {
		kind    servicemanager.Kind
		want    func(home string) []string
		foreign string
	}{
		{
			kind: servicemanager.KindSystemd,
			want: func(string) []string {
				return []string{
					nameSyncStateProbe,
					"systemctl --user daemon-reload",
					"systemctl --user enable --now pfm-name-sync.path " + nameSyncTimerUnit + " " + scheduleTimerUnit,
					"systemctl --user enable --now " + mcpUnitName,
					"systemctl --user restart " + mcpUnitName,
				}
			},
			foreign: "launchctl ",
		},
		{
			kind: servicemanager.KindLaunchd,
			want: func(home string) []string {
				agents := filepath.Join(home, "Library", "LaunchAgents")
				return []string{
					"launchctl print " + domain + "/" + launchdLabel,
					"launchctl bootstrap " + domain + " " + filepath.Join(agents, launchdLabel+".plist"),
					"launchctl bootstrap " + domain + " " + filepath.Join(agents, scheduleLaunchdLabel+".plist"),
					"launchctl bootstrap " + domain + " " + filepath.Join(agents, mcpLaunchdLabel+".plist"),
					themeFollowerCall,
				}
			},
			foreign: "systemctl ",
		},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			writeFixture(t, filepath.Join(themesync.DefaultStateDir(home), themesync.StateFileName), `{"enabled":true}`)
			runner := &fakeRunner{manager: true}
			options := Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: home, Runner: runner,
				MCPEnabled: map[string]bool{chatName: true},
				scheduler:  schedulerFor(test.kind),
				themeFollowerRefresh: func(context.Context, string) error {
					runner.calls = append(runner.calls, themeFollowerCall)
					return nil
				},
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			next := 0
			want := test.want(home)
			for _, call := range runner.calls {
				if strings.HasPrefix(call, test.foreign) {
					t.Fatalf("%s install ran %q:\n%s", test.kind, call, strings.Join(runner.calls, "\n"))
				}
				if next < len(want) && call == want[next] {
					next++
				}
			}
			if next != len(want) {
				t.Fatalf("%s install calls lack %q in order %q:\n%s",
					test.kind, want[next], want, strings.Join(runner.calls, "\n"))
			}
		})
	}
}
