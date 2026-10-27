package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// codexHerdrRows are the handlers Codex's hooks.json carries for the Herdr
// reporter: Codex's SessionEnd releases the pane when the chat exits, and its
// Interrupt (an Esc denial or an aborted turn, which fires no Stop) reports idle.
// Each handler's timeout outlasts the reporter's deadline (herdr.EngineKillTimeout)
// except where Codex caps it: SessionEnd and Interrupt at 3 s
// (codex-rs hooks discovery.rs SESSION_END_MAX_TIMEOUT_SEC), above which Codex
// warns at load.
var codexHerdrRows = []struct {
	event, verb string
	timeout     int
}{
	{"SessionStart", "start", 5},
	{"UserPromptSubmit", "working", 5},
	{"PostToolUse", "working", 5},
	{"PermissionRequest", "blocked", 5},
	{"Stop", "idle", 5},
	{"Interrupt", "idle", 3},
	{"SessionEnd", "end", 3},
}

func codexHerdrCommand(home, verb string) string {
	return filepath.Join(home, ".local", "bin", "pfm") + " internal herdr-state --engine codex " + verb
}

type codexHandlerDoc struct {
	Hooks map[string][]struct {
		Matcher *string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
			Async   *bool  `json:"async"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func decodeCodexHooks(t *testing.T, raw string) codexHandlerDoc {
	t.Helper()
	var document codexHandlerDoc
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("decode hooks.json: %v\n%s", err, raw)
	}
	return document
}

func TestCodexHerdrHandlersAreWrittenOnceOwnedAndKeepForeignHooks(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	path := filepath.Join(account, "hooks.json")
	writeFixture(
		t,
		path,
		`{"counter":`+beyondFloat64+`,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}],`+
			`"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo personal-tool","timeout":7}]}]}}`,
	)

	var first bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &first).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, first.String())
	}
	raw := readFixture(t, path)
	requireKeepsBeyondFloat64(t, "Codex Herdr handler wiring", []byte(raw))
	document := decodeCodexHooks(t, raw)
	for _, row := range codexHerdrRows {
		command := codexHerdrCommand(home, row.verb)
		if got := hookCommandCount(t, raw, row.event, command); got != 1 {
			t.Errorf("%s handler %q count=%d, want 1:\n%s", row.event, command, got, raw)
		}
		for _, entry := range document.Hooks[row.event] {
			for _, handler := range entry.Hooks {
				if handler.Command != command {
					continue
				}
				if entry.Matcher != nil && *entry.Matcher != "" {
					t.Errorf(
						"%s Herdr handler carries matcher %q, want every source and tool",
						row.event,
						*entry.Matcher,
					)
				}
				// Codex kills the handler at its timeout: the reporter's own
				// deadline must end, and its error log land, before that, within
				// the cap Codex puts on the event.
				if handler.Type != "command" || handler.Timeout != row.timeout || handler.Async != nil {
					t.Errorf(
						"%s Herdr handler = %+v, want a synchronous command with a %d s timeout",
						row.event,
						handler,
						row.timeout,
					)
				}
			}
		}
	}
	if got := hookCommandCount(t, raw, "Stop", "echo personal"); got != 1 {
		t.Errorf("personal Stop hook lost:\n%s", raw)
	}
	if got := hookCommandCount(t, raw, "PostToolUse", "echo personal-tool"); got != 1 {
		t.Errorf("personal PostToolUse hook lost:\n%s", raw)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("hooks.json mode = %v, %v; want 0600", info, err)
	}
	backups, err := filepath.Glob(path + ".*")
	if err != nil || len(backups) == 0 {
		t.Errorf("hooks.json rewrite left no backup beside %s: %v, %v", path, backups, err)
	}

	ownership, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(filepath.Join(
		home, ".local", "share", "pfm", "install",
	)))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range codexHerdrRows {
		key := settingsHookKey{Event: row.event, Command: codexHerdrCommand(home, row.verb)}
		if got := ownership[physicalSettingsPath(path)][key]; got != 1 {
			t.Errorf("ownership ledger counts %d for %+v, want 1: %v", got, key, ownership)
		}
	}

	var second bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &second).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != raw {
		t.Fatalf("a second run rewrote hooks.json:\n%s\n--- was ---\n%s", got, raw)
	}
	if strings.Contains(second.String(), "change  ") {
		t.Fatalf("a second run reported a change:\n%s", second.String())
	}
}

// An install that wrote every Herdr handler at 5 s left SessionEnd and Interrupt
// over Codex's 3 s cap, and a hand edit left Stop's a string; the next install
// rewrites those timeouts in place as numbers,
// adding no second handler, and leaves a personal handler's timeout alone.
func TestCodexHerdrHandlerTimeoutsOverCodexsCapAreRewritten(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	path := filepath.Join(account, "hooks.json")
	hooks := map[string]any{
		"Interrupt": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "echo personal", "timeout": 9},
		}}},
	}
	for _, row := range codexHerdrRows {
		entries, _ := hooks[row.event].([]any)
		// A hand-edited string timeout is no number Codex reads: it is rewritten.
		var timeout any = int(herdr.EngineKillTimeout / time.Second)
		if row.event == "Stop" {
			timeout = strconv.Itoa(row.timeout)
		}
		hooks[row.event] = append(entries, map[string]any{"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": codexHerdrCommand(home, row.verb),
				"timeout": timeout,
			},
		}})
	}
	encoded, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(encoded))

	var out bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &out).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	raw := readFixture(t, path)
	document := decodeCodexHooks(t, raw)
	for _, row := range codexHerdrRows {
		command := codexHerdrCommand(home, row.verb)
		if got := hookCommandCount(t, raw, row.event, command); got != 1 {
			t.Errorf("%s handler %q count=%d, want 1:\n%s", row.event, command, got, raw)
		}
		for _, entry := range document.Hooks[row.event] {
			for _, handler := range entry.Hooks {
				switch {
				case handler.Command == command && handler.Timeout != row.timeout:
					t.Errorf(
						"%s Herdr handler timeout = %d, want %d:\n%s",
						row.event,
						handler.Timeout,
						row.timeout,
						raw,
					)
				case handler.Command == "echo personal" && handler.Timeout != 9:
					t.Errorf("personal Interrupt handler timeout = %d, want its own 9:\n%s", handler.Timeout, raw)
				}
			}
		}
	}
}

func TestCodexHerdrHandlersAreRemovedOnUninstallAndForeignLookalikesStay(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	path := filepath.Join(account, "hooks.json")
	foreignLookalike := "/opt/other/bin/pfm internal herdr-state --engine codex idle"
	writeFixture(t, path, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"},`+
		`{"type":"command","command":"`+foreignLookalike+`"}]}]}}`)

	if err := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{}).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	installed := readFixture(t, path)
	if got := hookCommandCount(t, installed, "Stop", codexHerdrCommand(home, "idle")); got != 1 {
		t.Fatalf("Herdr Stop handler not installed:\n%s", installed)
	}
	if err := resumeUnkillEngine(home, account, "", ModeUninstall, &bytes.Buffer{}).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, path)
	var kept map[string][]struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	var envelope struct {
		Hooks json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Hooks, &kept); err != nil {
		t.Fatal(err)
	}
	var commands []string
	for event, entries := range kept {
		for _, entry := range entries {
			for _, handler := range entry.Hooks {
				commands = append(commands, event+"|"+handler.Command)
			}
		}
	}
	want := []string{"Stop|echo personal", "Stop|" + foreignLookalike}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("hooks after uninstall = %q, want only the foreign handlers %q:\n%s", commands, want, raw)
	}
	ownership, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(filepath.Join(
		home, ".local", "share", "pfm", "install",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ownership[physicalSettingsPath(path)]) != 0 {
		t.Fatalf("ledger still owns hooks after uninstall: %v", ownership)
	}
}

func TestCodexHerdrHandlerLedgerRowsAreExpectedNotDrift(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	if err := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{}).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	for _, result := range ProbeExpectedHooks(home, pfmconfig.Config{
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: account}},
	}) {
		if result.Hook.Target == "ownership" {
			t.Errorf("probe reports an owned Herdr handler as ledger drift: %+v", result)
		}
	}
}
