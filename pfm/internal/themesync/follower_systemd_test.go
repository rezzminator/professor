package themesync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	linuxUnitDir       = ".config/systemd/user/"
	linuxServerVscode  = ".vscode-server/data/Machine/settings.json"
	strayFollowerPlist = "Library/LaunchAgents/com.professor.pfm.theme.plist"
)

// newLinuxHarness is the harness on a Linux host driven through VS Code
// Remote-SSH: the VS Code Server's Machine settings, no ~/Library at all.
func newLinuxHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.goos = "linux"
	h.write(linuxServerVscode, fixtureVscode)
	h.rearm()
	if err := os.RemoveAll(h.path("Library")); err != nil {
		t.Fatal(err)
	}
	return h
}

// systemctlCalls lists the `systemctl --user` verbs (with their units) run
// since call index from, is-active reads left out.
func systemctlCalls(h *harness, from int) []string {
	var ran []string
	for _, call := range h.runner.Calls()[from:] {
		if len(call.Argv) > 2 && call.Argv[0] == "systemctl" && call.Argv[1] == "--user" &&
			call.Argv[2] != "is-active" {
			ran = append(ran, strings.Join(call.Argv[2:], " "))
		}
	}
	return ran
}

func wantNoLaunchd(t *testing.T, h *harness) {
	t.Helper()
	for _, call := range h.runner.Calls() {
		if call.Argv[0] == "launchctl" {
			t.Errorf("a Linux run ran %q", strings.Join(call.Argv, " "))
		}
	}
	if h.exists("Library") {
		t.Errorf("a Linux run created %s", h.path("Library"))
	}
}

// On Linux the follower is a systemd user unit trio — a oneshot service
// running `pfm theme sync`, a path unit watching what moves the source, a
// one-minute timer backstop — never a launch agent: no ~/Library path is
// written and launchctl never runs.
func TestLinuxFollowerIsASystemdUnitTrioNeverALaunchAgent(t *testing.T) {
	h := newLinuxHarness(t)
	h.on()
	wantNoLaunchd(t, h)
	if got, want := strings.Join(systemctlCalls(h, 0), "; "),
		"daemon-reload; enable pfm-theme.path pfm-theme.timer; restart pfm-theme.path pfm-theme.timer"; got != want {
		t.Errorf("on ran systemctl --user %q, want %q", got, want)
	}
	service := h.read(linuxUnitDir + "pfm-theme.service")
	for _, want := range []string{
		"Type=oneshot\n",
		`Environment="PATH=` + paths.ServicePath(h.home) + `"` + "\n",
		`Environment="XPC_SERVICE_NAME=com.professor.pfm.theme"` + "\n",
		`ExecStart="/opt/bin/pfm" theme sync` + "\n",
		"StandardOutput=append:" + filepath.Join(h.stateDir, "follow.log") + "\n",
		"StandardError=append:" + filepath.Join(h.stateDir, "follow.log") + "\n",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("pfm-theme.service lacks %q:\n%s", want, service)
		}
	}
	pathUnit := h.read(linuxUnitDir + "pfm-theme.path")
	for _, rel := range []string{settingsRel, ".claude/themes"} {
		watched, err := filepath.EvalSymlinks(h.path(rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pathUnit, "PathModified="+watched+"\n") {
			t.Errorf("pfm-theme.path does not watch %s:\n%s", watched, pathUnit)
		}
	}
	if strings.Contains(pathUnit, "GlobalPreferences") || !strings.Contains(pathUnit, "Unit=pfm-theme.service\n") {
		t.Errorf("pfm-theme.path watches macOS preferences or triggers another unit:\n%s", pathUnit)
	}
	timer := h.read(linuxUnitDir + "pfm-theme.timer")
	for _, want := range []string{"OnActiveSec=1s\n", "OnUnitInactiveSec=60s\n", "Unit=pfm-theme.service\n"} {
		if !strings.Contains(timer, want) {
			t.Errorf("pfm-theme.timer lacks %q:\n%s", want, timer)
		}
	}
	wantVerdict(t, h.status(), "follower", VerdictInSync, "")

	calls := len(h.runner.Calls())
	h.sync(false)
	if ran := systemctlCalls(h, calls); len(ran) != 0 {
		t.Errorf("a sync with the units current and active ran systemctl --user %q", ran)
	}
	h.unitsInactive = true
	h.rearm()
	wantVerdict(t, h.status(), "follower", VerdictDrift, "not active")
	calls = len(h.runner.Calls())
	h.sync(false)
	if got := strings.Join(
		systemctlCalls(h, calls),
		"; ",
	); !strings.Contains(
		got,
		"restart pfm-theme.path pfm-theme.timer",
	) {
		t.Errorf("a sync over inactive units ran systemctl --user %q, want a restart", got)
	}
	h.write(linuxUnitDir+"pfm-theme.path", "[Path]\n")
	wantVerdict(t, h.status(), "follower", VerdictDrift, "pfm-theme.path differs from the render")
}

// A rewritten unit owes the user manager a reload until one succeeds: a
// failed restart keeps it due (ERROR, never IN-SYNC on the old definition),
// and the next run reloads it.
func TestLinuxFollowerReloadOwedUntilARestartSucceeds(t *testing.T) {
	h := newLinuxHarness(t)
	h.on()
	h.write(linuxUnitDir+"pfm-theme.service", "[Service]\nstale render\n")
	h.runner.Script(
		[]string{"systemctl", "--user", "restart"},
		deps.RunResult{ExitCode: 1, Stderr: []byte("Failed to connect to bus: No medium found")},
		nil,
	)
	report := h.sync(false)
	wantVerdict(t, report, "follower", VerdictError, "No medium found")
	wantVerdict(t, h.status(), "follower", VerdictError, "not reloaded")
	h.rearm()
	calls := len(h.runner.Calls())
	h.sync(false)
	if got := strings.Join(systemctlCalls(h, calls), "; "); got !=
		"daemon-reload; enable pfm-theme.path pfm-theme.timer; restart pfm-theme.path pfm-theme.timer" {
		t.Errorf("the retry ran systemctl --user %q", got)
	}
	wantVerdict(t, h.status(), "follower", VerdictInSync, "")
}

// Off on Linux disables the units, deletes them and reloads the manager; a
// launch agent plist an earlier pfm wrote on Linux is removed, never loaded.
func TestLinuxFollowerOffDisablesAndRemovesTheUnits(t *testing.T) {
	h := newLinuxHarness(t)
	h.write(strayFollowerPlist, "<plist>written on Linux by an earlier pfm</plist>\n")
	h.on()
	if h.exists(strayFollowerPlist) {
		t.Error("on left the launch agent plist an earlier pfm wrote on Linux")
	}
	calls := len(h.runner.Calls())
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "follower", VerdictRestored, "")
	if got, want := strings.Join(systemctlCalls(h, calls), "; "),
		"daemon-reload; disable --now pfm-theme.path pfm-theme.timer; daemon-reload"; got != want {
		t.Errorf("off ran systemctl --user %q, want %q", got, want)
	}
	for _, unit := range []string{"pfm-theme.service", "pfm-theme.path", "pfm-theme.timer"} {
		if h.exists(linuxUnitDir + unit) {
			t.Errorf("off left %s", unit)
		}
	}
	for _, call := range h.runner.Calls() {
		if call.Argv[0] == "launchctl" {
			t.Errorf("a Linux run ran %q", strings.Join(call.Argv, " "))
		}
	}
	calls = len(h.runner.Calls())
	again, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, again, "follower", VerdictAbsent, "no systemd units installed")
	if ran := systemctlCalls(h, calls); len(ran) != 0 {
		t.Errorf("an off with no units ran systemctl --user %q", ran)
	}
}

// Off with a trigger's unit file gone (deleted by hand, or an apply cut
// short) disables the trigger still there: systemctl refuses a whole disable
// naming one unit with no file, which would leave the follower on for good.
func TestLinuxFollowerOffWithATriggerFileGoneDisablesTheRest(t *testing.T) {
	h := newLinuxHarness(t)
	h.on()
	if err := os.Remove(h.path(linuxUnitDir + "pfm-theme.timer")); err != nil {
		t.Fatal(err)
	}
	h.runner.Script(
		[]string{"systemctl", "--user", "disable", "--now", "pfm-theme.path", "pfm-theme.timer"},
		deps.RunResult{ExitCode: 1, Stderr: []byte("Failed to disable unit: Unit pfm-theme.timer does not exist")},
		nil,
	)
	calls := len(h.runner.Calls())
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "follower", VerdictRestored, "")
	if got, want := strings.Join(systemctlCalls(h, calls), "; "),
		"daemon-reload; disable --now pfm-theme.path; daemon-reload"; got != want {
		t.Errorf("off ran systemctl --user %q, want %q", got, want)
	}
	for _, unit := range []string{"pfm-theme.service", "pfm-theme.path", "pfm-theme.timer"} {
		if h.exists(linuxUnitDir + unit) {
			t.Errorf("off left %s", unit)
		}
	}
}

// On Linux the vscode row finds the settings this host has — the VS Code
// Server's Machine settings and the desktop products' under
// $XDG_CONFIG_HOME — backs each up once, and is ABSENT only when none
// exists, naming the Linux paths it looked at.
func TestLinuxVscodeFindsTheServerAndDesktopSettings(t *testing.T) {
	h := newLinuxHarness(t)
	h.write(".config/Code/User/settings.json", fixtureVscode)
	report := h.on()
	if got := row(t, report, "vscode"); got.Verdict == VerdictAbsent {
		t.Fatalf("vscode row = %s %q with the VS Code Server's settings present", got.Verdict, got.Detail)
	}
	backups := h.state().VSCode
	for _, rel := range []string{linuxServerVscode, ".config/Code/User/settings.json"} {
		if backups[h.path(rel)] == "" {
			t.Errorf("on backed up no %s: %v", rel, backups)
		}
	}
	h.psVscode = h.home + "/.vscode-server/cli/servers/Stable-0123/server/node --max-old-space-size=8192 " +
		h.home + "/.vscode-server/cli/servers/Stable-0123/server/out/server-main.js\n"
	h.rearm()
	if got := row(t, h.status(), "vscode"); strings.Contains(got.Detail, vscodeDeferred) {
		t.Errorf("vscode row = %s %q while the VS Code Server runs", got.Verdict, got.Detail)
	}

	bare := newLinuxHarness(t)
	if err := os.RemoveAll(bare.path(".vscode-server")); err != nil {
		t.Fatal(err)
	}
	absent := row(t, bare.on(), "vscode")
	if absent.Verdict != VerdictAbsent || !strings.Contains(absent.Detail, bare.path(linuxServerVscode)) ||
		!strings.Contains(absent.Detail, bare.path(".config/Code/User/settings.json")) ||
		strings.Contains(absent.Detail, "Library") {
		t.Errorf(
			"vscode row with no settings = %s %q, want ABSENT naming the Linux paths",
			absent.Verdict,
			absent.Detail,
		)
	}
	if !strings.Contains(absent.Detail, "no VS Code Server at "+bare.path(".vscode-server")) {
		t.Errorf(
			"vscode row with no settings = %q, want it naming the VS Code Server roots it looked for",
			absent.Detail,
		)
	}
}

// A Remote-SSH host whose VS Code Server never saved a Machine setting has
// ~/.vscode-server but no settings.json at all: the extension there still
// reads the contract and acks it, so the row judges that ack, never ABSENT.
func TestLinuxVscodeServerWithoutMachineSettingsIsPresent(t *testing.T) {
	h := newLinuxHarness(t)
	if err := os.Remove(h.path(linuxServerVscode)); err != nil {
		t.Fatal(err)
	}
	h.rearm()
	if got := row(t, h.on(), "vscode"); got.Verdict == VerdictAbsent {
		t.Errorf("on: vscode row = %s %q with ~/.vscode-server present", got.Verdict, got.Detail)
	}
	if got := row(t, h.status(), "vscode"); got.Verdict == VerdictAbsent {
		t.Errorf("status: vscode row = %s %q with ~/.vscode-server present", got.Verdict, got.Detail)
	}
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	if got := row(t, off, "vscode"); got.Verdict == VerdictAbsent {
		t.Errorf("off: vscode row = %s %q with ~/.vscode-server present", got.Verdict, got.Detail)
	}
}
