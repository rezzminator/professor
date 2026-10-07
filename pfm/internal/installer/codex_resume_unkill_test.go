package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const fakeCodexAppServer = `#!/bin/sh
LOG='@LOG@'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$LOG"
  case "$line" in
    *'"method":"initialize"'*) printf '%s\n' '{"id":0,"result":{}}' ;;
    *'"method":"hooks/list"'*)
      status=untrusted
      if grep -q '"method":"config/value/write"' "$LOG"; then status=trusted; fi
      printf '{"id":1,"result":{"data":[{"hooks":[{"key":"@ACCOUNT@/hooks.json:session_start:0:1","command":"echo personal","sourcePath":"@ACCOUNT@/hooks.json","source":"user","currentHash":"sha256:other","eventName":"sessionStart","matcher":"resume","enabled":true,"trustStatus":"untrusted"},{"key":"@ACCOUNT@/hooks.json:session_start:0:0","command":"@COMMAND@","sourcePath":"@ACCOUNT@/hooks.json","source":"user","currentHash":"sha256:deadbeef","eventName":"sessionStart","matcher":"resume","enabled":true,"trustStatus":"%s"}]}]}}\n' "$status" ;;
    *'"method":"config/value/write"'*) printf '%s\n' '{"id":1,"result":{}}' ;;
  esac
done
`

const personalStopHook = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo personal"}]}]}}`

// stageResumeUnkillAccount lays down a Codex account whose hooks.json holds one
// personal Stop hook, and the fake `codex app-server` that answers hook
// discovery with the handler's real key shape. It returns the physical account,
// the fake binary and the request log it appends to.
func stageResumeUnkillAccount(t *testing.T, home string) (account, binary, requestLog string) {
	t.Helper()
	account = filepath.Join(home, ".codex")
	writeFixture(t, filepath.Join(account, "hooks.json"), personalStopHook)
	physical, err := filepath.EvalSymlinks(account)
	if err != nil {
		t.Fatal(err)
	}
	requestLog = filepath.Join(home, "app-server.log")
	script := strings.NewReplacer(
		"@LOG@", requestLog, "@ACCOUNT@", physical, "@COMMAND@", resumeUnkillCommand(home),
	).Replace(fakeCodexAppServer)
	binary = filepath.Join(home, "fake-codex")
	if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return physical, binary, requestLog
}

func resumeUnkillCommand(home string) string {
	return filepath.Join(home, ".local", "bin", "pfm") + " internal resume-unkill"
}

func resumeUnkillEngine(home, account, binary string, mode Mode, stdout *bytes.Buffer) *engine {
	return &engine{
		options: Options{
			Mode: mode, Home: home, CodexHomes: []string{account}, CodexBinary: binary, Stdout: stdout,
		},
		apply:       true,
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
}

func TestCodexResumeUnkillHookIsWrittenOnceKeepsPersonalHooksAndIsOwned(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	command := resumeUnkillCommand(home)
	path := filepath.Join(account, "hooks.json")

	var first bytes.Buffer
	if err := resumeUnkillEngine(home, account, "", ModeApply, &first).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, path)
	if got := hookCommandCount(t, raw, "SessionStart", command); got != 1 {
		t.Fatalf("resume-unkill handler count=%d, want 1:\n%s", got, raw)
	}
	var document struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	entries := document.Hooks["SessionStart"]
	if len(entries) != 1 || entries[0].Matcher != "resume" || len(entries[0].Hooks) != 1 ||
		entries[0].Hooks[0].Type != "command" || entries[0].Hooks[0].Timeout != 30 {
		t.Fatalf("SessionStart entries=%+v, want one resume entry holding one command handler, timeout 30", entries)
	}
	if got := hookCommandCount(t, raw, "Stop", "echo personal"); got != 1 {
		t.Fatalf("personal Stop hook lost:\n%s", raw)
	}
	ownership, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(filepath.Join(
		home, ".local", "share", "pfm", "install",
	)))
	if err != nil {
		t.Fatal(err)
	}
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: command}
	if got := ownership[physicalSettingsPath(path)][key]; got != 1 {
		t.Fatalf("ownership ledger counts %d for the resume-unkill hook, want 1: %v", got, ownership)
	}
	if !strings.Contains(first.String(), "skip    Codex hook trust not recorded for "+account+
		": no Codex binary configured — the resume-unkill hook stays untrusted") {
		t.Fatalf("a missing Codex binary was not said out loud:\n%s", first.String())
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

func TestCodexResumeUnkillHookTrustIsRecordedThroughTheNativeAPI(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, binary, requestLog := stageResumeUnkillAccount(t, home)
	key := account + "/hooks.json:session_start:0:0"

	var transcript bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &transcript).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, transcript.String())
	}
	var receipt map[string]string
	receiptRaw := readFixture(t, filepath.Join(account, ".professor-hook-trust.json"))
	if err := json.Unmarshal([]byte(receiptRaw), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 1 || receipt[key] != "sha256:deadbeef" {
		t.Fatalf("receipt=%v, want only %s -> sha256:deadbeef", receipt, key)
	}
	logged := readFixture(t, requestLog)
	if got := strings.Count(logged, `"method":"config/value/write"`); got != 1 ||
		!strings.Contains(logged, `"keyPath":"hooks.state.\"`+key+`\""`) ||
		!strings.Contains(logged, `"trusted_hash":"sha256:deadbeef"`) {
		t.Fatalf("config/value/write requests=%d, want one carrying the hooks.state key and hash:\n%s", got, logged)
	}
	if !strings.Contains(transcript.String(), "change  trust resume-unkill hook "+account) {
		t.Fatalf("trust step not reported:\n%s", transcript.String())
	}

	var again bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &again).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(readFixture(t, requestLog), `"method":"config/value/write"`); got != 1 {
		t.Fatalf("a trusted hook was written again: %d config/value/write requests", got)
	}
}

func TestCodexResumeUnkillHookAndTrustAreRemovedOnUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account, binary, _ := stageResumeUnkillAccount(t, home)
	key := account + "/hooks.json:session_start:0:0"
	if err := resumeUnkillEngine(home, account, binary, ModeApply, &bytes.Buffer{}).wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	// Codex wrote the trust table on the install's behalf; a personal one sits beside it.
	writeFixture(t, filepath.Join(account, "config.toml"),
		"model = 'personal'\n[hooks.state.\""+key+"\"]\nenabled = true\ntrusted_hash = 'sha256:deadbeef'\n"+
			"[hooks.state.\"other:key\"]\nenabled = false\ntrusted_hash = 'sha256:foreign'\n")

	var transcript bytes.Buffer
	if err := resumeUnkillEngine(home, account, binary, ModeUninstall, &transcript).wireCodexHooks(); err != nil {
		t.Fatalf("%v\n%s", err, transcript.String())
	}
	raw := readFixture(t, filepath.Join(account, "hooks.json"))
	if got := hookCommandCount(t, raw, "SessionStart", resumeUnkillCommand(home)); got != 0 {
		t.Fatalf("uninstall left %d resume-unkill handlers:\n%s", got, raw)
	}
	if got := hookCommandCount(t, raw, "Stop", "echo personal"); got != 1 {
		t.Fatalf("uninstall lost the personal hook:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(account, ".professor-hook-trust.json")); !os.IsNotExist(err) {
		t.Fatalf("hook trust receipt survived uninstall: %v", err)
	}
	config := readFixture(t, filepath.Join(account, "config.toml"))
	if strings.Contains(config, "sha256:deadbeef") || !strings.Contains(config, "sha256:foreign") ||
		!strings.Contains(config, "model = 'personal'") {
		t.Fatalf("uninstall edited config.toml wrongly:\n%s", config)
	}
	if !strings.Contains(transcript.String(), "change  remove resume-unkill hook trust "+account) {
		t.Fatalf("trust removal not reported:\n%s", transcript.String())
	}
}

func TestCodexResumeUnkillTrustContinuesAfterAccountFailure(t *testing.T) {
	home := t.TempDir()
	second, binary, requestLog := stageResumeUnkillAccount(t, home)
	first := filepath.Join(home, ".codex-first")
	writeFixture(t, filepath.Join(first, "hooks.json"), `{"hooks":{}}`)
	var output bytes.Buffer
	installer := resumeUnkillEngine(home, first, binary, ModeApply, &output)
	installer.options.CodexHomes = []string{first, second}
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatalf("trust failure stopped wiring: %v", err)
	}
	err := errors.Join(installer.deferred...)
	if err == nil || !strings.Contains(err.Error(), "Codex hook trust for "+first+": ") ||
		strings.Contains(err.Error(), "Codex hook trust for "+second+": ") {
		t.Fatalf("deferred error=%v, want only first account", err)
	}
	if !strings.Contains(output.String(), "  FAIL    Codex hook trust for "+first+": ") ||
		!strings.Contains(readFixture(t, requestLog), `"method":"config/value/write"`) {
		t.Fatalf("second account did not receive trust after first failed:\n%s", output.String())
	}
	var receipt map[string]string
	if err := json.Unmarshal(
		[]byte(readFixture(t, filepath.Join(second, ".professor-hook-trust.json"))),
		&receipt,
	); err != nil {
		t.Fatal(err)
	}
	if receipt[second+"/hooks.json:session_start:0:0"] != "sha256:deadbeef" {
		t.Fatalf("second account receipt=%v", receipt)
	}
}

func TestCodexHookLedgerDryRunCountsOneWriteForTwoHomes(t *testing.T) {
	home := t.TempDir()
	first, _, _ := stageResumeUnkillAccount(t, home)
	second := filepath.Join(home, ".codex-second")
	writeFixture(t, filepath.Join(second, "hooks.json"), `{"hooks":{}}`)
	var output bytes.Buffer
	preview := resumeUnkillEngine(home, first, "", ModeDryRun, &output)
	preview.options.CodexHomes = []string{first, second}
	preview.apply = false
	if err := preview.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	line := "  change  write " + settingsHookOwnershipPath(preview.managedRoot) + "\n"
	if got := strings.Count(output.String(), line); got != 1 {
		t.Errorf("ledger writes=%d, want 1:\n%s", got, output.String())
	}
	apply := resumeUnkillEngine(home, first, "", ModeApply, &bytes.Buffer{})
	apply.options.CodexHomes = preview.options.CodexHomes
	if err := apply.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if preview.report.Changed != apply.report.Changed {
		t.Errorf("preview changed=%d, apply changed=%d", preview.report.Changed, apply.report.Changed)
	}
}

func TestCodexResumeUnkillReplacesOwnedStaleCommandAndKeepsForeignLookalike(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(map[bool]string{true: "owned", false: "foreign"}[owned], func(t *testing.T) {
			home := t.TempDir()
			account := filepath.Join(home, ".codex")
			path := filepath.Join(account, "hooks.json")
			const stale = "/old/home/.local/bin/pfm internal resume-unkill"
			writeFixture(t, path, strings.ReplaceAll(resumeUnkillHooksBody(home), resumeUnkillCommand(home), stale))
			installer := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{})
			if owned {
				encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
					physicalSettingsPath(path): {{Event: "SessionStart", Matcher: "resume", Command: stale}: 1},
				})
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, settingsHookOwnershipPath(installer.managedRoot), string(encoded))
			}
			if err := installer.wireCodexHooks(); err != nil {
				t.Fatal(err)
			}
			raw := readFixture(t, path)
			wantStale := 1
			if owned {
				wantStale = 0
			}
			if hookCommandCount(t, raw, "SessionStart", resumeUnkillCommand(home)) != 1 ||
				hookCommandCount(t, raw, "SessionStart", stale) != wantStale {
				t.Fatalf("wrong handlers after ownership-aware replacement:\n%s", raw)
			}
			ledger, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(installer.managedRoot))
			if err != nil {
				t.Fatal(err)
			}
			current := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
			if len(ledger[physicalSettingsPath(path)]) != 1 || ledger[physicalSettingsPath(path)][current] != 1 {
				t.Fatalf("ledger=%v, want ownership of current handler", ledger)
			}
		})
	}
}

func TestCodexHookLedgerPrunesClaudeRowsAndKeepsUnconfiguredCodexHomes(t *testing.T) {
	for _, configured := range []bool{true, false} {
		t.Run(map[bool]string{true: "configured", false: "skipped Codex"}[configured], func(t *testing.T) {
			home := t.TempDir()
			account := filepath.Join(home, ".codex")
			path := filepath.Join(account, "hooks.json")
			writeFixture(t, path, resumeUnkillHooksBody(home))
			installer := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{})
			if !configured {
				installer.options.CodexHomes = nil
			}
			key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
			encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
				physicalSettingsPath(path):                      {key: 1},
				filepath.Join(home, ".claude", "settings.json"): {key: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			ledgerPath := settingsHookOwnershipPath(installer.managedRoot)
			writeFixture(t, ledgerPath, string(encoded))
			if err := installer.wireCodexHooks(); err != nil {
				t.Fatal(err)
			}
			ledger, _, err := readSettingsHookOwnership(ledgerPath)
			if err != nil || len(ledger) != 1 || ledger[physicalSettingsPath(path)][key] != 1 {
				t.Fatalf("ledger=%v err=%v, want only Codex ownership", ledger, err)
			}
		})
	}
}

func TestCodexHookLedgerWritesCompletedHomesBeforeUninstallRefusal(t *testing.T) {
	home := t.TempDir()
	first, second := filepath.Join(home, ".codex"), filepath.Join(home, ".codex-second")
	firstPath, secondPath := filepath.Join(first, "hooks.json"), filepath.Join(second, "hooks.json")
	writeFixture(t, firstPath, resumeUnkillHooksBody(home))
	writeFixture(t, secondPath, "{broken")
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physicalSettingsPath(firstPath): {key: 1}, physicalSettingsPath(secondPath): {key: 1},
		filepath.Join(home, ".claude", "settings.json"): {key: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	installer := resumeUnkillEngine(home, first, "", ModeUninstall, &output)
	installer.options.CodexHomes = []string{first, second}
	ledgerPath := settingsHookOwnershipPath(installer.managedRoot)
	writeFixture(t, ledgerPath, string(encoded))
	err = installer.wireCodexHooks()
	if err == nil || !strings.Contains(err.Error(), "refuse to strand owned hooks") ||
		!strings.Contains(err.Error(), secondPath) {
		t.Fatalf("uninstall error=%v, want second home's refusal", err)
	}
	ledger, _, err := readSettingsHookOwnership(ledgerPath)
	if err != nil || len(ledger) != 1 || ledger[physicalSettingsPath(secondPath)][key] != 1 {
		t.Fatalf("ledger=%v err=%v, want remaining second-home ownership", ledger, err)
	}
	if hookCommandCount(t, readFixture(t, firstPath), "SessionStart", key.Command) != 0 {
		t.Fatal("first home's uninstall did not land")
	}
}

func TestCodexHookLedgerRecordsNoOwnershipForAFailedWrite(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	// A name that leaves atomicfile's scratch suffix past NAME_MAX fails the
	// write even for root.
	physical := filepath.Join(t.TempDir(), strings.Repeat("h", 245))
	writeFixture(t, physical, `{"hooks":{}}`)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, filepath.Join(account, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	installer := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{})
	if err := installer.wireCodexHooks(); err == nil {
		t.Fatal("a failed hooks.json write returned success")
	}
	ledger, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(installer.managedRoot))
	if err != nil {
		t.Fatal(err)
	}
	if owned := ledger[physicalSettingsPath(physical)]; len(owned) != 0 {
		t.Fatalf("ledger owns %v in a hooks.json the failed write never reached", owned)
	}
}

func TestCodexHookLedgerKeepsAHooksFileLinkedUnderAnotherName(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	physical := filepath.Join(home, "dotfiles", "codex-hooks.json")
	writeFixture(t, physical, `{"hooks":{}}`)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, filepath.Join(account, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	installer := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{})
	if err := installer.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	if hookCommandCount(t, readFixture(t, physical), "SessionStart", resumeUnkillCommand(home)) != 1 {
		t.Fatalf("install did not write the handler through the link:\n%s", readFixture(t, physical))
	}
	ledger, _, err := readSettingsHookOwnership(settingsHookOwnershipPath(installer.managedRoot))
	if err != nil {
		t.Fatal(err)
	}
	current := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
	if ledger[physicalSettingsPath(physical)][current] != 1 {
		t.Fatalf("ledger=%v, want ownership of the handler written into %s", ledger, physical)
	}

	// A home that fails before the linked one is reached never costs it its row.
	broken := filepath.Join(home, ".codex-broken")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "moved", "hooks.json"), filepath.Join(broken, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	failing := resumeUnkillEngine(home, account, "", ModeApply, &bytes.Buffer{})
	failing.options.CodexHomes = []string{broken, account}
	if err := failing.wireCodexHooks(); err == nil {
		t.Fatal("a dangling hooks.json returned nil")
	}
	ledger, _, err = readSettingsHookOwnership(settingsHookOwnershipPath(installer.managedRoot))
	if err != nil {
		t.Fatal(err)
	}
	if ledger[physicalSettingsPath(physical)][current] != 1 {
		t.Fatalf("ledger=%v after a failed earlier home, want ownership of %s kept", ledger, physical)
	}
}

func TestCodexHooksRefusesRetiredAccountOwnership(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "retired", "hooks.json")
	doc := settingsHookOwnershipDocument{
		Version: 1,
		Hooks: []settingsHookOwnershipRecord{
			{Path: old, Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home), Count: 1},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(raw))
	e := resumeUnkillEngine(home, filepath.Join(home, "current"), "", ModeUninstall, &bytes.Buffer{})
	err = e.wireCodexHooks()
	if err == nil || !strings.Contains(err.Error(), old) {
		t.Fatalf("retired account ownership error = %v", err)
	}
}

func TestCodexHooksRefusesRetiredPhysicalAliasOwnership(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "dotfiles", "codex-hooks.json")
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
	raw, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{old: {key: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ledger := settingsHookOwnershipPath(managedRootForHome(home))
	writeFixture(t, ledger, string(raw))
	e := resumeUnkillEngine(home, filepath.Join(home, "current"), "", ModeUninstall, &bytes.Buffer{})
	if err := e.wireCodexHooks(); err == nil || !strings.Contains(err.Error(), old) {
		t.Fatalf("retired alias ownership error=%v", err)
	}
	assertContent(t, ledger, string(raw))
}

func TestCodexHookLedgerRetiresOwnedClaudeAliasBeforeDroppingReceipt(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	body := resumeUnkillHooksBody(home)
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	entries := hooks["SessionStart"].([]any)
	hooks["SessionStart"] = append(
		entries,
		map[string]any{
			"matcher": "resume",
			"hooks":   []any{map[string]any{"type": "command", "command": "echo personal"}},
		},
	)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, string(raw))
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
	ledger, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{path: {key: 1}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(ledger))
	e := resumeUnkillEngine(home, filepath.Join(home, "current"), "", ModeUninstall, &bytes.Buffer{})
	if err := e.wireCodexHooks(); err != nil {
		t.Fatal(err)
	}
	got := readFixture(t, path)
	if hookCommandCount(t, got, "SessionStart", resumeUnkillCommand(home)) != 0 ||
		hookCommandCount(t, got, "SessionStart", "echo personal") != 1 {
		t.Fatalf("retired physical alias handler stranded or operator hook lost: %s", got)
	}
}
