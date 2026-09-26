package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInstallLeavesClaudeAccountFilesUnchanged(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude", "settings.json")
	secondary := filepath.Join(home, ".cc", "4", "settings.json")
	writeFixture(
		t,
		canonical,
		`{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[{"type":"command","command":"canonical-keep"}]}]}}`,
	)
	writeFixture(
		t,
		secondary,
		`{"hooks":{"SessionEnd":[{"matcher":"","hooks":[{"type":"command","command":"secondary-keep"}]}]}}`,
	)
	canonicalAlias := filepath.Join(home, ".cc", "1", "settings.json")
	if err := os.MkdirAll(filepath.Dir(canonicalAlias), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, canonicalAlias); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, ".claude.json")
	writeFixture(t, registry, `{"mcpServers":{"other":{"command":"custom"}}}`)
	before := map[string]string{}
	for _, path := range []string{canonical, secondary, registry} {
		before[path] = readFixture(t, path)
	}
	missingSettings := filepath.Join(home, ".cc", "5", "settings.json")
	missingRegistry := filepath.Join(home, ".cc", "5", ".claude.json")
	sourceRepo := t.TempDir()
	configPath := filepath.Join(home, "pfm.config.json")
	recordFixtureSourceRepo(t, home, sourceRepo)
	writeFixture(t, configPath, `{"version":2}`)

	now := func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC) }
	runner := &outputRunner{printOutput: "state = not running\n"}
	if _, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		SourceRepo:    sourceRepo,
		MCPConfigPath: configPath,
		CodexHomes:    []string{},
		ConfigDirs: []string{
			filepath.Join(home, ".claude"),
			filepath.Join(home, ".cc", "4"),
			filepath.Join(home, ".cc", "5"),
		},
		Now:    now,
		Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	wantedProbe := nameSyncStateProbe
	if schedulerIsLaunchd {
		wantedProbe = "launchctl print gui/" + strconv.Itoa(os.Getuid()) + "/" + launchdLabel
	}
	if len(runner.calls) == 0 || runner.calls[0] != wantedProbe {
		t.Fatalf(
			"installer did not probe the running-service gate before touching fixtures; fake runner calls: %v",
			runner.calls,
		)
	}

	for path, want := range before {
		if got := readFixture(t, path); got != want {
			t.Fatalf("install changed %s:\n%s", path, got)
		}
	}
	for _, path := range []string{missingSettings, missingRegistry} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("install created account file %s: %v", path, err)
		}
	}

	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		SourceRepo:    sourceRepo,
		MCPConfigPath: configPath,
		CodexHomes:    []string{},
		ConfigDirs: []string{
			filepath.Join(home, ".claude"),
			filepath.Join(home, ".cc", "4"),
			filepath.Join(home, ".cc", "5"),
		},
		Now:    now,
		Stdout: &second,
		Runner: &fakeRunner{},
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}
	if _, err := Run(context.Background(), Options{
		Mode:          ModeUninstall,
		Home:          home,
		SourceRepo:    sourceRepo,
		MCPConfigPath: configPath,
		ConfigDirs: []string{
			filepath.Join(home, ".claude"),
			filepath.Join(home, ".cc", "4"),
			filepath.Join(home, ".cc", "5"),
		},
		CodexHomes: []string{},
		Runner:     &fakeRunner{},
		Now:        now,
	}); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		if got := readFixture(t, path); got != want {
			t.Fatalf("uninstall changed %s:\n%s", path, got)
		}
	}

	// pfm writes no Codex hook, so an install over a Codex home with none
	// leaves no hooks.json behind at all.
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install wrote a Codex hooks file it owns nothing in: %v", err)
	}
}

func TestUninstallLeavesClaudeAccountFilesUnchanged(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, `{"version":2}`)
	configDir := filepath.Join(home, ".claude")
	settings := filepath.Join(configDir, "settings.json")
	registry := filepath.Join(home, ".claude.json")
	settingsRaw := fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"command":%q}]}]},"statusLine":{"command":%q}}`,
		claudeHookTemplates(home)[0].Command, home+"/.local/bin/pfm statusline")
	registryRaw := `{"mcpServers":{"chat":{"command":"pfm"},"other":{"command":"operator"}}}`
	writeFixture(t, settings, settingsRaw)
	writeFixture(t, registry, registryRaw)
	_, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, SourceRepo: sourceRepo,
		MCPConfigPath: configPath, ConfigDirs: []string{configDir}, CodexHomes: []string{},
		Runner: &fakeRunner{nameSyncIdle: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, settings); got != settingsRaw {
		t.Fatalf("uninstall changed settings: %s", got)
	}
	if got := readFixture(t, registry); got != registryRaw {
		t.Fatalf("uninstall changed registry: %s", got)
	}
}

func TestRetiredHookCommandMatchingRecognizesAllDreamAliases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "bare pfm agent inject", command: "pfm dream hook agent-inject", want: "dream-agent-inject"},
		{name: "path cc fleet nudge", command: "/opt/legacy/.local/bin/cc-fleet dream hook nudge", want: "dream-nudge"},
		{
			name:    "bare codex injection",
			command: "cc-fleet dream hook codex-subagent-inject",
			want:    "dream-codex-subagent-inject",
		},
		{
			name:    "legacy shell shim",
			command: "bash /opt/legacy/hooks/dreamer-agent-inject.sh --fixture",
			want:    "dream-agent-inject",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, retired := retiredHookCommandName(tc.command)
			if !retired || got != tc.want {
				t.Fatalf("retiredHookCommandName(%q)=(%q,%v), want (%q,true)", tc.command, got, retired, tc.want)
			}
		})
	}
	for _, command := range []string{
		"pfm dream hook agent-injector",
		"/opt/legacy/bin/pfm-helper dream hook nudge",
		"cc-fleet dream hook codex-subagent-injector",
	} {
		if name, retired := retiredHookCommandName(command); retired {
			t.Fatalf("near-miss command %q classified as retired %q", command, name)
		}
	}
}

func hookCommandCount(t *testing.T, raw, event, wanted string) int {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode settings fixture: %v\n%s", err, raw)
	}
	events, _ := document["hooks"].(map[string]any)
	count := 0
	for eventName, eventValue := range events {
		if event != "" && eventName != event {
			continue
		}
		entries, _ := eventValue.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			hooks, _ := entry["hooks"].([]any)
			for _, hookValue := range hooks {
				hook, _ := hookValue.(map[string]any)
				if hook["command"] == wanted {
					count++
				}
			}
		}
	}
	return count
}

func TestUninstallRefusesToStrandOwnedHookInInvalidCodexJSON(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	writeFixture(t, hooksPath, "{broken\n")
	expected := ExpectedHook{
		Event:   "SessionStart",
		Matcher: codexClearMatcher,
		Command: filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill",
	}
	physical := physicalSettingsPath(hooksPath)
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physical: {{Event: expected.Event, Matcher: expected.Matcher, Command: expected.Command}: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managed), string(encoded))
	installer := engine{options: Options{Mode: ModeUninstall, Home: home}, managedRoot: managed}
	err = installer.wireCodexHooks()
	if err == nil || !strings.Contains(err.Error(), "refuse to strand owned hooks in invalid Codex hooks JSON") {
		t.Fatalf("wireCodexHooks error=%v", err)
	}
}

func TestCodexHookWiringWritesNoClearKillHookAcrossConfiguredHomes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	homes := []string{filepath.Join(home, ".codex"), filepath.Join(home, ".codex-2")}
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, codexHome := range homes {
		hooksPath := filepath.Join(codexHome, "hooks.json")
		if _, err := os.Stat(hooksPath); err == nil {
			raw := readFixture(t, hooksPath)
			if strings.Contains(raw, "internal clear-kill") {
				t.Fatalf("%s carries the retired clear-kill hook:\n%s", hooksPath, raw)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", hooksPath, err)
		}
	}
}

func TestCodexHookWiringStripsALeftoverAcrossConfiguredHomes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	homes := []string{filepath.Join(home, ".codex"), filepath.Join(home, ".codex-2")}
	canonical := filepath.Join(home, ".local", "bin", "pfm") + " internal clear-kill"
	for _, codexHome := range homes {
		writeFixture(t, filepath.Join(codexHome, "hooks.json"), fmt.Sprintf(
			`{"hooks":{"SessionStart":[{"matcher":%q,"hooks":[{"type":"command","command":%q}]}]}}`,
			codexClearMatcher, canonical,
		))
	}
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, CodexHomes: homes, Stdout: io.Discard},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, codexHome := range homes {
		raw := readFixture(t, filepath.Join(codexHome, "hooks.json"))
		if got := hookCommandCount(t, raw, "SessionStart", canonical); got != 0 {
			t.Fatalf("%s has %d leftover clear-kill hooks, want zero\n%s", codexHome, got, raw)
		}
	}
}
