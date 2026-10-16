package installer

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstall with the theme sync on runs `pfm theme off` first (its plan says it
// would), so no surface is left themed by a pfm that is gone; an off that fails
// stops the uninstall with its error.
func TestUninstallRunsThemeOffWhileTheSyncIsOn(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		home := t.TempDir()
		writeFixture(t, filepath.Join(home, ".local", "state", "pfm", "theme", "state.json"), `{"enabled":true}`)
		var output bytes.Buffer
		ran := false
		plan := &engine{options: Options{Home: home, Stdout: &output, themeOff: func(context.Context, string) error {
			ran = true
			return nil
		}}}
		if err := plan.themeSyncOff(context.Background()); err != nil || ran {
			t.Fatalf("the uninstall plan ran theme off (ran %v, err %v)", ran, err)
		}
		if !strings.Contains(output.String(), "would run pfm theme off") {
			t.Fatalf("uninstall plan does not say it would run pfm theme off:\n%s", output.String())
		}
	})
	// A rerun after an off that failed mid-restore finds the sync disabled but a
	// Ghostty snapshot still held, and runs off again.
	for _, tc := range []struct {
		name, state string
		failing     bool
	}{
		{name: "failing=false", state: `{"enabled":true}`},
		{name: "failing=true", state: `{"enabled":true}`, failing: true},
		{name: "rerun after a partial off", state: `{"enabled":false,"ghostty":{"config":"c","added":"a","backup":"b"}}`},
	} {
		failing := tc.failing
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeFixture(t, filepath.Join(home, ".local", "state", "pfm", "theme", "state.json"), tc.state)
			var offs []string
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeUninstall, Home: home,
				Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
				themeOff: func(_ context.Context, offHome string) error {
					offs = append(offs, offHome)
					if failing {
						return fmt.Errorf("tmux restore refused")
					}
					return nil
				},
			})
			if len(offs) != 1 || offs[0] != home {
				t.Fatalf("theme off ran for %q, want once for %s", offs, home)
			}
			if failing {
				if err == nil || !strings.Contains(err.Error(), "pfm theme off") ||
					!strings.Contains(err.Error(), "tmux restore refused") {
					t.Fatalf("a failed theme off did not stop the uninstall with its error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("uninstall: %v\n%s", err, output.String())
			}
		})
	}
}
