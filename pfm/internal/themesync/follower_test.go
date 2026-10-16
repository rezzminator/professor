package themesync

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestFollowerPlistHoldsTheContractedKeys(t *testing.T) {
	plist := followerPlist("/opt/pfm", "/h", "/h/.local/state/pfm/theme")
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
	plist := followerPlist("/opt/p&fm<x>", "/h", "/s")
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
	h.set("tokyo-night", ModeAuto)
	plist := "Library/LaunchAgents/com.professor.pfm.theme.plist"
	h.write(plist, "<plist>stale render</plist>\n")
	h.env.LaunchdJob = "com.professor.pfm.theme"
	calls := len(h.runner.Calls())
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "follower", VerdictError, "cannot reload itself from inside launchd")
	if got, want := h.read(plist), followerPlist("/opt/bin/pfm", h.home, h.stateDir); got != want {
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
// the own-job guard never blocks a bootstrap that needs no bootout, and a
// retire for a forced mode settles any reload still owed.
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
		h.set("tokyo-night", ModeAuto)
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
		h.set("tokyo-night", ModeAuto)
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
	t.Run("retire clears a reload still owed", func(t *testing.T) {
		h := newHarness(t)
		h.set("tokyo-night", ModeAuto)
		h.write(plist, "<plist>stale render</plist>\n")
		h.env.LaunchdJob = followerLabel
		if _, err := Sync(context.Background(), h.env, false); err != nil {
			t.Fatal(err)
		}
		h.env.LaunchdJob = ""
		h.set("tokyo-night", ModeDark)
		state, _, err := (stateStore{dir: h.stateDir}).load()
		if err != nil {
			t.Fatal(err)
		}
		if state.Due[surfaceFollower] {
			t.Errorf("mode dark retired the follower but left it due: %v", state.Due)
		}
	})
}
