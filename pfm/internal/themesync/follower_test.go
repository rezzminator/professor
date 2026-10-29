package themesync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestFollowerPlistHoldsTheContractedKeys(t *testing.T) {
	plist, err := followerPlist("/opt/pfm", "/h", "/h/.local/state/pfm/theme")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>Label</key>\n\t<string>com.professor.pfm.theme</string>",
		"<string>/opt/pfm</string>\n\t\t<string>theme</string>\n\t\t<string>sync</string>",
		"<key>WatchPaths</key>\n\t<array>\n\t\t<string>/h/Library/Preferences/.GlobalPreferences.plist</string>",
		"<key>StartInterval</key>\n\t<integer>60</integer>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>StandardOutPath</key>\n\t<string>/h/.local/state/pfm/theme/follow.log</string>",
		"<key>StandardErrorPath</key>\n\t<string>/h/.local/state/pfm/theme/follow.log</string>",
		"<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n" +
			"\t\t<string>" + paths.ServicePath("/h") + "</string>\n\t</dict>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if !strings.HasPrefix(plist, "<?xml") || !strings.HasSuffix(plist, "</plist>\n") {
		t.Errorf("plist is not a whole document:\n%s", plist)
	}
}

func TestFollowerPlistEscapesXML(t *testing.T) {
	plist, err := followerPlist("/opt/p&fm<x>", "/h", "/s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plist, "/opt/p&amp;fm&lt;x&gt;") {
		t.Fatalf("binary path not escaped:\n%s", plist)
	}
}

// A follower plist rewritten from inside the follower's own launchd job is
// never reloaded there: bootout would make launchd stop the running sync before
// bootstrap, leaving no agent. The row names the remedy and stays due; the next
// run outside launchd reloads it.
func TestFollowerNeverBootsOutItsOwnJob(t *testing.T) {
	h := newHarness(t)
	h.on()
	plist := "Library/LaunchAgents/com.professor.pfm.theme.plist"
	h.write(plist, "<plist>stale render</plist>\n")
	h.env.LaunchdJob = "com.professor.pfm.theme"
	calls := len(h.runner.Calls())
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "follower", VerdictError, "cannot reload itself from inside launchd")
	want, err := followerPlist(agentlauncher.Program(h.home), h.home, h.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.read(plist); got != want {
		t.Errorf("plist not rewritten:\n%s", got)
	}
	for _, call := range h.runner.Calls()[calls:] {
		if joined := strings.Join(call.Argv, " "); strings.Contains(joined, "bootout") ||
			strings.Contains(joined, "bootstrap") {
			t.Errorf("the follower's own job ran %q", joined)
		}
	}
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "follower", VerdictError, "was not reloaded")

	h.env.LaunchdJob = ""
	calls = len(h.runner.Calls())
	report, err = Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "follower", VerdictInSync, "")
	var ran []string
	for _, call := range h.runner.Calls()[calls:] {
		if call.Argv[0] == "launchctl" && call.Argv[1] != "print" {
			ran = append(ran, call.Argv[1])
		}
	}
	if strings.Join(ran, " ") != "bootout bootstrap" {
		t.Errorf("outside launchd the due follower ran launchctl %q, want bootout then bootstrap", ran)
	}
}

// A rewritten follower plist owes launchd a reload until a bootstrap succeeds:
// a failed bootout keeps it due (never IN-SYNC while the old definition runs),
// and the own-job guard never blocks a bootstrap that needs no bootout.
func TestFollowerReloadOwedUntilABootstrapSucceeds(t *testing.T) {
	plist := "Library/LaunchAgents/com.professor.pfm.theme.plist"
	launchctlVerbs := func(h *harness, from int) string {
		var ran []string
		for _, call := range h.runner.Calls()[from:] {
			if call.Argv[0] == "launchctl" && call.Argv[1] != "print" {
				ran = append(ran, call.Argv[1])
			}
		}
		return strings.Join(ran, " ")
	}
	t.Run("a failed bootout stays due", func(t *testing.T) {
		h := newHarness(t)
		h.on()
		h.write(plist, "<plist>stale render</plist>\n")
		target := fmt.Sprintf("gui/%d/%s", os.Getuid(), followerLabel)
		h.runner.Script(
			[]string{"launchctl", "bootout", target},
			deps.RunResult{ExitCode: 5, Stderr: []byte("Boot-out failed: 5: Input/output error")},
			nil,
		)
		report, err := Sync(context.Background(), h.env, false)
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, report, "follower", VerdictError, "bootout exited 5")
		status, err := ReadBack(context.Background(), h.env)
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, status, "follower", VerdictError, "was not reloaded")
		h.rearm()
		calls := len(h.runner.Calls())
		report, err = Sync(context.Background(), h.env, false)
		if err != nil {
			t.Fatal(err)
		}
		wantVerdict(t, report, "follower", VerdictInSync, "")
		if got := launchctlVerbs(h, calls); got != "bootout bootstrap" {
			t.Errorf("the retry ran launchctl %q, want bootout then bootstrap", got)
		}
	})
	t.Run("own job with the agent unloaded bootstraps", func(t *testing.T) {
		h := newHarness(t)
		h.on()
		h.write(plist, "<plist>stale render</plist>\n")
		h.launchctlPrintFail = true
		h.rearm()
		h.env.LaunchdJob = followerLabel
		calls := len(h.runner.Calls())
		if _, err := Sync(context.Background(), h.env, false); err != nil {
			t.Fatal(err)
		}
		if got := launchctlVerbs(h, calls); got != "bootstrap" {
			t.Errorf("an unloaded agent inside its job ran launchctl %q, want bootstrap alone", got)
		}
	})
}

// The follower watches what moves the source: each account's real
// settings.json, each distinct real themes dir once, and the macOS global
// preferences; a symlinked account is watched by its real path.
func TestFollowerWatchesRealSettingsThemesDirsAndTheGlobalPreferences(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{".claude", ".claude2"} {
		if err := os.MkdirAll(filepath.Join(home, rel, "themes"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, rel, "settings.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".cc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude2"), filepath.Join(home, ".cc", "2")); err != nil {
		t.Fatal(err)
	}
	canonical := func(rel string) string {
		resolved, err := filepath.EvalSymlinks(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	want := []string{
		canonical(".claude/settings.json"), canonical(".claude/themes"),
		canonical(".claude2/settings.json"), canonical(".claude2/themes"),
		filepath.Join(home, "Library", "Preferences", ".GlobalPreferences.plist"),
	}
	got, err := followerWatchPaths(home, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("watch paths = %v, want %v", got, want)
	}
	plist, err := followerPlist("/opt/pfm", home, "/s")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range want {
		if !strings.Contains(plist, "<string>"+path+"</string>") {
			t.Errorf("plist does not watch %s:\n%s", path, plist)
		}
	}
}

// The follower's launch agent runs the stable launcher, never pfm itself: macOS
// re-registers an agent whose program is a rebuilt, ad-hoc-signed binary and
// posts a background-activity advisory per build. Turning the sync on stages the
// launcher beside the plist, and a launcher that went missing reads as drift,
// since launchd could not spawn the agent without it.
func TestFollowerRunsThroughTheStableLauncher(t *testing.T) {
	h := newHarness(t)
	h.on()
	plist := h.read("Library/LaunchAgents/com.professor.pfm.theme.plist")
	launcher := agentlauncher.Program(h.home)
	want := "<array>\n\t\t<string>" + launcher + "</string>\n\t\t<string>theme</string>\n\t\t<string>sync</string>"
	if !strings.Contains(plist, want) {
		t.Fatalf("follower plist does not run `theme sync` through the launcher %s:\n%s", launcher, plist)
	}
	if strings.Contains(plist, h.env.PfmBinary) {
		t.Errorf("follower plist names pfm %s directly:\n%s", h.env.PfmBinary, plist)
	}
	if current, err := agentlauncher.UpToDate(h.home); err != nil || !current {
		t.Fatalf("launcher after turning the sync on: current=%v err=%v", current, err)
	}
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "follower", VerdictDrift, "launcher")
}

func TestFollowerDiscoveryFailureKeepsLastValidWatches(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			h := newHarness(t)
			h.goos = platform
			h.rearm()
			h.on()
			s := newSession(h.env, stateStore{dir: h.stateDir}, h.state())
			path := s.followerPath()
			if platform == "linux" {
				path = s.systemdFollowerPath()
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(h.home, settingsRel)
			if err := os.Remove(settings); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("settings.json", settings); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				for _, result := range []SurfaceResult{s.applyFollower(context.Background()), s.readFollower(context.Background())} {
					if result.Verdict != VerdictError || !strings.Contains(result.Detail, "settings.json") {
						t.Errorf("discovery failure reported %s: %s", result.Verdict, result.Detail)
					}
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, before) {
					t.Errorf("discovery error replaced valid watches: %v", err)
				}
			}
			if err := os.Remove(settings); err != nil {
				t.Fatal(err)
			}
			h.write(settingsRel, `{"theme":"dark"}`)
			if result := s.applyFollower(context.Background()); result.Verdict != VerdictApplied {
				t.Errorf("recovery: %+v", result)
			}
		})
	}
}
