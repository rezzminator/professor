package themesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	// fixtureHibiscus is a custom Claude theme: base dark, Tokyo Night overrides.
	fixtureHibiscus = `{"name":"Hibiscus Night","base":"dark","overrides":{"claude":"#7aa2f7",` +
		`"inverseText":"#1a1b26","userMessageBackground":"#1f2335","selectionBg":"#283457","subtle":"#292e42",` +
		`"inactive":"#565f89","inactiveShimmer":"#7b84af","text":"#c0caf5","permission":"#bb9af7","success":"#9ece6a",` +
		`"red_FOR_SUBAGENTS_ONLY":"#f7768e","green_FOR_SUBAGENTS_ONLY":"#9ece6a","yellow_FOR_SUBAGENTS_ONLY":"#e0af68",` +
		`"blue_FOR_SUBAGENTS_ONLY":"#7aa2f7","purple_FOR_SUBAGENTS_ONLY":"#bb9af7","cyan_FOR_SUBAGENTS_ONLY":"#7dcfff",` +
		`"orange_FOR_SUBAGENTS_ONLY":"#ec937b"}}` + "\n"
	fixtureHerdr = "onboarding = false\n\n[theme]\nname = \"solarized\"\nauto_switch = false\n\n" +
		"# VS Code terminal shortcuts\n[keys]\nnew_tab = [\"prefix+c\"]\n"
	fixtureVscode = "{\n  \"workbench.colorTheme\": \"Sonar\"\n}\n"
	herdrBinary   = "/opt/bin/herdr"
	settingsRel   = ".claude/settings.json"
	hibiscusRel   = ".claude/themes/hibiscus-night.json"

	herdrRegistryRel = ".config/herdr/plugins.json"
	herdrManifestRel = ".config/herdr/pfm-indicators/herdr-plugin.toml"
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
	herdrLink          deps.RunResult
	herdrUnlink        deps.RunResult
	psVscode           string
	herdrOnPath        bool
	runner             *deps.FakeRunner
	launchctlPrintFail bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("HERDR_CONFIG_PATH", "")
	if err := os.Unsetenv("HERDR_CONFIG_PATH"); err != nil {
		t.Fatal(err)
	}
	harness := &harness{
		t:             t,
		home:          home,
		stateDir:      DefaultStateDir(home),
		tmux:          newFakeTmux("cc-a", "cc-b"),
		appearanceOut: "Dark",
		herdrOnPath:   true,
		herdrReload: deps.RunResult{
			Stdout: []byte(`{"result":{"type":"config_reload","status":"applied","diagnostics":[]}}`),
		},
	}
	harness.write(hibiscusRel, fixtureHibiscus)
	harness.write(settingsRel, `{"theme":"custom:hibiscus-night","model":"opus"}`)
	harness.write(".config/herdr/config.toml", fixtureHerdr)
	harness.write(herdrRegistryRel, `[{"plugin_id":"professor.indicators","name":"x"}]`)
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
		runner.Script([]string{herdrBinary, "plugin", "link"}, h.herdrLink, nil)
		runner.Script([]string{herdrBinary, "plugin", "unlink"}, h.herdrUnlink, nil)
	}
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
	h.env = Env{Home: h.home, StateDir: h.stateDir, PfmBinary: "/opt/bin/pfm", Runner: runner, Tmux: h.tmux}
}

func (h *harness) on() Report {
	h.t.Helper()
	report, err := On(context.Background(), h.env)
	if err != nil {
		h.t.Fatalf("On: %v", err)
	}
	return report
}

func (h *harness) sync(force bool) Report {
	h.t.Helper()
	report, err := Sync(context.Background(), h.env, force)
	if err != nil {
		h.t.Fatalf("Sync: %v", err)
	}
	return report
}

func (h *harness) status() Report {
	h.t.Helper()
	report, err := ReadBack(context.Background(), h.env)
	if err != nil {
		h.t.Fatalf("ReadBack: %v", err)
	}
	return report
}

func (h *harness) state() themeState {
	h.t.Helper()
	state, _, err := stateStore{dir: h.stateDir}.load()
	if err != nil {
		h.t.Fatal(err)
	}
	return state
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

func TestOnReadBackOffRoundTrip(t *testing.T) {
	h := newHarness(t)
	claudeBefore := h.read(settingsRel) + h.read(hibiscusRel)
	herdrBefore, vscodeBefore := h.read(
		".config/herdr/config.toml",
	), h.read(
		"Library/Application Support/Code/User/settings.json",
	)

	report := h.on()
	if !report.Enabled || report.Generation != 1 || !report.Changed || report.Servers != 2 {
		t.Fatalf("On = enabled %v gen %d changed %v servers %d, want enabled gen 1 changed 2 servers",
			report.Enabled, report.Generation, report.Changed, report.Servers)
	}
	if report.Source == nil || report.Source.Value != "custom:hibiscus-night" ||
		report.Source.Title != "Hibiscus Night" || report.Source.Side != Dark {
		t.Fatalf("On source = %+v, want custom:hibiscus-night titled Hibiscus Night", report.Source)
	}
	wantVerdict(t, report, "claude", VerdictSource, "custom:hibiscus-night · Hibiscus Night")
	wantVerdict(t, report, "tmux", VerdictInSync, "2 servers")
	for _, surface := range []string{"herdr", "follower"} {
		wantVerdict(t, report, surface, VerdictInSync, "")
	}
	wantVerdict(t, report, "vscode", VerdictPending, "applies when VS Code starts")
	if !h.state().Enabled || !h.exists("Library/LaunchAgents/com.professor.pfm.theme.plist") {
		t.Fatal("On wrote no enabled state or no follower plist")
	}
	for _, socket := range []string{"cc-a", "cc-b"} {
		got := h.tmux.servers[socket]
		for name, want := range map[string]string{
			"@pfm-c-bg": "#1a1b26", "@pfm-state-working": "#7aa2f7", "@pfm-state-blocked": "#e0af68",
			"@pfm-state-done": "#9ece6a", "@pfm-state-idle": "#7b84af", "@pfm-theme": "custom:hibiscus-night",
		} {
			if got[name] != want {
				t.Errorf("%s %s = %q, want %q", socket, name, got[name], want)
			}
		}
	}
	status := h.status()
	wantVerdict(t, status, "tmux", VerdictInSync, "2 servers")
	wantVerdict(t, status, "herdr", VerdictInSync, "")
	wantVerdict(t, status, "vscode", VerdictPending, "applies when VS Code starts")
	h.ackVscode(1)
	wantVerdict(t, h.status(), "vscode", VerdictInSync, "")

	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	if !off.OK() || off.Enabled {
		t.Fatalf("Off = %+v, want every surface restored", off)
	}
	wantVerdict(t, off, "claude", VerdictSource, "never written")
	if got := h.read(settingsRel) + h.read(hibiscusRel); got != claudeBefore {
		t.Error("the Claude settings or theme file changed across on and off")
	}
	if h.read(".config/herdr/config.toml") != herdrBefore {
		t.Error("herdr config.toml is not byte-identical after off")
	}
	if got := h.tmux.servers["cc-a"]; got["status-style"] != "bg=green,fg=black" || got["@pfm-theme"] != "" {
		t.Errorf("tmux cc-a after off = %v, want the prior styles and no @pfm-* options", got)
	}
	if h.exists("Library/LaunchAgents/com.professor.pfm.theme.plist") {
		t.Error("off left the follower plist")
	}
	var contract vscodeContract
	if err := json.Unmarshal(
		[]byte(h.read(".local/state/pfm/theme/current.json")),
		&contract,
	); err != nil ||
		contract.Enabled {
		t.Errorf("current.json after off = %+v (%v), want disabled", contract, err)
	}
	if h.read("Library/Application Support/Code/User/settings.json") != vscodeBefore {
		t.Error("pfm edited the VS Code settings.json")
	}
}

func TestSyncWithNothingChangedWritesAndRunsNothing(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.ackVscode(1)
	before, err := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	applies := h.tmux.applyCalls
	report := h.sync(false)
	if report.Changed || !report.OK() || report.Generation != 1 {
		t.Fatalf(
			"Sync with nothing changed = changed %v ok %v gen %d, want a silent gen 1",
			report.Changed,
			report.OK(),
			report.Generation,
		)
	}
	after, _ := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if !os.SameFile(before, after) || h.tmux.applyCalls != applies {
		t.Error("a sync that changed nothing wrote state.json or applied a tmux server")
	}
}

func TestSyncFollowsASourceChangeAndBumpsOneGeneration(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.write(settingsRel, `{"theme":"light"}`)
	report := h.sync(false)
	if report.Generation != 2 || !report.Changed || report.Appearance != Light || report.Source.Value != "light" {
		t.Fatalf(
			"Sync = gen %d changed %v appearance %s source %+v, want gen 2 light",
			report.Generation,
			report.Changed,
			report.Appearance,
			report.Source,
		)
	}
	if got := h.tmux.servers["cc-a"]["@pfm-c-bg"]; got != "#ffffff" {
		t.Errorf("tmux @pfm-c-bg = %q, want the light theme's #ffffff", got)
	}
	if block := h.read(".config/herdr/config.toml"); !strings.Contains(block, `name = "tokyo-night-day"`) ||
		!strings.Contains(block, `panel_bg = "#ffffff"`) || !strings.Contains(block, "gen 2 >>>") {
		t.Errorf("herdr config not at the light theme, gen 2:\n%s", block)
	}
	if !strings.Contains(h.read(".local/state/pfm/theme/current.json"), `"generation": 2`) {
		t.Error("the VS Code contract is not at generation 2")
	}
}

func TestAutoSourceFollowsTheSystemAppearance(t *testing.T) {
	h := newHarness(t)
	h.write(settingsRel, `{"theme":"auto"}`)
	h.on()
	herdr := h.read(".config/herdr/config.toml")
	if !strings.Contains(herdr, "auto_switch = true") || !strings.Contains(herdr, "[theme.custom.light]") ||
		h.tmux.servers["cc-a"]["@pfm-c-bg"] != "#000000" {
		t.Fatalf("auto under a dark system is not herdr auto plus tmux dark:\n%s", herdr)
	}
	h.appearanceOut = ""
	h.rearm()
	report := h.sync(false)
	if report.Generation != 2 || report.Appearance != Light || h.tmux.servers["cc-a"]["@pfm-c-bg"] != "#ffffff" {
		t.Fatalf("a system flip gave gen %d appearance %s tmux bg %q, want gen 2 light #ffffff",
			report.Generation, report.Appearance, h.tmux.servers["cc-a"]["@pfm-c-bg"])
	}
}

func TestSourceErrorNamesTheFileAndWritesNoSurface(t *testing.T) {
	h := newHarness(t)
	h.write(settingsRel, `{"theme":"custom:missing"}`)
	herdrBefore := h.read(".config/herdr/config.toml")
	report := h.on()
	want := "custom theme missing: open " + h.path(".claude/themes/missing.json") + ": no such file or directory"
	if len(report.Surfaces) != 1 || report.OK() {
		t.Fatalf("On with a missing custom theme = %+v, want only the failed claude row", report.Surfaces)
	}
	wantVerdict(t, report, "claude", VerdictError, want)
	if h.exists(".local/state/pfm/theme/state.json") || h.read(".config/herdr/config.toml") != herdrBefore ||
		h.tmux.applyCalls != 0 {
		t.Error("a source error wrote the state, a surface or a tmux server")
	}

	h.write(settingsRel, `{"theme":"dark"}`)
	h.on()
	h.write(settingsRel, `{"theme":`)
	status := h.status()
	wantVerdict(t, status, "claude", VerdictError, h.path(settingsRel))
	wantVerdict(t, status, "tmux", VerdictInSync, "")
	synced := h.sync(false)
	if synced.OK() || synced.Generation != 1 || synced.Changed {
		t.Errorf("Sync over an unparsable settings.json = ok %v gen %d changed %v, want a failed, unapplied gen 1",
			synced.OK(), synced.Generation, synced.Changed)
	}
}

// scriptedClock returns the scripted instants in order, then the last one.
type scriptedClock struct {
	clock.Clock
	times []time.Time
	next  int
}

func (c *scriptedClock) Now() time.Time {
	at := c.times[min(c.next, len(c.times)-1)]
	c.next++
	return at
}

func TestLastFlipRecordsEverySurfaceOffsetFromTheSourceMtime(t *testing.T) {
	h := newHarness(t)
	origin := time.Date(2026, 10, 16, 21, 0, 0, 0, time.UTC)
	for _, rel := range []string{settingsRel, hibiscusRel} {
		if err := os.Chtimes(h.path(rel), origin.Add(-time.Hour), origin.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	previous := flipClock
	flipClock = &scriptedClock{times: []time.Time{origin.Add(72 * time.Hour)}}
	t.Cleanup(func() { flipClock = previous })
	// A first On applies a theme nothing was showing before: it is no flip,
	// and the source's hour-old mtime is no latency.
	h.on()
	if flip := h.state().LastFlip; flip != nil {
		t.Fatalf("a first On recorded lastFlip %+v, want none", flip)
	}

	h.write(settingsRel, `{"theme":"light"}`)
	if err := os.Chtimes(h.path(settingsRel), origin, origin); err != nil {
		t.Fatal(err)
	}
	flipClock = &scriptedClock{times: []time.Time{
		origin.Add(410 * time.Millisecond), origin.Add(630 * time.Millisecond), origin.Add(640 * time.Millisecond),
	}}
	h.sync(false)
	flip := h.state().LastFlip
	if flip == nil || flip.Tmux != 0.41 || flip.Herdr != 0.63 || flip.VSCode != 0.64 ||
		flip.Source != h.path(settingsRel) || flip.At != origin.Format(time.RFC3339Nano) {
		t.Fatalf(
			"lastFlip = %+v, want tmux 0.41 herdr 0.63 vscode 0.64 from %s at %s",
			flip,
			h.path(settingsRel),
			origin,
		)
	}
	h.write(".local/state/pfm/theme/vscode-ack.json",
		`{"generation":2,"appliedAt":"2026-10-16T21:00:01.2Z","keys":[],"error":""}`)
	if got := h.status().LastFlip; got == nil || got.Ack == nil || *got.Ack != 1.2 {
		t.Fatalf("status lastFlip = %+v, want the ack at +1.2 s", got)
	}
}

func TestApplyTmuxServerIsInertUntilOnThenThemesOneSocket(t *testing.T) {
	h := newHarness(t)
	h.tmux.addServer("cc-born")
	if err := ApplyTmuxServer(
		context.Background(),
		h.tmux,
		h.stateDir,
		"cc-born",
	); err != nil ||
		h.tmux.applyCalls != 0 {
		t.Fatalf("ApplyTmuxServer before on = %v, %d applies, want a no-op", err, h.tmux.applyCalls)
	}
	h.on()
	h.tmux.addServer("cc-late")
	if err := ApplyTmuxServer(context.Background(), h.tmux, h.stateDir, "cc-late"); err != nil {
		t.Fatal(err)
	}
	if got := h.tmux.servers["cc-late"]; got["@pfm-theme"] != "custom:hibiscus-night" ||
		got["@pfm-c-bg"] != "#1a1b26" ||
		got["@pfm-theme-generation"] != "1" {
		t.Errorf("a server born after on = %v, want the applied theme at generation 1 with no source read", got)
	}
}

func TestSurfaceFailuresNameThemselvesAndNeverReadInSync(t *testing.T) {
	h := newHarness(t)
	h.tmux.failApply["cc-b"] = errors.New("lost connection")
	h.herdrReload = deps.RunResult{ExitCode: 2, Stderr: []byte("bad config")}
	h.rearm()
	report := h.on()
	wantVerdict(t, report, "tmux", VerdictError, "1 of 2 servers failed")
	wantVerdict(t, report, "herdr", VerdictError, "bad config")
	if got := h.tmux.servers["cc-a"]["@pfm-theme"]; got != "custom:hibiscus-night" {
		t.Errorf("one dead tmux server stopped the others: cc-a @pfm-theme = %q", got)
	}
	if report.OK() {
		t.Error("a report with failed surfaces is OK")
	}
	h.write("Library/Application Support/Code/User/settings.json", "{}")
	if err := os.Remove(h.path("Library/Application Support/Code/User/settings.json")); err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, h.status(), "vscode", VerdictAbsent, "no VS Code settings.json")
}

func TestOffAndReadBackWhenNeverOnDoNothing(t *testing.T) {
	h := newHarness(t)
	if report, err := Off(context.Background(), h.env); err != nil || len(report.Surfaces) != 0 {
		t.Fatalf("Off never on = %+v, %v, want an empty report", report, err)
	}
	if report := h.status(); report.Enabled || len(report.Surfaces) != 0 {
		t.Fatalf("ReadBack never on = %+v, want an empty report", report)
	}
	if report := h.sync(false); report.Enabled || h.exists(".local/state/pfm/theme/state.json") {
		t.Fatalf("Sync never on = %+v, want it inert", report)
	}
}

func TestLogOnceDedupesTheSameLineUntilCleared(t *testing.T) {
	h := newHarness(t)
	h.on()
	steps := []struct {
		line  string
		fresh bool
	}{{"boom", true}, {"boom", false}, {"other", true}, {"", true}, {"", false}, {"boom", true}}
	for _, step := range steps {
		fresh, err := LogOnce(h.env, step.line)
		if err != nil || fresh != step.fresh {
			t.Fatalf("LogOnce(%q) = %v, %v, want %v", step.line, fresh, err, step.fresh)
		}
	}
	if h.state().LastLog != "boom" {
		t.Errorf("lastLog = %q, want boom", h.state().LastLog)
	}
}

func TestVscodeRowFollowsTheAckAndTheProcessTable(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.psVscode = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n"
	h.rearm()
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(h.path(".local/state/pfm/theme/current.json"), old, old); err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, h.status(), "vscode", VerdictPending, "reload a VS Code window once")
	h.write(
		".local/state/pfm/theme/vscode-ack.json",
		`{"generation":1,"appliedAt":"x","keys":[],"error":"cannot write settings"}`,
	)
	wantVerdict(t, h.status(), "vscode", VerdictError, "cannot write settings")
	h.ackVscode(1)
	wantVerdict(t, h.status(), "vscode", VerdictInSync, "")
}

func TestFollowerRowReportsAnUnloadedAgentAsDrift(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.launchctlPrintFail = true
	h.rearm()
	wantVerdict(t, h.status(), "follower", VerdictDrift, "not loaded")
}

func TestStatusShowsASourceChangeAsDriftUntilSynced(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.write(settingsRel, `{"theme":"light"}`)
	status := h.status()
	wantVerdict(t, status, "tmux", VerdictDrift, "status-style")
	wantVerdict(t, status, "claude", VerdictSource, "light")
	if h.state().Generation != 1 {
		t.Error("status persisted a generation bump")
	}
}

func TestAnOldShapedEnabledStateIsAppliedByTheNextSync(t *testing.T) {
	h := newHarness(t)
	h.write(
		".local/state/pfm/theme/state.json",
		`{"enabled":true,"palette":"tokyo-night","mode":"auto","generation":4}`,
	)
	report := h.sync(false)
	if !report.Changed || report.Generation != 5 || h.state().Applied == nil {
		t.Fatalf(
			"Sync over an old state = gen %d changed %v applied %v, want gen 5 applied",
			report.Generation,
			report.Changed,
			h.state().Applied,
		)
	}
}

// hibiscusChips is the hibiscus theme's dark host-chip render, the values the follower
// paints over the user's own @host_accent and @p_* options.
var hibiscusChips = map[string]string{
	"@host_accent": "#7aa2f7", "@p_g0": "#1f2335", "@p_g3": "#1a1b26", "@p_fg": "#c0caf5",
	"@p_fg1": "#565f89", "@p_fg2": "#565f89", "@p_red": "#f7768e", "@p_amber": "#e0af68",
}

// userChips are the host-chip values a user's tmux config sets: @p_red is not
// among them, so the theme's paint of it has no prior value to return to.
var userChips = map[string]string{
	"@host_accent": "#00e5ff", "@p_g0": "#0e1c20", "@p_g3": "#1d3238", "@p_fg": "#e6f1f3", "@p_fg1": "#9fb6bb",
}

func copyOptions(options map[string]string) map[string]string {
	out := make(map[string]string, len(options))
	for name, value := range options {
		out[name] = value
	}
	return out
}

func wantChips(t *testing.T, label string, options map[string]string) {
	t.Helper()
	for name, want := range hibiscusChips {
		if got := options[name]; got != want {
			t.Errorf("%s: %s = %q, want the render's %q", label, name, got, want)
		}
	}
}

func TestChipsAreThemedOnAndRestoredExactlyOff(t *testing.T) {
	h := newHarness(t)
	for name, value := range userChips {
		h.tmux.servers["cc-a"][name] = value
	}
	original := copyOptions(h.tmux.servers["cc-a"])

	h.on()
	wantChips(t, "cc-a after on", h.tmux.servers["cc-a"])
	wantChips(t, "cc-b after on", h.tmux.servers["cc-b"])
	if got := h.tmux.servers["cc-a"]["@pfm-theme-prior-@host_accent"]; got != "v:#00e5ff" {
		t.Errorf("host accent prior = %q, want v:#00e5ff", got)
	}
	if got := h.tmux.servers["cc-a"]["@pfm-theme-prior-@p_red"]; got != "u:" {
		t.Errorf("unset @p_red prior = %q, want the unset sentinel u:", got)
	}

	if off, err := Off(context.Background(), h.env); err != nil || !off.OK() {
		t.Fatalf("Off = %+v, %v, want every surface restored", off.Surfaces, err)
	}
	if got := h.tmux.servers["cc-a"]; !reflect.DeepEqual(got, original) {
		t.Errorf("cc-a after off = %v, want its original %v (no @p_red, no @pfm-* left)", got, original)
	}
	for name := range h.tmux.servers["cc-b"] {
		if strings.HasPrefix(name, "@") {
			t.Errorf("cc-b kept %s after off, want a server that carried no chip options clean", name)
		}
	}
}

func TestAServerThemedBeforeChipsGetsThemCapturedOnTheNextSync(t *testing.T) {
	h := newHarness(t)
	original := copyOptions(h.tmux.servers["cc-a"])
	for name, value := range userChips {
		original[name] = value
	}
	h.on()
	// Shape cc-a as the previous follower left it: styles themed with their
	// priors and the generation marker, no chip marker, chips at the user values.
	legacy := h.tmux.servers["cc-a"]
	for name := range hibiscusChips {
		delete(legacy, name)
		delete(legacy, "@pfm-theme-prior-"+name)
	}
	delete(legacy, "@pfm-theme-chips")
	for name, value := range userChips {
		legacy[name] = value
	}

	h.sync(false)
	wantChips(t, "cc-a after the sync", legacy)
	if got := legacy["@pfm-theme-prior-@host_accent"]; got != "v:#00e5ff" {
		t.Errorf("host accent prior after the sync = %q, want v:#00e5ff", got)
	}
	if got := legacy["@pfm-theme-prior-@p_red"]; got != "u:" {
		t.Errorf("@p_red prior after the sync = %q, want u:", got)
	}
	if got := legacy["@pfm-theme-prior-status-style"]; got != "v:bg=green,fg=black" {
		t.Errorf("the sync recaptured status-style's prior as %q, want v:bg=green,fg=black", got)
	}

	if off, err := Off(context.Background(), h.env); err != nil || !off.OK() {
		t.Fatalf("Off = %+v, %v, want every surface restored", off.Surfaces, err)
	}
	if got := h.tmux.servers["cc-a"]; !reflect.DeepEqual(got, original) {
		t.Errorf("cc-a after off = %v, want its original %v", got, original)
	}
}

func TestAConfigResourceResetsAChipAndTheSyncHealsItKeepingThePrior(t *testing.T) {
	h := newHarness(t)
	for name, value := range userChips {
		h.tmux.servers["cc-a"][name] = value
	}
	h.on()
	wantVerdict(t, h.status(), "tmux", VerdictInSync, "2 servers")

	h.tmux.servers["cc-a"]["@host_accent"] = "#00e5ff" // a user `tmux source-file`
	wantVerdict(t, h.status(), "tmux", VerdictDrift, "@host_accent")
	report := h.sync(false)
	wantVerdict(t, report, "tmux", VerdictInSync, "2 servers")
	if got := h.tmux.servers["cc-a"]["@host_accent"]; got != "#7aa2f7" {
		t.Errorf("@host_accent after the sync = %q, want the accent #7aa2f7", got)
	}
	if got := h.tmux.servers["cc-a"]["@pfm-theme-prior-@host_accent"]; got != "v:#00e5ff" {
		t.Errorf("host accent prior after the heal = %q, want v:#00e5ff kept", got)
	}
}
