package installer

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
	// Herdr snapshot still held, and runs off again.
	for _, tc := range []struct {
		name, state string
		failing     bool
	}{
		{name: "failing=false", state: `{"enabled":true}`},
		{name: "failing=true", state: `{"enabled":true}`, failing: true},
		{name: "rerun after a partial off", state: `{"enabled":false,"herdr":{"span":"a","added":"","backup":"b"}}`},
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

func TestUninstallWaitsForVSCodeOwnershipOrExplicitLeave(t *testing.T) {
	for _, leave := range []bool{false, true} {
		t.Run(fmt.Sprintf("leave=%v", leave), func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".local", "state", "pfm", "theme")
			global := filepath.Join(dir, "vscode-applied.json")
			chrome := filepath.Join(dir, "vscode-chrome.json")
			globalData := `{"keys":{"titleBar.activeBackground":{},"[Fixture]":{"keys":{"window.activeBorder":{}}}}}`
			chromeData := `{"vscode-remote://ssh-remote+fixture/folder":{"keys":{"statusBar.border":{}}}}`
			writeFixture(t, global, globalData)
			writeFixture(t, chrome, chromeData)
			// No state file or VS Code installation: recovery records alone must gate uninstall.
			var output bytes.Buffer
			ran := 0
			options := Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeUninstall, Home: home,
				Stdout: &output, Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{},
				LeaveVSCodeColors: leave,
				themeOff:          func(context.Context, string) error { ran++; return nil },
			}
			_, err := Run(context.Background(), options)
			if ran != 1 {
				t.Fatalf("off called %d times, want once", ran)
			}
			detail := output.String()
			if leave {
				if err != nil || !strings.Contains(detail, "deliberately retained") {
					t.Fatalf("explicit leave failed: %v\n%s", err, detail)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "retry pfm uninstall") ||
					!strings.Contains(err.Error(), "--leave-vscode-colors") {
					t.Fatalf("uninstall did not give actionable refusal: %v", err)
				}
				detail = err.Error()
			}
			for _, want := range []string{
				global, chrome, "Global user settings", "titleBar.activeBackground",
				"[Fixture].window.activeBorder", "vscode-remote://ssh-remote+fixture/folder", "statusBar.border",
			} {
				if !strings.Contains(detail, want) {
					t.Errorf("leftover report omitted %q: %s", want, detail)
				}
			}
			for file, want := range map[string]string{global: globalData, chrome: chromeData} {
				raw, readErr := os.ReadFile(file)
				if readErr != nil || string(raw) != want {
					t.Fatalf("recovery record changed: %s = %s, %v", file, raw, readErr)
				}
			}
			// Model the extension consuming the off; a retry can now finish.
			for _, file := range []string{global, chrome} {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			}
			options.LeaveVSCodeColors = false
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatalf("retry after restoration: %v", err)
			}
		})
	}
}

func TestLeaveVSCodeColorsDoesNotBypassRestoreErrors(t *testing.T) {
	for _, brokenRecord := range []bool{false, true} {
		t.Run(fmt.Sprintf("brokenRecord=%v", brokenRecord), func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(home, ".local", "state", "pfm", "theme", "vscode-applied.json")
			data := `{"keys":{"window.activeBorder":{}}}`
			if brokenRecord {
				data = "not json"
			}
			writeFixture(t, file, data)
			var output bytes.Buffer
			instance := &engine{apply: true, options: Options{
				Home: home, Stdout: &output, LeaveVSCodeColors: true,
				themeOff: func(context.Context, string) error {
					if !brokenRecord {
						return fmt.Errorf("tmux restore refused")
					}
					return nil
				},
			}}
			if err := instance.themeSyncOff(context.Background()); err == nil {
				t.Fatal("leave flag bypassed a restoration/read failure")
			}
			if strings.Contains(output.String(), "deliberately retained") {
				t.Fatal("reported successful leave despite failure")
			}
		})
	}
}
