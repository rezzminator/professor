package themesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	fixtureBase = `{"name":"Tokyo Night","base":"dark","overrides":{"claude":"#c95cff","text":"#ff9ed6","error":"#ff3d6e",` +
		`"diffAdded":"#0d4035","selectionBg":"#3a2d6b","promptBorder":"#7c4dff","promptBorderShimmer":"#aa8bff"}}` + "\n"
	fixtureGold = `{"name":"Professor Gold","base":"dark","overrides":{"claude":"#ffd60a","text":"#fff0b3","error":"#ff3d6e",` +
		`"diffAdded":"#0d4035","selectionBg":"#5a4a00","promptBorder":"#ffd60a","promptBorderShimmer":"#fff7c2"}}` + "\n"
	fixtureHerdr = "onboarding = false\n\n[theme]\nname = \"solarized\"\nauto_switch = false\n\n" +
		"# VS Code terminal shortcuts\n[keys]\nnew_tab = [\"prefix+c\"]\n"
	fixtureGhostty = "font-size = 13\ntheme = Dracula" // no trailing newline on purpose
	fixtureVscode  = "{\n  \"workbench.colorTheme\": \"Sonar\"\n}\n"
	ghosttyBinary  = "/Applications/Ghostty.app/Contents/MacOS/ghostty"
	herdrBinary    = "/opt/bin/herdr"
)

type fakeTmux struct {
	servers    map[string]map[string]string
	failShow   map[string]bool
	failApply  map[string]error
	applyCalls int
}

func newFakeTmux(sockets ...string) *fakeTmux {
	fake := &fakeTmux{
		servers:   map[string]map[string]string{},
		failShow:  map[string]bool{},
		failApply: map[string]error{},
	}
	for _, socket := range sockets {
		fake.addServer(socket)
	}
	return fake
}

func (fake *fakeTmux) addServer(socket string) {
	fake.servers[socket] = map[string]string{
		"status-style": "bg=green,fg=black", "window-status-style": "default", "window-status-current-style": "reverse",
		"pane-border-style": "default", "pane-active-border-style": "fg=green", "message-style": "bg=yellow,fg=black",
		"message-command-style": "bg=black,fg=yellow", "mode-style": "bg=yellow,fg=black",
	}
}

func (fake *fakeTmux) Sockets(context.Context) ([]string, error) {
	var sockets []string
	for socket := range fake.servers {
		sockets = append(sockets, socket)
	}
	sort.Strings(sockets)
	return sockets, nil
}

func (fake *fakeTmux) Show(_ context.Context, socket, name string) (string, error) {
	options, ok := fake.servers[socket]
	if !ok || fake.failShow[socket] {
		return "", fmt.Errorf("tmux %s: server unreachable", socket)
	}
	value, ok := options[name]
	if !ok {
		return "", fmt.Errorf("invalid option: %s", name)
	}
	return value, nil
}

func (fake *fakeTmux) Apply(_ context.Context, socket string, commands [][]string) error {
	fake.applyCalls++
	if err := fake.failApply[socket]; err != nil {
		return err
	}
	options, ok := fake.servers[socket]
	if !ok {
		return fmt.Errorf("tmux %s: server unreachable", socket)
	}
	for _, command := range commands {
		switch {
		case len(command) == 4 && command[0] == "set-option" && command[1] == "-g":
			options[command[2]] = command[3]
		case len(command) == 3 && command[0] == "set-option" && command[1] == "-gu":
			delete(options, command[2])
		default:
			return fmt.Errorf("fakeTmux: unexpected command %v", command)
		}
	}
	return nil
}

type harness struct {
	t        *testing.T
	home     string
	stateDir string
	tmux     *fakeTmux
	env      Env

	appearanceOut      string // "Dark" or "" (missing key)
	appearanceFail     string
	herdrReload        deps.RunResult
	validate           deps.RunResult
	psGhostty          string
	psVscode           string
	herdrOnPath        bool
	runner             *deps.FakeRunner
	launchctlPrintFail bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	harness := &harness{
		t: t, home: home, stateDir: DefaultStateDir(home), tmux: newFakeTmux("cc-a", "cc-b"),
		appearanceOut: "Dark", herdrOnPath: true, psGhostty: "  501 " + ghosttyBinary + "\n  777 /bin/zsh -l\n",
		herdrReload: deps.RunResult{Stdout: []byte(`{"result":{"status":"applied"}}`)},
	}
	harness.write(".claude/themes/tokyo-night.json", fixtureBase)
	harness.write(".claude/themes/professor-gold.json", fixtureGold)
	harness.write(".claude/settings.json", `{"theme":"custom:professor-gold"}`)
	harness.write(".config/herdr/config.toml", fixtureHerdr)
	harness.write("Library/Application Support/com.mitchellh.ghostty/config.ghostty", fixtureGhostty)
	harness.write("Library/Application Support/Code/User/settings.json", fixtureVscode)
	harness.rearm()
	return harness
}

func (h *harness) path(rel string) string { return filepath.Join(h.home, filepath.FromSlash(rel)) }

func (h *harness) write(rel, content string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(h.path(rel)), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.path(rel), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	content, err := os.ReadFile(h.path(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(content)
}

func (h *harness) exists(rel string) bool {
	_, err := os.Stat(h.path(rel))
	return err == nil
}

// rearm rebuilds the scripted runner from the harness's current knobs.
func (h *harness) rearm() {
	runner := new(deps.FakeRunner)
	defaults := []string{"defaults", "read", "-g", "AppleInterfaceStyle"}
	switch {
	case h.appearanceFail != "":
		runner.Script(defaults, deps.RunResult{ExitCode: 1, Stderr: []byte(h.appearanceFail)}, nil)
	case h.appearanceOut == "":
		runner.Script(
			defaults,
			deps.RunResult{ExitCode: 1, Stderr: []byte("... AppleInterfaceStyle does not exist\n")},
			nil,
		)
	default:
		runner.Script(defaults, deps.RunResult{Stdout: []byte(h.appearanceOut + "\n")}, nil)
	}
	if h.herdrOnPath {
		runner.ScriptLookPath("herdr", herdrBinary, nil)
		runner.Script([]string{herdrBinary, "server", "reload-config"}, h.herdrReload, nil)
	}
	runner.Script([]string{"ps", "-axo", "pid=,args="}, deps.RunResult{Stdout: []byte(h.psGhostty)}, nil)
	runner.Script([]string{"ps", "-o", "args=", "-p", "501"}, deps.RunResult{Stdout: []byte(ghosttyBinary + "\n")}, nil)
	runner.Script([]string{"ps", "-o", "comm=", "-p", "501"}, deps.RunResult{Stdout: []byte(ghosttyBinary + "\n")}, nil)
	runner.Script([]string{"kill", "-USR2", "501"}, deps.RunResult{}, nil)
	runner.Script([]string{ghosttyBinary, "+validate-config"}, h.validate, nil)
	runner.Script([]string{"ps", "-axo", "args="}, deps.RunResult{Stdout: []byte(h.psVscode)}, nil)
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	runner.Script([]string{"launchctl", "bootout"}, deps.RunResult{}, nil)
	runner.Script([]string{"launchctl", "bootstrap", domain}, deps.RunResult{}, nil)
	printResult := deps.RunResult{}
	if h.launchctlPrintFail {
		printResult.ExitCode = 113
	}
	runner.Script([]string{"launchctl", "print"}, printResult, nil)
	h.runner = runner
	h.env = Env{
		Home: h.home, StateDir: h.stateDir, PfmBinary: "/opt/bin/pfm", Runner: runner, Tmux: h.tmux,
		systemApps: h.path("SystemApplications"),
	}
}

func (h *harness) set(palette string, mode Mode) Report {
	h.t.Helper()
	report, err := Set(context.Background(), h.env, Request{Palette: palette, Mode: mode})
	if err != nil {
		h.t.Fatalf("Set(%s, %s): %v", palette, mode, err)
	}
	return report
}

func (h *harness) ackVscode(generation int) {
	h.t.Helper()
	ack := fmt.Sprintf(
		`{"generation":%d,"appliedAt":"2026-10-16T03:00:00Z","keys":["terminal.background"],"error":""}`,
		generation,
	)
	h.write(".local/state/pfm/theme/vscode-ack.json", ack)
}

func row(t *testing.T, report Report, surface string) SurfaceResult {
	t.Helper()
	for _, result := range report.Surfaces {
		if result.Surface == surface {
			return result
		}
	}
	t.Fatalf("report has no %q row: %+v", surface, report.Surfaces)
	return SurfaceResult{}
}

func wantVerdict(t *testing.T, report Report, surface string, want Verdict, detail string) {
	t.Helper()
	got := row(t, report, surface)
	if got.Verdict != want || !strings.Contains(got.Detail, detail) {
		t.Errorf("%s row = %s %q, want %s containing %q", surface, got.Verdict, got.Detail, want, detail)
	}
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, err := os.ReadFile(path)
		files[path] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSetReadBackOffRoundTripIsByteIdentical(t *testing.T) {
	h := newHarness(t)
	report := h.set("tokyo-night", ModeAuto)

	if !report.Enabled || report.Generation != 1 || report.Palette != "tokyo-night" || report.Appearance != Dark ||
		report.Mode != ModeAuto {
		t.Fatalf("report header = %+v", report)
	}
	wantVerdict(t, report, "claude", VerdictInSync, "")
	wantVerdict(t, report, "tmux", VerdictInSync, "2 servers")
	wantVerdict(t, report, "herdr", VerdictInSync, "")
	wantVerdict(t, report, "ghostty", VerdictInSync, "")
	wantVerdict(t, report, "vscode", VerdictPending, "applies when VS Code starts")
	wantVerdict(t, report, "follower", VerdictInSync, "")
	for _, surface := range []string{"claude", "tmux", "herdr", "ghostty"} {
		if got := row(t, report, surface).Generation; got != 1 {
			t.Errorf("%s generation = %d, want 1", surface, got)
		}
	}
	if !report.OK() {
		t.Fatalf("report not OK: %+v", report.Surfaces)
	}

	var gold claudeFile
	if err := json.Unmarshal([]byte(h.read(".claude/themes/professor-gold.json")), &gold); err != nil {
		t.Fatal(err)
	}
	if gold.Name != "Professor Gold · Tokyo Night" || gold.Overrides["promptBorder"] != "#ffd60a" ||
		gold.Overrides["text"] != tokyoNight(t).Dark.Foreground || len(gold.Overrides) != 7 {
		t.Fatalf("rendered gold = %+v", gold)
	}
	if h.read(".claude/themes/tokyo-night.json") != fixtureBase {
		t.Fatal("the installed base theme is not a professor-* file and must stay untouched")
	}
	if !strings.Contains(h.read(".config/herdr/config.toml"), "# >>> pfm theme · tokyo-night · gen 1 >>>") {
		t.Fatalf("herdr block missing:\n%s", h.read(".config/herdr/config.toml"))
	}
	ghosttyCfg := h.read("Library/Application Support/com.mitchellh.ghostty/config.ghostty")
	include := h.path("Library/Application Support/com.mitchellh.ghostty/pfm-theme.ghostty")
	if !strings.HasSuffix(ghosttyCfg, "config-file = ?"+include+"\n") ||
		!strings.HasPrefix(ghosttyCfg, fixtureGhostty) {
		t.Fatalf("ghostty config:\n%q", ghosttyCfg)
	}
	for _, rel := range []string{
		"Library/Application Support/com.mitchellh.ghostty/pfm-theme.ghostty",
		"Library/Application Support/com.mitchellh.ghostty/pfm-theme/tokyo-night-dark",
		"Library/Application Support/com.mitchellh.ghostty/pfm-theme/tokyo-night-light",
		"Library/LaunchAgents/com.professor.pfm.theme.plist",
		".local/state/pfm/theme/current.json", ".local/state/pfm/theme/state.json",
	} {
		if !h.exists(rel) {
			t.Errorf("%s was not written", rel)
		}
	}
	var contract vscodeContract
	if err := json.Unmarshal([]byte(h.read(".local/state/pfm/theme/current.json")), &contract); err != nil ||
		!contract.Enabled || contract.Generation != 1 || contract.Colors["terminal.background"] != "#1a1b26" {
		t.Fatalf("current.json = %+v, %v", contract, err)
	}
	for _, backup := range []string{
		"Library/Application Support/com.mitchellh.ghostty/config.ghostty.pfm-theme-",
		".config/herdr/config.toml.pfm-theme-",
		"Library/Application Support/Code/User/settings.json.pfm-theme-",
	} {
		matches, _ := filepath.Glob(h.path(backup) + "*")
		if len(matches) != 1 {
			t.Errorf("want exactly one backup %s*, got %v", backup, matches)
		}
	}
	for _, socket := range []string{"cc-a", "cc-b"} {
		options := h.tmux.servers[socket]
		if options["@pfm-theme"] != "tokyo-night" || options["@pfm-theme-generation"] != "1" ||
			options["status-style"] != "bg=#1f2335,fg=#c0caf5" || options["@pfm-theme-prior-status-style"] != "v:bg=green,fg=black" {
			t.Errorf("%s options = %v", socket, options)
		}
	}

	if !HoldsClaudeTheme(h.stateDir, h.path(".claude/themes/professor-gold.json")) ||
		HoldsClaudeTheme(h.stateDir, h.path(".claude/themes/tokyo-night.json")) ||
		HoldsClaudeTheme(h.stateDir, "/elsewhere/professor-gold.json") {
		t.Fatal("HoldsClaudeTheme must hold exactly the professor-* files the sync rendered")
	}

	h.ackVscode(1)
	readBack, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range readBack.Surfaces {
		if result.Verdict != VerdictInSync {
			t.Errorf("ReadBack %s = %s %q, want IN-SYNC", result.Surface, result.Verdict, result.Detail)
		}
	}

	// The agent-state hook sets @pfm-state on a chat's server while the sync is on.
	h.tmux.servers["cc-a"]["@pfm-state"] = "working"
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled {
		t.Fatal("Off report still enabled")
	}
	for _, surface := range []string{"claude", "tmux", "herdr", "ghostty", "follower"} {
		wantVerdict(t, off, surface, VerdictRestored, "")
	}
	wantVerdict(t, off, "vscode", VerdictApplied, "")
	for rel, want := range map[string]string{
		".claude/themes/professor-gold.json": fixtureGold, ".config/herdr/config.toml": fixtureHerdr,
		"Library/Application Support/com.mitchellh.ghostty/config.ghostty": fixtureGhostty,
	} {
		if got := h.read(rel); got != want {
			t.Errorf("%s after Off differs:\n%q\nwant\n%q", rel, got, want)
		}
	}
	for _, rel := range []string{
		"Library/Application Support/com.mitchellh.ghostty/pfm-theme.ghostty",
		"Library/Application Support/com.mitchellh.ghostty/pfm-theme",
		"Library/LaunchAgents/com.professor.pfm.theme.plist",
	} {
		if h.exists(rel) {
			t.Errorf("%s survived Off", rel)
		}
	}
	var offContract vscodeContract
	if err := json.Unmarshal([]byte(h.read(".local/state/pfm/theme/current.json")), &offContract); err != nil ||
		offContract.Enabled || offContract.Generation != 2 || len(offContract.Colors) != 0 {
		t.Fatalf("current.json after Off = %+v, %v", offContract, err)
	}
	if SyncOn(h.stateDir) || HoldsClaudeTheme(h.stateDir, h.path(".claude/themes/professor-gold.json")) {
		t.Fatal("sync still on after Off")
	}
	original := newFakeTmux("cc-a").servers["cc-a"]
	for _, socket := range []string{"cc-a", "cc-b"} {
		if fmt.Sprint(h.tmux.servers[socket]) != fmt.Sprint(original) {
			t.Errorf("%s after Off = %v, want the original %v", socket, h.tmux.servers[socket], original)
		}
	}
	backups, _ := filepath.Glob(h.path("Library/Application Support/Code/User/settings.json.pfm-theme-") + "*")
	if len(backups) != 1 || h.read("Library/Application Support/Code/User/settings.json") != fixtureVscode {
		t.Fatalf("VS Code settings or backup changed: %v", backups)
	}

	// A second off, with a chat server born after the first, changes no
	// server: only a server carrying the theme has styles to restore.
	h.tmux.addServer("cc-c")
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	for _, socket := range []string{"cc-a", "cc-b", "cc-c"} {
		if fmt.Sprint(h.tmux.servers[socket]) != fmt.Sprint(original) {
			t.Errorf("%s after a second Off = %v, want the original %v", socket, h.tmux.servers[socket], original)
		}
	}
}

func TestSyncWithNothingChangedWritesAndRunsNothing(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	h.ackVscode(1)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	before := snapshotFiles(t, h.home)
	for path := range before {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	applies, calls := h.tmux.applyCalls, len(h.runner.Calls())

	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Generation != 1 || !report.OK() {
		t.Fatalf("report = %+v", report)
	}
	for _, result := range report.Surfaces {
		if result.Verdict != VerdictInSync {
			t.Errorf("%s = %s %q", result.Surface, result.Verdict, result.Detail)
		}
	}
	if h.tmux.applyCalls != applies {
		t.Error("a no-op sync applied tmux options")
	}
	for _, call := range h.runner.Calls()[calls:] {
		if joined := strings.Join(call.Argv, " "); strings.Contains(joined, "reload-config") ||
			strings.Contains(
				joined,
				"USR2",
			) || strings.Contains(joined, "bootstrap") || strings.Contains(joined, "bootout") {
			t.Errorf("a no-op sync ran %q", joined)
		}
	}
	for path, content := range snapshotFiles(t, h.home) {
		if before[path] != content {
			t.Errorf("%s changed content", path)
		}
		if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(old) {
			t.Errorf("%s was rewritten (mtime %v, err %v)", path, info.ModTime(), err)
		}
	}
}

func TestSyncAfterAppearanceFlipBumpsGenerationEverywhere(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	h.appearanceOut = ""
	h.rearm()

	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Generation != 2 || report.Appearance != Light {
		t.Fatalf("report = %+v", report)
	}
	var gold claudeFile
	if err := json.Unmarshal([]byte(h.read(".claude/themes/professor-gold.json")), &gold); err != nil {
		t.Fatal(err)
	}
	if gold.Base != "light" || gold.Overrides["text"] != tokyoNight(t).Light.Foreground {
		t.Errorf("claude not re-rendered for light: %+v", gold)
	}
	var contract vscodeContract
	if err := json.Unmarshal([]byte(h.read(".local/state/pfm/theme/current.json")), &contract); err != nil ||
		contract.Generation != 2 || contract.Appearance != "light" || contract.Colors["terminal.background"] != "#e1e2e7" {
		t.Errorf("current.json = %+v, %v", contract, err)
	}
	if h.tmux.servers["cc-a"]["@pfm-theme-generation"] != "2" ||
		h.tmux.servers["cc-a"]["status-style"] != "bg=#d0d5e3,fg=#3760bf" {
		t.Errorf("tmux = %v", h.tmux.servers["cc-a"])
	}
	for _, surface := range []string{"claude", "tmux", "herdr", "ghostty"} {
		if got := row(t, report, surface); got.Generation != 2 || got.Verdict != VerdictInSync {
			t.Errorf("%s = %+v", surface, got)
		}
	}
	if !strings.Contains(h.read(".config/herdr/config.toml"), "gen 2 >>>") {
		t.Error("herdr block not restamped to generation 2")
	}
}

func TestForcedModeNeedsNoAppearanceAndNoFollower(t *testing.T) {
	h := newHarness(t)
	h.appearanceFail = "must not be asked"
	h.rearm()
	report := h.set("solarized", ModeLight)
	if report.Appearance != Light || report.Mode != ModeLight {
		t.Fatalf("report = %+v", report)
	}
	wantVerdict(t, report, "follower", VerdictAbsent, "mode light")
	if h.exists("Library/LaunchAgents/com.professor.pfm.theme.plist") {
		t.Fatal("a forced mode must not install the follower")
	}
	for _, call := range h.runner.Calls() {
		if call.Argv[0] == "defaults" {
			t.Fatal("a forced mode must not read the macOS appearance")
		}
	}
}

func TestSurfaceFailuresNameThemselvesAndNeverReadInSync(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(h *harness)
		surface string
		verdict Verdict
		detail  string
		wantOK  bool
	}{
		{
			"herdr reload fails",
			func(h *harness) { h.herdrReload = deps.RunResult{ExitCode: 1, Stderr: []byte("boom")} },
			"herdr",
			VerdictError,
			"boom",
			false,
		},
		{
			"herdr server not running", func(h *harness) {
				h.herdrReload = deps.RunResult{ExitCode: 1, Stderr: []byte("herdr: no server is running")}
			},
			"herdr", VerdictApplied, "applies when Herdr starts", true,
		},
		{
			"ghostty validate fails",
			func(h *harness) { h.validate = deps.RunResult{ExitCode: 1, Stdout: []byte("invalid theme")} },
			"ghostty",
			VerdictError,
			"invalid theme",
			false,
		},
		{
			"tmux socket fails",
			func(h *harness) { h.tmux.failApply["cc-b"] = errors.New("lost connection") },
			"tmux",
			VerdictError,
			"cc-b",
			false,
		},
		{
			"no ghostty process still applies",
			func(h *harness) { h.psGhostty = "  777 /bin/zsh -l\n" },
			"ghostty",
			VerdictApplied,
			"applies when Ghostty starts",
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.arrange(h)
			h.rearm()
			report := h.set("tokyo-night", ModeAuto)
			wantVerdict(t, report, tc.surface, tc.verdict, tc.detail)
			if report.OK() != tc.wantOK {
				t.Errorf("OK() = %v, want %v", report.OK(), tc.wantOK)
			}
			if tc.surface == "tmux" && h.tmux.servers["cc-a"]["@pfm-theme"] != "tokyo-night" {
				t.Error("one failing socket must not stop the others")
			}
			if tc.surface != "claude" {
				wantVerdict(t, report, "claude", VerdictInSync, "")
			}
		})
	}
}

func TestAbsentRowsNameWhatWasLookedFor(t *testing.T) {
	h := newHarness(t)
	for _, rel := range []string{
		".config/herdr/config.toml", "Library/Application Support/com.mitchellh.ghostty",
		"Library/Application Support/Code", ".claude/themes/professor-gold.json",
	} {
		if err := os.RemoveAll(h.path(rel)); err != nil {
			t.Fatal(err)
		}
	}
	h.tmux = newFakeTmux()
	h.herdrOnPath = false
	h.rearm()
	report := h.set("tokyo-night", ModeAuto)
	wantVerdict(t, report, "herdr", VerdictAbsent, ".config/herdr/config.toml")
	wantVerdict(t, report, "ghostty", VerdictAbsent, "com.mitchellh.ghostty/config.ghostty")
	wantVerdict(t, report, "vscode", VerdictAbsent, "Code/User/settings.json")
	wantVerdict(t, report, "tmux", VerdictAbsent, "no live pfm chat servers")
	wantVerdict(t, report, "claude", VerdictAbsent, "professor-*.json")
	if !report.OK() {
		t.Fatalf("absent surfaces are not failures: %+v", report.Surfaces)
	}
	off, err := Off(context.Background(), h.env)
	if err != nil || !off.OK() {
		t.Fatalf("Off over absent surfaces = %+v, %v", off, err)
	}
}

func TestUnresolvableAppearanceChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.appearanceFail = "boom"
	h.rearm()
	before := snapshotFiles(t, h.home)
	if _, err := Set(context.Background(), h.env, Request{Palette: "tokyo-night", Mode: ModeAuto}); err == nil ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("Set err = %v, want the appearance failure", err)
	}
	if after := snapshotFiles(t, h.home); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("a failed appearance read must leave every file as it was")
	}
	if _, err := Set(context.Background(), h.env, Request{Palette: "nord", Mode: ModeAuto}); err == nil {
		t.Fatal("an unknown palette must be an error")
	}
	if _, err := Set(context.Background(), h.env, Request{Palette: "tokyo-night", Mode: Mode("sepia")}); err == nil {
		t.Fatal("an unknown mode must be an error")
	}
}

func TestSyncHealsASurfaceThatFailedBefore(t *testing.T) {
	h := newHarness(t)
	h.validate = deps.RunResult{ExitCode: 1, Stdout: []byte("invalid theme")}
	h.rearm()
	wantVerdict(t, h.set("tokyo-night", ModeAuto), "ghostty", VerdictError, "invalid theme")
	h.validate = deps.RunResult{}
	h.rearm()
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "ghostty", VerdictInSync, "")
	if report.Generation != 1 {
		t.Fatalf("healing must keep generation 1, got %d", report.Generation)
	}
}

func TestSyncRepairsDriftedHerdrBlock(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	cfg := h.path(".config/herdr/config.toml")
	if err := os.WriteFile(
		cfg,
		[]byte(strings.Replace(h.read(".config/herdr/config.toml"), "#7aa2f7", "#000000", 1)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "herdr", VerdictDrift, "")
	if status.OK() {
		t.Fatal("a drifted surface must fail OK()")
	}
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "herdr", VerdictInSync, "")
}

func TestSetAnotherPaletteKeepsTheFirstSnapshot(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	report := h.set("solarized", ModeAuto)
	if report.Generation != 2 {
		t.Fatalf("generation = %d, want 2", report.Generation)
	}
	if !strings.Contains(h.read(".claude/themes/professor-gold.json"), "Professor Gold · Solarized") {
		t.Fatal("claude not re-rendered as Solarized")
	}
	if strings.Count(h.read(".config/herdr/config.toml"), ">>> pfm theme") != 1 {
		t.Fatal("herdr must hold exactly one pfm block")
	}
	entries, _ := os.ReadDir(h.path("Library/Application Support/com.mitchellh.ghostty/pfm-theme"))
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "solarized-dark,solarized-light" {
		t.Fatalf("stale palette files left behind: %v", names)
	}
	if h.tmux.servers["cc-a"]["@pfm-theme-prior-status-style"] != "v:bg=green,fg=black" {
		t.Fatalf("the prior was overwritten by the first theme: %v", h.tmux.servers["cc-a"])
	}
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	if h.read(".claude/themes/professor-gold.json") != fixtureGold ||
		h.read(".config/herdr/config.toml") != fixtureHerdr ||
		h.tmux.servers["cc-a"]["status-style"] != "bg=green,fg=black" {
		t.Fatal("Off after two sets must still restore the pre-first-set originals")
	}
}

func TestOffAndReadBackWhenNeverSetDoNothing(t *testing.T) {
	h := newHarness(t)
	before := snapshotFiles(t, h.home)
	off, err := Off(context.Background(), h.env)
	if err != nil || off.Enabled {
		t.Fatalf("Off = %+v, %v", off, err)
	}
	status, err := ReadBack(context.Background(), h.env)
	if err != nil || status.Enabled || len(status.Surfaces) != 0 {
		t.Fatalf("ReadBack = %+v, %v", status, err)
	}
	if fmt.Sprint(snapshotFiles(t, h.home)) != fmt.Sprint(before) || h.exists(".local/state/pfm/theme") {
		t.Fatal("an inert pfm theme must touch nothing")
	}
}

func TestApplyTmuxServerIsInertUntilSetThenThemesOneSocket(t *testing.T) {
	h := newHarness(t)
	h.tmux.addServer("cc-new")
	if err := ApplyTmuxServer(
		context.Background(),
		h.tmux,
		h.stateDir,
		"cc-new",
	); err != nil ||
		h.tmux.applyCalls != 0 {
		t.Fatalf("inert apply = %v, %d apply calls", err, h.tmux.applyCalls)
	}
	h.set("tokyo-night", ModeAuto)
	h.tmux.addServer("cc-late")
	if err := ApplyTmuxServer(context.Background(), h.tmux, h.stateDir, "cc-late"); err != nil {
		t.Fatal(err)
	}
	options := h.tmux.servers["cc-late"]
	if options["@pfm-theme"] != "tokyo-night" || options["@pfm-theme-prior-mode-style"] != "v:bg=yellow,fg=black" {
		t.Fatalf("cc-late = %v", options)
	}
	h.tmux.failShow["cc-late"] = true
	if err := ApplyTmuxServer(context.Background(), h.tmux, h.stateDir, "cc-late"); err == nil ||
		!strings.Contains(err.Error(), "cc-late") {
		t.Fatalf("a dead socket must be an error naming it, got %v", err)
	}
}

func TestClaudeAccountThemeSelection(t *testing.T) {
	h := newHarness(t)
	h.write(".cc/1/settings.json", `{"theme":"dark"}`)
	h.write(".cc/2/settings.json", `{"theme":"custom:professor-gold"}`)
	report := h.set("tokyo-night", ModeAuto)
	wantVerdict(t, report, "claude", VerdictInSync, ".cc/1 theme dark")

	if err := os.Remove(h.path(".cc/2/settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path(".claude/settings.json")); err != nil {
		t.Fatal(err)
	}
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "claude", VerdictDrift, ".cc/1 theme dark")
}

func TestClaudeEmbeddedTokenSetWhenTheBaseThemeIsMissing(t *testing.T) {
	h := newHarness(t)
	if err := os.Remove(h.path(".claude/themes/tokyo-night.json")); err != nil {
		t.Fatal(err)
	}
	h.set("tokyo-night", ModeAuto)
	var gold claudeFile
	if err := json.Unmarshal(
		[]byte(h.read(".claude/themes/professor-gold.json")),
		&gold,
	); err != nil ||
		len(gold.Overrides) != 47 {
		t.Fatalf("overrides = %d keys, %v; want the embedded 47", len(gold.Overrides), err)
	}
}

func TestVscodeRowFollowsTheAckAndTheProcessTable(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	current := h.path(".local/state/pfm/theme/current.json")
	minuteAgo := time.Now().Add(-time.Minute)
	if err := os.Chtimes(current, minuteAgo, minuteAgo); err != nil {
		t.Fatal(err)
	}
	h.psVscode = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n"
	h.rearm()
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "vscode", VerdictDrift, "no ack")
	h.write(
		".local/state/pfm/theme/vscode-ack.json",
		`{"generation":1,"appliedAt":"x","keys":[],"error":"cannot write settings"}`,
	)
	status, err = ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "vscode", VerdictError, "cannot write settings")
	h.ackVscode(1)
	status, err = ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "vscode", VerdictInSync, "")
}

func TestFollowerRowReportsAnUnloadedAgentAsDrift(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	h.launchctlPrintFail = true
	h.rearm()
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "follower", VerdictDrift, "not loaded")
}

// A user config kept as a symlink into a dotfiles checkout stays that symlink
// through set and off: the edit lands in the file it points to, and off puts
// that file's bytes back.
func TestSetAndOffWriteThroughASymlinkedUserConfig(t *testing.T) {
	h := newHarness(t)
	links := map[string]string{
		".config/herdr/config.toml":                                        "dotfiles/herdr.toml",
		"Library/Application Support/com.mitchellh.ghostty/config.ghostty": "dotfiles/ghostty",
	}
	originals := map[string]string{}
	for link, target := range links {
		originals[target] = h.read(link)
		h.write(target, originals[target])
		if err := os.Remove(h.path(link)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(h.path(target), h.path(link)); err != nil {
			t.Fatal(err)
		}
	}
	wantLinks := func(stage string) {
		t.Helper()
		for link, target := range links {
			info, err := os.Lstat(h.path(link))
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s: %s is no longer a symlink (err %v)", stage, link, err)
			}
			if dest, err := os.Readlink(h.path(link)); err != nil || dest != h.path(target) {
				t.Fatalf("%s: %s points at %q (err %v)", stage, link, dest, err)
			}
		}
	}

	report := h.set("tokyo-night", ModeAuto)
	wantVerdict(t, report, "herdr", VerdictInSync, "")
	wantVerdict(t, report, "ghostty", VerdictInSync, "")
	wantLinks("set")
	if !strings.Contains(h.read("dotfiles/herdr.toml"), "# >>> pfm theme") {
		t.Fatal("set did not edit the file the herdr link points at")
	}
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	wantLinks("off")
	for target, original := range originals {
		if got := h.read(target); got != original {
			t.Errorf("off left %s as %q, want the original %q", target, got, original)
		}
	}
}

// A surface file that exists but cannot be read is ERROR on read-back, never
// DRIFT: drift means the sync can rewrite it, an unreadable file means it cannot look.
func TestUnreadableSurfaceFileReadsBackAsError(t *testing.T) {
	for surface, rel := range map[string]string{
		"claude": ".claude/themes/professor-gold.json",
		"vscode": ".local/state/pfm/theme/current.json",
	} {
		t.Run(surface, func(t *testing.T) {
			h := newHarness(t)
			h.set("tokyo-night", ModeAuto)
			if err := os.Remove(h.path(rel)); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(h.path(rel), 0o755); err != nil {
				t.Fatal(err)
			}
			report, err := ReadBack(context.Background(), h.env)
			if err != nil {
				t.Fatal(err)
			}
			wantVerdict(t, report, surface, VerdictError, filepath.Base(rel))
		})
	}
}
