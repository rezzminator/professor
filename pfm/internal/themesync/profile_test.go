package themesync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// tokyoNightName is the seeded profile the harness's Claude theme file and
// settings.json name: its profile, when a test writes one, sits at ProfilePath.
const tokyoNightName = "tokyo-night"

// profileJSON is a profile file for tokyo-night carrying the given
// surfaces object; "" writes the palette alone.
func profileJSON(surfaces string) string {
	if surfaces == "" {
		return `{"name":"Tokyo Night · neon pink","base":"dark","overrides":{}}`
	}
	return `{"name":"Tokyo Night · neon pink","base":"dark","overrides":{},"surfaces":` + surfaces + `}`
}

// pinnedSurfaces pins one key or two on every surface; every other key keeps
// what derive fills.
const pinnedSurfaces = `{"tmux":{"@host_accent":"#ff2e93","status-style":"bg=#111111,fg=#eeeeee"},` +
	`"herdr":{"accent":"#ff2e93"},` +
	`"vscode":{"colors":{"terminal.background":"#101010"},"chrome":{"titleBar.activeBackground":"#2a0a1a"}}}`

func (h *harness) writeProfile(content string) string {
	h.t.Helper()
	path := ProfilePath(h.home, tokyoNightName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *harness) contract() vscodeContract {
	h.t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.stateDir, vscodeContractFile))
	if err != nil {
		h.t.Fatal(err)
	}
	var contract vscodeContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		h.t.Fatal(err)
	}
	return contract
}

func wantValues(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s %s = %q, want %q", label, name, got[name], value)
		}
	}
}

func TestAPinnedProfileTakesOverTheRenderedKeysAndDeriveKeepsTheRest(t *testing.T) {
	h := newHarness(t)
	h.writeProfile(profileJSON(pinnedSurfaces))
	h.on()

	for _, socket := range []string{"cc-a", "cc-b"} {
		wantValues(t, "tmux "+socket, h.tmux.servers[socket], map[string]string{
			"@host_accent": "#ff2e93", "status-style": "bg=#111111,fg=#eeeeee",
			"@p_g0": "#221634", "@pfm-c-accent": "#c95cff", "message-style": "bg=#4a3d6b,fg=#ff9ed6",
		})
	}
	config := h.read(".config/herdr/config.toml")
	if !strings.Contains(config, "\naccent = \"#ff2e93\"\n") || strings.Contains(config, "\naccent = \"#c95cff\"\n") {
		t.Errorf("herdr config lacks the pinned accent #ff2e93:\n%s", config)
	}
	if !strings.Contains(config, "\npanel_bg = \"#0f0f1a\"\n") {
		t.Errorf("herdr config lost the derived panel_bg #0f0f1a:\n%s", config)
	}
	contract := h.contract()
	wantValues(t, "vscode chrome", contract.Chrome, map[string]string{
		"titleBar.activeBackground": "#2a0a1a", "statusBar.background": "#221634",
	})
	wantValues(t, "vscode colors", contract.Colors, map[string]string{
		"terminal.background": "#101010", "terminal.foreground": "#ff9ed6",
	})
	if pins := h.state().Applied.Pins; pins == nil || pins.Tmux["@host_accent"] != "#ff2e93" {
		t.Errorf("state.json Applied.Pins = %+v, want the profile's pins persisted", pins)
	}
}

func TestReadBackJudgesTheSurfacesAgainstThePinnedRender(t *testing.T) {
	h := newHarness(t)
	h.writeProfile(profileJSON(pinnedSurfaces))
	h.on()
	h.ackVscode(1)
	status := h.status()
	wantVerdict(t, status, "tmux", VerdictInSync, "2 servers")
	wantVerdict(t, status, "herdr", VerdictInSync, "")
	wantVerdict(t, status, "vscode", VerdictInSync, "")

	// A server reset to derive's value is out of sync, and the sync heals it to the pin.
	h.tmux.servers["cc-a"]["@host_accent"] = "#c95cff"
	wantVerdict(t, h.status(), "tmux", VerdictDrift, "@host_accent")
	wantVerdict(t, h.sync(false), "tmux", VerdictInSync, "2 servers")
	wantValues(t, "healed cc-a", h.tmux.servers["cc-a"], map[string]string{"@host_accent": "#ff2e93"})

	config := h.read(".config/herdr/config.toml")
	h.write(".config/herdr/config.toml", strings.Replace(config, "\naccent = \"#ff2e93\"", "\naccent = \"#c95cff\"", 1))
	wantVerdict(t, h.status(), "herdr", VerdictDrift, "differs from the render")
	wantVerdict(t, h.sync(false), "herdr", VerdictInSync, "")
	if got := h.read(".config/herdr/config.toml"); !strings.Contains(got, "\naccent = \"#ff2e93\"\n") {
		t.Errorf("the sync did not put the pinned accent back:\n%s", got)
	}

	contractPath := filepath.Join(h.stateDir, vscodeContractFile)
	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	h.write(".local/state/pfm/theme/current.json", strings.Replace(string(raw), "#2a0a1a", "#221634", 1))
	wantVerdict(t, h.status(), "vscode", VerdictDrift, "differs from the render")
	h.sync(false)
	if got := h.contract().Chrome["titleBar.activeBackground"]; got != "#2a0a1a" {
		t.Errorf("the sync left titleBar.activeBackground at %q, want the pin #2a0a1a", got)
	}
}

func TestAPinChangeIsAChangedThemeAndAnUnchangedPinIsNot(t *testing.T) {
	h := newHarness(t)
	h.writeProfile(profileJSON(`{"tmux":{"@host_accent":"#ff2e93"}}`))
	if report := h.on(); report.Generation != 1 {
		t.Fatalf("On generation = %d, want 1", report.Generation)
	}
	for range 2 {
		if report := h.sync(false); report.Generation != 1 || report.Changed {
			t.Fatalf("a sync over an unchanged profile = generation %d changed %v, want generation 1 untouched",
				report.Generation, report.Changed)
		}
	}

	path := h.writeProfile(profileJSON(`{"tmux":{"@host_accent":"#00ffaa"}}`))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	report := h.sync(false)
	if report.Generation != 2 || !report.Changed {
		t.Fatalf("a sync after a pin change = generation %d changed %v, want generation 2 applied",
			report.Generation, report.Changed)
	}
	wantValues(
		t,
		"cc-a",
		h.tmux.servers["cc-a"],
		map[string]string{"@host_accent": "#00ffaa", "@pfm-theme-generation": "2"},
	)
	state := h.state()
	if state.Applied.Pins == nil || state.Applied.Pins.Tmux["@host_accent"] != "#00ffaa" {
		t.Errorf("state Applied.Pins = %+v, want the new pin", state.Applied.Pins)
	}
	if state.LastFlip == nil || state.LastFlip.Source != path {
		t.Errorf("last flip = %+v, want the profile file %s as its origin", state.LastFlip, path)
	}

	h.writeProfile(profileJSON(""))
	if report := h.sync(false); report.Generation != 3 {
		t.Errorf("a sync after the pins were removed = generation %d, want 3", report.Generation)
	}
	wantValues(t, "cc-a unpinned", h.tmux.servers["cc-a"], map[string]string{"@host_accent": "#c95cff"})
}

func TestAServerBornLaterRendersThePins(t *testing.T) {
	h := newHarness(t)
	h.writeProfile(profileJSON(pinnedSurfaces))
	h.on()
	h.tmux.addServer("cc-late")
	if err := ApplyTmuxServer(context.Background(), h.tmux, h.stateDir, "cc-late"); err != nil {
		t.Fatal(err)
	}
	wantValues(t, "cc-late", h.tmux.servers["cc-late"], map[string]string{
		"@host_accent": "#ff2e93", "status-style": "bg=#111111,fg=#eeeeee", "@p_g0": "#221634",
	})
}

func TestAnUnreadableProfileIsASourceErrorNamingTheFile(t *testing.T) {
	cases := []struct {
		name    string
		write   func(t *testing.T, path string)
		mention string
	}{
		{"not json", writeFileAt(`{not json`), "parse profile"},
		{"unknown top-level key", writeFileAt(`{"name":"x","overrides":{},"colours":{}}`), `"colours"`},
		{"unknown pin key", writeFileAt(profileJSON(`{"tmux":{"@nope":"#ffffff"}}`)), "@nope"},
		{"bad pin colour", writeFileAt(profileJSON(`{"herdr":{"accent":"purple"}}`)), "purple"},
		{"a directory", func(t *testing.T, path string) {
			t.Helper()
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			path := ProfilePath(h.home, tokyoNightName)
			tc.write(t, path)
			report := h.on()
			claude := row(t, report, "claude")
			if claude.Verdict != VerdictError || !strings.Contains(claude.Detail, path) ||
				!strings.Contains(claude.Detail, tc.mention) {
				t.Errorf(
					"claude row = %s %q, want ERROR naming %s and %q",
					claude.Verdict,
					claude.Detail,
					path,
					tc.mention,
				)
			}
			if report.OK() || len(report.Surfaces) != 1 {
				t.Errorf("report = %+v, want the source error alone, nothing applied", report.Surfaces)
			}
			if _, themed := h.tmux.servers["cc-a"]["@pfm-theme"]; themed {
				t.Error("a surface was written under a profile that could not be read")
			}
		})
	}
}

func writeFileAt(content string) func(t *testing.T, path string) {
	return func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAProfileBrokenAfterASyncStopsTheRunAndLeavesTheSurfacesAlone(t *testing.T) {
	h := newHarness(t)
	h.writeProfile(profileJSON(pinnedSurfaces))
	h.on()
	path := h.writeProfile(`{not json`)
	for label, report := range map[string]Report{"sync": h.sync(false), "status": h.status()} {
		claude := row(t, report, "claude")
		if claude.Verdict != VerdictError || !strings.Contains(claude.Detail, path) {
			t.Errorf("%s claude row = %s %q, want ERROR naming %s", label, claude.Verdict, claude.Detail, path)
		}
	}
	wantValues(t, "cc-a after the broken sync", h.tmux.servers["cc-a"], map[string]string{"@host_accent": "#ff2e93"})
}

func TestNoProfileFileRendersTheUnpinnedTheme(t *testing.T) {
	for name, profile := range map[string]string{"absent": "", "palette only": profileJSON("")} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if profile != "" {
				h.writeProfile(profile)
			}
			h.on()
			unpinned := tokyoNightApplied(t)
			if applied := h.state().Applied; applied == nil || !reflect.DeepEqual(*applied, unpinned) {
				t.Fatalf("state Applied = %+v, want the unpinned %+v", applied, unpinned)
			}
			for _, option := range tmuxOptions(unpinned, Dark, 1) {
				if got := h.tmux.servers["cc-a"][option.Name]; got != option.Value {
					t.Errorf("tmux %s = %q, want the unpinned %q", option.Name, got, option.Value)
				}
			}
			if config := h.read(".config/herdr/config.toml"); !strings.Contains(config, herdrRender(unpinned, 1)) {
				t.Errorf("herdr config lacks the unpinned block:\n%s", config)
			}
			if got, want := h.contract(), unpinned.vscodeContract(Dark, 1); !reflect.DeepEqual(got, want) {
				t.Errorf("contract = %+v, want the unpinned %+v", got, want)
			}
		})
	}
}

func TestPinsApplyToBothSidesOfAnAutoTheme(t *testing.T) {
	auto, err := resolveValue("auto", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	applied := auto.Applied
	applied.Pins = &SurfacePins{
		Tmux:   map[string]string{"@host_accent": "#ff2e93"},
		Herdr:  &HerdrPins{Tokens: map[string]string{"accent": "#ff2e93"}},
		VSCode: &VSCodePins{Chrome: map[string]string{"titleBar.activeBackground": "#2a0a1a"}},
	}

	block := herdrRender(applied, 1)
	dark, light, found := strings.Cut(block, "[theme.custom.light]")
	if !found || !strings.Contains(dark, "\naccent = \"#ff2e93\"\n") ||
		!strings.Contains(light, "\naccent = \"#ff2e93\"\n") {
		t.Errorf("an auto block pins the accent in %v tables, want both:\n%s", found, block)
	}
	if !strings.Contains(light, "panel_bg = \"#ffffff\"") {
		t.Errorf("the light table lost its derived panel_bg:\n%s", light)
	}
	for _, side := range []Appearance{Dark, Light} {
		options := map[string]string{}
		for _, option := range tmuxOptions(applied, side, 1) {
			options[option.Name] = option.Value
		}
		if options["@host_accent"] != "#ff2e93" {
			t.Errorf("tmux %s @host_accent = %q, want the pin", side, options["@host_accent"])
		}
		contract := applied.vscodeContract(side, 1)
		if contract.Chrome["titleBar.activeBackground"] != "#2a0a1a" {
			t.Errorf(
				"vscode %s titleBar.activeBackground = %q, want the pin",
				side,
				contract.Chrome["titleBar.activeBackground"],
			)
		}
	}
	if got := applied.vscodeContract(Light, 1).Colors["terminal.background"]; got != "#ffffff" {
		t.Errorf("light terminal.background = %q, want derive's #ffffff", got)
	}
}

func TestParseProfileIsStrictAndChecksItsPins(t *testing.T) {
	profile, err := ParseProfile([]byte(profileJSON(pinnedSurfaces)))
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "Tokyo Night · neon pink" || profile.Base != "dark" || profile.Surfaces == nil ||
		profile.Surfaces.Tmux["@host_accent"] != "#ff2e93" ||
		profile.Surfaces.VSCode.Chrome["titleBar.activeBackground"] != "#2a0a1a" {
		t.Errorf("profile = %+v", profile)
	}
	palette, err := ParseProfile([]byte(profileJSON("")))
	if err != nil || palette.Surfaces != nil {
		t.Errorf("a palette-only profile = %+v, %v, want no pins", palette, err)
	}
	for name, tc := range map[string]struct{ content, want string }{
		"not json":             {`{not json`, "parse profile"},
		"unknown top-level":    {`{"name":"x","overrides":{},"extra":1}`, `"extra"`},
		"unknown nested":       {`{"name":"x","overrides":{},"surfaces":{"vscode":{"colours":{}}}}`, `"colours"`},
		"trailing data":        {`{"name":"x","overrides":{}} {}`, "data after"},
		"no name":              {`{"overrides":{}}`, "no name"},
		"unknown tmux pin":     {`{"name":"x","overrides":{},"surfaces":{"tmux":{"@nope":"#fff"}}}`, "@nope"},
		"bad herdr pin colour": {`{"name":"x","overrides":{},"surfaces":{"herdr":{"accent":"purple"}}}`, "purple"},
		"unknown claude token": {`{"name":"x","overrides":{"claude":"#fff","promptBordr":"#fff"}}`, `overrides "promptBordr": not a Claude Code theme token`},
		"bad claude colour":    {`{"name":"x","overrides":{"claude":"pinkish"}}`, `overrides "claude"`},
		"unknown base":         {`{"name":"x","base":"midnight","overrides":{}}`, `base "midnight": not a Claude Code built-in theme`},
	} {
		_, err := ParseProfile([]byte(tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
	if _, err := ParseProfile([]byte(`{"name":"x","overrides":{},"surfaces":{"tmux":{"@nope":"#fff"}}}`)); err == nil ||
		!strings.Contains(err.Error(), "profile x") || !strings.Contains(err.Error(), "tmux") {
		t.Errorf("an invalid pin = %v, want an error naming the profile and the surface", err)
	}
}

func TestClaudeTokenRegistryParsesAndCoversEveryShippedProfile(t *testing.T) {
	if len(claudeTokens) < 70 || !claudeTokens["claude"] || !claudeTokens["rainbow_violet_shimmer"] {
		t.Fatalf("claudeTokens holds %d ids, want Claude Code's whole token set", len(claudeTokens))
	}
	for _, bad := range []string{"claude\nclaude\n", "not a token\n"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("parseClaudeTokens(%q) did not panic", bad)
				}
			}()
			parseClaudeTokens(bad)
		}()
	}
	for token := range builtinThemes[builtinDefault] {
		if !claudeTokens[token] {
			t.Errorf("built-in token %q is missing from claude_token_ids.txt", token)
		}
	}
}

func TestClaudeFileIsThePaletteAlone(t *testing.T) {
	palette := `{"name":"Tokyo Night · neon pink","base":"dark","overrides":{"claude":"#ff2e93"}}` + "\n"
	profile, err := ParseProfile([]byte(palette))
	if err != nil {
		t.Fatal(err)
	}
	got, err := profile.ClaudeFile([]byte(palette))
	if err != nil || string(got) != palette {
		t.Errorf("a palette-only file = %q, %v, want its bytes verbatim", got, err)
	}

	pinned := `{"name":"Tokyo Night · neon pink","base":"dark","overrides":{"claude":"#ff2e93"},` +
		`"surfaces":{"tmux":{"@host_accent":"#ff2e93"}}}`
	profile, err = ParseProfile([]byte(pinned))
	if err != nil {
		t.Fatal(err)
	}
	got, err = profile.ClaudeFile([]byte(pinned))
	want := "{\n  \"name\": \"Tokyo Night · neon pink\",\n  \"base\": \"dark\",\n  \"overrides\": {\n    \"claude\": \"#ff2e93\"\n  }\n}\n"
	if err != nil || string(got) != want {
		t.Errorf("a pinned file = %q, %v, want name, base and overrides alone:\n%s", got, err, want)
	}
	if _, err := profile.ClaudeFile([]byte(`{not json`)); err == nil {
		t.Error("ClaudeFile over unparsable content succeeded")
	}
}
